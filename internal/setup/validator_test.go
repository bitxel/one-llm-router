package setup

import (
	"strings"
	"testing"

	"github.com/user/one-llm-router/internal/api/errcode"
)

func TestValidator_DriverEnum(t *testing.T) {
	t.Parallel()
	v := NewValidator()
	for _, d := range SupportedDrivers {
		if err := v.DriverEnum(d); err != nil {
			t.Errorf("DriverEnum(%q) = %v; want nil", d, err)
		}
	}
	for _, bad := range []string{"", "sqlserver", "oracle", "SQLITE3", " sqlite3"} {
		err := v.DriverEnum(bad)
		if err == nil {
			t.Errorf("DriverEnum(%q) expected ValidationError", bad)
			continue
		}
		if err.Code != errcode.InvalidDriver {
			t.Errorf("DriverEnum(%q) code=%d want %d", bad, err.Code, errcode.InvalidDriver)
		}
		if err.Field != "db.driver" {
			t.Errorf("DriverEnum(%q) field=%q want db.driver", bad, err.Field)
		}
	}
}

func TestValidator_DSN(t *testing.T) {
	t.Parallel()
	v := NewValidator()
	if err := v.DSN("router.db"); err != nil {
		t.Fatalf("valid dsn rejected: %v", err)
	}
	if err := v.DSN(""); err == nil || err.Code != errcode.InvalidDSN {
		t.Fatalf("empty dsn: want 2003, got %v", err)
	}
	long := strings.Repeat("a", DSNMaxLen+1)
	if err := v.DSN(long); err == nil || err.Code != errcode.InvalidDSN {
		t.Fatalf("long dsn: want 2003, got %v", err)
	}
}

func TestValidator_AccountName(t *testing.T) {
	t.Parallel()
	v := NewValidator()
	ok := []string{"prod-01", "my_account", "a", "A-Z_0-9"}
	bad := []string{"", "has space", "../bad", "tooooooooooooooooooooooooooooooooooooooooooooooooooooooooooolong65"}
	for _, s := range ok {
		if err := v.AccountName(s); err != nil {
			t.Errorf("AccountName(%q) rejected: %v", s, err)
		}
	}
	for _, s := range bad {
		err := v.AccountName(s)
		if err == nil {
			t.Errorf("AccountName(%q) expected error", s)
			continue
		}
		if err.Code != errcode.InvalidAccountName {
			t.Errorf("AccountName(%q) code=%d want %d", s, err.Code, errcode.InvalidAccountName)
		}
	}
}

func TestValidator_APIKey(t *testing.T) {
	t.Parallel()
	v := NewValidator()
	if err := v.APIKey("sk-abcd"); err != nil {
		t.Fatalf("valid api key rejected: %v", err)
	}
	if err := v.APIKey(""); err == nil || err.Code != errcode.InvalidAPIKey {
		t.Fatalf("empty api_key: want 2005, got %v", err)
	}
	long := strings.Repeat("x", APIKeyMaxLen+1)
	if err := v.APIKey(long); err == nil || err.Code != errcode.InvalidAPIKey {
		t.Fatalf("long api_key: want 2005, got %v", err)
	}
}

func TestValidator_BaseURL(t *testing.T) {
	t.Parallel()
	v := NewValidator()
	if err := v.BaseURL(nil); err != nil {
		t.Fatalf("nil base_url rejected: %v", err)
	}
	empty := ""
	if err := v.BaseURL(&empty); err != nil {
		t.Fatalf("empty base_url rejected: %v", err)
	}
	// Shared validator (domain.ValidateBaseURL) rejects paths on
	// purpose; the router appends client paths like /v1/... itself.
	ok := "https://upstream.example.com"
	if err := v.BaseURL(&ok); err != nil {
		t.Fatalf("valid base_url rejected: %v", err)
	}
	okWithSlash := "https://upstream.example.com/"
	if err := v.BaseURL(&okWithSlash); err != nil {
		t.Fatalf("trailing-slash base_url rejected: %v", err)
	}
	// http scheme: still rejected because the shared validator
	// enforces http OR https, but the old setup-only check allowed
	// only https. Both schemes are legitimate for internal
	// deployments so we relax to match the shared rule — the test
	// exercises a fundamentally invalid input instead.
	missingScheme := "upstream.example.com"
	if err := v.BaseURL(&missingScheme); err == nil || err.Code != errcode.InvalidBaseURL {
		t.Fatalf("relative base_url: want %d, got %v", errcode.InvalidBaseURL, err)
	}
	withPath := "https://upstream.example.com/v1"
	if err := v.BaseURL(&withPath); err == nil || err.Code != errcode.InvalidBaseURL {
		t.Fatalf("base_url with path: want %d, got %v", errcode.InvalidBaseURL, err)
	}
	ftp := "ftp://upstream.example.com"
	if err := v.BaseURL(&ftp); err == nil || err.Code != errcode.InvalidBaseURL {
		t.Fatalf("ftp base_url: want %d, got %v", errcode.InvalidBaseURL, err)
	}
	longURL := "https://" + strings.Repeat("a", BaseURLMaxLen)
	if err := v.BaseURL(&longURL); err == nil || err.Code != errcode.InvalidBaseURL {
		t.Fatalf("long base_url: want %d, got %v", errcode.InvalidBaseURL, err)
	}
}

func TestValidator_RuntimeFields(t *testing.T) {
	t.Parallel()
	v := NewValidator()

	valid := 30
	level := "info"
	rt := &RuntimeRequestBlock{
		LogRetentionDays: &valid,
		LogLevel:         &level,
	}
	if err := v.RuntimeFields(rt); err != nil {
		t.Fatalf("valid runtime rejected: %v", err)
	}

	bad := 0
	rt2 := &RuntimeRequestBlock{LogRetentionDays: &bad}
	if err := v.RuntimeFields(rt2); err == nil || err.Code != errcode.InvalidRetention {
		t.Fatalf("retention=0: want 2007, got %v", err)
	}
	tooBig := 366
	rt3 := &RuntimeRequestBlock{LogRetentionDays: &tooBig}
	if err := v.RuntimeFields(rt3); err == nil || err.Code != errcode.InvalidRetention {
		t.Fatalf("retention=366: want 2007, got %v", err)
	}

	badLevel := "verbose"
	rt4 := &RuntimeRequestBlock{LogLevel: &badLevel}
	if err := v.RuntimeFields(rt4); err == nil || err.Code != errcode.InvalidLogLevel {
		t.Fatalf("level=verbose: want 2014, got %v", err)
	}
}

func TestValidator_RuntimeRequired(t *testing.T) {
	t.Parallel()
	v := NewValidator()

	rt := &RuntimeRequestBlock{}
	empty := map[string]any{}
	if err := v.RuntimeRequired(rt, empty); err == nil || err.Code != errcode.MalformedBody {
		t.Fatalf("all missing: want 2008, got %v", err)
	}

	b := true
	d := 30
	l := "info"
	raw := map[string]any{
		"log_client_request_body":    true,
		"log_upstream_request_body":  true,
		"log_upstream_response_body": true,
		"log_retention_days":         30,
		"log_level":                  "info",
	}
	full := &RuntimeRequestBlock{
		LogClientRequestBody:    &b,
		LogUpstreamRequestBody:  &b,
		LogUpstreamResponseBody: &b,
		LogRetentionDays:        &d,
		LogLevel:                &l,
	}
	if err := v.RuntimeRequired(full, raw); err != nil {
		t.Fatalf("full runtime: unexpected err %v", err)
	}

	partialRaw := map[string]any{
		"log_client_request_body": true,
		"log_level":               "info",
	}
	partial := &RuntimeRequestBlock{LogClientRequestBody: &b, LogLevel: &l}
	if err := v.RuntimeRequired(partial, partialRaw); err == nil || err.Code != errcode.MalformedBody {
		t.Fatalf("partial runtime: want 2008, got %v", err)
	}
}

func TestValidator_Commit_Happy(t *testing.T) {
	t.Parallel()
	v := NewValidator()
	req := &CommitRequest{
		DB:           DBRequestBlock{Driver: "sqlite3", URL: "router.db"},
		FirstAccount: AccountRequestBlock{Name: "prod-01", Provider: "openai", APIKey: "sk-abc"},
		Plugins: PluginsRequestBlock{
			AdminAuth:  PluginFlagBlock{Enabled: false},
			ClientKeys: PluginFlagBlock{Enabled: false},
		},
	}
	if err := v.Commit(req); err != nil {
		t.Fatalf("valid commit rejected: %v", err)
	}
}

// setup-api.md v2.4 — the wizard may omit first_account so operators
// can defer upstream-account registration to the admin portal. An
// entirely-empty AccountRequestBlock MUST pass validation; a partially
// populated block MUST still fail with the per-field errcode.
func TestValidator_Commit_FirstAccountOptional(t *testing.T) {
	t.Parallel()
	v := NewValidator()

	t.Run("fully empty passes", func(t *testing.T) {
		t.Parallel()
		req := &CommitRequest{
			DB:           DBRequestBlock{Driver: "sqlite3", URL: "router.db"},
			FirstAccount: AccountRequestBlock{}, // every field zero-valued
			Plugins:      PluginsRequestBlock{},
		}
		if err := v.Commit(req); err != nil {
			t.Fatalf("empty first_account rejected: %v", err)
		}
		if !req.FirstAccount.IsEmpty() {
			t.Fatalf("IsEmpty() should be true for zero-valued block")
		}
	})

	t.Run("partial still fails", func(t *testing.T) {
		t.Parallel()
		req := &CommitRequest{
			DB: DBRequestBlock{Driver: "sqlite3", URL: "router.db"},
			FirstAccount: AccountRequestBlock{
				Name:     "prod",
				Provider: "openai",
				APIKey:   "", // ← operator forgot the key; must 2005
			},
			Plugins: PluginsRequestBlock{},
		}
		err := v.Commit(req)
		if err == nil {
			t.Fatalf("partial first_account accepted: expected 2005")
		}
		if err.Code != errcode.InvalidAPIKey || err.Field != "first_account.api_key" {
			t.Fatalf("partial first_account: want 2005/first_account.api_key, got %d/%s",
				err.Code, err.Field)
		}
	})

	t.Run("BaseURL-only still counts as partial", func(t *testing.T) {
		t.Parallel()
		url := "https://api.openai.com"
		req := &CommitRequest{
			DB:           DBRequestBlock{Driver: "sqlite3", URL: "router.db"},
			FirstAccount: AccountRequestBlock{BaseURL: &url},
			Plugins:      PluginsRequestBlock{},
		}
		if req.FirstAccount.IsEmpty() {
			t.Fatalf("IsEmpty() should be false when BaseURL is set")
		}
		err := v.Commit(req)
		if err == nil {
			t.Fatalf("base_url-only first_account accepted: expected 2004")
		}
		if err.Code != errcode.InvalidAccountName {
			t.Fatalf("base_url-only: want 2004, got %d", err.Code)
		}
	})

	// Name-only (provider missing) must fall through Name validation
	// and surface 2015 invalid_account_provider — this pins the
	// deterministic per-field order documented in setup-api.md v2.4.
	t.Run("name-only fails on provider 2015", func(t *testing.T) {
		t.Parallel()
		req := &CommitRequest{
			DB:           DBRequestBlock{Driver: "sqlite3", URL: "router.db"},
			FirstAccount: AccountRequestBlock{Name: "prod"},
			Plugins:      PluginsRequestBlock{},
		}
		if req.FirstAccount.IsEmpty() {
			t.Fatalf("IsEmpty() should be false when Name is set")
		}
		err := v.Commit(req)
		if err == nil {
			t.Fatalf("name-only first_account accepted: expected 2015")
		}
		if err.Code != errcode.InvalidAccountProvider {
			t.Fatalf("name-only: want 2015, got %d", err.Code)
		}
	})

	// Malformed base_url must surface 2016 invalid_base_url (and NOT
	// be swallowed by the optional-block skip). Guards against a
	// regression where IsEmpty()==false but partial validation is
	// accidentally bypassed.
	t.Run("malformed base_url fails on 2016", func(t *testing.T) {
		t.Parallel()
		bad := "not-a-url"
		req := &CommitRequest{
			DB: DBRequestBlock{Driver: "sqlite3", URL: "router.db"},
			FirstAccount: AccountRequestBlock{
				Name:     "prod",
				Provider: "openai",
				APIKey:   "sk-abc",
				BaseURL:  &bad,
			},
			Plugins: PluginsRequestBlock{},
		}
		err := v.Commit(req)
		if err == nil {
			t.Fatalf("malformed base_url accepted: expected 2016")
		}
		if err.Code != errcode.InvalidBaseURL {
			t.Fatalf("malformed base_url: want 2016, got %d", err.Code)
		}
	})
}

func TestValidator_Commit_EveryCodeReachable(t *testing.T) {
	t.Parallel()
	v := NewValidator()

	// Each case crafts a minimally-broken payload and asserts the
	// single (code, field) tuple that should bubble up. Ensures
	// every registered 2002/2003/2004/2005/2007/2008/2014 code
	// has a reaching input.
	baseValid := func() *CommitRequest {
		return &CommitRequest{
			DB:           DBRequestBlock{Driver: "sqlite3", URL: "router.db"},
			FirstAccount: AccountRequestBlock{Name: "prod", Provider: "openai", APIKey: "sk-abc"},
			Plugins:      PluginsRequestBlock{},
		}
	}

	cases := []struct {
		name      string
		mutate    func(*CommitRequest)
		wantCode  int
		wantField string
	}{
		{
			name:     "bad driver 2002",
			mutate:   func(r *CommitRequest) { r.DB.Driver = "nope" },
			wantCode: errcode.InvalidDriver, wantField: "db.driver",
		},
		{
			name:     "empty dsn 2003",
			mutate:   func(r *CommitRequest) { r.DB.URL = "" },
			wantCode: errcode.InvalidDSN, wantField: "db.url",
		},
		{
			name:     "bad account name 2004",
			mutate:   func(r *CommitRequest) { r.FirstAccount.Name = "has space" },
			wantCode: errcode.InvalidAccountName, wantField: "first_account.name",
		},
		{
			name:     "empty api_key 2005",
			mutate:   func(r *CommitRequest) { r.FirstAccount.APIKey = "" },
			wantCode: errcode.InvalidAPIKey, wantField: "first_account.api_key",
		},
		{
			name: "retention=0 → 2007",
			mutate: func(r *CommitRequest) {
				b := true
				d := 0
				l := "info"
				r.Runtime = &RuntimeRequestBlock{
					LogClientRequestBody:    &b,
					LogUpstreamRequestBody:  &b,
					LogUpstreamResponseBody: &b,
					LogRetentionDays:        &d,
					LogLevel:                &l,
				}
				r.RawRuntime = map[string]any{
					"log_client_request_body":    true,
					"log_upstream_request_body":  true,
					"log_upstream_response_body": true,
					"log_retention_days":         0,
					"log_level":                  "info",
				}
			},
			wantCode: errcode.InvalidRetention, wantField: "runtime.log_retention_days",
		},
		{
			name: "partial runtime → 2008",
			mutate: func(r *CommitRequest) {
				b := true
				r.Runtime = &RuntimeRequestBlock{LogClientRequestBody: &b}
				r.RawRuntime = map[string]any{"log_client_request_body": true}
			},
			wantCode: errcode.MalformedBody,
		},
		{
			name: "bad log level → 2014",
			mutate: func(r *CommitRequest) {
				b := true
				d := 30
				l := "verbose"
				r.Runtime = &RuntimeRequestBlock{
					LogClientRequestBody:    &b,
					LogUpstreamRequestBody:  &b,
					LogUpstreamResponseBody: &b,
					LogRetentionDays:        &d,
					LogLevel:                &l,
				}
				r.RawRuntime = map[string]any{
					"log_client_request_body":    true,
					"log_upstream_request_body":  true,
					"log_upstream_response_body": true,
					"log_retention_days":         30,
					"log_level":                  "verbose",
				}
			},
			wantCode: errcode.InvalidLogLevel, wantField: "runtime.log_level",
		},
		{
			name:     "bad provider → 2015",
			mutate:   func(r *CommitRequest) { r.FirstAccount.Provider = "anthropic" },
			wantCode: errcode.InvalidAccountProvider, wantField: "first_account.provider",
		},
		{
			name: "bad base_url → 2016",
			mutate: func(r *CommitRequest) {
				bad := "not-a-url"
				r.FirstAccount.BaseURL = &bad
			},
			wantCode: errcode.InvalidBaseURL, wantField: "first_account.base_url",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := baseValid()
			tc.mutate(req)
			got := v.Commit(req)
			if got == nil {
				t.Fatalf("want code=%d, got nil", tc.wantCode)
			}
			if got.Code != tc.wantCode {
				t.Errorf("code=%d want %d (msg=%q)", got.Code, tc.wantCode, got.Msg)
			}
			if tc.wantField != "" && got.Field != tc.wantField {
				t.Errorf("field=%q want %q", got.Field, tc.wantField)
			}
		})
	}
}

func TestApplyRuntimeDefaults(t *testing.T) {
	t.Parallel()
	got := ApplyRuntimeDefaults(nil)
	if got.LogLevel != "info" || got.LogRetentionDays != 30 ||
		got.LogClientRequestBody || got.LogUpstreamRequestBody || got.LogUpstreamResponseBody {
		t.Fatalf("nil runtime: defaults wrong %+v", got)
	}

	b := true
	d := 90
	l := "debug"
	got = ApplyRuntimeDefaults(&RuntimeRequestBlock{
		LogClientRequestBody: &b,
		LogRetentionDays:     &d,
		LogLevel:             &l,
	})
	if !got.LogClientRequestBody || got.LogRetentionDays != 90 || got.LogLevel != "debug" {
		t.Fatalf("partial override: %+v", got)
	}
	if got.LogUpstreamRequestBody {
		t.Fatalf("unset LogUpstreamRequestBody got %v, want default false", got.LogUpstreamRequestBody)
	}
	if got.LogUpstreamResponseBody {
		t.Fatalf("unset LogUpstreamResponseBody got %v, want default false", got.LogUpstreamResponseBody)
	}
}

func TestValidationError_Error(t *testing.T) {
	t.Parallel()
	ve := &ValidationError{Code: errcode.InvalidDriver, Msg: "x", Field: "db.driver"}
	got := ve.Error()
	if !strings.Contains(got, "x") || !strings.Contains(got, "db.driver") {
		t.Errorf("Error() = %q", got)
	}
	veNoField := &ValidationError{Code: errcode.MalformedBody, Msg: "bad"}
	if got := veNoField.Error(); got != "bad" {
		t.Errorf("Error() w/o field = %q want bad", got)
	}
}
