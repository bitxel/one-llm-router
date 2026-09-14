package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/proxydial"
)

var (
	ErrUpstreamConnectFailed   = errors.New("upstream connect failed")
	ErrUpstreamTimeout         = errors.New("upstream timeout")
	ErrInvalidUpstreamRequest  = errors.New("invalid upstream request")
	ErrUpstreamResponseInvalid = errors.New("upstream response invalid")
	ErrRequestBodyTooLarge     = errors.New("request body too large")
)

type Client struct {
	// httpClient is the DIRECT egress client: its Transport has no
	// Proxy set, so it never consults environment variables either —
	// egress is exclusively governed by proxydial (Feature 009).
	httpClient *http.Client
	// proxiedHTTP shares the same tuning but routes through the
	// process-wide proxy URL; the Proxy closure reads the atomic slot
	// so settings updates hot-swap egress without rebuilding anything.
	proxiedHTTP         *http.Client
	codexBackendBaseURL string
}

type UsageWindow struct {
	UsedPercent float64 `json:"used_percent"`
	// LimitWindowSeconds is the rolling window duration in seconds.
	// Absent on sparse upstream payloads, hence a pointer.
	LimitWindowSeconds *int64 `json:"limit_window_seconds,omitempty"`
	// ResetAt is the unix epoch seconds when the window resets.
	// Absent on sparse upstream payloads, hence a pointer.
	ResetAt *int64 `json:"reset_at,omitempty"`
}

type UsageRateLimit struct {
	PrimaryWindow   *UsageWindow `json:"primary_window"`
	SecondaryWindow *UsageWindow `json:"secondary_window"`
}

type UsageResponse struct {
	RateLimit *UsageRateLimit `json:"rate_limit"`
}

// NewClient creates an upstream HTTP client. The timeout applies only to
// connection and TLS handshake, NOT to the full response — this is critical
// for long-running SSE streams that may take minutes.
//
// Two pre-built clients are created (direct + proxied) so the per-request
// choice in clientFor is a pointer select, never an allocation.
func NewClient(timeout time.Duration) *Client {
	newTransport := func(proxyFn func(*http.Request) (*url.URL, error)) *http.Transport {
		return &http.Transport{
			Proxy:                 proxyFn,
			DialContext:           (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: timeout,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			DisableCompression:    true,
		}
	}

	return &Client{
		codexBackendBaseURL: ChatGPTBackendBaseURL,
		httpClient: &http.Client{
			Transport: newTransport(nil),
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		proxiedHTTP: &http.Client{
			Transport: newTransport(proxydial.ProxyFunc()),
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// httpClientFor resolves the pre-built client for one account-scoped
// egress decision (Feature 009). useProxy=false → direct; useProxy=true
// with a configured global proxy → proxied; useProxy=true WITHOUT a
// configured proxy → proxydial.ErrProxyRequired. Callers MUST surface
// that error as an upstream failure — silently falling back to direct
// would violate the fail-fast constitution (spec 009 US-4). The warn
// line carries the account id + actionable hint because it is the ONLY
// diagnostic for this state.
func (c *Client) httpClientFor(account domain.UpstreamAccount) (*http.Client, error) {
	return c.httpClientForUseProxy(account.UseProxy, account.ID)
}

// httpClientForUseProxy is the same decision for paths that only carry
// the flattened flag (bridge UpstreamRequest, FetchUsage). accountID is
// used only for the stale-opt-in diagnostic.
func (c *Client) httpClientForUseProxy(useProxy bool, accountID int64) (*http.Client, error) {
	if !useProxy {
		return c.httpClient, nil
	}
	if _, err := proxydial.Decide(true); err != nil {
		slog.Warn("outbound proxy required but not configured",
			"account_id", accountID,
			"error", err.Error(),
			"hint", proxydial.ProxyRequiredHint)
		return nil, err
	}
	return c.proxiedHTTP, nil
}

func (c *Client) SetCodexBackendBaseURLForTest(baseURL string) {
	c.codexBackendBaseURL = baseURL
}

// ResetAnchorProbeModel is the model used for the reset-anchor probe.
// Reuses the playground default — a model known to be served for OAuth
// Codex accounts — so the minimal message is accepted and registers
// usage.
const ResetAnchorProbeModel = "gpt-5.6-luna"

// resetAnchorProbeInput is the raw OpenAI-compatible responses request
// that anchors an account's rolling quota window. Codex's 5h/7d buckets
// only report a fixed reset_at once a real message registers usage (the
// window is rolling: reset = first message + window duration), so a
// fresh or just-reset account returns empty windows until one message
// is sent. The raw form is normalized through normalizeCodexResponsesBody
// before sending — the ChatGPT codex backend rejects unnormalized
// payloads with 400.
var resetAnchorProbeInput = []byte(`{"model":"gpt-5.6-luna","input":"hi"}`)

// ProbeResetAnchor sends ONE minimal Codex message through the account
// so the backend materializes the rolling quota window and starts
// returning a fixed reset_at on the next usage fetch. The response body
// is deliberately discarded — the only goal is to register usage. A
// non-2xx (e.g. the account is genuinely limited) surfaces as an error
// so the caller can log it; it never blocks snapshot persistence.
func (c *Client) ProbeResetAnchor(ctx context.Context, account domain.UpstreamAccount, accessToken string) error {
	body, _, err := normalizeCodexResponsesBody(resetAnchorProbeInput)
	if err != nil {
		return fmt.Errorf("build reset-anchor probe body: %w", err)
	}
	targetURL := strings.TrimRight(c.codexBaseURL(), "/") + "/codex/responses"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create reset-anchor probe: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", CodexCLIUserAgent)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Accept-Encoding", "identity")
	if account.ChatGPTAccountID != nil && *account.ChatGPTAccountID != "" {
		req.Header.Set("chatgpt-account-id", *account.ChatGPTAccountID)
	}

	httpClient, err := c.httpClientFor(account)
	if err != nil {
		return fmt.Errorf("%w: reset-anchor probe: %w", ErrUpstreamConnectFailed, err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		if isTimeout(err) {
			return fmt.Errorf("%w: reset-anchor probe: %w", ErrUpstreamTimeout, err)
		}
		return fmt.Errorf("%w: reset-anchor probe: %w", ErrUpstreamConnectFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Drain a bounded slice so the pooled connection can be reused.
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return fmt.Errorf("reset-anchor probe: read upstream response: %w", readErr)
	}
	if resp.StatusCode != http.StatusOK {
		diagnostic := responseDiagnostic(responseBody)
		if diagnostic == nil {
			return fmt.Errorf("reset-anchor probe: upstream returned status %d", resp.StatusCode)
		}
		return fmt.Errorf("reset-anchor probe: upstream returned status %d: %s", resp.StatusCode, *diagnostic)
	}
	return nil
}

// FetchUsage queries the ChatGPT backend usage endpoint for one OAuth
// account. useProxy is the account's outbound-proxy opt-in; a stale
// opt-in with no configured proxy fails with proxydial.ErrProxyRequired
// (the refresher logs it with the row id — never silently direct).
func (c *Client) FetchUsage(ctx context.Context, accessToken string, chatGPTAccountID string, useProxy bool, accountID int64) (*UsageResponse, error) {
	client, err := c.httpClientForUseProxy(useProxy, accountID)
	if err != nil {
		return nil, fmt.Errorf("resolve egress: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.codexBackendBaseURL+"/wham/usage", nil)
	if err != nil {
		return nil, fmt.Errorf("create usage request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", CodexCLIUserAgent)
	if chatGPTAccountID != "" {
		req.Header.Set("chatgpt-account-id", chatGPTAccountID)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do usage request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	resp, err = decompressResponse(resp)
	if err != nil {
		return nil, fmt.Errorf("decompress usage response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage request failed with status %d", resp.StatusCode)
	}

	var usage UsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&usage); err != nil {
		return nil, fmt.Errorf("decode usage response: %w", err)
	}

	return &usage, nil
}
