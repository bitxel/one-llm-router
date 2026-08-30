package oauth

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/user/one-llm-router/internal/proxydial"
)

// TestOpenAIProvider_RefreshEgressFollowsAccountRow verifies that
// Refresh routes through the global proxy only when the account opted
// in (spec 009 §3.3), and fails as transient request_failed for a
// stale opt-in. The slot is process-global: sequential on purpose.
func TestOpenAIProvider_RefreshEgressFollowsAccountRow(t *testing.T) {
	var hits atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		resp, err := http.Get(r.RequestURI)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(proxy.Close)

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","id_token":"id","expires_in":120}`))
	}))
	t.Cleanup(tokenSrv.Close)

	// No injected HTTP client: the production egress pair must honour
	// the global slot.
	provider, err := NewOpenAIProvider(OpenAIProviderConfig{
		TokenURL:       tokenSrv.URL + "/oauth/token",
		AuthorizeURL:   tokenSrv.URL + "/oauth/authorize",
		DeviceCodeURL:  tokenSrv.URL + "/device/code",
		DeviceTokenURL: tokenSrv.URL + "/device/token",
	})
	if err != nil {
		t.Fatalf("NewOpenAIProvider: %v", err)
	}
	t.Cleanup(func() { _ = proxydial.Configure("") })

	// Not opted in → direct even with the proxy configured.
	mustConfigure(t, proxy.URL)
	if _, err := provider.Refresh(t.Context(), []byte("rt"), false); err != nil {
		t.Fatalf("direct refresh: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("proxy hits = %d after direct refresh, want 0", hits.Load())
	}

	// Opted in + configured → proxied.
	if _, err := provider.Refresh(t.Context(), []byte("rt"), true); err != nil {
		t.Fatalf("proxied refresh: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("proxy hits = %d after proxied refresh, want 1", hits.Load())
	}

	// Stale opt-in → transient request_failed, never a silent direct.
	mustConfigure(t, "")
	_, err = provider.Refresh(t.Context(), []byte("rt"), true)
	if err == nil {
		t.Fatal("stale opt-in refresh succeeded, want transient request_failed")
	}
	var exchangeErr *TokenExchangeError
	if !errors.As(err, &exchangeErr) {
		t.Fatalf("stale opt-in error = %v, want TokenExchangeError", err)
	}
	if exchangeErr.Code() != "request_failed" {
		t.Fatalf("code = %q, want request_failed (transient classification)", exchangeErr.Code())
	}
}

func mustConfigure(t *testing.T, raw string) {
	t.Helper()
	if err := proxydial.Configure(raw); err != nil {
		t.Fatalf("Configure(%q): %v", raw, err)
	}
	t.Cleanup(func() { _ = proxydial.Configure("") })
}

// newEgressTestProvider builds a production-shaped provider (no
// injected HTTP client) pointing its endpoints at the test token
// server.
func newEgressTestProvider(t *testing.T, baseURL string) Provider {
	t.Helper()
	provider, err := NewOpenAIProvider(OpenAIProviderConfig{
		TokenURL:       baseURL + "/oauth/token",
		AuthorizeURL:   baseURL + "/oauth/authorize",
		DeviceCodeURL:  baseURL + "/device/code",
		DeviceTokenURL: baseURL + "/device/token",
	})
	if err != nil {
		t.Fatalf("NewOpenAIProvider: %v", err)
	}
	return provider
}

// TestOpenAIProvider_BindEgressFollowsGlobalProxy covers spec 009
// §3.2: pre-account bind traffic (token exchange) routes through the
// global proxy when configured and dials direct otherwise.
func TestOpenAIProvider_BindEgressFollowsGlobalProxy(t *testing.T) {
	var hits atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		resp, err := http.Get(r.RequestURI)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(proxy.Close)

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","id_token":"id","expires_in":120}`))
	}))
	t.Cleanup(tokenSrv.Close)

	provider := newEgressTestProvider(t, tokenSrv.URL)
	mustConfigure(t, proxy.URL)

	// Bind with the proxy configured → through the proxy.
	if _, err := provider.ExchangeCode(t.Context(), "code-1", "verifier-1"); err != nil {
		t.Fatalf("proxied bind exchange: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("proxy hits = %d after proxied bind, want 1", hits.Load())
	}

	// Bind with the slot cleared → direct, proxy untouched.
	mustConfigure(t, "")
	if _, err := provider.ExchangeCode(t.Context(), "code-2", "verifier-2"); err != nil {
		t.Fatalf("direct bind exchange: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("proxy hits = %d after direct bind, want 1", hits.Load())
	}
}
