package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
)

var playgroundWaitLimit = core.PlaygroundWaitLimit

type playgroundResponsesRequest struct {
	Model           string `json:"model"`
	Input           string `json:"input"`
	Stream          bool   `json:"stream"`
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
}

type playgroundChatCompletionsRequest struct {
	Model               string                  `json:"model"`
	Messages            []playgroundChatMessage `json:"messages"`
	Stream              bool                    `json:"stream"`
	MaxCompletionTokens int                     `json:"max_completion_tokens,omitempty"`
}

type playgroundChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func BuildPlaygroundResponsesBody(model, text string, maxOutputTokens int) ([]byte, error) {
	return json.Marshal(playgroundResponsesRequest{
		Model:           model,
		Input:           text,
		Stream:          false,
		MaxOutputTokens: maxOutputTokens,
	})
}

func BuildPlaygroundChatCompletionsBody(model, text string, maxOutputTokens int) ([]byte, error) {
	return json.Marshal(playgroundChatCompletionsRequest{
		Model: model,
		Messages: []playgroundChatMessage{
			{Role: "user", Content: text},
		},
		Stream:              false,
		MaxCompletionTokens: maxOutputTokens,
	})
}

func (c *Client) buildPlaygroundUpstreamRequest(account domain.UpstreamAccount, request core.PlaygroundRunRequest) ([]byte, string, error) {
	switch request.EffectiveEndpoint() {
	case core.PlaygroundEndpointResponses:
		body, err := BuildPlaygroundResponsesBody(request.Model, request.Text, request.EffectiveMaxOutputTokens())
		if err != nil {
			return nil, "", fmt.Errorf("build playground responses body: %w", err)
		}
		if account.IsOAuth() {
			body, _, err = normalizeCodexResponsesBody(body)
			if err != nil {
				return nil, "", fmt.Errorf("build codex playground responses body: %w", err)
			}
			return body, strings.TrimRight(c.codexBaseURL(), "/") + "/codex/responses", nil
		}
		return body, strings.TrimRight(account.EffectiveBaseURL(), "/") + "/responses", nil

	case core.PlaygroundEndpointChatCompletions:
		body, err := BuildPlaygroundChatCompletionsBody(request.Model, request.Text, request.EffectiveMaxOutputTokens())
		if err != nil {
			return nil, "", fmt.Errorf("build playground chat completions body: %w", err)
		}
		if account.IsOAuth() {
			adapterReq, err := http.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
			if err != nil {
				return nil, "", fmt.Errorf("create playground chat adapter request: %w", err)
			}
			adapted, err := buildChatAdapterRequest(adapterReq)
			if err != nil {
				return nil, "", fmt.Errorf("adapt playground chat completions body: %w", err)
			}
			return adapted.upstreamBody, strings.TrimRight(c.codexBaseURL(), "/") + "/codex/responses", nil
		}
		return body, strings.TrimRight(account.EffectiveBaseURL(), "/") + "/chat/completions", nil

	default:
		return nil, "", fmt.Errorf("unsupported playground endpoint %q", request.EffectiveEndpoint())
	}
}

func (c *Client) RunPlayground(ctx context.Context, account domain.UpstreamAccount, token []byte, request core.PlaygroundRunRequest) (*core.PlaygroundUpstreamResult, error) {
	if c == nil || c.httpClient == nil {
		return nil, errors.New("openai playground client is nil")
	}

	endpoint := request.EffectiveEndpoint()
	body, targetURL, err := c.buildPlaygroundUpstreamRequest(account, request)
	if err != nil {
		return nil, err
	}

	waitCtx, cancel := context.WithTimeout(ctx, playgroundWaitLimit)
	defer cancel()

	upstreamReq, err := http.NewRequestWithContext(waitCtx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create playground upstream request: %w", err)
	}
	upstreamEndpoint := upstreamReq.URL.Path
	upstreamReq.Header.Set("Authorization", "Bearer "+string(token))
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Accept", "application/json")
	if account.IsOAuth() {
		upstreamReq.Header.Set("Accept", "text/event-stream")
		upstreamReq.Header.Set("Accept-Encoding", "identity")
		upstreamReq.Header.Set("User-Agent", CodexCLIUserAgent)
	}
	if account.IsOAuth() && account.ChatGPTAccountID != nil && *account.ChatGPTAccountID != "" {
		upstreamReq.Header.Set("chatgpt-account-id", *account.ChatGPTAccountID)
	}

	resp, err := c.httpClient.Do(upstreamReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if isTimeout(err) || waitCtx.Err() != nil {
			return nil, &core.PlaygroundUpstreamTimeoutError{
				WaitLimitMS:         int(playgroundWaitLimit / time.Millisecond),
				UpstreamEndpoint:    upstreamEndpoint,
				UpstreamRequestBody: body,
			}
		}
		return nil, &core.PlaygroundUpstreamConnectError{
			UpstreamEndpoint:    upstreamEndpoint,
			UpstreamRequestBody: body,
			Cause:               fmt.Errorf("%w: %w", ErrUpstreamConnectFailed, err),
		}
	}
	defer func() { _ = resp.Body.Close() }()

	responseBody, err := readPlaygroundResponseBody(resp.Body)
	if err != nil {
		var tooLarge *core.PlaygroundResponseTooLargeError
		if errors.As(err, &tooLarge) {
			tooLarge.UpstreamEndpoint = upstreamEndpoint
			tooLarge.UpstreamRequestBody = body
		}
		return nil, err
	}
	responseMode := DetectResponseMode(resp.Header.Get("Content-Type"))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &core.PlaygroundUpstreamError{
			UpstreamStatus:       resp.StatusCode,
			UpstreamEndpoint:     upstreamEndpoint,
			UpstreamRequestBody:  body,
			UpstreamResponseBody: responseBody,
			ProviderError:        ExtractErrorCode(responseBody),
			ProviderMessage:      ExtractErrorMessage(responseBody),
		}
	}
	if account.IsOAuth() && endpoint == core.PlaygroundEndpointResponses && !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "application/json") {
		rawSSEBody := responseBody
		responseBody, err = collectCodexSSEBytes(rawSSEBody)
		if err != nil {
			return nil, &core.PlaygroundResponseMalformedError{
				Reason:               core.PlaygroundMalformedInvalidJSON,
				UpstreamEndpoint:     upstreamEndpoint,
				UpstreamRequestBody:  body,
				UpstreamResponseBody: rawSSEBody,
			}
		}
		responseMode = domain.ResponseModeJSON
	}
	if account.IsOAuth() && endpoint == core.PlaygroundEndpointChatCompletions {
		rawCodexBody := responseBody
		converted, statusCode, err := chatCompletionFromResponsesBody(bytes.NewReader(rawCodexBody), resp.Header.Get("Content-Type"), request.Model)
		if err != nil {
			return nil, &core.PlaygroundResponseMalformedError{
				Reason:               core.PlaygroundMalformedInvalidJSON,
				UpstreamEndpoint:     upstreamEndpoint,
				UpstreamRequestBody:  body,
				UpstreamResponseBody: rawCodexBody,
			}
		}
		responseBody = converted
		responseMode = domain.ResponseModeJSON
		if statusCode < 200 || statusCode >= 300 {
			return nil, &core.PlaygroundUpstreamError{
				UpstreamStatus:       statusCode,
				UpstreamEndpoint:     upstreamEndpoint,
				UpstreamRequestBody:  body,
				UpstreamResponseBody: responseBody,
				ProviderError:        ExtractErrorCode(responseBody),
				ProviderMessage:      ExtractErrorMessage(responseBody),
			}
		}
	}

	raw, err := decodePlaygroundJSON(responseBody)
	if err != nil {
		var malformed *core.PlaygroundResponseMalformedError
		if errors.As(err, &malformed) {
			malformed.UpstreamEndpoint = upstreamEndpoint
			malformed.UpstreamRequestBody = body
			malformed.UpstreamResponseBody = responseBody
		}
		return nil, err
	}
	text, textAvailable := extractPlaygroundText(endpoint, responseBody)
	result := &core.PlaygroundUpstreamResult{
		StatusCode:          resp.StatusCode,
		ResponseMode:        responseMode,
		UpstreamEndpoint:    upstreamEndpoint,
		UpstreamRequestBody: body,
		Text:                text,
		TextAvailable:       textAvailable,
		RawResponse:         raw,
		Usage:               usageInts(ExtractUsageFromJSON(responseBody)),
	}
	if request.IncludeRawResponse {
		result.RawResponseAvailable = true
	} else {
		reason := core.PlaygroundRawNotRequested
		result.RawResponseOmittedReason = &reason
	}
	return result, nil
}

func ExtractPlaygroundOutputText(body []byte) (string, bool) {
	var resp struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Content []struct {
				Type string              `json:"type"`
				Text playgroundTextValue `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", false
	}
	if resp.OutputText != "" {
		return resp.OutputText, true
	}

	var parts []string
	for _, item := range resp.Output {
		for _, content := range item.Content {
			if content.Type != "" && content.Type != "output_text" {
				continue
			}
			if content.Text.Value != "" {
				parts = append(parts, content.Text.Value)
			}
		}
	}
	text := strings.Join(parts, "")
	if text == "" {
		return "", false
	}
	return text, true
}

func ExtractPlaygroundChatOutputText(body []byte) (string, bool) {
	var resp struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", false
	}
	var parts []string
	for _, choice := range resp.Choices {
		text, ok := playgroundChatContentText(choice.Message.Content)
		if ok {
			parts = append(parts, text)
		}
	}
	text := strings.Join(parts, "")
	if text == "" {
		return "", false
	}
	return text, true
}

func extractPlaygroundText(endpoint core.PlaygroundEndpoint, body []byte) (string, bool) {
	switch endpoint {
	case core.PlaygroundEndpointChatCompletions:
		return ExtractPlaygroundChatOutputText(body)
	default:
		return ExtractPlaygroundOutputText(body)
	}
}

func playgroundChatContentText(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || jsonRawIsNull(raw) {
		return "", false
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, text != ""
	}

	var contentParts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &contentParts); err != nil {
		return "", false
	}
	var parts []string
	for _, part := range contentParts {
		if part.Type != "" && part.Type != "text" {
			continue
		}
		if part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	text = strings.Join(parts, "")
	return text, text != ""
}

type playgroundTextValue struct {
	Value string
}

func (v *playgroundTextValue) UnmarshalJSON(body []byte) error {
	var text string
	if err := json.Unmarshal(body, &text); err == nil {
		v.Value = text
		return nil
	}

	var object struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &object); err != nil {
		return nil
	}
	v.Value = object.Value
	return nil
}

func readPlaygroundResponseBody(body io.Reader) ([]byte, error) {
	limited := io.LimitReader(body, int64(core.PlaygroundResponseReadLimitBytes)+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read playground response body: %w", err)
	}
	if len(data) > core.PlaygroundResponseReadLimitBytes {
		return nil, &core.PlaygroundResponseTooLargeError{
			LimitBytes: core.PlaygroundResponseReadLimitBytes,
		}
	}
	return data, nil
}

func decodePlaygroundJSON(body []byte) (domain.JSONMap, error) {
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, &core.PlaygroundResponseMalformedError{
			Reason: core.PlaygroundMalformedInvalidJSON,
		}
	}
	object, ok := raw.(map[string]any)
	if !ok {
		return nil, &core.PlaygroundResponseMalformedError{
			Reason: core.PlaygroundMalformedUnsafeJSON,
		}
	}
	return domain.JSONMap(object), nil
}

func ExtractErrorMessage(body []byte) *string {
	if body == nil {
		return nil
	}
	var resp struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || resp.Error == nil || resp.Error.Message == "" {
		return nil
	}
	return &resp.Error.Message
}

func usageInts(usage domain.JSONMap) map[string]int {
	if len(usage) == 0 {
		return nil
	}
	out := make(map[string]int, len(usage))
	for key, value := range usage {
		switch v := value.(type) {
		case int:
			out[key] = v
		case float64:
			out[key] = int(v)
		case json.Number:
			if n, err := v.Int64(); err == nil {
				out[key] = int(n)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
