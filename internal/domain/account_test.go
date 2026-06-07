package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func validAPIKeyRow() *UpstreamAccount {
	return &UpstreamAccount{
		ID:         1,
		Name:       "k1",
		Provider:   ProviderOpenAI,
		APIKey:     "sk-abcdef",
		Status:     AccountStatusActive,
		AuthMethod: AuthMethodAPIKey,
	}
}

func validOAuthRow(method AuthMethod) *UpstreamAccount {
	now := time.Now().UTC()
	later := now.Add(time.Hour)
	return &UpstreamAccount{
		ID:              2,
		Name:            "o1",
		Provider:        ProviderOpenAI,
		Status:          AccountStatusActive,
		AuthMethod:      method,
		AccessToken:     []byte("at"),
		RefreshToken:    []byte("rt"),
		IDToken:         []byte("it"),
		LastRefresh:     &now,
		AccessExpiresAt: &later,
	}
}

func TestEffectiveBaseURL_Default(t *testing.T) {
	a := UpstreamAccount{Provider: ProviderOpenAI}
	assert.Equal(t, "https://api.openai.com/v1", a.EffectiveBaseURL())
}

func TestEffectiveBaseURL_CustomProvider(t *testing.T) {
	a := UpstreamAccount{Provider: ProviderAnthropic}
	assert.Equal(t, "https://api.anthropic.com/v1", a.EffectiveBaseURL())
}

func TestEffectiveBaseURL_Override(t *testing.T) {
	url := "https://proxy.example.com"
	a := UpstreamAccount{Provider: ProviderOpenAI, BaseURL: &url}
	assert.Equal(t, "https://proxy.example.com", a.EffectiveBaseURL())
}

func TestEffectiveBaseURL_EmptyOverride(t *testing.T) {
	empty := ""
	a := UpstreamAccount{Provider: ProviderOpenAI, BaseURL: &empty}
	assert.Equal(t, "https://api.openai.com/v1", a.EffectiveBaseURL())
}

func TestEffectiveBaseURL_UnknownProvider(t *testing.T) {
	a := UpstreamAccount{Provider: "unknown"}
	assert.Equal(t, "https://api.openai.com/v1", a.EffectiveBaseURL())
}

// --- 003: AuthMethod discriminator + Validate() -----------------------------

func TestUpstreamAccount_Validate_APIKeyHappy(t *testing.T) {
	t.Parallel()
	assert.NoError(t, validAPIKeyRow().Validate())
}

func TestUpstreamAccount_Validate_OAuthHappy_AllThreeMethods(t *testing.T) {
	t.Parallel()
	for _, m := range []AuthMethod{AuthMethodOAuthBrowser, AuthMethodOAuthDevice, AuthMethodOAuthImport} {
		m := m
		t.Run(string(m), func(t *testing.T) {
			t.Parallel()
			assert.NoError(t, validOAuthRow(m).Validate())
		})
	}
}

func TestUpstreamAccount_Validate_APIKeyEmptyFails(t *testing.T) {
	t.Parallel()
	row := validAPIKeyRow()
	row.APIKey = ""
	err := row.Validate()
	assert.ErrorIs(t, err, ErrInvalidAccountShape)
	assert.Contains(t, err.Error(), "api_key=empty")
}

func TestUpstreamAccount_Validate_APIKeyRowCarryingTokenFails(t *testing.T) {
	t.Parallel()
	row := validAPIKeyRow()
	row.AccessToken = []byte("leak")
	err := row.Validate()
	assert.ErrorIs(t, err, ErrInvalidAccountShape)
	assert.Contains(t, err.Error(), "access_token=non-nil")
}

// TestUpstreamAccount_Validate_APIKeyRowRejectsZeroLengthOAuthSlice
// is the regression guard for a subtle external-review finding: on
// an api_key row, `AccessToken = []byte{}` (non-nil but zero-length)
// historically passed validation because the code tested len>0
// rather than != nil. xorm persists []byte{} as a zero-length BLOB
// (NOT as NULL), which silently violates data-model.md rule 1 — the
// column must be NULL on api_key rows, not just "empty". This test
// locks the tight == nil semantics across all three OAuth byte
// columns.
func TestUpstreamAccount_Validate_APIKeyRowRejectsZeroLengthOAuthSlice(t *testing.T) {
	t.Parallel()
	for _, col := range []string{"access_token", "refresh_token", "id_token"} {
		col := col
		t.Run(col, func(t *testing.T) {
			t.Parallel()
			row := validAPIKeyRow()
			switch col {
			case "access_token":
				row.AccessToken = []byte{}
			case "refresh_token":
				row.RefreshToken = []byte{}
			case "id_token":
				row.IDToken = []byte{}
			}
			err := row.Validate()
			assert.ErrorIs(t, err, ErrInvalidAccountShape)
			assert.Contains(t, err.Error(), col+"=non-nil")
		})
	}
}

func TestUpstreamAccount_Validate_OAuthRowMissingRefreshTokenFails(t *testing.T) {
	t.Parallel()
	row := validOAuthRow(AuthMethodOAuthBrowser)
	row.RefreshToken = nil
	err := row.Validate()
	assert.ErrorIs(t, err, ErrInvalidAccountShape)
	assert.Contains(t, err.Error(), "refresh_token=empty")
}

func TestUpstreamAccount_Validate_OAuthRowMissingAccessExpiresAtFails(t *testing.T) {
	t.Parallel()
	row := validOAuthRow(AuthMethodOAuthDevice)
	row.AccessExpiresAt = nil
	err := row.Validate()
	assert.ErrorIs(t, err, ErrInvalidAccountShape)
	assert.Contains(t, err.Error(), "access_expires_at=nil")
}

func TestUpstreamAccount_Validate_OAuthRowWithAPIKeyFails(t *testing.T) {
	t.Parallel()
	row := validOAuthRow(AuthMethodOAuthImport)
	row.APIKey = "sk-leak"
	err := row.Validate()
	assert.ErrorIs(t, err, ErrInvalidAccountShape)
	assert.Contains(t, err.Error(), "api_key=non-empty")
}

func TestUpstreamAccount_Validate_UnknownMethod(t *testing.T) {
	t.Parallel()
	row := validAPIKeyRow()
	row.AuthMethod = AuthMethod("oauth_made_up")
	err := row.Validate()
	assert.ErrorIs(t, err, ErrUnknownAuthMethod)
	assert.Contains(t, err.Error(), `"oauth_made_up"`)
	// Critically: shape errors and unknown-method errors are distinct
	// under errors.Is so handlers can branch on which one fired.
	assert.False(t, errors.Is(err, ErrInvalidAccountShape),
		"ErrUnknownAuthMethod must not match ErrInvalidAccountShape under errors.Is")
}

func TestUpstreamAccount_Validate_NilReceiver(t *testing.T) {
	t.Parallel()
	var a *UpstreamAccount
	err := a.Validate()
	assert.ErrorIs(t, err, ErrInvalidAccountShape)
	assert.Contains(t, err.Error(), "nil receiver")
}

func TestUpstreamAccount_IsOAuth(t *testing.T) {
	t.Parallel()
	cases := map[AuthMethod]bool{
		AuthMethodAPIKey:       false,
		AuthMethodOAuthBrowser: true,
		AuthMethodOAuthDevice:  true,
		AuthMethodOAuthImport:  true,
		AuthMethod("bogus"):    false,
	}
	for m, want := range cases {
		assert.Equalf(t, want, (UpstreamAccount{AuthMethod: m}).IsOAuth(),
			"IsOAuth(%q)", m)
	}
}

// --- 003: secret redaction via String() and json.Marshal --------------------

func TestUpstreamAccount_String_RedactsSecrets(t *testing.T) {
	t.Parallel()
	row := &UpstreamAccount{
		ID: 7, Name: "n", Provider: ProviderOpenAI, Status: AccountStatusActive,
		APIKey:       "sk-SUPERSECRET",
		AuthMethod:   AuthMethodOAuthBrowser,
		AccessToken:  []byte("access-TOPSECRET"),
		RefreshToken: []byte("refresh-TOPSECRET"),
		IDToken:      []byte("idtok-TOPSECRET"),
	}
	s := row.String()
	for _, needle := range []string{"SUPERSECRET", "access-TOPSECRET", "refresh-TOPSECRET", "idtok-TOPSECRET"} {
		if strings.Contains(s, needle) {
			t.Errorf("String() leaked %q: %s", needle, s)
		}
	}
	assert.Contains(t, s, "<redacted>")
	assert.Contains(t, s, "len=16") // access token length
}

// TestUpstreamAccount_Validate_ShapeErrorText locks the stable
// offender-list text that account.go §Validate advertises. A future
// refactor that reorders or renames these strings is a breaking
// change for vendor review / log-scrape tooling and this test is the
// canary.
func TestUpstreamAccount_Validate_ShapeErrorText(t *testing.T) {
	t.Parallel()

	t.Run("api_key shape lists every offender in a stable order", func(t *testing.T) {
		t.Parallel()
		now := time.Now()
		later := now.Add(time.Hour)
		email := "e"
		plan := "p"
		cid := "c"
		row := &UpstreamAccount{
			AuthMethod:       AuthMethodAPIKey,
			APIKey:           "", // offender 1
			AccessToken:      []byte("a"),
			RefreshToken:     []byte("r"),
			IDToken:          []byte("i"),
			LastRefresh:      &now,
			AccessExpiresAt:  &later,
			Email:            &email,
			PlanType:         &plan,
			ChatGPTAccountID: &cid,
		}
		err := row.Validate()
		assert.ErrorIs(t, err, ErrInvalidAccountShape)
		msg := err.Error()
		// Order-sensitive — the validator is documented to list
		// offenders in declaration order; drift breaks vendor log
		// parsers.
		wantOrder := []string{
			"api_key=empty",
			"access_token=non-nil",
			"refresh_token=non-nil",
			"id_token=non-nil",
			"last_refresh=non-nil",
			"access_expires_at=non-nil",
			"email=non-nil",
			"plan_type=non-nil",
			"chatgpt_account_id=non-nil",
		}
		lastIdx := -1
		for _, needle := range wantOrder {
			idx := strings.Index(msg, needle)
			if idx < 0 {
				t.Errorf("api_key shape error missing %q in %s", needle, msg)
				continue
			}
			if idx <= lastIdx {
				t.Errorf("offenders out of order at %q (idx=%d, previous=%d) in %s",
					needle, idx, lastIdx, msg)
			}
			lastIdx = idx
		}
		assert.Contains(t, msg, "auth_method=api_key requires api_key set + all OAuth fields nil")
	})

	t.Run("oauth shape lists every offender in a stable order", func(t *testing.T) {
		t.Parallel()
		row := &UpstreamAccount{
			AuthMethod:      AuthMethodOAuthBrowser,
			APIKey:          "sk-leak",
			AccessToken:     nil,
			RefreshToken:    nil,
			IDToken:         nil,
			LastRefresh:     nil,
			AccessExpiresAt: nil,
		}
		err := row.Validate()
		assert.ErrorIs(t, err, ErrInvalidAccountShape)
		msg := err.Error()
		wantOrder := []string{
			"api_key=non-empty",
			"access_token=empty",
			"refresh_token=empty",
			"id_token=empty",
			"last_refresh=nil",
			"access_expires_at=nil",
		}
		lastIdx := -1
		for _, needle := range wantOrder {
			idx := strings.Index(msg, needle)
			if idx < 0 {
				t.Errorf("oauth shape error missing %q in %s", needle, msg)
				continue
			}
			if idx <= lastIdx {
				t.Errorf("offenders out of order at %q (idx=%d, previous=%d) in %s",
					needle, idx, lastIdx, msg)
			}
			lastIdx = idx
		}
		assert.Contains(t, msg,
			"auth_method=oauth_browser requires api_key nil + {access_token,refresh_token,id_token,last_refresh,access_expires_at} set")
	})
}

// TestUpstreamAccount_StringerCoverage proves that the `%s`, `%v`,
// and `%+v` verbs ALL go through Stringer and therefore redact
// secrets — both when an UpstreamAccount is printed directly and
// when it is embedded as a field of an outer struct.
func TestUpstreamAccount_StringerCoverage(t *testing.T) {
	t.Parallel()
	row := UpstreamAccount{
		APIKey:       "sk-STRINGER-COVER",
		AccessToken:  []byte("access-STRINGER-COVER"),
		RefreshToken: []byte("refresh-STRINGER-COVER"),
		IDToken:      []byte("idtok-STRINGER-COVER"),
	}
	wrapper := struct {
		Acct UpstreamAccount
		Note string
	}{Acct: row, Note: "wrapped"}

	// staticcheck S1025 would normally flag `fmt.Sprintf("%s", row)`
	// in favour of `row.String()` — but that's precisely the
	// assertion under test: we MUST exercise the verb-dispatch
	// path to prove Go's fmt package routes `%s`, `%v`, and `%+v`
	// through Stringer (not just that the method itself redacts).
	// Substituting row.String() here would hide a regression
	// where a future Go release (or a custom Formatter impl) stops
	// honouring Stringer for `%s` on a struct receiver.
	//nolint:staticcheck
	safeVerbs := []string{
		fmt.Sprintf("%s", row),
		fmt.Sprintf("%v", row),
		fmt.Sprintf("%+v", row),
		fmt.Sprintf("%v", wrapper),
		fmt.Sprintf("%+v", wrapper),
	}
	needles := []string{
		"sk-STRINGER-COVER",
		"access-STRINGER-COVER",
		"refresh-STRINGER-COVER",
		"idtok-STRINGER-COVER",
	}
	for i, out := range safeVerbs {
		for _, needle := range needles {
			if strings.Contains(out, needle) {
				t.Errorf("format variant %d leaked %q: %s", i, needle, out)
			}
		}
	}
}

// TestUpstreamAccount_SharpV_KnownLeak documents the accepted-risk
// gap that `%#v` (Go-syntax form) ignores the Stringer interface by
// design. The test exists purely to make the limitation visible: it
// asserts that the leak still happens, and will FAIL the moment a
// future contributor closes it (e.g. by wrapping secrets in opaque
// types). That's the signal to delete both the "KNOWN LIMITATION"
// comment block in account.go String() and this test.
func TestUpstreamAccount_SharpV_KnownLeak(t *testing.T) {
	t.Parallel()
	row := UpstreamAccount{
		APIKey:       "sk-KNOWN-LEAK",
		AccessToken:  []byte("access-KNOWN-LEAK"),
		RefreshToken: []byte("refresh-KNOWN-LEAK"),
		IDToken:      []byte("idtok-KNOWN-LEAK"),
	}
	leakS := fmt.Sprintf("%#v", row)
	leakedAny := false
	for _, needle := range []string{"sk-KNOWN-LEAK", "access-KNOWN-LEAK", "refresh-KNOWN-LEAK", "idtok-KNOWN-LEAK"} {
		if strings.Contains(leakS, needle) {
			leakedAny = true
			break
		}
	}
	if !leakedAny {
		t.Fatalf("%%#v no longer leaks secrets — REMOVE the 'KNOWN LIMITATION' block in account.go String() doc and delete this test: %s", leakS)
	}
}

func TestUpstreamAccount_JSON_OmitsSecretFields(t *testing.T) {
	t.Parallel()
	row := validOAuthRow(AuthMethodOAuthBrowser)
	row.AccessToken = []byte("access-TOPSECRET")
	row.RefreshToken = []byte("refresh-TOPSECRET")
	row.IDToken = []byte("idtok-TOPSECRET")
	row.APIKey = "sk-SHOULD-NEVER-SERIALIZE" // violates shape but we're testing the marshaller only
	b, err := json.Marshal(row)
	assert.NoError(t, err)
	body := string(b)
	for _, needle := range []string{"access-TOPSECRET", "refresh-TOPSECRET", "idtok-TOPSECRET", "sk-SHOULD-NEVER-SERIALIZE",
		"access_token", "refresh_token", "id_token", "api_key"} {
		if strings.Contains(body, needle) {
			t.Errorf("JSON leaked %q: %s", needle, body)
		}
	}
}
