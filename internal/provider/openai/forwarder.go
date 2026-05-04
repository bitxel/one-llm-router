package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/user/one-llm-router/internal/domain"
)

const (
	ChatGPTBackendBaseURL = "https://chatgpt.com/backend-api"
	CodexCLIUserAgent     = "codex_cli_rs/0.120.0 (Mac OS 26.2; arm64) dumb"
	codexSSECollectLimit  = 16 << 20
)

type codexBackendBodyResult struct {
	body         io.Reader
	upstreamBody []byte
	collectSSE   bool
}

type ForwardCapture struct {
	UpstreamRequestBody          []byte
	UpstreamResponseBody         []byte
	UpstreamResponseBodySnapshot func() []byte
	TokenUsage                   func() domain.JSONMap
	ModelParams                  func() domain.JSONMap
}

type GatewayOAuthBehavior string

const (
	GatewayOAuthBehaviorCodexMapping GatewayOAuthBehavior = "codex_mapping"
	GatewayOAuthBehaviorModelsFacade GatewayOAuthBehavior = "models_facade"
	GatewayOAuthBehaviorChatAdapter  GatewayOAuthBehavior = "chat_adapter"
)

type GatewayBodyPolicy string

const (
	GatewayBodyPolicyJSONCaptureAllowed GatewayBodyPolicy = "json_capture_allowed"
	GatewayBodyPolicyCaptureDisabled    GatewayBodyPolicy = "capture_disabled"
	GatewayBodyPolicyWebSocketNoBody    GatewayBodyPolicy = "websocket_no_body"
)

type GatewayRoute struct {
	OAuthUpstreamPath string
	OAuthBehavior     GatewayOAuthBehavior
	BodyPolicy        GatewayBodyPolicy
}

// hopByHopRequestHeaders must never be forwarded to upstream (RFC 7230 §6.1).
// Mirrors codex-lb's allowlist for parity: forwarding Connection/Upgrade/TE/
// etc. corrupts the upstream pool and leaks Proxy-Authorization.
var hopByHopRequestHeaders = map[string]struct{}{
	"connection":          {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"proxy-connection":    {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},
	// Host and Authorization are rewritten below; drop the inbound copies.
	"host":          {},
	"authorization": {},
}

var routerOwnedRequestHeaders = map[string]struct{}{
	"chatgpt-account-id": {},
	"cookie":             {},
	"x-admin-token":      {},
	"x-csrf-token":       {},
	"x-xsrf-token":       {},
}

func (c *Client) ForwardAccountRequest(ctx context.Context, account domain.UpstreamAccount, token []byte, original *http.Request) (*http.Response, error) {
	resp, _, err := c.ForwardAccountRequestWithCapture(ctx, account, token, original)
	return resp, err
}

func (c *Client) ForwardAccountRequestWithCapture(ctx context.Context, account domain.UpstreamAccount, token []byte, original *http.Request) (*http.Response, ForwardCapture, error) {
	if account.IsOAuth() {
		return c.ForwardCodexRequestWithCapture(ctx, account, string(token), original)
	}
	return c.ForwardRequestWithCapture(ctx, account.EffectiveBaseURL(), string(token), original)
}

func (c *Client) ForwardGatewayRequestWithCapture(ctx context.Context, account domain.UpstreamAccount, token []byte, original *http.Request, route GatewayRoute) (*http.Response, ForwardCapture, error) {
	if !account.IsOAuth() {
		return c.ForwardRequestWithCapture(ctx, account.EffectiveBaseURL(), string(token), original)
	}
	switch route.OAuthBehavior {
	case GatewayOAuthBehaviorModelsFacade:
		return c.ForwardCodexModelsFacadeWithCapture(ctx, account, string(token), original, route)
	case GatewayOAuthBehaviorChatAdapter:
		return c.ForwardChatCompletionsWithCapture(ctx, account, string(token), original, route)
	default:
		return c.ForwardCodexRouteWithCapture(ctx, account, string(token), original, route)
	}
}

func (c *Client) ForwardBridgeRequestWithCapture(ctx context.Context, upstream UpstreamRequest, adapter ClientResponseAdapter) (*http.Response, ForwardCapture, error) {
	if strings.TrimSpace(upstream.URL) == "" {
		return nil, ForwardCapture{}, fmt.Errorf("%w: missing upstream URL", ErrInvalidUpstreamRequest)
	}

	body := upstream.Body
	if body == nil {
		body = bytes.NewReader(upstream.RawBody)
	}
	req, err := http.NewRequestWithContext(ctx, upstream.Method, upstream.URL, body)
	if err != nil {
		return nil, ForwardCapture{}, fmt.Errorf("create bridge upstream request: %w", err)
	}
	req.Header = upstream.Headers.Clone()
	if upstream.ContentLength >= 0 {
		req.ContentLength = upstream.ContentLength
	} else if upstream.Body == nil {
		req.ContentLength = int64(len(upstream.RawBody))
	}

	resp, err := c.httpClient.Do(req)
	capture := ForwardCapture{UpstreamRequestBody: append([]byte(nil), upstream.RawBody...)}
	if err != nil {
		if errors.Is(err, ErrRequestBodyTooLarge) {
			return nil, capture, ErrRequestBodyTooLarge
		}
		if isTimeout(err) {
			return nil, capture, ErrUpstreamTimeout
		}
		return nil, capture, fmt.Errorf("%w: %w", ErrUpstreamConnectFailed, err)
	}
	return adaptBridgeResponse(resp, capture, upstream, adapter)
}

func adaptBridgeResponse(resp *http.Response, capture ForwardCapture, upstream UpstreamRequest, adapter ClientResponseAdapter) (*http.Response, ForwardCapture, error) {
	if isChatGPTContract(upstream.Contract()) {
		resp.Header.Del("Set-Cookie")
	}
	if adapter == nil {
		return resp, capture, nil
	}
	switch adapter.Kind() {
	case ClientResponseAdapterOpenAIResponsesJSON:
		return collectResponsesSSEForJSON(resp, capture)
	case ClientResponseAdapterOpenAIModelsList:
		return adaptCodexModelsListResponse(resp, capture)
	case ClientResponseAdapterChatCompletionsJSON, ClientResponseAdapterChatCompletionsSSE:
		chatAdapter, ok := adapter.(clientResponseAdapter)
		if !ok {
			return nil, capture, fmt.Errorf("%w: missing chat response adapter metadata", ErrUpstreamResponseInvalid)
		}
		return adaptChatCompletionsResponse(resp, capture, chatAdapterRequest{
			model:        chatAdapter.model,
			stream:       adapter.Kind() == ClientResponseAdapterChatCompletionsSSE,
			includeUsage: chatAdapter.includeUsage,
			upstreamBody: upstream.RawBody,
		})
	default:
		return resp, capture, nil
	}
}

func collectResponsesSSEForJSON(resp *http.Response, capture ForwardCapture) (*http.Response, ForwardCapture, error) {
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "application/json") {
		return resp, capture, nil
	}
	rawBody, err := readCodexSSEResponse(resp.Body)
	_ = resp.Body.Close()
	capture.UpstreamResponseBody = rawBody
	if err != nil {
		return nil, capture, fmt.Errorf("%w: %w", ErrUpstreamResponseInvalid, err)
	}
	collected, err := collectCodexSSEBytes(rawBody)
	if err != nil {
		return nil, capture, fmt.Errorf("%w: %w", ErrUpstreamResponseInvalid, err)
	}
	capture.UpstreamResponseBody = collected
	resp.Body = io.NopCloser(bytes.NewReader(collected))
	resp.ContentLength = int64(len(collected))
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Trailer")
	resp.Header.Del("Transfer-Encoding")
	resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(collected)))
	resp.Header.Set("Content-Type", "application/json")
	return resp, capture, nil
}

func adaptCodexModelsListResponse(resp *http.Response, capture ForwardCapture) (*http.Response, ForwardCapture, error) {
	if resp.StatusCode >= http.StatusBadRequest {
		return resp, capture, nil
	}
	rawBody, err := readBoundedProviderBody(resp.Body, codexSSECollectLimit, "codex models response too large")
	_ = resp.Body.Close()
	if err != nil {
		return nil, capture, fmt.Errorf("%w: read codex models response: %w", ErrUpstreamResponseInvalid, err)
	}
	transformed, err := codexModelsToOpenAIList(rawBody)
	if err != nil {
		return nil, capture, fmt.Errorf("%w: %w", ErrUpstreamResponseInvalid, err)
	}
	resp.Body = io.NopCloser(bytes.NewReader(transformed))
	resp.ContentLength = int64(len(transformed))
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Trailer")
	resp.Header.Del("Transfer-Encoding")
	resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(transformed)))
	resp.Header.Set("Content-Type", "application/json")
	return resp, capture, nil
}

func isChatGPTContract(contract Contract) bool {
	return strings.HasPrefix(string(contract), "contract.chatgpt.")
}

func (c *Client) ForwardRequest(ctx context.Context, upstreamBaseURL, apiKey string, original *http.Request) (*http.Response, error) {
	resp, _, err := c.ForwardRequestWithCapture(ctx, upstreamBaseURL, apiKey, original)
	return resp, err
}

func (c *Client) ForwardRequestWithCapture(ctx context.Context, upstreamBaseURL, apiKey string, original *http.Request) (*http.Response, ForwardCapture, error) {
	targetURL := strings.TrimRight(upstreamBaseURL, "/") + original.URL.Path
	if original.URL.RawQuery != "" {
		targetURL += "?" + original.URL.RawQuery
	}

	var bodyBytes []byte
	if original.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(original.Body)
		if err != nil {
			return nil, ForwardCapture{}, fmt.Errorf("read upstream request body: %w", err)
		}
		original.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}
	body := bytes.NewReader(bodyBytes)

	req, err := http.NewRequestWithContext(ctx, original.Method, targetURL, body)
	if err != nil {
		return nil, ForwardCapture{}, fmt.Errorf("create upstream request: %w", err)
	}

	copyForwardHeaders(req.Header, original.Header)
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if isTimeout(err) {
			return nil, ForwardCapture{UpstreamRequestBody: bodyBytes}, ErrUpstreamTimeout
		}
		return nil, ForwardCapture{UpstreamRequestBody: bodyBytes}, fmt.Errorf("%w: %w", ErrUpstreamConnectFailed, err)
	}
	return resp, ForwardCapture{UpstreamRequestBody: bodyBytes}, nil
}

func (c *Client) ForwardCodexRequest(ctx context.Context, account domain.UpstreamAccount, accessToken string, original *http.Request) (*http.Response, error) {
	resp, _, err := c.ForwardCodexRequestWithCapture(ctx, account, accessToken, original)
	return resp, err
}

func (c *Client) ForwardCodexRequestWithCapture(ctx context.Context, account domain.UpstreamAccount, accessToken string, original *http.Request) (*http.Response, ForwardCapture, error) {
	return c.ForwardCodexRouteWithCapture(ctx, account, accessToken, original, GatewayRoute{
		OAuthUpstreamPath: codexBackendPath(original.URL.Path),
		OAuthBehavior:     GatewayOAuthBehaviorCodexMapping,
		BodyPolicy:        GatewayBodyPolicyJSONCaptureAllowed,
	})
}

func (c *Client) ForwardCodexRouteWithCapture(ctx context.Context, account domain.UpstreamAccount, accessToken string, original *http.Request, route GatewayRoute) (*http.Response, ForwardCapture, error) {
	upstreamPath := route.OAuthUpstreamPath
	if upstreamPath == "" {
		upstreamPath = codexBackendPath(original.URL.Path)
	}
	targetURL := strings.TrimRight(c.codexBaseURL(), "/") + upstreamPath
	if original.URL.RawQuery != "" {
		targetURL += "?" + original.URL.RawQuery
	}

	body, err := codexBackendBody(original, route.BodyPolicy)
	if err != nil {
		return nil, ForwardCapture{}, fmt.Errorf("%w: %w", ErrInvalidUpstreamRequest, err)
	}

	req, err := http.NewRequestWithContext(ctx, original.Method, targetURL, body.body)
	if err != nil {
		return nil, ForwardCapture{}, fmt.Errorf("create codex upstream request: %w", err)
	}

	if upstreamPath == "/transcribe" {
		copyTranscribeHeaders(req.Header, original.Header)
	} else {
		copyForwardHeaders(req.Header, original.Header)
	}
	if route.BodyPolicy == GatewayBodyPolicyCaptureDisabled && original.ContentLength >= 0 {
		req.ContentLength = original.ContentLength
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", CodexCLIUserAgent)
	if shouldRequestIdentityEncodingForGatewayRoute(route, upstreamPath) {
		forceIdentityAcceptEncoding(req.Header)
	}
	if route.BodyPolicy != GatewayBodyPolicyCaptureDisabled {
		req.Header.Set("Content-Type", "application/json")
	}
	if account.ChatGPTAccountID != nil && *account.ChatGPTAccountID != "" {
		req.Header.Set("chatgpt-account-id", *account.ChatGPTAccountID)
	}
	if upstreamPath == "/codex/responses" {
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Accept-Encoding", "identity")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, ErrRequestBodyTooLarge) {
			return nil, ForwardCapture{UpstreamRequestBody: body.upstreamBody}, ErrRequestBodyTooLarge
		}
		if isTimeout(err) {
			return nil, ForwardCapture{UpstreamRequestBody: body.upstreamBody}, ErrUpstreamTimeout
		}
		return nil, ForwardCapture{UpstreamRequestBody: body.upstreamBody}, fmt.Errorf("%w: %w", ErrUpstreamConnectFailed, err)
	}
	capture := ForwardCapture{UpstreamRequestBody: body.upstreamBody}
	if body.collectSSE {
		resp, capture, err = collectResponsesSSEForJSON(resp, capture)
		if err != nil {
			return nil, capture, err
		}
	}
	resp.Header.Del("Set-Cookie")
	return resp, capture, nil
}

func shouldRequestIdentityEncodingForGatewayRoute(route GatewayRoute, upstreamPath string) bool {
	switch upstreamPath {
	case "/codex/responses", "/codex/responses/compact":
		return true
	}
	switch route.OAuthBehavior {
	case GatewayOAuthBehaviorModelsFacade, GatewayOAuthBehaviorChatAdapter:
		return true
	default:
		return false
	}
}

func (c *Client) ForwardCodexModelsFacadeWithCapture(ctx context.Context, account domain.UpstreamAccount, accessToken string, original *http.Request, route GatewayRoute) (*http.Response, ForwardCapture, error) {
	route.OAuthBehavior = GatewayOAuthBehaviorCodexMapping
	route.BodyPolicy = GatewayBodyPolicyCaptureDisabled
	resp, capture, err := c.ForwardCodexRouteWithCapture(ctx, account, accessToken, original, route)
	if err != nil {
		return nil, capture, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return resp, capture, nil
	}
	return adaptCodexModelsListResponse(resp, capture)
}

func copyForwardHeaders(dst http.Header, src http.Header) {
	dynamicDrops := map[string]struct{}{}
	for _, raw := range src.Values("Connection") {
		for _, token := range strings.Split(raw, ",") {
			if t := strings.TrimSpace(strings.ToLower(token)); t != "" {
				dynamicDrops[t] = struct{}{}
			}
		}
	}

	for key, values := range src {
		lower := strings.ToLower(key)
		if _, drop := hopByHopRequestHeaders[lower]; drop {
			continue
		}
		if _, drop := routerOwnedRequestHeaders[lower]; drop {
			continue
		}
		if _, drop := dynamicDrops[lower]; drop {
			continue
		}
		if strings.HasPrefix(lower, "x-forwarded-") || strings.HasPrefix(lower, "cf-") {
			continue
		}
		if strings.HasPrefix(lower, "sec-websocket-") {
			continue
		}
		for _, v := range values {
			dst.Add(key, v)
		}
	}
}

func copyTranscribeHeaders(dst http.Header, src http.Header) {
	dynamicDrops := map[string]struct{}{}
	for _, raw := range src.Values("Connection") {
		for _, token := range strings.Split(raw, ",") {
			if t := strings.TrimSpace(strings.ToLower(token)); t != "" {
				dynamicDrops[t] = struct{}{}
			}
		}
	}

	for key, values := range src {
		lower := strings.ToLower(key)
		if _, drop := hopByHopRequestHeaders[lower]; drop {
			continue
		}
		if _, drop := routerOwnedRequestHeaders[lower]; drop {
			continue
		}
		if _, drop := dynamicDrops[lower]; drop {
			continue
		}
		if lower == "content-type" || lower == "user-agent" ||
			strings.HasPrefix(lower, "x-openai-") || strings.HasPrefix(lower, "x-codex-") {
			for _, v := range values {
				dst.Add(key, v)
			}
		}
	}
}

func forceIdentityAcceptEncoding(headers http.Header) {
	headers.Set("Accept-Encoding", "identity")
}

func (c *Client) codexBaseURL() string {
	if c == nil {
		return ChatGPTBackendBaseURL
	}
	base := strings.TrimRight(strings.TrimSpace(c.codexBackendBaseURL), "/")
	if base == "" {
		return ChatGPTBackendBaseURL
	}
	return base
}

func (c *Client) CodexBackendBaseURL() string {
	return c.codexBaseURL()
}

func codexBackendPath(path string) string {
	switch path {
	case "/v1/responses":
		return "/codex/responses"
	case "/v1/responses/compact":
		return "/codex/responses/compact"
	case "/backend-api/codex/responses":
		return "/codex/responses"
	case "/backend-api/codex/responses/compact":
		return "/codex/responses/compact"
	case "/v1/models", "/backend-api/codex/models":
		return "/codex/models"
	case "/backend-api/transcribe":
		return "/transcribe"
	default:
		return path
	}
}

func codexBackendBody(original *http.Request, bodyPolicy GatewayBodyPolicy) (codexBackendBodyResult, error) {
	if original.Body == nil {
		return codexBackendBodyResult{}, nil
	}
	if bodyPolicy == GatewayBodyPolicyCaptureDisabled {
		return codexBackendBodyResult{body: original.Body}, nil
	}
	data, err := io.ReadAll(original.Body)
	if err != nil {
		return codexBackendBodyResult{}, fmt.Errorf("read codex upstream request body: %w", err)
	}
	original.Body = io.NopCloser(bytes.NewReader(data))
	if len(data) == 0 {
		return codexBackendBodyResult{body: bytes.NewReader(data), upstreamBody: data}, nil
	}
	switch original.URL.Path {
	case "/v1/responses":
		encoded, collectSSE, err := normalizeCodexResponsesBody(data)
		if err != nil {
			return codexBackendBodyResult{}, err
		}
		return codexBackendBodyResult{body: bytes.NewReader(encoded), upstreamBody: encoded, collectSSE: collectSSE}, nil
	case "/backend-api/codex/responses":
		encoded, err := normalizeNativeCodexResponsesBody(data)
		if err != nil {
			return codexBackendBodyResult{}, err
		}
		return codexBackendBodyResult{body: bytes.NewReader(encoded), upstreamBody: encoded}, nil
	case "/v1/responses/compact":
		encoded, err := normalizeCodexResponsesCompactBody(data)
		if err != nil {
			return codexBackendBodyResult{}, err
		}
		return codexBackendBodyResult{body: bytes.NewReader(encoded), upstreamBody: encoded}, nil
	case "/backend-api/codex/responses/compact":
		encoded, err := normalizeNativeCodexResponsesCompactBody(data)
		if err != nil {
			return codexBackendBodyResult{}, err
		}
		return codexBackendBodyResult{body: bytes.NewReader(encoded), upstreamBody: encoded}, nil
	default:
		return codexBackendBodyResult{body: bytes.NewReader(data), upstreamBody: data}, nil
	}
}

func codexModelsToOpenAIList(data []byte) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode codex models response: %w", err)
	}
	rawModels, ok := payload["models"].([]any)
	if !ok {
		return nil, errors.New("codex models response missing models array")
	}

	items := make([]map[string]any, 0, len(rawModels))
	for _, raw := range rawModels {
		model, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("codex models response contains non-object model")
		}
		slug, ok := model["slug"].(string)
		if !ok || strings.TrimSpace(slug) == "" {
			continue
		}
		item := map[string]any{
			"id":     slug,
			"object": "model",
		}
		if created, ok := numericJSONValue(model["created"]); ok {
			item["created"] = created
		}
		if ownedBy, ok := model["owned_by"].(string); ok && ownedBy != "" {
			item["owned_by"] = ownedBy
		}
		if metadata := codexModelMetadata(model); len(metadata) > 0 {
			item["metadata"] = metadata
		}
		items = append(items, item)
	}

	return json.Marshal(map[string]any{
		"object": "list",
		"data":   items,
	})
}

func codexModelMetadata(model map[string]any) map[string]any {
	keys := []string{
		"display_name",
		"description",
		"context_window",
		"input_modalities",
		"supported_reasoning_levels",
		"default_reasoning_level",
		"supports_reasoning_summaries",
		"support_verbosity",
		"default_verbosity",
		"prefer_websockets",
		"supports_parallel_tool_calls",
		"supported_in_api",
		"minimal_client_version",
		"priority",
	}
	metadata := map[string]any{}
	for _, key := range keys {
		if value, ok := model[key]; ok {
			metadata[key] = value
		}
	}
	return metadata
}

func numericJSONValue(value any) (any, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return v, true
	case int64:
		return v, true
	case json.Number:
		return v, true
	default:
		return nil, false
	}
}

func inputMissingOrEmpty(value any) bool {
	if value == nil {
		return true
	}
	if list, ok := value.([]any); ok && len(list) == 0 {
		return true
	}
	return false
}

func coerceMessages(instructions any, messages any) (string, []any) {
	var instructionParts []string
	if text, ok := instructions.(string); ok && strings.TrimSpace(text) != "" {
		instructionParts = append(instructionParts, text)
	}

	items, ok := messages.([]any)
	if !ok {
		return strings.Join(instructionParts, "\n\n"), nil
	}
	input := make([]any, 0, len(items))
	for _, item := range items {
		msg, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		switch role {
		case "system", "developer":
			if text := contentText(msg["content"]); text != "" {
				instructionParts = append(instructionParts, text)
			}
		case "assistant":
			input = append(input, map[string]any{
				"role":    "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": contentText(msg["content"])}},
			})
		case "tool":
			callID := firstString(msg["tool_call_id"], msg["toolCallId"], msg["call_id"])
			if callID != "" {
				input = append(input, map[string]any{
					"type":    "function_call_output",
					"call_id": callID,
					"output":  contentText(msg["content"]),
				})
			}
		default:
			if text := contentText(msg["content"]); text != "" {
				input = append(input, inputTextMessage(text))
			}
		}
	}
	return strings.Join(instructionParts, "\n\n"), input
}

func inputTextMessage(text string) map[string]any {
	return map[string]any{
		"role":    "user",
		"content": []any{map[string]any{"type": "input_text", "text": text}},
	}
}

func contentText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			if text := contentText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "")
	case map[string]any:
		for _, key := range []string{"text", "refusal", "content"} {
			if text, ok := v[key].(string); ok {
				return text
			}
		}
		if data, err := json.Marshal(v); err == nil {
			return string(data)
		}
	}
	return ""
}

func firstString(values ...any) string {
	for _, value := range values {
		if text, ok := value.(string); ok && text != "" {
			return text
		}
	}
	return ""
}

func collectCodexSSEResponse(body io.Reader) ([]byte, error) {
	data, err := readCodexSSEResponse(body)
	if err != nil {
		return nil, err
	}
	return collectCodexSSEBytes(data)
}

func readCodexSSEResponse(body io.Reader) ([]byte, error) {
	data, err := readBoundedProviderBody(body, codexSSECollectLimit, "codex SSE response too large")
	if err != nil {
		return data, fmt.Errorf("read codex SSE response: %w", err)
	}
	return data, nil
}

func readBoundedProviderBody(body io.Reader, limit int64, limitMessage string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return data, err
	}
	if int64(len(data)) > limit {
		return data, errors.New(limitMessage)
	}
	return data, nil
}

func collectCodexSSEBytes(data []byte) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), codexSSECollectLimit)

	var collector ResponsesSSECollector
	var eventData []string
	flush := func() error {
		if len(eventData) == 0 {
			return nil
		}
		raw := strings.Join(eventData, "\n")
		eventData = nil
		if err := collector.AddEvent([]byte(raw)); err != nil {
			return fmt.Errorf("decode codex SSE event: %w", err)
		}
		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		if data, ok := sseDataField(line); ok {
			eventData = append(eventData, data)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan codex SSE response: %w", err)
	}
	if err := flush(); err != nil {
		return nil, err
	}
	encoded, ok, err := collector.AggregateJSON(true)
	if err != nil {
		return nil, fmt.Errorf("collect codex SSE response: %w", err)
	}
	if !ok {
		return nil, errors.New("codex SSE response missing terminal event")
	}
	return encoded, nil
}

func sseDataField(line string) (string, bool) {
	if !strings.HasPrefix(line, "data") {
		return "", false
	}
	rest := line[len("data"):]
	if rest == "" {
		return "", true
	}
	if !strings.HasPrefix(rest, ":") {
		return "", false
	}
	value := strings.TrimPrefix(rest, ":")
	value = strings.TrimPrefix(value, " ")
	return value, true
}

func isTimeout(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}
