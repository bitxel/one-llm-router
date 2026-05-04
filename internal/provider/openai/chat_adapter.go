package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/user/one-llm-router/internal/domain"
)

var errChatAdapterValidation = errors.New("chat adapter validation failed")
var errChatAdapterStreamFailed = errors.New("responses stream failed")

// chatStreamCaptureLimit matches the request-log body cap; larger streams can
// still pass through, but logging only needs a bounded diagnostic sample.
const chatStreamCaptureLimit = 1 << 20

var chatAdapterAllowedFields = map[string]struct{}{
	"model":                 {},
	"messages":              {},
	"tools":                 {},
	"tool_choice":           {},
	"parallel_tool_calls":   {},
	"stream":                {},
	"temperature":           {},
	"top_p":                 {},
	"stop":                  {},
	"n":                     {},
	"presence_penalty":      {},
	"frequency_penalty":     {},
	"seed":                  {},
	"service_tier":          {},
	"response_format":       {},
	"max_tokens":            {},
	"max_completion_tokens": {},
	"store":                 {},
	"stream_options":        {},
}

var chatAdapterUnsupportedFields = map[string]struct{}{
	"logprobs":      {},
	"top_logprobs":  {},
	"audio":         {},
	"modalities":    {},
	"prediction":    {},
	"function_call": {},
}

func (c *Client) ForwardChatCompletionsWithCapture(ctx context.Context, account domain.UpstreamAccount, accessToken string, original *http.Request, route GatewayRoute) (*http.Response, ForwardCapture, error) {
	adapted, err := buildChatAdapterRequest(original)
	if err != nil {
		return nil, ForwardCapture{}, fmt.Errorf("%w: %w", ErrInvalidUpstreamRequest, err)
	}

	upstreamReq := original.Clone(original.Context())
	upstreamReq.Body = io.NopCloser(bytes.NewReader(adapted.upstreamBody))
	upstreamReq.ContentLength = int64(len(adapted.upstreamBody))
	upstreamReq.Header = original.Header.Clone()
	upstreamReq.Header.Set("Content-Type", "application/json")

	route.OAuthBehavior = GatewayOAuthBehaviorCodexMapping
	route.BodyPolicy = GatewayBodyPolicyJSONCaptureAllowed
	resp, capture, err := c.ForwardCodexRouteWithCapture(ctx, account, accessToken, upstreamReq, route)
	if err != nil {
		return nil, capture, err
	}
	return adaptChatCompletionsResponse(resp, capture, adapted)
}

func adaptChatCompletionsResponse(resp *http.Response, capture ForwardCapture, adapted chatAdapterRequest) (*http.Response, ForwardCapture, error) {
	if resp.StatusCode >= http.StatusBadRequest {
		return resp, capture, nil
	}
	if adapted.stream {
		usageCapture := &chatStreamUsageCapture{}
		metadataCapture := &chatStreamMetadataCapture{}
		capture.TokenUsage = usageCapture.Snapshot
		capture.ModelParams = metadataCapture.Snapshot
		if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
			bodyCapture := newStreamingUpstreamBodyCapture(chatStreamCaptureLimit)
			capture.UpstreamResponseBodySnapshot = bodyCapture.Snapshot
			resp.Body = chatCompletionStreamFromResponses(
				&readCloser{Reader: io.TeeReader(resp.Body, bodyCapture), Closer: resp.Body},
				adapted.model,
				adapted.includeUsage,
				usageCapture,
				metadataCapture,
			)
		} else {
			buffered := bufio.NewReader(resp.Body)
			prefix, err := readBodyShapePrefix(buffered, 32)
			if err != nil {
				_ = resp.Body.Close()
				return nil, capture, fmt.Errorf("%w: read chat stream response prefix: %w", ErrUpstreamResponseInvalid, err)
			}
			body := io.MultiReader(bytes.NewReader(prefix), buffered)
			if looksLikeSSEBody(prefix) {
				bodyCapture := newStreamingUpstreamBodyCapture(chatStreamCaptureLimit)
				capture.UpstreamResponseBodySnapshot = bodyCapture.Snapshot
				resp.Body = chatCompletionStreamFromResponses(
					&readCloser{Reader: io.TeeReader(body, bodyCapture), Closer: resp.Body},
					adapted.model,
					adapted.includeUsage,
					usageCapture,
					metadataCapture,
				)
			} else {
				rawBody, err := readBoundedProviderBody(body, codexSSECollectLimit, "chat adapter upstream response too large")
				_ = resp.Body.Close()
				capture.UpstreamResponseBody = rawBody
				if err != nil {
					return nil, capture, fmt.Errorf("%w: read chat stream fallback body: %w", ErrUpstreamResponseInvalid, err)
				}
				converted, err := chatCompletionStreamFromResponsesJSON(rawBody, adapted.model, adapted.includeUsage, usageCapture, metadataCapture)
				if err != nil {
					return nil, capture, fmt.Errorf("%w: %w", ErrUpstreamResponseInvalid, err)
				}
				resp.Body = converted
			}
		}
		resp.Header.Del("Content-Length")
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("Trailer")
		resp.Header.Del("Transfer-Encoding")
		resp.Header.Set("Content-Type", "text/event-stream")
		resp.ContentLength = -1
		return resp, capture, nil
	}
	converted, statusCode, err := chatCompletionFromResponsesBody(resp.Body, resp.Header.Get("Content-Type"), adapted.model)
	_ = resp.Body.Close()
	if err != nil {
		return nil, capture, fmt.Errorf("%w: %w", ErrUpstreamResponseInvalid, err)
	}
	resp.StatusCode = statusCode
	resp.Status = fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode))
	resp.Body = io.NopCloser(bytes.NewReader(converted))
	resp.ContentLength = int64(len(converted))
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Trailer")
	resp.Header.Del("Transfer-Encoding")
	resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(converted)))
	resp.Header.Set("Content-Type", "application/json")
	return resp, capture, nil
}

type chatAdapterRequest struct {
	model        string
	stream       bool
	includeUsage bool
	upstreamBody []byte
}

func buildChatAdapterRequest(original *http.Request) (chatAdapterRequest, error) {
	if original.Body == nil {
		return chatAdapterRequest{}, fmt.Errorf("%w: missing request body", errChatAdapterValidation)
	}
	data, err := io.ReadAll(original.Body)
	if err != nil {
		return chatAdapterRequest{}, fmt.Errorf("read chat request body: %w", err)
	}
	original.Body = io.NopCloser(bytes.NewReader(data))

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return chatAdapterRequest{}, fmt.Errorf("%w: decode request JSON: %w", errChatAdapterValidation, err)
	}
	for field, value := range raw {
		if _, unsupported := chatAdapterUnsupportedFields[field]; unsupported {
			if !jsonRawIsNull(value) {
				return chatAdapterRequest{}, fmt.Errorf("%w: unsupported field %q", errChatAdapterValidation, field)
			}
			continue
		}
		if _, ok := chatAdapterAllowedFields[field]; !ok {
			return chatAdapterRequest{}, fmt.Errorf("%w: unknown field %q", errChatAdapterValidation, field)
		}
	}

	model, err := requiredJSONString(raw, "model")
	if err != nil {
		return chatAdapterRequest{}, err
	}
	messages, err := decodeChatMessages(raw["messages"])
	if err != nil {
		return chatAdapterRequest{}, err
	}

	stream, err := optionalJSONBool(raw["stream"])
	if err != nil {
		return chatAdapterRequest{}, err
	}
	if err := validateChatStore(raw["store"]); err != nil {
		return chatAdapterRequest{}, err
	}
	if err := validateChatN(raw["n"]); err != nil {
		return chatAdapterRequest{}, err
	}
	maxOutputTokens, err := chatMaxOutputTokens(raw["max_tokens"], raw["max_completion_tokens"])
	if err != nil {
		return chatAdapterRequest{}, err
	}

	instructions, input, err := convertChatMessages(messages)
	if err != nil {
		return chatAdapterRequest{}, err
	}
	payload := map[string]any{
		"model":        model,
		"instructions": instructions,
		"input":        input,
		"store":        false,
		"stream":       true,
	}
	if maxOutputTokens != nil {
		payload["max_output_tokens"] = *maxOutputTokens
	}
	copyOptionalRawJSON(payload, raw, "temperature")
	copyOptionalRawJSON(payload, raw, "top_p")
	copyOptionalRawJSON(payload, raw, "stop")
	copyOptionalRawJSON(payload, raw, "presence_penalty")
	copyOptionalRawJSON(payload, raw, "frequency_penalty")
	copyOptionalRawJSON(payload, raw, "seed")
	copyOptionalRawJSON(payload, raw, "service_tier")
	copyOptionalRawJSON(payload, raw, "parallel_tool_calls")
	if tools, ok, err := normalizeChatTools(raw["tools"]); err != nil {
		return chatAdapterRequest{}, err
	} else if ok {
		payload["tools"] = tools
	}
	if toolChoice, ok, err := normalizeChatToolChoice(raw["tool_choice"]); err != nil {
		return chatAdapterRequest{}, err
	} else if ok {
		payload["tool_choice"] = toolChoice
	}
	if text, ok, err := chatTextFormat(raw["response_format"]); err != nil {
		return chatAdapterRequest{}, err
	} else if ok {
		payload["text"] = map[string]any{"format": text}
	}
	includeUsage, err := chatStreamIncludeUsage(raw["stream_options"])
	if err != nil {
		return chatAdapterRequest{}, err
	}

	upstreamBody, err := marshalNativeCodexResponsesPayload(payload)
	if err != nil {
		return chatAdapterRequest{}, err
	}
	return chatAdapterRequest{model: model, stream: stream, includeUsage: includeUsage, upstreamBody: upstreamBody}, nil
}

func requiredJSONString(raw map[string]json.RawMessage, field string) (string, error) {
	value, ok := raw[field]
	if !ok || jsonRawIsNull(value) {
		return "", fmt.Errorf("%w: missing %q", errChatAdapterValidation, field)
	}
	var out string
	if err := json.Unmarshal(value, &out); err != nil || strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("%w: %q must be a non-empty string", errChatAdapterValidation, field)
	}
	return out, nil
}

func optionalJSONBool(value json.RawMessage) (bool, error) {
	if len(value) == 0 || jsonRawIsNull(value) {
		return false, nil
	}
	var out bool
	if err := json.Unmarshal(value, &out); err != nil {
		return false, fmt.Errorf("%w: stream must be boolean", errChatAdapterValidation)
	}
	return out, nil
}

func jsonRawIsNull(value json.RawMessage) bool {
	return len(value) == 0 || strings.TrimSpace(string(value)) == "null"
}

type chatMessage map[string]any

func decodeChatMessages(raw json.RawMessage) ([]chatMessage, error) {
	if len(raw) == 0 || jsonRawIsNull(raw) {
		return nil, fmt.Errorf("%w: missing messages", errChatAdapterValidation)
	}
	var values []map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("%w: messages must be an array of objects", errChatAdapterValidation)
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("%w: messages must be non-empty", errChatAdapterValidation)
	}
	out := make([]chatMessage, 0, len(values))
	for _, value := range values {
		role, _ := value["role"].(string)
		switch role {
		case "system", "developer", "user", "assistant", "tool":
		default:
			return nil, fmt.Errorf("%w: unsupported message role %q", errChatAdapterValidation, role)
		}
		out = append(out, chatMessage(value))
	}
	return out, nil
}

func validateChatStore(raw json.RawMessage) error {
	if len(raw) == 0 || jsonRawIsNull(raw) {
		return nil
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("%w: store must be boolean", errChatAdapterValidation)
	}
	if value {
		return fmt.Errorf("%w: store=true is not supported", errChatAdapterValidation)
	}
	return nil
}

func validateChatN(raw json.RawMessage) error {
	if len(raw) == 0 || jsonRawIsNull(raw) {
		return nil
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("%w: n must be integer", errChatAdapterValidation)
	}
	if value != 1 {
		return fmt.Errorf("%w: n must be 1", errChatAdapterValidation)
	}
	return nil
}

func chatMaxOutputTokens(maxTokensRaw, maxCompletionTokensRaw json.RawMessage) (*int, error) {
	maxTokens, hasMaxTokens, err := optionalJSONInt(maxTokensRaw, "max_tokens")
	if err != nil {
		return nil, err
	}
	maxCompletionTokens, hasMaxCompletionTokens, err := optionalJSONInt(maxCompletionTokensRaw, "max_completion_tokens")
	if err != nil {
		return nil, err
	}
	switch {
	case hasMaxTokens && hasMaxCompletionTokens && maxTokens != maxCompletionTokens:
		return nil, fmt.Errorf("%w: conflicting token limits", errChatAdapterValidation)
	case hasMaxCompletionTokens:
		return &maxCompletionTokens, nil
	case hasMaxTokens:
		return &maxTokens, nil
	default:
		return nil, nil
	}
}

func optionalJSONInt(raw json.RawMessage, field string) (int, bool, error) {
	if len(raw) == 0 || jsonRawIsNull(raw) {
		return 0, false, nil
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false, fmt.Errorf("%w: %s must be integer", errChatAdapterValidation, field)
	}
	if value < 0 {
		return 0, false, fmt.Errorf("%w: %s must be non-negative", errChatAdapterValidation, field)
	}
	return value, true, nil
}

func convertChatMessages(messages []chatMessage) (string, []any, error) {
	var instructions []string
	input := make([]any, 0, len(messages))
	for _, msg := range messages {
		role, _ := msg["role"].(string)
		switch role {
		case "system", "developer":
			text, err := chatTextContent(msg["content"])
			if err != nil {
				return "", nil, fmt.Errorf("%w: %s messages must be text-only", errChatAdapterValidation, role)
			}
			if text != "" {
				instructions = append(instructions, text)
			}
		default:
			items, err := chatInputItemsFromMessage(role, msg)
			if err != nil {
				return "", nil, err
			}
			input = append(input, items...)
		}
	}
	return strings.Join(instructions, "\n"), input, nil
}

func chatInputItemsFromMessage(role string, msg chatMessage) ([]any, error) {
	switch role {
	case "assistant":
		return assistantChatInputItems(msg)
	case "tool":
		callID := firstString(msg["tool_call_id"], msg["toolCallId"], msg["call_id"])
		if callID == "" {
			return nil, fmt.Errorf("%w: tool messages must include tool_call_id", errChatAdapterValidation)
		}
		return []any{map[string]any{
			"type":    "function_call_output",
			"call_id": callID,
			"output":  contentText(msg["content"]),
		}}, nil
	default:
		item := map[string]any{"role": role}
		for key, value := range msg {
			if key == "role" {
				continue
			}
			item[key] = value
		}
		return []any{item}, nil
	}
}

func assistantChatInputItems(msg chatMessage) ([]any, error) {
	var items []any
	var contentParts []any
	if text := contentText(msg["content"]); text != "" {
		contentParts = append(contentParts, map[string]any{"type": "output_text", "text": text})
	}
	if refusal, ok := msg["refusal"].(string); ok && refusal != "" {
		contentParts = append(contentParts, map[string]any{"type": "refusal", "refusal": refusal})
	}
	if len(contentParts) > 0 {
		items = append(items, map[string]any{"role": "assistant", "content": contentParts})
	}

	rawToolCalls, ok := msg["tool_calls"]
	if !ok || rawToolCalls == nil {
		return items, nil
	}
	toolCalls, ok := rawToolCalls.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: assistant tool_calls must be an array", errChatAdapterValidation)
	}
	for idx, raw := range toolCalls {
		toolCall, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: assistant tool_calls[%d] must be an object", errChatAdapterValidation, idx)
		}
		callID := firstString(toolCall["id"], toolCall["call_id"], toolCall["tool_call_id"])
		if callID == "" {
			return nil, fmt.Errorf("%w: assistant tool_calls[%d].id is required", errChatAdapterValidation, idx)
		}
		fn, ok := toolCall["function"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: assistant tool_calls[%d].function is required", errChatAdapterValidation, idx)
		}
		name := firstString(fn["name"])
		if name == "" {
			return nil, fmt.Errorf("%w: assistant tool_calls[%d].function.name is required", errChatAdapterValidation, idx)
		}
		args, ok := fn["arguments"].(string)
		if !ok {
			return nil, fmt.Errorf("%w: assistant tool_calls[%d].function.arguments must be a string", errChatAdapterValidation, idx)
		}
		items = append(items, map[string]any{
			"type":      "function_call",
			"call_id":   callID,
			"name":      name,
			"arguments": args,
		})
	}
	return items, nil
}

func chatTextContent(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case []any:
		parts := make([]string, 0, len(v))
		for _, part := range v {
			switch p := part.(type) {
			case string:
				parts = append(parts, p)
			case map[string]any:
				partType, _ := p["type"].(string)
				if partType != "" && partType != "text" {
					return "", errors.New("non-text content part")
				}
				text, ok := p["text"].(string)
				if !ok {
					return "", errors.New("text part missing text")
				}
				parts = append(parts, text)
			default:
				return "", errors.New("invalid content part")
			}
		}
		return strings.Join(parts, "\n"), nil
	case map[string]any:
		partType, _ := v["type"].(string)
		if partType != "" && partType != "text" {
			return "", errors.New("non-text content part")
		}
		text, ok := v["text"].(string)
		if !ok {
			return "", errors.New("text part missing text")
		}
		return text, nil
	default:
		return "", errors.New("invalid text content")
	}
}

func copyOptionalRawJSON(payload map[string]any, raw map[string]json.RawMessage, field string) {
	value, ok := raw[field]
	if !ok || jsonRawIsNull(value) {
		return
	}
	var decoded any
	if err := json.Unmarshal(value, &decoded); err == nil {
		payload[field] = decoded
	}
}

func normalizeChatTools(raw json.RawMessage) ([]any, bool, error) {
	if len(raw) == 0 || jsonRawIsNull(raw) {
		return nil, false, nil
	}
	var tools []map[string]any
	if err := json.Unmarshal(raw, &tools); err != nil {
		return nil, false, fmt.Errorf("%w: tools must be an array", errChatAdapterValidation)
	}
	out := make([]any, 0, len(tools))
	for _, tool := range tools {
		toolType, _ := tool["type"].(string)
		if toolType == "" {
			toolType = "function"
		}
		if toolType != "function" && toolType != "web_search" && toolType != "web_search_preview" {
			return nil, false, fmt.Errorf("%w: unsupported tool type %q", errChatAdapterValidation, toolType)
		}
		if toolType == "web_search_preview" {
			toolType = "web_search"
		}
		if fn, ok := tool["function"].(map[string]any); ok {
			name, _ := fn["name"].(string)
			if name == "" {
				return nil, false, fmt.Errorf("%w: function tool missing name", errChatAdapterValidation)
			}
			out = append(out, map[string]any{
				"type":        "function",
				"name":        name,
				"description": fn["description"],
				"parameters":  fn["parameters"],
			})
			continue
		}
		tool["type"] = toolType
		out = append(out, tool)
	}
	return out, true, nil
}

func normalizeChatToolChoice(raw json.RawMessage) (any, bool, error) {
	if len(raw) == 0 || jsonRawIsNull(raw) {
		return nil, false, nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, false, fmt.Errorf("%w: invalid tool_choice", errChatAdapterValidation)
	}
	choice, ok := decoded.(map[string]any)
	if !ok {
		return decoded, true, nil
	}
	if fn, ok := choice["function"].(map[string]any); ok {
		name, _ := fn["name"].(string)
		if name == "" {
			return nil, false, fmt.Errorf("%w: tool_choice function missing name", errChatAdapterValidation)
		}
		toolType, _ := choice["type"].(string)
		if toolType == "" {
			toolType = "function"
		}
		return map[string]any{"type": toolType, "name": name}, true, nil
	}
	return choice, true, nil
}

func chatTextFormat(raw json.RawMessage) (map[string]any, bool, error) {
	if len(raw) == 0 || jsonRawIsNull(raw) {
		return nil, false, nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, false, fmt.Errorf("%w: invalid response_format", errChatAdapterValidation)
	}
	if value, ok := decoded.(string); ok {
		if value != "text" && value != "json_object" {
			return nil, false, fmt.Errorf("%w: unsupported response_format %q", errChatAdapterValidation, value)
		}
		return map[string]any{"type": value}, true, nil
	}
	obj, ok := decoded.(map[string]any)
	if !ok {
		return nil, false, fmt.Errorf("%w: response_format must be string or object", errChatAdapterValidation)
	}
	formatType, _ := obj["type"].(string)
	switch formatType {
	case "text", "json_object":
		return map[string]any{"type": formatType}, true, nil
	case "json_schema":
		jsonSchema, ok := obj["json_schema"].(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("%w: response_format.json_schema required", errChatAdapterValidation)
		}
		out := map[string]any{"type": "json_schema"}
		for _, key := range []string{"name", "schema", "strict"} {
			if value, ok := jsonSchema[key]; ok {
				out[key] = value
			}
		}
		return out, true, nil
	default:
		return nil, false, fmt.Errorf("%w: unsupported response_format %q", errChatAdapterValidation, formatType)
	}
}

func chatStreamIncludeUsage(raw json.RawMessage) (bool, error) {
	if len(raw) == 0 || jsonRawIsNull(raw) {
		return false, nil
	}
	var options map[string]any
	if err := json.Unmarshal(raw, &options); err != nil {
		return false, fmt.Errorf("%w: stream_options must be an object", errChatAdapterValidation)
	}
	value, ok := options["include_usage"]
	if !ok || value == nil {
		return false, nil
	}
	include, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%w: stream_options.include_usage must be boolean", errChatAdapterValidation)
	}
	return include, nil
}

func chatCompletionFromResponsesBody(body io.Reader, contentType, model string) ([]byte, int, error) {
	data, err := readBoundedProviderBody(body, codexSSECollectLimit, "chat adapter upstream response too large")
	if err != nil {
		return nil, 0, fmt.Errorf("read responses body: %w", err)
	}
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") || bytes.Contains(data, []byte("data:")) {
		return chatCompletionFromResponsesSSE(data, model)
	}
	return chatCompletionFromResponsesJSON(data, model)
}

func chatCompletionFromResponsesJSON(data []byte, model string) ([]byte, int, error) {
	var response map[string]any
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, 0, fmt.Errorf("decode responses JSON: %w", err)
	}
	if errObj, ok := responseFailure(response); ok {
		out, err := json.Marshal(map[string]any{"error": errObj})
		return out, http.StatusBadGateway, err
	}
	msg := chatMessageFromResponse(response, "", "", nil)
	if !chatResponseHasAssistantPayload(response, msg) {
		return nil, 0, errors.New("responses JSON missing assistant output")
	}
	finishReason := "stop"
	if len(msg.ToolCalls) > 0 {
		finishReason = "tool_calls"
	}
	return marshalChatCompletion(response, model, msg, responseUsage(response), finishReason)
}

func looksLikeSSEBody(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return false
	}
	switch trimmed[0] {
	case ':', 'd', 'e':
		return true
	default:
		return false
	}
}

func readBodyShapePrefix(reader *bufio.Reader, limit int) ([]byte, error) {
	prefix := make([]byte, 0, limit)
	for len(prefix) < limit {
		b, err := reader.ReadByte()
		if errors.Is(err, io.EOF) {
			return prefix, nil
		}
		if err != nil {
			return prefix, err
		}
		prefix = append(prefix, b)
		if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
			return prefix, nil
		}
	}
	return prefix, nil
}

func chatCompletionFromResponsesSSE(data []byte, model string) ([]byte, int, error) {
	var content strings.Builder
	var refusal strings.Builder
	var response map[string]any
	var finishReason = "stop"
	terminal := false
	toolIndexer := newChatToolCallIndexer()
	var toolCalls []chatToolCallState

	events, err := parseSSEDataEvents(data)
	if err != nil {
		return nil, 0, err
	}
	for _, event := range events {
		var payload map[string]any
		if err := json.Unmarshal([]byte(event), &payload); err != nil {
			return nil, 0, fmt.Errorf("decode responses SSE event: %w", err)
		}
		eventType, _ := payload["type"].(string)
		switch eventType {
		case "response.output_text.delta":
			if delta, ok := payload["delta"].(string); ok {
				content.WriteString(delta)
			}
		case "response.output_text.done":
			if text, ok := payload["text"].(string); ok {
				content.Reset()
				content.WriteString(text)
			}
		case "response.refusal.delta":
			if delta, ok := payload["delta"].(string); ok {
				refusal.WriteString(delta)
			}
		case "response.failed", "error":
			errorPayload := extractResponsesError(payload)
			out, err := json.Marshal(map[string]any{"error": errorPayload})
			return out, http.StatusBadGateway, err
		case "response.completed", "response.incomplete":
			terminal = true
			if resp, ok := payload["response"].(map[string]any); ok {
				response = resp
				mergeResponseOutputToolCalls(&toolCalls, toolIndexer, response)
				if eventType == "response.incomplete" {
					finishReason = incompleteFinishReason(resp)
				}
			}
		}
		if delta := chatToolDeltaFromPayload(payload, toolIndexer); delta != nil {
			mergeChatToolCallDelta(&toolCalls, *delta)
		}
	}
	if !terminal {
		return nil, 0, errors.New("responses stream missing terminal event")
	}
	if response == nil {
		return nil, 0, errors.New("responses stream terminal event missing response")
	}
	msg := chatMessageFromResponse(response, content.String(), refusal.String(), toolCalls)
	if !chatResponseHasAssistantPayload(response, msg) {
		return nil, 0, errors.New("responses stream missing assistant output")
	}
	if len(msg.ToolCalls) > 0 {
		finishReason = "tool_calls"
	}
	return marshalChatCompletion(response, model, msg, responseUsage(response), finishReason)
}

func parseSSEDataEvents(data []byte) ([]string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), codexSSECollectLimit)
	var events []string
	var eventData strings.Builder
	flush := func() {
		if eventData.Len() == 0 {
			return
		}
		events = append(events, strings.TrimSuffix(eventData.String(), "\n"))
		eventData.Reset()
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			eventData.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			eventData.WriteByte('\n')
		}
	}
	flush()
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan responses SSE: %w", err)
	}
	return events, nil
}

type chatStreamUsageCapture struct {
	mu    sync.Mutex
	usage domain.JSONMap
}

func (c *chatStreamUsageCapture) Set(usage domain.JSONMap) {
	if c == nil || len(usage) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.usage = domain.JSONMap{}
	for key, value := range usage {
		c.usage[key] = value
	}
}

func (c *chatStreamUsageCapture) Snapshot() domain.JSONMap {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.usage) == 0 {
		return nil
	}
	out := domain.JSONMap{}
	for key, value := range c.usage {
		out[key] = value
	}
	return out
}

type chatStreamMetadataCapture struct {
	mu     sync.Mutex
	params domain.JSONMap
}

func (c *chatStreamMetadataCapture) MarkGeneratedProtocolID(id string) {
	if c == nil || id == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.params == nil {
		c.params = domain.JSONMap{}
	}
	c.params["chat_adapter_generated_id"] = true
	c.params["chat_adapter_protocol_id"] = id
}

func (c *chatStreamMetadataCapture) Snapshot() domain.JSONMap {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.params) == 0 {
		return nil
	}
	out := domain.JSONMap{}
	for key, value := range c.params {
		out[key] = value
	}
	return out
}

type streamingUpstreamBodyCapture struct {
	mu    sync.Mutex
	limit int64
	body  bytes.Buffer
}

func newStreamingUpstreamBodyCapture(limit int64) *streamingUpstreamBodyCapture {
	return &streamingUpstreamBodyCapture{limit: limit}
}

func (c *streamingUpstreamBodyCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if int64(c.body.Len()) < c.limit {
		remaining := int(c.limit - int64(c.body.Len()))
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = c.body.Write(p[:remaining])
	}
	return len(p), nil
}

func (c *streamingUpstreamBodyCapture) Snapshot() []byte {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	data := append([]byte(nil), c.body.Bytes()...)
	c.mu.Unlock()
	if len(data) == 0 {
		return nil
	}
	if collected, ok, err := CollectResponsesSSEBytes(data, false); err == nil && ok {
		return collected
	}
	return data
}

type readCloser struct {
	io.Reader
	io.Closer
}

func chatCompletionStreamFromResponses(upstream io.ReadCloser, model string, includeUsage bool, usageCapture *chatStreamUsageCapture, metadataCapture *chatStreamMetadataCapture) io.ReadCloser {
	reader, writer := io.Pipe()
	go func() {
		defer func() { _ = upstream.Close() }()
		err := writeChatCompletionStream(writer, upstream, model, includeUsage, usageCapture, metadataCapture)
		_ = writer.CloseWithError(err)
	}()
	return reader
}

func chatCompletionStreamFromResponsesJSON(data []byte, model string, includeUsage bool, usageCapture *chatStreamUsageCapture, metadataCapture *chatStreamMetadataCapture) (io.ReadCloser, error) {
	var response map[string]any
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode responses JSON: %w", err)
	}

	var out bytes.Buffer
	if errObj, ok := responseFailure(response); ok {
		if err := writeChatSSEPayload(&out, map[string]any{"error": errObj}); err != nil {
			return nil, err
		}
		if _, err := io.WriteString(&out, "data: [DONE]\n\n"); err != nil {
			return nil, err
		}
		return &terminalErrorReadCloser{
			reader: bytes.NewReader(out.Bytes()),
			err:    errChatAdapterStreamFailed,
		}, nil
	}

	msg := chatMessageFromResponse(response, "", "", nil)
	if !chatResponseHasAssistantPayload(response, msg) {
		return nil, errors.New("responses JSON missing assistant output")
	}

	usage := responseUsage(response)
	usageCapture.Set(recordUsageFromChatUsage(usage))

	chunkID := "chatcmpl_" + domain.NewRequestID()
	usingGeneratedID := true
	if responseID, ok := response["id"].(string); ok && responseID != "" {
		chunkID = responseID
		usingGeneratedID = false
	}
	markChunk := func() {
		if usingGeneratedID {
			metadataCapture.MarkGeneratedProtocolID(chunkID)
		}
	}
	created := time.Now().Unix()
	sentRole := false
	if content, ok := msg.Content.(string); ok && content != "" {
		markChunk()
		if err := writeChatSSEChunk(&out, chatStreamChunk(chunkID, created, model, "assistant", content, "", "", nil, nil, includeUsage)); err != nil {
			return nil, err
		}
		sentRole = true
	}
	if msg.Refusal != "" {
		role := ""
		if !sentRole {
			role = "assistant"
			sentRole = true
		}
		markChunk()
		if err := writeChatSSEChunk(&out, chatStreamChunk(chunkID, created, model, role, "", msg.Refusal, "", nil, nil, includeUsage)); err != nil {
			return nil, err
		}
	}

	toolIndexer := newChatToolCallIndexer()
	var toolCalls []chatToolCallState
	mergeResponseOutputToolCalls(&toolCalls, toolIndexer, response)
	if err := flushPendingToolCalls(&out, &toolCalls, chunkID, created, model, &sentRole, includeUsage, markChunk); err != nil {
		return nil, err
	}

	finishReason := "stop"
	if len(msg.ToolCalls) > 0 {
		finishReason = "tool_calls"
	} else if status, _ := response["status"].(string); status == "incomplete" {
		finishReason = incompleteFinishReason(response)
	}
	markChunk()
	if err := writeChatSSEChunk(&out, chatStreamChunk(chunkID, created, model, "", "", "", finishReason, nil, nil, includeUsage)); err != nil {
		return nil, err
	}
	if includeUsage {
		markChunk()
		if err := writeChatSSEPayload(&out, map[string]any{
			"id":      chunkID,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   model,
			"choices": []any{},
			"usage":   usage,
		}); err != nil {
			return nil, err
		}
	}
	if _, err := io.WriteString(&out, "data: [DONE]\n\n"); err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(out.Bytes())), nil
}

type terminalErrorReadCloser struct {
	reader *bytes.Reader
	err    error
}

func (r *terminalErrorReadCloser) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if errors.Is(err, io.EOF) && r.err != nil {
		return n, r.err
	}
	return n, err
}

func (r *terminalErrorReadCloser) Close() error {
	return nil
}

func writeChatCompletionStream(dst io.Writer, upstream io.Reader, model string, includeUsage bool, usageCapture *chatStreamUsageCapture, metadataCapture *chatStreamMetadataCapture) error {
	scanner := bufio.NewScanner(upstream)
	scanner.Buffer(make([]byte, 0, 64*1024), codexSSECollectLimit)
	created := time.Now().Unix()
	chunkID := "chatcmpl_" + domain.NewRequestID()
	usingGeneratedID := true
	sentChunk := false
	sentRole := false
	toolIndexer := newChatToolCallIndexer()
	var toolCalls []chatToolCallState
	sawToolCall := false
	terminalSeen := false
	var eventData strings.Builder
	adoptResponseID := func(response map[string]any) {
		if sentChunk || response == nil {
			return
		}
		if responseID, ok := response["id"].(string); ok && responseID != "" {
			chunkID = responseID
			usingGeneratedID = false
		}
	}
	markChunk := func() {
		if usingGeneratedID {
			metadataCapture.MarkGeneratedProtocolID(chunkID)
		}
		sentChunk = true
	}

	flush := func() error {
		if eventData.Len() == 0 {
			return nil
		}
		data := strings.TrimSuffix(eventData.String(), "\n")
		eventData.Reset()
		if data == "[DONE]" {
			if terminalSeen {
				return nil
			}
			return errors.New("responses stream ended before terminal event")
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return fmt.Errorf("decode responses stream event: %w", err)
		}
		eventType, _ := payload["type"].(string)
		switch eventType {
		case "response.created", "response.in_progress":
			response, _ := payload["response"].(map[string]any)
			adoptResponseID(response)
			return nil
		case "response.output_text.delta":
			delta, _ := payload["delta"].(string)
			role := ""
			if !sentRole {
				role = "assistant"
				sentRole = true
			}
			markChunk()
			return writeChatSSEChunk(dst, chatStreamChunk(chunkID, created, model, role, delta, "", "", nil, nil, includeUsage))
		case "response.refusal.delta":
			delta, _ := payload["delta"].(string)
			role := ""
			if !sentRole {
				role = "assistant"
				sentRole = true
			}
			markChunk()
			return writeChatSSEChunk(dst, chatStreamChunk(chunkID, created, model, role, "", delta, "", nil, nil, includeUsage))
		case "response.failed", "error":
			if err := writeChatSSEPayload(dst, map[string]any{"error": extractResponsesError(payload)}); err != nil {
				return err
			}
			if _, err := io.WriteString(dst, "data: [DONE]\n\n"); err != nil {
				return err
			}
			return errChatAdapterStreamFailed
		case "response.completed", "response.incomplete":
			terminalSeen = true
			response, _ := payload["response"].(map[string]any)
			if response == nil {
				return errors.New("responses stream terminal event missing response")
			}
			adoptResponseID(response)
			usageCapture.Set(recordUsageFromChatUsage(responseUsage(response)))
			mergeResponseOutputToolCalls(&toolCalls, toolIndexer, response)
			if err := flushPendingToolCalls(dst, &toolCalls, chunkID, created, model, &sentRole, includeUsage, markChunk); err != nil {
				return err
			}
			sawToolCall = sawToolCall || compactChatToolCalls(toolCalls) != nil
			finishReason := "stop"
			if sawToolCall {
				finishReason = "tool_calls"
			} else if eventType == "response.incomplete" {
				finishReason = incompleteFinishReason(response)
			}
			markChunk()
			if err := writeChatSSEChunk(dst, chatStreamChunk(chunkID, created, model, "", "", "", finishReason, nil, nil, includeUsage)); err != nil {
				return err
			}
			if includeUsage {
				markChunk()
				if err := writeChatSSEPayload(dst, map[string]any{
					"id":      chunkID,
					"object":  "chat.completion.chunk",
					"created": created,
					"model":   model,
					"choices": []any{},
					"usage":   responseUsage(response),
				}); err != nil {
					return err
				}
			}
			_, err := io.WriteString(dst, "data: [DONE]\n\n")
			return err
		default:
			if delta := chatToolDeltaFromPayload(payload, toolIndexer); delta != nil {
				toolState := mergeChatToolCallDelta(&toolCalls, *delta)
				streamDelta := toolState.buildStreamDelta()
				if streamDelta == nil {
					return nil
				}
				sawToolCall = true
				role := ""
				if !sentRole {
					role = "assistant"
					sentRole = true
				}
				markChunk()
				return writeChatSSEChunk(dst, chatStreamChunk(chunkID, created, model, role, "", "", "", []any{streamDelta.toChunkToolCall()}, nil, includeUsage))
			}
			return nil
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if data, ok := strings.CutPrefix(line, "data:"); ok {
			eventData.WriteString(strings.TrimSpace(data))
			eventData.WriteByte('\n')
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan responses stream: %w", err)
	}
	if terminalSeen {
		return nil
	}
	return errors.New("responses stream missing terminal event")
}

func chatStreamChunk(
	id string,
	created int64,
	model string,
	role string,
	content string,
	refusal string,
	finishReason string,
	toolCalls []any,
	usage map[string]any,
	includeUsage bool,
) map[string]any {
	delta := map[string]any{}
	if role != "" {
		delta["role"] = role
	}
	if content != "" {
		delta["content"] = content
	}
	if refusal != "" {
		delta["refusal"] = refusal
	}
	if len(toolCalls) > 0 {
		delta["tool_calls"] = toolCalls
	}
	choice := map[string]any{
		"index": 0,
		"delta": delta,
	}
	if finishReason != "" {
		choice["finish_reason"] = finishReason
	} else {
		choice["finish_reason"] = nil
	}
	chunk := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []any{choice},
	}
	if includeUsage {
		chunk["usage"] = usage
	}
	return chunk
}

func writeChatSSEChunk(dst io.Writer, payload map[string]any) error {
	return writeChatSSEPayload(dst, payload)
}

func writeChatSSEPayload(dst io.Writer, payload map[string]any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode chat stream chunk: %w", err)
	}
	_, err = fmt.Fprintf(dst, "data: %s\n\n", data)
	return err
}

type chatCompletionMessage struct {
	Content   any
	Refusal   string
	ToolCalls []any
}

func marshalChatCompletion(response map[string]any, model string, message chatCompletionMessage, usage map[string]any, finishReason string) ([]byte, int, error) {
	id := "chatcmpl_" + domain.NewRequestID()
	if response != nil {
		if responseID, ok := response["id"].(string); ok && responseID != "" {
			id = responseID
		}
	}
	messagePayload := map[string]any{
		"role":    "assistant",
		"content": message.Content,
	}
	if message.Refusal != "" {
		messagePayload["refusal"] = message.Refusal
	}
	if len(message.ToolCalls) > 0 {
		messagePayload["tool_calls"] = message.ToolCalls
	}
	payload := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"message":       messagePayload,
				"finish_reason": finishReason,
			},
		},
	}
	if usage != nil {
		payload["usage"] = usage
	}
	out, err := json.Marshal(payload)
	return out, http.StatusOK, err
}

func responseText(response map[string]any) string {
	if response == nil {
		return ""
	}
	if text, ok := response["output_text"].(string); ok {
		return text
	}
	if output, ok := response["output"].([]any); ok {
		var b strings.Builder
		for _, item := range output {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if content, ok := itemMap["content"].([]any); ok {
				for _, part := range content {
					partMap, ok := part.(map[string]any)
					if !ok {
						continue
					}
					if text, ok := partMap["text"].(string); ok {
						b.WriteString(text)
					}
				}
			}
		}
		return b.String()
	}
	return ""
}

func chatMessageFromResponse(response map[string]any, streamedContent, streamedRefusal string, streamedToolCalls []chatToolCallState) chatCompletionMessage {
	content := streamedContent
	if content == "" {
		content = responseText(response)
	}
	refusal := streamedRefusal
	if refusal == "" {
		refusal = responseRefusal(response)
	}
	toolCalls := compactChatToolCalls(streamedToolCalls)
	if len(toolCalls) == 0 {
		toolCalls = responseToolCalls(response)
	}

	var contentValue any = content
	if content == "" && (refusal != "" || len(toolCalls) > 0) {
		contentValue = nil
	}
	return chatCompletionMessage{
		Content:   contentValue,
		Refusal:   refusal,
		ToolCalls: toolCalls,
	}
}

func responseRefusal(response map[string]any) string {
	if response == nil {
		return ""
	}
	output, ok := response["output"].([]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, item := range output {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		content, ok := itemMap["content"].([]any)
		if !ok {
			continue
		}
		for _, part := range content {
			partMap, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if refusal, ok := partMap["refusal"].(string); ok {
				b.WriteString(refusal)
			}
		}
	}
	return b.String()
}

func responseToolCalls(response map[string]any) []any {
	if response == nil {
		return nil
	}
	indexer := newChatToolCallIndexer()
	var states []chatToolCallState
	mergeResponseOutputToolCalls(&states, indexer, response)
	return compactChatToolCalls(states)
}

func responseFailure(response map[string]any) (map[string]any, bool) {
	if response == nil {
		return nil, false
	}
	if errObj, ok := response["error"].(map[string]any); ok && len(errObj) > 0 {
		return errObj, true
	}
	status, _ := response["status"].(string)
	if status == "failed" || status == "cancelled" {
		return extractResponsesError(map[string]any{"response": response}), true
	}
	return nil, false
}

func chatResponseHasAssistantPayload(response map[string]any, message chatCompletionMessage) bool {
	if text, ok := message.Content.(string); ok && text != "" {
		return true
	}
	if _, ok := response["output_text"].(string); ok {
		return true
	}
	if _, ok := response["output"].([]any); ok {
		return true
	}
	return message.Refusal != "" || len(message.ToolCalls) > 0
}

type chatToolCallIndexer struct {
	indexes map[string]int
	next    int
}

func newChatToolCallIndexer() *chatToolCallIndexer {
	return &chatToolCallIndexer{indexes: map[string]int{}}
}

func (i *chatToolCallIndexer) indexFor(callID, name string) int {
	key := ""
	switch {
	case callID != "":
		key = "id:" + callID
	case name != "":
		key = "name:" + name
	default:
		return 0
	}
	if idx, ok := i.indexes[key]; ok {
		return idx
	}
	idx := i.next
	i.indexes[key] = idx
	i.next++
	return idx
}

type chatToolCallDelta struct {
	Index         int
	CallID        string
	Name          string
	Arguments     *string
	ToolType      string
	ArgumentsMode string
}

func (d chatToolCallDelta) toChunkToolCall() map[string]any {
	out := map[string]any{"index": d.Index}
	if d.CallID != "" {
		out["id"] = d.CallID
	}
	toolType := d.ToolType
	if toolType == "" {
		toolType = "function"
	}
	out["type"] = toolType
	if d.Name != "" || d.Arguments != nil {
		fn := map[string]any{}
		if d.Name != "" {
			fn["name"] = d.Name
		}
		if d.Arguments != nil {
			fn["arguments"] = *d.Arguments
		}
		out["function"] = fn
	}
	return out
}

type chatToolCallState struct {
	Index            int
	CallID           string
	Name             string
	Arguments        string
	ToolType         string
	EmittedCallID    string
	EmittedName      string
	EmittedArguments string
	EmittedToolType  string
}

func (s *chatToolCallState) applyDelta(delta chatToolCallDelta) {
	if delta.CallID != "" {
		s.CallID = delta.CallID
	}
	if delta.Name != "" {
		s.Name = delta.Name
	}
	if delta.Arguments != nil {
		switch delta.ArgumentsMode {
		case "replace":
			s.Arguments = *delta.Arguments
		default:
			s.Arguments += *delta.Arguments
		}
	}
	if delta.ToolType != "" {
		s.ToolType = delta.ToolType
	}
	if s.ToolType == "" {
		s.ToolType = "function"
	}
}

func (s *chatToolCallState) buildStreamDelta() *chatToolCallDelta {
	var callID string
	var name string
	var args *string
	toolType := s.ToolType
	if toolType == "" {
		toolType = "function"
	}
	if s.CallID != "" && s.CallID != s.EmittedCallID {
		callID = s.CallID
	}
	if s.Name != "" && s.Name != s.EmittedName {
		name = s.Name
	}
	if s.Arguments != s.EmittedArguments {
		pending := s.Arguments
		if strings.HasPrefix(s.Arguments, s.EmittedArguments) {
			pending = strings.TrimPrefix(s.Arguments, s.EmittedArguments)
		}
		args = &pending
	}
	if callID == "" && name == "" && args == nil && s.EmittedToolType == toolType {
		return nil
	}
	if callID != "" {
		s.EmittedCallID = s.CallID
	}
	if name != "" {
		s.EmittedName = s.Name
	}
	if args != nil {
		s.EmittedArguments = s.Arguments
	}
	s.EmittedToolType = toolType
	return &chatToolCallDelta{
		Index:     s.Index,
		CallID:    callID,
		Name:      name,
		Arguments: args,
		ToolType:  toolType,
	}
}

func (s chatToolCallState) toMessageToolCall() (map[string]any, bool) {
	fn := map[string]any{}
	if s.Name != "" {
		fn["name"] = s.Name
	}
	if s.Arguments != "" {
		fn["arguments"] = s.Arguments
	}
	if s.CallID == "" && len(fn) == 0 {
		return nil, false
	}
	toolType := s.ToolType
	if toolType == "" {
		toolType = "function"
	}
	out := map[string]any{"type": toolType}
	if s.CallID != "" {
		out["id"] = s.CallID
	}
	if len(fn) > 0 {
		out["function"] = fn
	}
	return out, true
}

func chatToolDeltaFromPayload(payload map[string]any, indexer *chatToolCallIndexer) *chatToolCallDelta {
	if !isChatToolCallEvent(payload) {
		return nil
	}
	candidate := chatToolCallCandidate(payload)
	deltaMap, _ := candidate["delta"].(map[string]any)
	deltaText, _ := candidate["delta"].(string)

	callID := firstString(candidate["call_id"], candidate["tool_call_id"], candidate["id"])
	if callID == "" && deltaMap != nil {
		callID = firstString(deltaMap["id"], deltaMap["call_id"], deltaMap["tool_call_id"])
	}

	name := firstString(candidate["name"], candidate["tool_name"])
	if name == "" && deltaMap != nil {
		name = firstString(deltaMap["name"])
	}
	if name == "" {
		if fn, ok := candidate["function"].(map[string]any); ok {
			name = firstString(fn["name"])
		}
	}
	if name == "" && deltaMap != nil {
		if fn, ok := deltaMap["function"].(map[string]any); ok {
			name = firstString(fn["name"])
		}
	}

	var arguments *string
	if args, ok := candidate["arguments"].(string); ok {
		arguments = &args
	}
	if arguments == nil && deltaText != "" {
		arguments = &deltaText
	}
	if arguments == nil && deltaMap != nil {
		if args, ok := deltaMap["arguments"].(string); ok {
			arguments = &args
		} else if fn, ok := deltaMap["function"].(map[string]any); ok {
			if args, ok := fn["arguments"].(string); ok {
				arguments = &args
			}
		}
	}

	toolType := firstString(candidate["tool_type"], candidate["type"])
	if strings.HasPrefix(toolType, "response.") {
		toolType = ""
	}
	if toolType == "tool_call" || toolType == "function_call" {
		toolType = "function"
	}
	if callID == "" && name == "" && arguments == nil {
		return nil
	}
	return &chatToolCallDelta{
		Index:         indexer.indexFor(callID, name),
		CallID:        callID,
		Name:          name,
		Arguments:     arguments,
		ToolType:      toolType,
		ArgumentsMode: chatToolArgumentsMode(payload),
	}
}

func isChatToolCallEvent(payload map[string]any) bool {
	eventType, _ := payload["type"].(string)
	if strings.Contains(eventType, "tool_call") || strings.Contains(eventType, "function_call") {
		return true
	}
	if item, ok := payload["item"].(map[string]any); ok {
		itemType, _ := item["type"].(string)
		if strings.Contains(itemType, "tool") || strings.Contains(itemType, "function") {
			return true
		}
		for _, key := range []string{"call_id", "tool_call_id", "arguments", "function", "name"} {
			if _, ok := item[key]; ok {
				return true
			}
		}
	}
	if _, ok := payload["call_id"]; ok {
		return true
	}
	if _, ok := payload["tool_call_id"]; ok {
		return true
	}
	if _, hasArgs := payload["arguments"]; hasArgs {
		_, hasName := payload["name"]
		_, hasFn := payload["function"]
		return hasName || hasFn
	}
	return false
}

func chatToolCallCandidate(payload map[string]any) map[string]any {
	if item, ok := payload["item"].(map[string]any); ok {
		itemType, _ := item["type"].(string)
		if strings.Contains(itemType, "tool") || strings.Contains(itemType, "function") {
			return item
		}
		for _, key := range []string{"call_id", "tool_call_id", "arguments", "function", "name"} {
			if _, ok := item[key]; ok {
				return item
			}
		}
	}
	return payload
}

func chatToolArgumentsMode(payload map[string]any) string {
	eventType, _ := payload["type"].(string)
	switch {
	case strings.HasSuffix(eventType, ".delta"):
		return "append"
	case strings.HasSuffix(eventType, ".done"), eventType == "response.output_item.added":
		return "replace"
	default:
		return "replace"
	}
}

func mergeChatToolCallDelta(states *[]chatToolCallState, delta chatToolCallDelta) *chatToolCallState {
	for len(*states) <= delta.Index {
		*states = append(*states, chatToolCallState{Index: len(*states), ToolType: "function"})
	}
	state := &(*states)[delta.Index]
	state.applyDelta(delta)
	return state
}

func mergeResponseOutputToolCalls(states *[]chatToolCallState, indexer *chatToolCallIndexer, response map[string]any) {
	if response == nil {
		return
	}
	output, ok := response["output"].([]any)
	if !ok {
		return
	}
	for _, raw := range output {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		payload := map[string]any{
			"type": "response.output_item.done",
			"item": item,
		}
		if delta := chatToolDeltaFromPayload(payload, indexer); delta != nil {
			mergeChatToolCallDelta(states, *delta)
		}
	}
}

func flushPendingToolCalls(
	dst io.Writer,
	states *[]chatToolCallState,
	chunkID string,
	created int64,
	model string,
	sentRole *bool,
	includeUsage bool,
	onChunk func(),
) error {
	for idx := range *states {
		streamDelta := (&(*states)[idx]).buildStreamDelta()
		if streamDelta == nil {
			continue
		}
		role := ""
		if !*sentRole {
			role = "assistant"
			*sentRole = true
		}
		if onChunk != nil {
			onChunk()
		}
		if err := writeChatSSEChunk(dst, chatStreamChunk(chunkID, created, model, role, "", "", "", []any{streamDelta.toChunkToolCall()}, nil, includeUsage)); err != nil {
			return err
		}
	}
	return nil
}

func compactChatToolCalls(states []chatToolCallState) []any {
	if len(states) == 0 {
		return nil
	}
	out := make([]any, 0, len(states))
	for _, state := range states {
		if call, ok := state.toMessageToolCall(); ok {
			out = append(out, call)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func responseUsage(response map[string]any) map[string]any {
	if response == nil {
		return nil
	}
	rawUsage, ok := response["usage"].(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	if value, ok := rawUsage["input_tokens"]; ok {
		out["prompt_tokens"] = value
	}
	if value, ok := rawUsage["output_tokens"]; ok {
		out["completion_tokens"] = value
	}
	if value, ok := rawUsage["total_tokens"]; ok {
		out["total_tokens"] = value
	}
	if details, ok := rawUsage["input_tokens_details"].(map[string]any); ok {
		if cached, ok := details["cached_tokens"]; ok {
			out["prompt_tokens_details"] = map[string]any{"cached_tokens": cached}
		}
	}
	if details, ok := rawUsage["output_tokens_details"].(map[string]any); ok {
		if reasoning, ok := details["reasoning_tokens"]; ok {
			out["completion_tokens_details"] = map[string]any{"reasoning_tokens": reasoning}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func recordUsageFromChatUsage(usage map[string]any) domain.JSONMap {
	if len(usage) == 0 {
		return nil
	}
	out := domain.JSONMap{}
	if value, ok := usage["prompt_tokens"]; ok {
		out["input"] = value
	}
	if value, ok := usage["completion_tokens"]; ok {
		out["output"] = value
	}
	if details, ok := usage["prompt_tokens_details"].(map[string]any); ok {
		if cached, ok := details["cached_tokens"]; ok {
			out["cached_input"] = cached
		}
	}
	if details, ok := usage["completion_tokens_details"].(map[string]any); ok {
		if reasoning, ok := details["reasoning_tokens"]; ok {
			out["reasoning"] = reasoning
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func extractResponsesError(payload map[string]any) map[string]any {
	if errorObj, ok := payload["error"].(map[string]any); ok {
		return errorObj
	}
	if response, ok := payload["response"].(map[string]any); ok {
		if errorObj, ok := response["error"].(map[string]any); ok {
			return errorObj
		}
	}
	return map[string]any{
		"code":    "upstream_error",
		"message": "Upstream response failed",
		"type":    "server_error",
	}
}

func incompleteFinishReason(response map[string]any) string {
	if details, ok := response["incomplete_details"].(map[string]any); ok {
		if reason, ok := details["reason"].(string); ok {
			switch reason {
			case "max_output_tokens", "max_tokens":
				return "length"
			case "content_filter":
				return "content_filter"
			}
		}
	}
	return "stop"
}
