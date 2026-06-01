package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

var (
	ErrUpstreamConnectFailed   = errors.New("upstream connect failed")
	ErrUpstreamTimeout         = errors.New("upstream timeout")
	ErrInvalidUpstreamRequest  = errors.New("invalid upstream request")
	ErrUpstreamResponseInvalid = errors.New("upstream response invalid")
	ErrRequestBodyTooLarge     = errors.New("request body too large")
)

type Client struct {
	httpClient          *http.Client
	codexBackendBaseURL string
}

type UsageWindow struct {
	UsedPercent float64 `json:"used_percent"`
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
func NewClient(timeout time.Duration) *Client {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   timeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		DisableCompression:    true,
	}

	return &Client{
		codexBackendBaseURL: ChatGPTBackendBaseURL,
		httpClient: &http.Client{
			Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (c *Client) SetCodexBackendBaseURLForTest(baseURL string) {
	c.codexBackendBaseURL = baseURL
}

func (c *Client) FetchUsage(ctx context.Context, accessToken string, chatGPTAccountID string) (*UsageResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.codexBackendBaseURL+"/wham/usage", nil)
	if err != nil {
		return nil, fmt.Errorf("create usage request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", CodexCLIUserAgent)
	if chatGPTAccountID != "" {
		req.Header.Set("chatgpt-account-id", chatGPTAccountID)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do usage request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage request failed with status %d", resp.StatusCode)
	}

	var usage UsageResponse
	if err := json.NewDecoder(resp.Body).Decode(&usage); err != nil {
		return nil, fmt.Errorf("decode usage response: %w", err)
	}

	return &usage, nil
}
