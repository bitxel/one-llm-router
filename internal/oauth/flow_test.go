package oauth

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const codeChallengeForV = "TJRIXgwhrmxBzh3-e2v6zupato5AokdvUCCOUm9QYIA"

func TestFlowTypes(t *testing.T) {
	t.Run("enum values", func(t *testing.T) {
		assert.Equal(t, FlowMethod("browser"), FlowBrowser)
		assert.Equal(t, FlowMethod("device"), FlowDevice)
		assert.Equal(t, Rail(""), RailUnknown)
		assert.Equal(t, Rail("loopback"), RailLoopback)
		assert.Equal(t, Rail("manual_paste"), RailManualPaste)
		assert.Equal(t, FlowStatus("idle"), FlowStatusIdle)
		assert.Equal(t, FlowStatus("pending"), FlowStatusPending)
		assert.Equal(t, FlowStatus("success"), FlowStatusSuccess)
		assert.Equal(t, FlowStatus("error"), FlowStatusError)
	})

	t.Run("flow struct shape", func(t *testing.T) {
		flowType := reflect.TypeOf(Flow{})

		var exported []string
		for i := range flowType.NumField() {
			field := flowType.Field(i)
			if field.PkgPath == "" {
				exported = append(exported, field.Name)
			}
		}

		assert.Equal(t, []string{
			"ID",
			"Method",
			"ListenerBound",
			"Consumed",
			"ConsumedBy",
			"State",
			"CodeVerifier",
			"DeviceAuthID",
			"UserCode",
			"VerificationURL",
			"ExpiresAt",
			"PollInterval",
			"CallbackServer",
			"Status",
			"CreatedAt",
		}, exported)

		field, ok := flowType.FieldByName("Consumed")
		if assert.True(t, ok) {
			assert.Equal(t, reflect.TypeOf(atomic.Bool{}), field.Type)
		}

		field, ok = flowType.FieldByName("CallbackServer")
		if assert.True(t, ok) {
			assert.Equal(t, reflect.TypeOf((*http.Server)(nil)), field.Type)
		}

		field, ok = flowType.FieldByName("mu")
		if assert.True(t, ok) {
			assert.NotEmpty(t, field.PkgPath)
			assert.Equal(t, reflect.TypeOf(sync.RWMutex{}), field.Type)
		}
	})

	t.Run("build authorize url", func(t *testing.T) {
		got, err := openAIProvider{}.BuildAuthorizeURL("s", "v")
		if !assert.NoError(t, err) {
			return
		}

		parsed, err := url.Parse(got)
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, "https", parsed.Scheme)
		assert.Equal(t, "auth.openai.com", parsed.Host)
		assert.Equal(t, "/oauth/authorize", parsed.Path)

		query := parsed.Query()
		assert.Equal(t, openAIClientID, query.Get("client_id"))
		assert.Equal(t, openAIOriginator, query.Get("originator"))
		assert.Equal(t, openAIRedirectURI, query.Get("redirect_uri"))
		assert.Equal(t, openAIScope, query.Get("scope"))
		assert.Equal(t, openAIAuthorizeResponseType, query.Get("response_type"))
		assert.Equal(t, openAICodeChallengeMethodS256, query.Get("code_challenge_method"))
		assert.Equal(t, codeChallengeForV, query.Get("code_challenge"))
		assert.Equal(t, "s", query.Get("state"))
		assert.Equal(t, "true", query.Get("id_token_add_organizations"))
		assert.Equal(t, "true", query.Get("codex_cli_simplified_flow"))

		assert.Contains(t, got, "redirect_uri="+url.QueryEscape(openAIRedirectURI))
		assert.Contains(t, got, "scope=openid+profile+email+offline_access+api.connectors.read+api.connectors.invoke")
	})

	t.Run("build authorize url ignores non canonical redirect uri overrides", func(t *testing.T) {
		got, err := openAIProvider{redirectURI: "http://localhost:1456/auth/callback"}.BuildAuthorizeURL("s", "v")
		if !assert.NoError(t, err) {
			return
		}

		parsed, err := url.Parse(got)
		if !assert.NoError(t, err) {
			return
		}
		assert.Equal(t, openAIRedirectURI, parsed.Query().Get("redirect_uri"))
	})

	t.Run("build authorize url rejects missing state", func(t *testing.T) {
		_, err := openAIProvider{}.BuildAuthorizeURL("", "v")
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "state")
		}
	})

	t.Run("build authorize url rejects missing verifier", func(t *testing.T) {
		_, err := openAIProvider{}.BuildAuthorizeURL("s", "")
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "verifier")
		}
	})

	t.Run("tokens are redacted", func(t *testing.T) {
		tokens := Tokens{
			AccessToken:  []byte("access-secret"),
			RefreshToken: []byte("refresh-secret"),
			IDToken:      []byte("id-secret"),
			ExpiresIn:    time.Hour,
			LastRefresh:  time.Unix(1, 0).UTC(),
		}

		assert.Equal(t, "<redacted>", tokens.String())

		encoded, err := json.Marshal(tokens)
		if !assert.NoError(t, err) {
			return
		}

		var redacted string
		if assert.NoError(t, json.Unmarshal(encoded, &redacted)) {
			assert.Equal(t, "<redacted>", redacted)
		}
		assert.False(t, strings.Contains(string(encoded), "access-secret"))
		assert.False(t, strings.Contains(string(encoded), "refresh-secret"))
		assert.False(t, strings.Contains(string(encoded), "id-secret"))
	})
}
