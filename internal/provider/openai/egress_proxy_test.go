package openai

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/proxydial"
)

// startForwardingProxy spins a local HTTP proxy: it counts hits and
// forwards absolute-form requests (the form net/http uses for http://
// targets behind an HTTP proxy) to the real destination.
func startForwardingProxy(t *testing.T, hits *atomic.Int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		target := r.RequestURI
		if target == "" || target[0] == '/' {
			// Relative form — nothing sensible to forward; fail loudly.
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		resp, err := http.Get(target)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func mustConfigureProxy(t *testing.T, raw string) {
	t.Helper()
	if err := proxydial.Configure(raw); err != nil {
		t.Fatalf("Configure(%q): %v", raw, err)
	}
	t.Cleanup(func() { _ = proxydial.Configure("") })
}

// TestClient_EgressFollowsAccountSwitch exercises the 009 truth table
// end-to-end through a local forwarding proxy. The slot is process
// global, so these cases run sequentially (no t.Parallel).
func TestClient_EgressFollowsAccountSwitch(t *testing.T) {
	var hits atomic.Int64
	proxy := startForwardingProxy(t, &hits)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(upstream.Close)

	client := NewClient(2 * time.Second)
	account := domain.UpstreamAccount{ID: 42, Name: "acct", Provider: domain.ProviderOpenAI, Status: domain.AccountStatusActive}
	orig := httptest.NewRequest(http.MethodPost, upstream.URL+"/v1/chat/completions", nil)
	orig.Header.Set("Content-Type", "application/json")

	upstreamURL := upstream.URL
	account.BaseURL = &upstreamURL

	// 1. use_proxy=false → direct, proxy untouched.
	resp, _, err := client.ForwardAccountRequestWithCapture(t.Context(), account, []byte("sk"), orig)
	if err != nil {
		t.Fatalf("direct forward: %v", err)
	}
	_ = resp.Body.Close()
	if hits.Load() != 0 {
		t.Fatalf("proxy hits = %d after direct forward, want 0", hits.Load())
	}

	// 2. opt-in + configured → through the proxy.
	mustConfigureProxy(t, proxy.URL)
	account.UseProxy = true
	resp, _, err = client.ForwardAccountRequestWithCapture(t.Context(), account, []byte("sk"), orig)
	if err != nil {
		t.Fatalf("proxied forward: %v", err)
	}
	_ = resp.Body.Close()
	if hits.Load() != 1 {
		t.Fatalf("proxy hits = %d after proxied forward, want 1", hits.Load())
	}

	// 3. bypass still wins even with the proxy configured.
	account.UseProxy = false
	resp, _, err = client.ForwardAccountRequestWithCapture(t.Context(), account, []byte("sk"), orig)
	if err != nil {
		t.Fatalf("bypass forward: %v", err)
	}
	_ = resp.Body.Close()
	if hits.Load() != 1 {
		t.Fatalf("proxy hits = %d after bypass forward, want 1", hits.Load())
	}

	// 4. dead proxy → fail fast as upstream connect error, never direct.
	mustConfigureProxy(t, "http://127.0.0.1:1")
	account.UseProxy = true
	resp, _, err = client.ForwardAccountRequestWithCapture(t.Context(), account, []byte("sk"), orig)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("dead proxy forward succeeded, want upstream connect failure")
	}
	if !errors.Is(err, ErrUpstreamConnectFailed) {
		t.Fatalf("dead proxy error = %v, want ErrUpstreamConnectFailed chain", err)
	}

	// 5. opt-in with nothing configured → ErrProxyRequired (fail-fast defence).
	mustConfigureProxy(t, "")
	_, _, err = client.ForwardAccountRequestWithCapture(t.Context(), account, []byte("sk"), orig) //nolint:bodyclose // no response body exists on this error path
	if !errors.Is(err, proxydial.ErrProxyRequired) {
		t.Fatalf("stale opt-in error = %v, want ErrProxyRequired chain", err)
	}
}
