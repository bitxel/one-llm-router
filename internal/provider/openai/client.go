package openai

import (
	"errors"
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
