// Package proxydial owns the router's single outbound-proxy decision.
//
// Feature 009 keeps the model deliberately small: ONE global proxy URL
// (config.json `network.proxy_url`, managed via the Settings API) and
// ONE per-account opt-in switch (upstream_accounts.use_proxy). This
// package holds the process-wide slot for the configured proxy so every
// egress family (data-plane forwarder, OAuth control plane, model
// refresher, usage fetch) shares one source of truth with lock-free hot
// reload — settings updates swap the slot atomically and in-flight
// connections finish on their original path.
//
// Environment variables (HTTP_PROXY/HTTPS_PROXY/...) are deliberately
// ignored: every consumer installs an explicit Proxy func (or none), so
// the settings value is the only thing that can change egress. See
// specs/009-outbound-proxy/spec.md §US-5.
//
// Secret hygiene: proxy URLs may carry user:password. Everything that
// crosses a log or API boundary MUST go through MaskedURL; the raw URL
// is only ever handled by Configure and the http.Transport layer.
package proxydial

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// ErrInvalidProxyURL reports a proxy_url that failed validation. Wrap
// target for the 9001 invalid_proxy_url envelope code.
var ErrInvalidProxyURL = errors.New("proxydial: invalid proxy url")

// ErrProxyRequired reports that egress was requested through a proxy
// (account use_proxy=true) but no global proxy is configured. The
// fail-fast defence for stale opt-ins — callers surface it as an
// upstream connection error, never as a silent direct fallback.
// Wrap target for the 9002 proxy_url_required envelope code on the
// admin write path, and for the data-plane 502-class failure.
var ErrProxyRequired = errors.New("proxydial: account use_proxy=true but no outbound proxy is configured")

// ProxyRequiredHint is the actionable operator guidance used by egress
// fail-fast diagnostics and the 9002 envelope detail.
const ProxyRequiredHint = "disable use_proxy for this account or configure network.proxy_url"

// supportedSchemes is exactly what net/http's Transport understands for
// Proxy URLs ("http", "https", "socks5", "socks5h" — socks5 is treated
// the same as socks5h, and userinfo credentials are honoured natively).
// Verified against the Go 1.25 source docs; keep in sync when bumping.
var supportedSchemes = map[string]struct{}{
	"http":    {},
	"https":   {},
	"socks5":  {},
	"socks5h": {},
}

// slot is the process-wide proxy URL. nil means direct egress. Swapped
// atomically by Configure so http.Transport.Proxy closures can read it
// without locks on the hot dial path.
var slot atomic.Pointer[url.URL]

// Configure validates rawURL and installs it as the process-wide
// outbound proxy. An empty (or whitespace-only) rawURL clears the
// slot. An invalid URL returns ErrInvalidProxyURL and leaves the
// previously-installed value untouched.
func Configure(rawURL string) error {
	parsed, err := ParseAndValidate(rawURL)
	if err != nil {
		return err
	}
	if parsed == nil {
		slot.Store(nil)
		return nil
	}
	slot.Store(parsed)
	return nil
}

// Current returns the configured proxy URL, or nil when egress is
// direct.
func Current() *url.URL { return slot.Load() }

// Configured reports whether a proxy is currently installed. The
// settings projection and the admin write-path validation both read
// this instead of poking the pointer directly.
func Configured() bool { return slot.Load() != nil }

// ProxyFunc returns the stable Proxy closure for proxy-aware transports.
// The returned func reads the atomic slot on every dial, so Configure
// hot-swaps egress without rebuilding any Transport. An empty slot is an
// error here, rather than direct egress, because this function is only
// installed on clients selected for proxy-required traffic.
func ProxyFunc() func(*http.Request) (*url.URL, error) {
	return func(*http.Request) (*url.URL, error) {
		if proxy := slot.Load(); proxy != nil {
			return proxy, nil
		}
		return nil, ErrProxyRequired
	}
}

// Decide resolves one egress decision: useProxy=false (or an unset
// slot) → direct; useProxy=true with a configured proxy → that proxy;
// useProxy=true without a configured proxy → ErrProxyRequired (the
// stale-opt-in defence; never silently direct).
func Decide(useProxy bool) (*url.URL, error) {
	if !useProxy {
		return nil, nil
	}
	if u := slot.Load(); u != nil {
		return u, nil
	}
	return nil, ErrProxyRequired
}

// ParseAndValidate implements the deliberately thin 009 validation
// rule: scheme allow-list + url.Parse + non-empty host. Anything
// stricter (port ranges, length caps, query rejection) was cut by the
// over-engineering review — an invalid port simply fails at dial time
// with an equally actionable error. An empty input is legal and
// yields (nil, nil) meaning "no proxy".
func ParseAndValidate(raw string) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		// url.Parse wraps some failures with the complete input URL. Do not
		// expose that value because it may contain proxy credentials.
		return nil, fmt.Errorf("%w: malformed URL", ErrInvalidProxyURL)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if _, ok := supportedSchemes[strings.ToLower(parsed.Scheme)]; !ok {
		return nil, fmt.Errorf(
			"%w: scheme %q not supported (use http, https, socks5 or socks5h)",
			ErrInvalidProxyURL, parsed.Scheme)
	}
	if parsed.Hostname() == "" {
		return nil, fmt.Errorf("%w: missing host", ErrInvalidProxyURL)
	}
	return parsed, nil
}

// maskedPassword is the fixed stand-in for proxy credentials on any
// log or API boundary.
const maskedPassword = "*****"

// MaskedURL renders a proxy URL safe for logs and API projections:
// scheme://user:*****@host:port when credentials are present, the
// unchanged URL otherwise. Unparsable input renders as a fixed
// placeholder (never the raw bytes). Assembled manually because
// url.UserPassword(...).String() percent-escapes the mask.
func MaskedURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "<invalid-proxy-url>"
	}
	if parsed.User == nil {
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		return parsed.String()
	}
	// Username-only auth: nothing to mask beyond what's already public.
	if _, hasPassword := parsed.User.Password(); !hasPassword {
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		return parsed.String()
	}
	return strings.ToLower(parsed.Scheme) + "://" +
		url.User(parsed.User.Username()).String() + ":" + maskedPassword +
		"@" + parsed.Host + parsed.EscapedPath() + querySuffix(parsed.RawQuery)
}

// querySuffix appends "?query" without the implicit "/" that
// RequestURI() would synthesise for path-less URLs.
func querySuffix(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	return "?" + rawQuery
}

// connectivityTarget is the fixed upstream the 009 connectivity test
// dials through a candidate proxy. Any HTTP status (401/403/404 still
// prove the tunnel reached the upstream) counts as reachable; only
// transport-level failures fail the test. Package var so tests can
// point it at a local server.
var connectivityTarget = "https://api.openai.com/v1/models"

// ConnectivityTestTimeout is the dial/TLS/response budget for one
// proxy connectivity test (10s — generous for socks5 handshakes but
// bounded so the admin test endpoint never hangs).
const ConnectivityTestTimeout = 10 * time.Second

// ConnectivityError reports a failed proxy connectivity test. Reason
// is a stable, coarse, operator-grep-able classification ("timeout",
// "connection_refused", "proxy_auth_failed", "dns_failed",
// "tls_failed", "network_error", "unreachable") intended for the 9003
// envelope's detail field. The wrapped Err may embed the TARGET URL
// (safe) but never proxy credentials, and is for logs only.
type ConnectivityError struct {
	Reason string
	Err    error
}

func (e *ConnectivityError) Error() string {
	return fmt.Sprintf("proxydial: connectivity test failed: %s: %v", e.Reason, e.Err)
}

func (e *ConnectivityError) Unwrap() error { return e.Err }

// TestConnectivity dials connectivityTarget through rawProxyURL with a
// fresh Transport (the candidate is the uncommitted settings value, so
// the process-wide slot is deliberately NOT touched) and reports
// whether the proxy path works. nil means reachable. An invalid or
// empty URL returns ErrInvalidProxyURL; a transport failure returns a
// *ConnectivityError carrying the coarse Reason.
func TestConnectivity(ctx context.Context, rawProxyURL string, timeout time.Duration) error {
	parsed, err := ParseAndValidate(rawProxyURL)
	if err != nil {
		return err
	}
	if parsed == nil {
		return fmt.Errorf("%w: empty proxy url", ErrInvalidProxyURL)
	}

	tr := &http.Transport{
		Proxy:                 http.ProxyURL(parsed),
		DialContext:           (&net.Dialer{Timeout: timeout, KeepAlive: 0}).DialContext,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
	}
	defer tr.CloseIdleConnections()

	client := &http.Client{
		Transport: tr,
		Timeout:   timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, connectivityTarget, nil)
	if err != nil {
		return fmt.Errorf("%w: build test request: %w", ErrInvalidProxyURL, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return &ConnectivityError{Reason: classifyConnectivityError(err), Err: err}
	}
	_ = resp.Body.Close()
	return nil
}

// classifyConnectivityError maps a Transport error to the stable coarse
// Reason used by the 9003 envelope. Deliberately string-based at the
// tail so unexpected error shapes degrade to "unreachable" instead of
// leaking raw bytes (which could embed a misdialed proxy host).
func classifyConnectivityError(err error) string {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns_failed"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "connection refused") || strings.Contains(msg, "refused"):
		return "connection_refused"
	case strings.Contains(msg, "407") || strings.Contains(msg, "proxy authentication"):
		return "proxy_auth_failed"
	case strings.Contains(msg, "certificate") || strings.Contains(msg, "x509") || strings.Contains(msg, "tls"):
		return "tls_failed"
	case strings.Contains(msg, "no such host") || strings.Contains(msg, "unknown host"):
		return "dns_failed"
	}
	return "network_error"
}
