package proxydial

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseAndValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		raw     string
		wantNil bool
		wantErr bool
	}{
		{name: "empty clears", raw: "", wantNil: true},
		{name: "whitespace clears", raw: "   ", wantNil: true},
		{name: "http ok", raw: "http://127.0.0.1:7890"},
		{name: "https ok", raw: "https://proxy.example.com"},
		{name: "socks5 ok", raw: "socks5://127.0.0.1:1080"},
		{name: "socks5h ok", raw: "socks5h://127.0.0.1:1080"},
		{name: "credentials ok", raw: "socks5://user:pass@10.0.0.1:1080"},
		{name: "uppercase scheme ok", raw: "SOCKS5://10.0.0.1:1080"},
		{name: "unknown scheme", raw: "ftp://10.0.0.1:21", wantErr: true},
		{name: "no scheme", raw: "10.0.0.1:1080", wantErr: true},
		{name: "missing host", raw: "socks5://", wantErr: true},
		{name: "garbage", raw: "://", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u, err := ParseAndValidate(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseAndValidate(%q) = nil error, want error", tc.raw)
				}
				if !errors.Is(err, ErrInvalidProxyURL) {
					t.Fatalf("ParseAndValidate(%q) error = %v, want ErrInvalidProxyURL", tc.raw, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseAndValidate(%q) unexpected error: %v", tc.raw, err)
			}
			if tc.wantNil && u != nil {
				t.Fatalf("ParseAndValidate(%q) = %v, want nil", tc.raw, u)
			}
			if !tc.wantNil && u == nil {
				t.Fatalf("ParseAndValidate(%q) = nil, want parsed URL", tc.raw)
			}
			if tc.name == "uppercase scheme ok" && u.Scheme != "socks5" {
				t.Fatalf("ParseAndValidate(%q) scheme = %q, want lowercase socks5", tc.raw, u.Scheme)
			}
		})
	}
}

func TestConfigure_Decide_TruthTable(t *testing.T) {
	if Configured() {
		t.Fatalf("slot starts configured in a fresh test binary state")
	}

	// Unconfigured: useProxy=true must fail fast, not silently direct.
	if _, err := Decide(true); !errors.Is(err, ErrProxyRequired) {
		t.Fatalf("Decide(true) with empty slot = %v, want ErrProxyRequired", err)
	}
	if u, err := Decide(false); err != nil || u != nil {
		t.Fatalf("Decide(false) with empty slot = (%v, %v), want (nil, nil)", u, err)
	}

	if err := Configure("socks5://user:secret@127.0.0.1:1080"); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if !Configured() {
		t.Fatal("Configured() = false after Configure, want true")
	}
	if u, err := Decide(true); err != nil || u == nil {
		t.Fatalf("Decide(true) configured = (%v, %v), want (proxy, nil)", u, err)
	}
	if u, err := Decide(false); err != nil || u != nil {
		t.Fatalf("Decide(false) configured = (%v, %v), want (nil, nil) — bypass wins", u, err)
	}

	// Invalid Configure leaves the previous slot untouched.
	if err := Configure("ftp://nope"); !errors.Is(err, ErrInvalidProxyURL) {
		t.Fatalf("Configure(invalid) = %v, want ErrInvalidProxyURL", err)
	}
	if !Configured() {
		t.Fatal("Configured() = false after rejected Configure, want previous slot kept")
	}

	// Clearing works.
	if err := Configure(""); err != nil {
		t.Fatalf("Configure(\"\"): %v", err)
	}
	if Configured() {
		t.Fatal("Configured() = true after clear, want false")
	}
}

func TestProxyFunc_ReadsHotSlot(t *testing.T) {
	proxyFn := ProxyFunc()
	if u, err := proxyFn(&http.Request{}); !errors.Is(err, ErrProxyRequired) || u != nil {
		t.Fatalf("ProxyFunc() before Configure = (%v, %v), want (nil, ErrProxyRequired)", u, err)
	}
	if err := Configure("http://127.0.0.1:7890"); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	u, _ := proxyFn(&http.Request{})
	if u == nil || u.Host != "127.0.0.1:7890" {
		t.Fatalf("ProxyFunc() after Configure = %v, want 127.0.0.1:7890", u)
	}
	if err := Configure(""); err != nil {
		t.Fatalf("Configure clear: %v", err)
	}
	if u, err := proxyFn(&http.Request{}); !errors.Is(err, ErrProxyRequired) || u != nil {
		t.Fatalf("ProxyFunc() after clear = (%v, %v), want (nil, ErrProxyRequired)", u, err)
	}
}

func TestMaskedURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want string
	}{
		{"", ""},
		{"socks5://127.0.0.1:7890", "socks5://127.0.0.1:7890"},
		{"socks5://user:secret@127.0.0.1:1080", "socks5://user:*****@127.0.0.1:1080"},
		{"SOCKS5://user:secret@127.0.0.1:1080", "socks5://user:*****@127.0.0.1:1080"},
		{"http://user@10.0.0.1:8080", "http://user@10.0.0.1:8080"},
		{"not a url\x7f", "<invalid-proxy-url>"},
	}
	for _, tc := range cases {
		if got := MaskedURL(tc.raw); got != tc.want {
			t.Errorf("MaskedURL(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestParseAndValidate_DoesNotExposeMalformedURL(t *testing.T) {
	errText := "socks5://user:secret%zz@proxy:1080"
	_, err := ParseAndValidate(errText)
	if !errors.Is(err, ErrInvalidProxyURL) {
		t.Fatalf("error = %v, want ErrInvalidProxyURL", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("error exposes proxy credentials: %v", err)
	}
}

func TestConfigure_ConcurrentHotSwap(t *testing.T) {
	proxyFn := ProxyFunc()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			raw := ""
			if i%2 == 0 {
				raw = "socks5://10.0.0.1:1080"
			}
			_ = Configure(raw)
			_, _ = proxyFn(&http.Request{})
		}(i)
	}
	wg.Wait()
	_ = Configure("")
}

// startForwardingProxy spins a local HTTP proxy that forwards
// absolute-form requests to their real destination, counting hits.
func startForwardingProxy(t *testing.T, hits *atomic.Int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		target := r.RequestURI
		if target == "" || target[0] == '/' {
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

func TestTestConnectivity(t *testing.T) {
	origTarget := connectivityTarget
	connectivityTarget = "http://unused.example.test/probe"
	t.Cleanup(func() { connectivityTarget = origTarget })

	t.Run("reachable through http proxy", func(t *testing.T) {
		var hits atomic.Int64
		proxy := startForwardingProxy(t, &hits)
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(target.Close)
		connectivityTarget = target.URL

		if err := TestConnectivity(t.Context(), proxy.URL, time.Second); err != nil {
			t.Fatalf("TestConnectivity(proxy) = %v, want nil", err)
		}
		if hits.Load() != 1 {
			t.Fatalf("proxy hits = %d, want 1", hits.Load())
		}
	})

	t.Run("credentials are honored by the transport", func(t *testing.T) {
		var hits atomic.Int64
		proxy := startForwardingProxy(t, &hits)
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(target.Close)
		connectivityTarget = target.URL

		u, _ := url.Parse(proxy.URL)
		u.User = url.UserPassword("user", "secret")
		if err := TestConnectivity(t.Context(), u.String(), time.Second); err != nil {
			t.Fatalf("TestConnectivity(proxy with creds) = %v, want nil", err)
		}
		if hits.Load() != 1 {
			t.Fatalf("proxy hits = %d, want 1", hits.Load())
		}
	})

	t.Run("unreachable proxy classifies as connection_refused", func(t *testing.T) {
		err := TestConnectivity(t.Context(), "http://127.0.0.1:1", time.Second)
		if err == nil {
			t.Fatal("TestConnectivity(dead proxy) = nil, want ConnectivityError")
		}
		var connErr *ConnectivityError
		if !errors.As(err, &connErr) {
			t.Fatalf("err = %T %v, want *ConnectivityError", err, err)
		}
		if connErr.Reason != "connection_refused" {
			t.Fatalf("reason = %q, want connection_refused", connErr.Reason)
		}
	})

	t.Run("invalid scheme is ErrInvalidProxyURL", func(t *testing.T) {
		err := TestConnectivity(t.Context(), "ftp://nope", time.Second)
		if !errors.Is(err, ErrInvalidProxyURL) {
			t.Fatalf("err = %v, want ErrInvalidProxyURL", err)
		}
	})

	t.Run("empty proxy url is ErrInvalidProxyURL", func(t *testing.T) {
		err := TestConnectivity(t.Context(), "", time.Second)
		if !errors.Is(err, ErrInvalidProxyURL) {
			t.Fatalf("err = %v, want ErrInvalidProxyURL", err)
		}
	})
}
