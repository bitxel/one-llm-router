package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type codexResponsesRequestMode string

const (
	codexResponsesOpenAICompat codexResponsesRequestMode = "openai_compat"
	codexResponsesNative       codexResponsesRequestMode = "codex_native"
)

type codexResponsesRequest struct {
	Model              string
	Messages           []any
	Input              any
	Instructions       string
	Tools              []any
	ToolChoice         any
	ParallelToolCalls  *bool
	Reasoning          map[string]any
	Store              bool
	StreamRequested    bool
	Include            []string
	ServiceTier        string
	Conversation       string
	PreviousResponseID string
	Truncation         string
	PromptCacheKey     string
	Text               map[string]any
	Payload            map[string]any
}

type codexResponsesCompactRequest struct {
	Model          string
	Messages       []any
	Input          any
	Instructions   string
	Reasoning      map[string]any
	Store          bool
	ServiceTier    string
	PromptCacheKey string
	Payload        map[string]any
}

var codexUnsupportedUpstreamFields = map[string]struct{}{
	"max_output_tokens":      {},
	"prompt_cache_retention": {},
	"safety_identifier":      {},
	"temperature":            {},
}

var codexUnsupportedToolTypes = map[string]struct{}{
	"file_search":          {},
	"code_interpreter":     {},
	"computer_use":         {},
	"computer_use_preview": {},
	// "image_generation":     {},
}

var codexResponsesIncludeAllowlist = map[string]struct{}{
	"code_interpreter_call.outputs":         {},
	"computer_call_output.output.image_url": {},
	"file_search_call.results":              {},
	"message.input_image.image_url":         {},
	"message.output_text.logprobs":          {},
	"reasoning.encrypted_content":           {},
	"web_search_call.action.sources":        {},
}

var codexInterleavedReasoningKeys = map[string]struct{}{
	"function_call":     {},
	"reasoning_content": {},
	"reasoning_details": {},
	"tool_calls":        {},
}

var codexInterleavedReasoningPartTypes = map[string]struct{}{
	"reasoning":         {},
	"reasoning_content": {},
	"reasoning_details": {},
}

var codexAssistantTextPartTypes = map[string]struct{}{
	"input_text":  {},
	"output_text": {},
	"text":        {},
}

var codexToolTextPartTypes = map[string]struct{}{
	"input_text":  {},
	"output_text": {},
	"refusal":     {},
	"text":        {},
}

func normalizeCodexResponsesBody(data []byte) ([]byte, bool, error) {
	req, err := decodeCodexResponsesRequest(data, codexResponsesOpenAICompat)
	if err != nil {
		return nil, false, err
	}
	encoded, err := json.Marshal(req.Payload)
	if err != nil {
		return nil, false, fmt.Errorf("encode codex upstream request body: %w", err)
	}
	return encoded, !req.StreamRequested, nil
}

func normalizeNativeCodexResponsesBody(data []byte) ([]byte, error) {
	req, err := decodeCodexResponsesRequest(data, codexResponsesNative)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(req.Payload)
	if err != nil {
		return nil, fmt.Errorf("encode native codex upstream request body: %w", err)
	}
	return encoded, nil
}

func normalizeCodexResponsesCompactBody(data []byte) ([]byte, error) {
	req, err := decodeCodexResponsesCompactRequest(data, codexResponsesOpenAICompat)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(req.Payload)
	if err != nil {
		return nil, fmt.Errorf("encode compact upstream request body: %w", err)
	}
	return encoded, nil
}

func normalizeNativeCodexResponsesCompactBody(data []byte) ([]byte, error) {
	req, err := decodeCodexResponsesCompactRequest(data, codexResponsesNative)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(req.Payload)
	if err != nil {
		return nil, fmt.Errorf("encode native compact upstream request body: %w", err)
	}
	return encoded, nil
}

func marshalNativeCodexResponsesPayload(payload map[string]any) ([]byte, error) {
	req, err := normalizeCodexResponsesPayloadMap(payload, codexResponsesNative)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(req.Payload)
	if err != nil {
		return nil, fmt.Errorf("encode responses request: %w", err)
	}
	return encoded, nil
}

func decodeCodexResponsesRequest(data []byte, mode codexResponsesRequestMode) (codexResponsesRequest, error) {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return codexResponsesRequest{}, fmt.Errorf("decode codex upstream request body: %w", err)
	}
	return normalizeCodexResponsesPayloadMap(payload, mode)
}

func normalizeCodexResponsesPayloadMap(payload map[string]any, mode codexResponsesRequestMode) (codexResponsesRequest, error) {
	if payload == nil {
		return codexResponsesRequest{}, errors.New("responses request body must be an object")
	}
	if err := validateCodexResponsesObjectFields(payload); err != nil {
		return codexResponsesRequest{}, err
	}
	normalizeCodexOpenAICompatibleAliases(payload)
	if err := validateCodexResponsesScalarFields(payload); err != nil {
		return codexResponsesRequest{}, err
	}
	normalizeCodexPreviousResponseID(payload)
	if err := validateCodexConversation(payload); err != nil {
		return codexResponsesRequest{}, err
	}
	if err := validateCodexTruncation(payload); err != nil {
		return codexResponsesRequest{}, err
	}
	model, err := requiredCodexString(payload, "model", "responses request model is required")
	if err != nil {
		return codexResponsesRequest{}, err
	}
	instructions, err := codexInstructions(payload)
	if err != nil {
		return codexResponsesRequest{}, err
	}
	stream, err := optionalCodexBool(payload, "stream")
	if err != nil {
		return codexResponsesRequest{}, err
	}
	store, err := optionalCodexBool(payload, "store")
	if err != nil {
		return codexResponsesRequest{}, err
	}
	if store != nil && *store {
		return codexResponsesRequest{}, errors.New("responses request store must be false")
	}
	if err := validateCodexInclude(payload); err != nil {
		return codexResponsesRequest{}, err
	}
	if err := normalizeCodexTools(payload); err != nil {
		return codexResponsesRequest{}, err
	}
	normalizeCodexToolChoice(payload)

	messages, hasMessages, err := codexMessages(payload)
	if err != nil {
		return codexResponsesRequest{}, err
	}
	inputMissing := inputMissingOrEmpty(payload["input"])
	if mode == codexResponsesOpenAICompat {
		switch {
		case hasMessages && !inputMissing:
			return codexResponsesRequest{}, errors.New("responses request must provide either messages or input, not both")
		case hasMessages:
			coercedInstructions, input := coerceMessages(instructions, messages)
			payload["instructions"] = coercedInstructions
			payload["input"] = input
			delete(payload, "messages")
			instructions = coercedInstructions
		case inputMissing:
			return codexResponsesRequest{}, errors.New("responses request input or messages is required")
		}
	} else if inputMissing {
		return codexResponsesRequest{}, errors.New("responses request input is required")
	}
	if err := normalizeCodexInput(payload); err != nil {
		return codexResponsesRequest{}, err
	}
	payload["store"] = false
	payload["stream"] = true
	stripCodexUnsupportedUpstreamFields(payload)

	return codexResponsesRequest{
		Model:              model,
		Messages:           messages,
		Input:              payload["input"],
		Instructions:       instructions,
		Tools:              codexAnySlice(payload["tools"]),
		ToolChoice:         payload["tool_choice"],
		ParallelToolCalls:  codexBoolPointer(payload["parallel_tool_calls"]),
		Reasoning:          codexAnyMap(payload["reasoning"]),
		Store:              false,
		StreamRequested:    stream != nil && *stream,
		Include:            codexStringSlice(payload["include"]),
		ServiceTier:        codexStringValue(payload["service_tier"]),
		Conversation:       codexStringValue(payload["conversation"]),
		PreviousResponseID: codexStringValue(payload["previous_response_id"]),
		Truncation:         codexStringValue(payload["truncation"]),
		PromptCacheKey:     codexStringValue(payload["prompt_cache_key"]),
		Text:               codexAnyMap(payload["text"]),
		Payload:            payload,
	}, nil
}

func decodeCodexResponsesCompactRequest(data []byte, mode codexResponsesRequestMode) (codexResponsesCompactRequest, error) {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return codexResponsesCompactRequest{}, fmt.Errorf("decode compact upstream request body: %w", err)
	}
	if payload == nil {
		return codexResponsesCompactRequest{}, errors.New("compact request body must be an object")
	}
	if err := validateCodexOptionalObject(payload, "reasoning", "compact request reasoning must be an object"); err != nil {
		return codexResponsesCompactRequest{}, err
	}
	normalizeCodexOpenAICompatibleAliases(payload)
	if err := validateCodexOptionalString(payload, "service_tier", "compact request service_tier must be a string"); err != nil {
		return codexResponsesCompactRequest{}, err
	}
	if err := validateCodexOptionalString(payload, "prompt_cache_key", "compact request prompt_cache_key must be a string"); err != nil {
		return codexResponsesCompactRequest{}, err
	}
	model, err := requiredCodexString(payload, "model", "compact request model is required")
	if err != nil {
		return codexResponsesCompactRequest{}, err
	}
	instructions, err := codexInstructions(payload)
	if err != nil {
		return codexResponsesCompactRequest{}, err
	}
	store, err := optionalCodexBool(payload, "store")
	if err != nil {
		return codexResponsesCompactRequest{}, err
	}
	if store != nil && *store {
		return codexResponsesCompactRequest{}, errors.New("compact request store must be false")
	}
	messages, hasMessages, err := codexMessages(payload)
	if err != nil {
		return codexResponsesCompactRequest{}, err
	}
	inputMissing := inputMissingOrEmpty(payload["input"])
	if mode == codexResponsesOpenAICompat {
		switch {
		case hasMessages && !inputMissing:
			return codexResponsesCompactRequest{}, errors.New("compact request must provide either messages or input, not both")
		case hasMessages:
			coercedInstructions, input := coerceMessages(instructions, messages)
			payload["instructions"] = coercedInstructions
			payload["input"] = input
			delete(payload, "messages")
			instructions = coercedInstructions
		case inputMissing:
			return codexResponsesCompactRequest{}, errors.New("compact request input or messages is required")
		}
	} else if inputMissing {
		return codexResponsesCompactRequest{}, errors.New("compact request input is required")
	}
	if err := normalizeCodexInput(payload); err != nil {
		return codexResponsesCompactRequest{}, err
	}
	stripCodexUnsupportedUpstreamFields(payload)
	delete(payload, "store")

	return codexResponsesCompactRequest{
		Model:          model,
		Messages:       messages,
		Input:          payload["input"],
		Instructions:   instructions,
		Reasoning:      codexAnyMap(payload["reasoning"]),
		Store:          false,
		ServiceTier:    codexStringValue(payload["service_tier"]),
		PromptCacheKey: codexStringValue(payload["prompt_cache_key"]),
		Payload:        payload,
	}, nil
}

func normalizeCodexOpenAICompatibleAliases(payload map[string]any) {
	moveCodexStringAlias(payload, "promptCacheKey", "prompt_cache_key")
	moveCodexStringAlias(payload, "promptCacheRetention", "prompt_cache_retention")

	reasoning := codexAnyMap(payload["reasoning"])
	if reasoning == nil {
		reasoning = map[string]any{}
	}
	moveCodexNestedStringAlias(payload, &reasoning, "reasoningEffort", "effort")
	moveCodexNestedStringAlias(payload, &reasoning, "reasoningSummary", "summary")
	if len(reasoning) > 0 {
		payload["reasoning"] = reasoning
	}

	text := codexAnyMap(payload["text"])
	if text == nil {
		text = map[string]any{}
	}
	moveCodexNestedStringAlias(payload, &text, "textVerbosity", "verbosity")
	moveCodexNestedStringAlias(payload, &text, "verbosity", "verbosity")
	if len(text) > 0 {
		payload["text"] = text
	}

	if serviceTier, ok := payload["service_tier"].(string); ok && strings.EqualFold(strings.TrimSpace(serviceTier), "fast") {
		payload["service_tier"] = "priority"
	}
}

func moveCodexStringAlias(payload map[string]any, from, to string) {
	value, ok := payload[from]
	if !ok {
		return
	}
	delete(payload, from)
	if _, exists := payload[to]; exists {
		return
	}
	if text, ok := value.(string); ok {
		payload[to] = text
	}
}

func moveCodexNestedStringAlias(payload map[string]any, target *map[string]any, from, to string) {
	value, ok := payload[from]
	if !ok {
		return
	}
	delete(payload, from)
	if _, exists := (*target)[to]; exists {
		return
	}
	if text, ok := value.(string); ok {
		(*target)[to] = text
	}
}

func requiredCodexString(payload map[string]any, field, message string) (string, error) {
	value, ok := payload[field]
	if !ok || value == nil {
		return "", errors.New(message)
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", errors.New(message)
	}
	return text, nil
}

func codexInstructions(payload map[string]any) (string, error) {
	value, ok := payload["instructions"]
	if !ok || value == nil {
		payload["instructions"] = ""
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", errors.New("responses request instructions must be a string")
	}
	return text, nil
}

func optionalCodexBool(payload map[string]any, field string) (*bool, error) {
	value, ok := payload[field]
	if !ok || value == nil {
		return nil, nil
	}
	flag, ok := value.(bool)
	if !ok {
		return nil, fmt.Errorf("%s must be boolean", field)
	}
	return &flag, nil
}

func validateCodexResponsesObjectFields(payload map[string]any) error {
	if err := validateCodexOptionalObject(payload, "reasoning", "responses request reasoning must be an object"); err != nil {
		return err
	}
	if err := validateCodexOptionalObject(payload, "text", "responses request text must be an object"); err != nil {
		return err
	}
	if text := codexAnyMap(payload["text"]); text != nil {
		if err := validateCodexOptionalObject(text, "format", "responses request text.format must be an object"); err != nil {
			return err
		}
	}
	return nil
}

func validateCodexResponsesScalarFields(payload map[string]any) error {
	for _, field := range []struct {
		name    string
		message string
	}{
		{name: "service_tier", message: "responses request service_tier must be a string"},
		{name: "conversation", message: "responses request conversation must be a string"},
		{name: "previous_response_id", message: "responses request previous_response_id must be a string"},
		{name: "prompt_cache_key", message: "responses request prompt_cache_key must be a string"},
	} {
		if err := validateCodexOptionalString(payload, field.name, field.message); err != nil {
			return err
		}
	}
	return validateCodexToolChoice(payload)
}

func validateCodexOptionalObject(payload map[string]any, field, message string) error {
	value, ok := payload[field]
	if !ok || value == nil {
		return nil
	}
	if _, ok := value.(map[string]any); !ok {
		return errors.New(message)
	}
	return nil
}

func validateCodexOptionalString(payload map[string]any, field, message string) error {
	value, ok := payload[field]
	if !ok || value == nil {
		return nil
	}
	if _, ok := value.(string); !ok {
		return errors.New(message)
	}
	return nil
}

func validateCodexToolChoice(payload map[string]any) error {
	value, ok := payload["tool_choice"]
	if !ok || value == nil {
		return nil
	}
	switch value.(type) {
	case string, map[string]any:
		return nil
	default:
		return errors.New("responses request tool_choice must be a string or object")
	}
}

func codexMessages(payload map[string]any) ([]any, bool, error) {
	value, ok := payload["messages"]
	if !ok || value == nil {
		return nil, false, nil
	}
	messages, ok := value.([]any)
	if !ok {
		return nil, false, errors.New("responses request messages must be an array")
	}
	return messages, true, nil
}

func normalizeCodexInput(payload map[string]any) error {
	switch input := payload["input"].(type) {
	case string:
		payload["input"] = []any{inputTextMessage(input)}
	case []any:
		if codexInputHasFileID(input) {
			return errors.New("input_file.file_id is not supported")
		}
		sanitized, err := sanitizeCodexInputItems(input)
		if err != nil {
			return err
		}
		payload["input"] = sanitized
	default:
		return errors.New("responses request input must be a string or array")
	}
	return nil
}

func sanitizeCodexInputItems(items []any) ([]any, error) {
	out := make([]any, 0, len(items))
	for _, item := range items {
		sanitized, err := sanitizeCodexInputItem(item)
		if err != nil {
			return nil, err
		}
		out = append(out, sanitized)
	}
	return out, nil
}

func sanitizeCodexInputItem(item any) (any, error) {
	obj, ok := item.(map[string]any)
	if !ok {
		return item, nil
	}
	out := make(map[string]any, len(obj))
	for key, value := range obj {
		if _, interleaved := codexInterleavedReasoningKeys[key]; interleaved {
			continue
		}
		if key == "content" {
			if sanitized, ok := sanitizeCodexContent(value); ok {
				out[key] = sanitized
			}
			continue
		}
		out[key] = value
	}
	return normalizeCodexRoleInputItem(out)
}

func sanitizeCodexContent(value any) (any, bool) {
	switch typed := value.(type) {
	case []any:
		out := make([]any, 0, len(typed))
		for _, part := range typed {
			sanitized, ok := sanitizeCodexContentPart(part)
			if ok {
				out = append(out, sanitized)
			}
		}
		return out, true
	case map[string]any:
		return sanitizeCodexContentPart(typed)
	default:
		return value, true
	}
}

func sanitizeCodexContentPart(part any) (any, bool) {
	obj, ok := part.(map[string]any)
	if !ok {
		return part, true
	}
	partType, _ := obj["type"].(string)
	if _, interleaved := codexInterleavedReasoningPartTypes[partType]; interleaved {
		return nil, false
	}
	out := make(map[string]any, len(obj))
	for key, value := range obj {
		if _, interleaved := codexInterleavedReasoningKeys[key]; interleaved {
			continue
		}
		out[key] = value
	}
	return out, true
}

func normalizeCodexRoleInputItem(value map[string]any) (any, error) {
	role, _ := value["role"].(string)
	switch role {
	case "assistant":
		return normalizeCodexAssistantInputItem(value), nil
	case "tool":
		return normalizeCodexToolInputItem(value)
	default:
		return value, nil
	}
}

func normalizeCodexAssistantInputItem(value map[string]any) any {
	content, ok := value["content"]
	if !ok {
		return value
	}
	updated := make(map[string]any, len(value))
	for key, item := range value {
		updated[key] = item
	}
	updated["content"] = normalizeCodexAssistantContent(content)
	return updated
}

func normalizeCodexAssistantContent(content any) any {
	switch typed := content.(type) {
	case nil:
		return nil
	case string:
		return []any{map[string]any{"type": "output_text", "text": typed}}
	case []any:
		out := make([]any, 0, len(typed))
		for _, part := range typed {
			out = append(out, normalizeCodexAssistantContentPart(part))
		}
		return out
	case map[string]any:
		return []any{normalizeCodexAssistantContentPart(typed)}
	default:
		return content
	}
}

func normalizeCodexAssistantContentPart(part any) any {
	if text, ok := extractCodexTextContentPart(part, codexAssistantTextPartTypes); ok {
		return map[string]any{"type": "output_text", "text": text}
	}
	if text, ok := part.(string); ok {
		return map[string]any{"type": "output_text", "text": text}
	}
	return part
}

func normalizeCodexToolInputItem(value map[string]any) (any, error) {
	callID := firstNonEmptyCodexString(value["tool_call_id"], value["toolCallId"], value["call_id"])
	if callID == "" {
		return nil, errors.New("tool input items must include 'tool_call_id'")
	}
	outputValue := value["output"]
	if outputValue == nil {
		outputValue = value["content"]
	}
	output, err := normalizeCodexToolOutputValue(outputValue)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"type":    "function_call_output",
		"call_id": callID,
		"output":  output,
	}, nil
}

func normalizeCodexToolOutputValue(content any) (string, error) {
	switch typed := content.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	case []any:
		parts := make([]string, 0, len(typed))
		for _, part := range typed {
			if text, ok := part.(string); ok {
				parts = append(parts, text)
				continue
			}
			if text, ok := extractCodexTextContentPart(part, codexToolTextPartTypes); ok {
				parts = append(parts, text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, ""), nil
		}
		return marshalCodexCompactJSON(content)
	case map[string]any:
		if text, ok := extractCodexTextContentPart(typed, codexToolTextPartTypes); ok {
			return text, nil
		}
		return marshalCodexCompactJSON(content)
	default:
		return fmt.Sprint(content), nil
	}
}

func extractCodexTextContentPart(part any, allowedTypes map[string]struct{}) (string, bool) {
	obj, ok := part.(map[string]any)
	if !ok {
		return "", false
	}
	partType, _ := obj["type"].(string)
	if text, ok := obj["text"].(string); ok {
		if partType == "" {
			return text, true
		}
		if _, allowed := allowedTypes[partType]; allowed {
			return text, true
		}
	}
	if partType == "refusal" {
		if refusal, ok := obj["refusal"].(string); ok {
			return refusal, true
		}
	}
	return "", false
}

func marshalCodexCompactJSON(value any) (string, error) {
	var builder strings.Builder
	encoder := json.NewEncoder(&builder)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", fmt.Errorf("encode tool output: %w", err)
	}
	return strings.TrimSuffix(builder.String(), "\n"), nil
}

func codexInputHasFileID(items []any) bool {
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if isCodexInputFileWithID(obj) {
			return true
		}
		content := obj["content"]
		var parts []any
		switch typed := content.(type) {
		case []any:
			parts = typed
		case map[string]any:
			parts = []any{typed}
		}
		for _, part := range parts {
			partObj, ok := part.(map[string]any)
			if ok && isCodexInputFileWithID(partObj) {
				return true
			}
		}
	}
	return false
}

func isCodexInputFileWithID(item map[string]any) bool {
	if item["type"] != "input_file" {
		return false
	}
	fileID, ok := item["file_id"].(string)
	return ok && fileID != ""
}

func validateCodexInclude(payload map[string]any) error {
	value, ok := payload["include"]
	if !ok || value == nil {
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		return errors.New("responses request include must be an array")
	}
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return errors.New("responses request include entries must be strings")
		}
		if _, allowed := codexResponsesIncludeAllowlist[text]; !allowed {
			return fmt.Errorf("unsupported include value: %s", text)
		}
	}
	return nil
}

func validateCodexConversation(payload map[string]any) error {
	_, hasConversation := nonEmptyCodexString(payload["conversation"])
	_, hasPrevious := nonEmptyCodexString(payload["previous_response_id"])
	if hasConversation && hasPrevious {
		return errors.New("provide either conversation or previous_response_id, not both")
	}
	return nil
}

func normalizeCodexPreviousResponseID(payload map[string]any) {
	value, ok := payload["previous_response_id"]
	if !ok || value == nil {
		return
	}
	text, ok := value.(string)
	if !ok {
		return
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		delete(payload, "previous_response_id")
		return
	}
	payload["previous_response_id"] = trimmed
}

func validateCodexTruncation(payload map[string]any) error {
	if value, ok := payload["truncation"]; ok && value != nil {
		return errors.New("truncation is not supported")
	}
	return nil
}

func normalizeCodexTools(payload map[string]any) error {
	value, ok := payload["tools"]
	if !ok || value == nil {
		return nil
	}
	tools, ok := value.([]any)
	if !ok {
		return errors.New("responses request tools must be an array")
	}
	for _, tool := range tools {
		toolObj, ok := tool.(map[string]any)
		if !ok {
			continue
		}
		toolType, _ := toolObj["type"].(string)
		if toolType == "web_search_preview" {
			toolObj["type"] = "web_search"
			toolType = "web_search"
		}
		if _, unsupported := codexUnsupportedToolTypes[toolType]; unsupported {
			return fmt.Errorf("unsupported tool type: %s", toolType)
		}
	}
	return nil
}

func normalizeCodexToolChoice(payload map[string]any) {
	choice, ok := payload["tool_choice"].(map[string]any)
	if !ok {
		return
	}
	toolType, _ := choice["type"].(string)
	if toolType == "web_search_preview" {
		choice["type"] = "web_search"
	}
}

func stripCodexUnsupportedUpstreamFields(payload map[string]any) {
	canonicalizeCodexTools(payload)
	for key := range codexUnsupportedUpstreamFields {
		delete(payload, key)
	}
}

func canonicalizeCodexTools(payload map[string]any) {
	value, ok := payload["tools"]
	if !ok || value == nil {
		return
	}
	tools, ok := value.([]any)
	if !ok || len(tools) == 0 {
		return
	}
	sortedTools := make([]any, 0, len(tools))
	for _, tool := range tools {
		sortedTools = append(sortedTools, sortCodexJSONValue(tool))
	}
	sort.SliceStable(sortedTools, func(i, j int) bool {
		return codexToolSortKey(sortedTools[i]) < codexToolSortKey(sortedTools[j])
	})
	payload["tools"] = sortedTools
}

func codexToolSortKey(tool any) string {
	toolObj, ok := tool.(map[string]any)
	if !ok {
		return ""
	}
	if name, ok := toolObj["name"].(string); ok {
		return name
	}
	function, ok := toolObj["function"].(map[string]any)
	if !ok {
		return ""
	}
	name, _ := function["name"].(string)
	return name
}

func sortCodexJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = sortCodexJSONValue(item)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, sortCodexJSONValue(item))
		}
		return out
	default:
		return value
	}
}

func codexAnyMap(value any) map[string]any {
	typed, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return typed
}

func codexAnySlice(value any) []any {
	typed, ok := value.([]any)
	if !ok {
		return nil
	}
	return typed
}

func codexStringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if ok {
			out = append(out, text)
		}
	}
	return out
}

func codexBoolPointer(value any) *bool {
	flag, ok := value.(bool)
	if !ok {
		return nil
	}
	return &flag
}

func codexStringValue(value any) string {
	text, _ := value.(string)
	return text
}

func nonEmptyCodexString(value any) (string, bool) {
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", false
	}
	return text, true
}

func firstNonEmptyCodexString(values ...any) string {
	for _, value := range values {
		text, ok := value.(string)
		if ok && text != "" {
			return text
		}
	}
	return ""
}
