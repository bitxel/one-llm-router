package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestExchange(t *testing.T) {
	t.Run("exchange code success", func(t *testing.T) {
		var gotForm url.Values
		var gotAuthorization string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/oauth/token", r.URL.Path)
			assert.Equal(t, formContentTypeURLEncoded, r.Header.Get("Content-Type"))
			gotAuthorization = r.Header.Get("Authorization")

			body, err := io.ReadAll(r.Body)
			if !assert.NoError(t, err) {
				return
			}
			gotForm, err = url.ParseQuery(string(body))
			if !assert.NoError(t, err) {
				return
			}

			_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","id_token":"eyJ","expires_in":3600}`))
		}))
		t.Cleanup(srv.Close)

		now := time.Date(2026, 4, 21, 11, 12, 13, 0, time.UTC)
		provider := newTestOpenAIProvider(srv.URL + "/oauth/token")
		provider.now = func() time.Time { return now }

		tokens, err := provider.ExchangeCode(context.Background(), "auth-code", "verifier")
		if !assert.NoError(t, err) {
			return
		}

		assert.Empty(t, gotAuthorization)
		assert.Equal(t, "authorization_code", gotForm.Get("grant_type"))
		assert.Equal(t, "auth-code", gotForm.Get("code"))
		assert.Equal(t, "verifier", gotForm.Get("code_verifier"))
		assert.Equal(t, openAIRedirectURI, gotForm.Get("redirect_uri"))
		assert.Equal(t, openAIClientID, gotForm.Get("client_id"))

		assert.Equal(t, []byte("at"), tokens.AccessToken)
		assert.Equal(t, []byte("rt"), tokens.RefreshToken)
		assert.Equal(t, []byte("eyJ"), tokens.IDToken)
		assert.Equal(t, time.Hour, tokens.ExpiresIn)
		assert.Equal(t, now, tokens.LastRefresh)
	})

	t.Run("exchange code ignores non canonical redirect uri overrides", func(t *testing.T) {
		var gotForm url.Values
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if !assert.NoError(t, err) {
				return
			}
			gotForm, err = url.ParseQuery(string(body))
			if !assert.NoError(t, err) {
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","id_token":"eyJ","expires_in":60}`))
		}))
		t.Cleanup(srv.Close)

		provider := newTestOpenAIProvider(srv.URL + "/oauth/token")
		provider.redirectURI = "http://localhost:1456/auth/callback"

		_, err := provider.ExchangeCode(context.Background(), "code", "verifier")
		if !assert.NoError(t, err) {
			return
		}
		assert.Equal(t, openAIRedirectURI, gotForm.Get("redirect_uri"))
	})

	t.Run("refresh success", func(t *testing.T) {
		var gotForm url.Values

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if !assert.NoError(t, err) {
				return
			}
			gotForm, err = url.ParseQuery(string(body))
			if !assert.NoError(t, err) {
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"new-at","refresh_token":"new-rt","id_token":"new-id","expires_in":120}`))
		}))
		t.Cleanup(srv.Close)

		provider := newTestOpenAIProvider(srv.URL + "/oauth/token")
		tokens, err := provider.Refresh(context.Background(), []byte("refresh-me"))
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, "refresh_token", gotForm.Get("grant_type"))
		assert.Equal(t, "refresh-me", gotForm.Get("refresh_token"))
		assert.Equal(t, openAIClientID, gotForm.Get("client_id"))
		assert.Empty(t, gotForm.Get("scope"))
		assert.Equal(t, []byte("new-at"), tokens.AccessToken)
	})

	t.Run("invalid grant returns token exchange error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"expired"}`))
		}))
		t.Cleanup(srv.Close)

		provider := newTestOpenAIProvider(srv.URL + "/oauth/token")
		_, err := provider.ExchangeCode(context.Background(), "bad-code", "verifier")
		if !assert.Error(t, err) {
			return
		}

		var exchangeErr *TokenExchangeError
		if assert.True(t, errors.As(err, &exchangeErr)) {
			assert.Equal(t, "invalid_grant", exchangeErr.Code())
			assert.Equal(t, "expired", exchangeErr.Message())
			assert.Equal(t, http.StatusBadRequest, exchangeErr.HTTPStatus())
			assert.False(t, exchangeErr.Retryable())
		}
	})

	t.Run("server error is retryable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"server_error","error_description":"temporary"}`))
		}))
		t.Cleanup(srv.Close)

		provider := newTestOpenAIProvider(srv.URL + "/oauth/token")
		_, err := provider.ExchangeCode(context.Background(), "code", "verifier")
		if !assert.Error(t, err) {
			return
		}

		var exchangeErr *TokenExchangeError
		if assert.True(t, errors.As(err, &exchangeErr)) {
			assert.Equal(t, "server_error", exchangeErr.Code())
			assert.True(t, exchangeErr.Retryable())
			assert.Equal(t, http.StatusBadGateway, exchangeErr.HTTPStatus())
		}
	})

	t.Run("non json response becomes invalid_response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`not-json`))
		}))
		t.Cleanup(srv.Close)

		provider := newTestOpenAIProvider(srv.URL + "/oauth/token")
		_, err := provider.ExchangeCode(context.Background(), "code", "verifier")
		if !assert.Error(t, err) {
			return
		}

		var exchangeErr *TokenExchangeError
		if assert.True(t, errors.As(err, &exchangeErr)) {
			assert.Equal(t, "invalid_response", exchangeErr.Code())
			assert.Equal(t, http.StatusBadRequest, exchangeErr.HTTPStatus())
		}
	})

	t.Run("response body over 64kb becomes invalid_response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(strings.Repeat("x", oauthResponseBodyLimit+1)))
		}))
		t.Cleanup(srv.Close)

		provider := newTestOpenAIProvider(srv.URL + "/oauth/token")
		_, err := provider.ExchangeCode(context.Background(), "code", "verifier")
		if !assert.Error(t, err) {
			return
		}
		var exchangeErr *TokenExchangeError
		if assert.True(t, errors.As(err, &exchangeErr)) {
			assert.Equal(t, "invalid_response", exchangeErr.Code())
		}
	})

	t.Run("timeout respects 10 second client timeout", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(11 * time.Second)
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)

		provider := newTestOpenAIProvider(srv.URL + "/oauth/token")

		start := time.Now()
		_, err := provider.ExchangeCode(context.Background(), "code", "verifier")
		elapsed := time.Since(start)

		if !assert.Error(t, err) {
			return
		}
		var exchangeErr *TokenExchangeError
		if assert.True(t, errors.As(err, &exchangeErr)) {
			assert.Equal(t, "request_failed", exchangeErr.Code())
			assert.Zero(t, exchangeErr.HTTPStatus())
		}
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.GreaterOrEqual(t, elapsed, 10*time.Second)
		assert.Less(t, elapsed, 11*time.Second+500*time.Millisecond)
	})

	t.Run("redaction helpers do not leak secrets", func(t *testing.T) {
		tokens := Tokens{
			AccessToken:  []byte("access-secret"),
			RefreshToken: []byte("refresh-secret"),
			IDToken:      []byte("id-secret"),
		}

		assert.Equal(t, "<redacted>", tokens.String())
		assert.NotContains(t, tokens.String(), "access-secret")
		assert.NotContains(t, strings.ToLower((&TokenExchangeError{code: "invalid_grant", message: "bad"}).Error()), "access-secret")
	})

	t.Run("logs do not leak token or response body bytes", func(t *testing.T) {
		var logs bytes.Buffer
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"access-secret refresh-secret id-secret"}`))
		}))
		t.Cleanup(srv.Close)

		provider := &openAIProvider{
			tokenURL: srv.URL + "/oauth/token",
			logger:   slog.New(slog.NewTextHandler(&logs, nil)),
		}

		_, err := provider.ExchangeCode(context.Background(), "code", "verifier")
		assert.Error(t, err)
		assert.Empty(t, logs.String())
		assert.NotContains(t, err.Error(), "refresh_token")
		assert.NotContains(t, err.Error(), "id_token")
	})

	t.Run("device poll access denied in payload beats transient 403 handling", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/api/accounts/deviceauth/token", r.URL.Path)
			assert.Equal(t, jsonContentType, r.Header.Get("Content-Type"))
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"access_denied","error_description":"denied by operator"}`))
		}))
		t.Cleanup(srv.Close)

		provider := &openAIProvider{
			deviceTokenURL: srv.URL + "/api/accounts/deviceauth/token",
			logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		}

		_, err := provider.PollDeviceCode(context.Background(), "device-auth-id", "ABCD-1234")
		if !assert.Error(t, err) {
			return
		}

		var exchangeErr *TokenExchangeError
		if assert.True(t, errors.As(err, &exchangeErr)) {
			assert.Equal(t, "access_denied", exchangeErr.Code())
			assert.Equal(t, "denied by operator", exchangeErr.Message())
			assert.Equal(t, http.StatusForbidden, exchangeErr.HTTPStatus())
		}
	})

	t.Run("device poll terminal status field is surfaced as provider error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"status":"expired_token","error_description":"device code expired"}`))
		}))
		t.Cleanup(srv.Close)

		provider := &openAIProvider{
			deviceTokenURL: srv.URL + "/api/accounts/deviceauth/token",
			logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		}

		_, err := provider.PollDeviceCode(context.Background(), "device-auth-id", "ABCD-1234")
		if !assert.Error(t, err) {
			return
		}

		var exchangeErr *TokenExchangeError
		if assert.True(t, errors.As(err, &exchangeErr)) {
			assert.Equal(t, "expired_token", exchangeErr.Code())
			assert.Equal(t, "device code expired", exchangeErr.Message())
			assert.Equal(t, http.StatusBadRequest, exchangeErr.HTTPStatus())
		}
	})

	t.Run("request device code success defaults interval and requires expires_in", func(t *testing.T) {
		var gotBody map[string]string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/api/accounts/deviceauth/usercode", r.URL.Path)
			assert.Equal(t, jsonContentType, r.Header.Get("Content-Type"))

			body, err := io.ReadAll(r.Body)
			if !assert.NoError(t, err) {
				return
			}
			if !assert.NoError(t, json.Unmarshal(body, &gotBody)) {
				return
			}

			_, _ = w.Write([]byte(`{"device_auth_id":"dev_auth_123","user_code":"ABCD-1234","verification_uri":"http://127.0.0.1:40123/codex/device","expires_in":900}`))
		}))
		t.Cleanup(srv.Close)

		provider := &openAIProvider{
			deviceCodeURL: srv.URL + "/api/accounts/deviceauth/usercode",
			logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		}

		deviceCode, err := provider.RequestDeviceCode(context.Background())
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, map[string]string{"client_id": openAIClientID}, gotBody)
		assert.Equal(t, "dev_auth_123", deviceCode.DeviceAuthID)
		assert.Equal(t, "ABCD-1234", deviceCode.UserCode)
		assert.Equal(t, "http://127.0.0.1:40123/codex/device", deviceCode.VerificationURL)
		assert.Equal(t, defaultDevicePollInterval, deviceCode.Interval)
		assert.Equal(t, 15*time.Minute, deviceCode.ExpiresIn)
	})

	t.Run("request device code missing expires_in is invalid_response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"device_auth_id":"dev_auth_123","user_code":"ABCD-1234","verification_uri":"https://example.com/device","interval":5}`))
		}))
		t.Cleanup(srv.Close)

		provider := &openAIProvider{
			deviceCodeURL: srv.URL,
			logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		}

		_, err := provider.RequestDeviceCode(context.Background())
		if !assert.Error(t, err) {
			return
		}

		var exchangeErr *TokenExchangeError
		if assert.True(t, errors.As(err, &exchangeErr)) {
			assert.Equal(t, "invalid_response", exchangeErr.Code())
			assert.Contains(t, exchangeErr.Message(), "missing expires_in")
			assert.Equal(t, http.StatusOK, exchangeErr.HTTPStatus())
		}
	})

	t.Run("request device code missing verification_uri falls back to auth base url", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"device_auth_id":"dev_auth_123","user_code":"ABCD-1234","expires_in":900}`))
		}))
		t.Cleanup(srv.Close)

		provider := &openAIProvider{
			deviceCodeURL: srv.URL,
			logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		}

		deviceCode, err := provider.RequestDeviceCode(context.Background())
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, "dev_auth_123", deviceCode.DeviceAuthID)
		assert.Equal(t, "ABCD-1234", deviceCode.UserCode)
		assert.Equal(t, srv.URL+"/codex/device", deviceCode.VerificationURL)
		assert.Equal(t, 15*time.Minute, deviceCode.ExpiresIn)
	})

	t.Run("request device code missing verification_uri preserves configured path prefix", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/router/api/accounts/deviceauth/usercode", r.URL.Path)
			_, _ = w.Write([]byte(`{"device_auth_id":"dev_auth_path_123","user_code":"WXYZ-9876","expires_in":900}`))
		}))
		t.Cleanup(srv.Close)

		provider := &openAIProvider{
			deviceCodeURL:  srv.URL + "/router/api/accounts/deviceauth/usercode",
			deviceTokenURL: srv.URL + "/router/api/accounts/deviceauth/token",
			logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		}

		deviceCode, err := provider.RequestDeviceCode(context.Background())
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, "dev_auth_path_123", deviceCode.DeviceAuthID)
		assert.Equal(t, "WXYZ-9876", deviceCode.UserCode)
		assert.Equal(t, srv.URL+"/router/codex/device", deviceCode.VerificationURL)
		assert.Equal(t, 15*time.Minute, deviceCode.ExpiresIn)
	})

	t.Run("request device code 404 maps to device auth unavailable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error_description":"plan required"}`))
		}))
		t.Cleanup(srv.Close)

		provider := &openAIProvider{
			deviceCodeURL: srv.URL,
			logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		}

		_, err := provider.RequestDeviceCode(context.Background())
		if !assert.Error(t, err) {
			return
		}

		var exchangeErr *TokenExchangeError
		if assert.True(t, errors.As(err, &exchangeErr)) {
			assert.Equal(t, "device_auth_unavailable", exchangeErr.Code())
			assert.Equal(t, "plan required", exchangeErr.Message())
			assert.Equal(t, http.StatusNotFound, exchangeErr.HTTPStatus())
		}
	})

	t.Run("device poll success posts device auth body then exchanges tokens", func(t *testing.T) {
		var gotPollBody map[string]string
		var gotTokenForm url.Values

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/accounts/deviceauth/token":
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, jsonContentType, r.Header.Get("Content-Type"))
				body, err := io.ReadAll(r.Body)
				if !assert.NoError(t, err) {
					return
				}
				if !assert.NoError(t, json.Unmarshal(body, &gotPollBody)) {
					return
				}
				_, _ = w.Write([]byte(`{"authorization_code":"auth-code","code_verifier":"device-verifier"}`))
			case "/oauth/token":
				assert.Equal(t, formContentTypeURLEncoded, r.Header.Get("Content-Type"))
				body, err := io.ReadAll(r.Body)
				if !assert.NoError(t, err) {
					return
				}
				gotTokenForm, err = url.ParseQuery(string(body))
				if !assert.NoError(t, err) {
					return
				}
				_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","id_token":"eyJ","expires_in":120}`))
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(srv.Close)

		now := time.Date(2026, 4, 22, 18, 0, 0, 0, time.UTC)
		provider := &openAIProvider{
			deviceTokenURL: srv.URL + "/api/accounts/deviceauth/token",
			tokenURL:       srv.URL + "/oauth/token",
			logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
			now:            func() time.Time { return now },
		}

		tokens, err := provider.PollDeviceCode(context.Background(), "device-auth-id", "ABCD-1234")
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, map[string]string{
			"device_auth_id": "device-auth-id",
			"user_code":      "ABCD-1234",
		}, gotPollBody)
		assert.Equal(t, "authorization_code", gotTokenForm.Get("grant_type"))
		assert.Equal(t, "auth-code", gotTokenForm.Get("code"))
		assert.Equal(t, "device-verifier", gotTokenForm.Get("code_verifier"))
		assert.Equal(t, openAIRedirectURI, gotTokenForm.Get("redirect_uri"))
		assert.Equal(t, openAIClientID, gotTokenForm.Get("client_id"))
		assert.Equal(t, []byte("at"), tokens.AccessToken)
		assert.Equal(t, now, tokens.LastRefresh)
	})
}

func newTestOpenAIProvider(tokenURL string) *openAIProvider {
	return &openAIProvider{
		tokenURL: tokenURL,
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}
