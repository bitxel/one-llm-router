package openai

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ResponsesSSECollector folds OpenAI Responses SSE events into the same
// aggregate JSON shape returned by non-streaming /v1/responses.
type ResponsesSSECollector struct {
	deltaText    strings.Builder
	doneText     string
	response     map[string]any
	usage        any
	terminalType string
}

func (c *ResponsesSSECollector) AddEvent(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "[DONE]" {
		return nil
	}

	var event map[string]any
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		return fmt.Errorf("decode responses SSE event: %w", err)
	}

	eventType, _ := event["type"].(string)
	switch eventType {
	case "response.output_text.delta":
		if delta, ok := event["delta"].(string); ok {
			c.deltaText.WriteString(delta)
		}
	case "response.output_text.done":
		if text, ok := event["text"].(string); ok {
			c.doneText = text
		}
	case "response.content_part.done", "response.output_item.done":
		if c.doneText == "" {
			c.doneText = eventOutputText(event)
		}
	case "response.completed", "response.failed", "response.incomplete":
		c.terminalType = eventType
		if resp, ok := event["response"].(map[string]any); ok {
			c.response = resp
			c.usage = resp["usage"]
		}
	case "error":
		c.terminalType = eventType
		if errPayload, ok := event["error"]; ok {
			c.response = map[string]any{"error": errPayload}
		} else {
			c.response = event
		}
	}
	return nil
}

func (c *ResponsesSSECollector) AggregateJSON(requireTerminal bool) ([]byte, bool, error) {
	if c == nil || c.terminalType == "" {
		if requireTerminal {
			return nil, false, errors.New("responses SSE response missing terminal event")
		}
		return nil, false, nil
	}
	if c.response == nil {
		return nil, true, fmt.Errorf("responses SSE %s missing response", c.terminalType)
	}

	if c.terminalType != "error" {
		text := c.doneText
		if text == "" {
			text = c.deltaText.String()
		}
		if text == "" {
			text = responseOutputText(c.response)
		}
		if _, ok := c.response["output_text"]; !ok {
			c.response["output_text"] = text
		}
		if c.usage != nil {
			if _, ok := c.response["usage"]; !ok {
				c.response["usage"] = c.usage
			}
		}
	}

	encoded, err := json.Marshal(c.response)
	if err != nil {
		return nil, true, fmt.Errorf("encode responses SSE aggregate: %w", err)
	}
	return encoded, true, nil
}

func CollectResponsesSSEBytes(data []byte, requireTerminal bool) ([]byte, bool, error) {
	return collectResponsesSSEBytes(data, requireTerminal)
}

func collectResponsesSSEBytes(data []byte, requireTerminal bool) ([]byte, bool, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), responseSSEScannerMaxBytes(len(data)))

	var collector ResponsesSSECollector
	var eventData []string
	flush := func() error {
		if len(eventData) == 0 {
			return nil
		}
		raw := strings.Join(eventData, "\n")
		eventData = nil
		return collector.AddEvent([]byte(raw))
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return nil, false, err
			}
			continue
		}
		if data, ok := sseDataField(line); ok {
			eventData = append(eventData, data)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, false, fmt.Errorf("scan responses SSE response: %w", err)
	}
	if err := flush(); err != nil {
		return nil, false, err
	}
	return collector.AggregateJSON(requireTerminal)
}

func IsOutputTextTokenEvent(data []byte) bool {
	var event map[string]any
	if err := json.Unmarshal(data, &event); err != nil {
		return false
	}

	eventType, _ := event["type"].(string)
	switch eventType {
	case "response.output_text.delta":
		return nonEmptyString(event["delta"])
	case "response.output_text.done":
		return nonEmptyString(event["text"])
	case "response.content_part.added", "response.content_part.done":
		return eventOutputText(event) != ""
	case "response.output_item.added", "response.output_item.done":
		return eventOutputText(event) != ""
	default:
		return false
	}
}

func RequestWantsStreaming(body []byte) bool {
	var req struct {
		Stream *bool `json:"stream"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Stream == nil {
		return false
	}
	return *req.Stream
}

func eventOutputText(event map[string]any) string {
	if part, ok := event["part"].(map[string]any); ok {
		return partOutputText(part)
	}
	if item, ok := event["item"].(map[string]any); ok {
		return itemOutputText(item)
	}
	return ""
}

func responseOutputText(response map[string]any) string {
	output, ok := response["output"].([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, rawItem := range output {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		if text := itemOutputText(item); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "")
}

func itemOutputText(item map[string]any) string {
	content, ok := item["content"].([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, rawPart := range content {
		part, ok := rawPart.(map[string]any)
		if !ok {
			continue
		}
		if text := partOutputText(part); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "")
}

func partOutputText(part map[string]any) string {
	partType, _ := part["type"].(string)
	if partType != "" && partType != "output_text" {
		return ""
	}
	text, _ := part["text"].(string)
	return text
}

func nonEmptyString(value any) bool {
	text, ok := value.(string)
	return ok && text != ""
}

func responseSSEScannerMaxBytes(size int) int {
	const defaultMax = 64 * 1024
	limit := codexSSECollectLimit
	if size > limit {
		limit = size
	}
	if limit < defaultMax {
		return defaultMax
	}
	return limit + 1
}
