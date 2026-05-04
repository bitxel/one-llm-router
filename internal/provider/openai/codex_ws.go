package openai

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/coder/websocket"
	"github.com/user/one-llm-router/internal/domain"
)

const responsesWebSocketBetaToken = "responses_websockets=2026-02-06"

func ResolveCodexTurnState(inbound string) string {
	trimmed := strings.TrimSpace(inbound)
	if trimmed != "" {
		return trimmed
	}

	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return "turn_" + hex.EncodeToString(raw[:])
}

func (c *Client) DialCodexWebSocket(
	ctx context.Context,
	account domain.UpstreamAccount,
	accessToken string,
	original *http.Request,
	route GatewayRoute,
	turnState string,
) (*websocket.Conn, *http.Response, error) {
	upstreamPath := route.OAuthUpstreamPath
	if upstreamPath == "" && original != nil {
		upstreamPath = codexBackendPath(original.URL.Path)
	}
	if upstreamPath == "" {
		return nil, nil, fmt.Errorf("%w: missing codex websocket upstream path", ErrInvalidUpstreamRequest)
	}

	targetURL := codexWebSocketURL(c.codexBaseURL(), upstreamPath)
	if original != nil && original.URL.RawQuery != "" {
		targetURL += "?" + original.URL.RawQuery
	}

	headers := http.Header{}
	if original != nil {
		copyForwardHeaders(headers, original.Header)
	}
	headers.Set("Authorization", "Bearer "+accessToken)
	headers.Set("User-Agent", CodexCLIUserAgent)
	headers.Set("x-codex-turn-state", turnState)
	appendOpenAIBeta(headers, responsesWebSocketBetaToken)
	if account.ChatGPTAccountID != nil && *account.ChatGPTAccountID != "" {
		headers.Set("chatgpt-account-id", *account.ChatGPTAccountID)
	}

	return c.dialWebSocket(ctx, targetURL, headers, turnState)
}

func (c *Client) DialBridgeWebSocket(ctx context.Context, upstream UpstreamRequest, turnState string) (*websocket.Conn, *http.Response, error) {
	if strings.TrimSpace(upstream.URL) == "" {
		return nil, nil, fmt.Errorf("%w: missing bridge websocket upstream URL", ErrInvalidUpstreamRequest)
	}
	return c.dialWebSocket(ctx, websocketURL(upstream.URL), upstream.Headers.Clone(), turnState)
}

func (c *Client) dialWebSocket(ctx context.Context, targetURL string, headers http.Header, turnState string) (*websocket.Conn, *http.Response, error) {
	headers.Set("x-codex-turn-state", turnState)
	appendOpenAIBeta(headers, responsesWebSocketBetaToken)

	conn, resp, err := websocket.Dial(ctx, targetURL, &websocket.DialOptions{
		HTTPClient:      c.httpClient,
		HTTPHeader:      headers,
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		if isTimeout(err) {
			return nil, resp, ErrUpstreamTimeout
		}
		return nil, resp, fmt.Errorf("%w: %w", ErrUpstreamConnectFailed, err)
	}
	return conn, resp, nil
}

func codexWebSocketURL(baseURL, path string) string {
	base := strings.TrimRight(baseURL, "/")
	base = websocketURL(base)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

func websocketURL(target string) string {
	target = strings.TrimSpace(target)
	switch {
	case strings.HasPrefix(target, "https://"):
		return "wss://" + strings.TrimPrefix(target, "https://")
	case strings.HasPrefix(target, "http://"):
		return "ws://" + strings.TrimPrefix(target, "http://")
	default:
		return target
	}
}

func appendOpenAIBeta(headers http.Header, token string) {
	for _, value := range headers.Values("OpenAI-Beta") {
		for _, part := range strings.Split(value, ",") {
			if strings.TrimSpace(part) == token {
				return
			}
		}
	}
	headers.Add("OpenAI-Beta", token)
}
