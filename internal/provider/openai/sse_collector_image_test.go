package openai

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func imageCollectorEvent(t *testing.T, value map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return "data: " + string(encoded) + "\n\n"
}

func collectImageEvents(t *testing.T, events ...map[string]any) map[string]any {
	t.Helper()
	var stream strings.Builder
	for _, event := range events {
		stream.WriteString(imageCollectorEvent(t, event))
	}
	body, terminal, err := CollectResponsesSSEBytes([]byte(stream.String()), true)
	if err != nil {
		t.Fatalf("collect responses SSE: %v", err)
	}
	if !terminal {
		t.Fatal("completed stream did not report terminal event")
	}
	var response map[string]any
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode aggregate: %v", err)
	}
	return response
}

func TestResponsesSSECollectorReconstructsImageOutput(t *testing.T) {
	image := map[string]any{
		"id": "ig_1", "type": "image_generation_call", "status": "completed",
		"result": "iVBORw0KGgoAAAANSUhEUg==", "output_format": "png",
		"size": "1024x1024", "quality": "high", "background": "opaque",
		"revised_prompt": "A red watercolor apple on white paper.",
	}
	for _, tc := range []struct {
		name     string
		response map[string]any
	}{
		{"empty_terminal_output", map[string]any{"id": "resp_image", "output": []any{}}},
		{"missing_terminal_output", map[string]any{"id": "resp_image"}},
		{"null_terminal_output", map[string]any{"id": "resp_image", "output": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.response["status"] = "completed"
			tc.response["usage"] = map[string]any{"input_tokens": 10, "output_tokens": 20}
			response := collectImageEvents(t,
				map[string]any{"type": "response.output_item.done", "output_index": 0, "item": image},
				map[string]any{"type": "response.completed", "response": tc.response},
			)
			if !reflect.DeepEqual(response["output"], []any{image}) {
				t.Fatalf("output = %#v; want complete image item %#v", response["output"], image)
			}
			if response["id"] != "resp_image" || response["status"] != "completed" {
				t.Fatalf("terminal response metadata changed: %#v", response)
			}
			usage, ok := response["usage"].(map[string]any)
			if !ok || usage["input_tokens"] != float64(10) || usage["output_tokens"] != float64(20) {
				t.Fatalf("terminal usage lost: %#v", response["usage"])
			}
		})
	}
}

func TestResponsesSSECollectorImageOutputPreservesFailureStatus(t *testing.T) {
	image := map[string]any{"id": "ig_1", "type": "image_generation_call", "status": "completed", "result": "aW1hZ2U="}
	for _, terminalType := range []string{"failed", "incomplete"} {
		t.Run(terminalType, func(t *testing.T) {
			errorPayload := map[string]any{"code": "image_generation_failed", "message": "A later image failed."}
			response := collectImageEvents(t,
				map[string]any{"type": "response.output_item.done", "output_index": 0, "item": image},
				map[string]any{"type": "response.output_item.added", "output_index": 1, "item": map[string]any{"id": "ig_2", "type": "image_generation_call", "status": "in_progress"}},
				map[string]any{"type": "response." + terminalType, "response": map[string]any{"id": "resp_1", "status": terminalType, "error": errorPayload, "output": []any{}}},
			)
			if !reflect.DeepEqual(response["output"], []any{image}) {
				t.Fatalf("output = %#v; want only completed item %#v", response["output"], image)
			}
			if response["status"] != terminalType || !reflect.DeepEqual(response["error"], errorPayload) {
				t.Fatalf("terminal failure status or error changed: %#v", response)
			}
		})
	}
}

func TestResponsesSSECollectorImageOutputUsesIndexAndDeduplicates(t *testing.T) {
	message := map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Here is the image."}}}
	image := map[string]any{"id": "ig_1", "type": "image_generation_call", "status": "completed", "result": "aW1hZ2U="}
	function := map[string]any{"id": "fc_1", "type": "function_call", "name": "save", "arguments": "{}", "call_id": "call_1"}
	imageDone := map[string]any{"type": "response.output_item.done", "output_index": 1, "item": image}
	response := collectImageEvents(t,
		imageDone,
		map[string]any{"type": "response.output_item.done", "output_index": 2, "item": function},
		map[string]any{"type": "response.output_item.done", "output_index": 0, "item": message},
		imageDone,
		map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_1", "output": []any{}}},
	)
	if !reflect.DeepEqual(response["output"], []any{message, image, function}) {
		t.Fatalf("output = %#v; want all complete items in output_index order", response["output"])
	}
	if response["output_text"] != "Here is the image." {
		t.Fatalf("output_text = %#v; want message text", response["output_text"])
	}
}

func TestResponsesSSECollectorTerminalImageOutputIsAuthoritative(t *testing.T) {
	cached := map[string]any{"id": "ig_1", "type": "image_generation_call", "result": "Y2FjaGVk"}
	terminal := map[string]any{"id": "ig_1", "type": "image_generation_call", "result": "dGVybWluYWw=", "output_format": "png"}
	response := collectImageEvents(t,
		map[string]any{"type": "response.output_item.done", "output_index": 0, "item": cached},
		map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_1", "output": []any{terminal}}},
	)
	if !reflect.DeepEqual(response["output"], []any{terminal}) {
		t.Fatalf("output = %#v; want authoritative terminal output %#v", response["output"], terminal)
	}
}

func TestResponsesSSECollectorRejectsMalformedImageOutput(t *testing.T) {
	image := map[string]any{"id": "ig_1", "type": "image_generation_call", "result": "aW1hZ2U="}
	for _, tc := range []struct {
		name  string
		event map[string]any
	}{
		{"missing_index", map[string]any{"type": "response.output_item.done", "item": image}},
		{"negative_index", map[string]any{"type": "response.output_item.done", "output_index": -1, "item": image}},
		{"fractional_index", map[string]any{"type": "response.output_item.done", "output_index": 0.5, "item": image}},
		{"string_index", map[string]any{"type": "response.output_item.done", "output_index": "0", "item": image}},
		{"overflow_index", map[string]any{"type": "response.output_item.done", "output_index": json.Number("9223372036854775808"), "item": image}},
		{"invalid_item", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": "not an object"}},
		{"null_item", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := imageCollectorEvent(t, tc.event) + imageCollectorEvent(t, map[string]any{"type": "response.completed", "response": map[string]any{"output": []any{}}})
			if body, _, err := CollectResponsesSSEBytes([]byte(stream), true); err == nil {
				t.Fatalf("malformed completed item returned success: %s", body)
			}
		})
	}
}

func TestResponsesSSECollectorRejectsTrailingJSON(t *testing.T) {
	var collector ResponsesSSECollector
	if err := collector.AddEvent([]byte(`{"type":"response.completed","response":{"output":[]}} {"extra":true}`)); err == nil {
		t.Fatal("SSE data containing two JSON objects was accepted")
	}
}

func TestResponsesSSECollectorRejectsConflictingOutputIndex(t *testing.T) {
	var collector ResponsesSSECollector
	for i, result := range []string{"Zmlyc3Q=", "c2Vjb25k"} {
		event, err := json.Marshal(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"id": "ig_1", "type": "image_generation_call", "result": result}})
		if err != nil {
			t.Fatal(err)
		}
		err = collector.AddEvent(event)
		if i == 0 && err != nil {
			t.Fatalf("first completed image failed: %v", err)
		}
		if i == 1 && err == nil {
			t.Fatal("conflicting completed items at the same output_index were accepted")
		}
	}
}
