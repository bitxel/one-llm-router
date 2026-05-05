package openai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractUsageFromEvent_ResponseCompleted(t *testing.T) {
	event := []byte(`{
		"type": "response.completed",
		"usage": {
			"input_tokens": 100,
			"output_tokens": 50,
			"input_tokens_details": {"cached_tokens": 20},
			"output_tokens_details": {"reasoning_tokens": 10}
		}
	}`)

	usage := ExtractUsageFromEvent(event)
	require.NotNil(t, usage)
	assert.Equal(t, 100, usage["input"])
	assert.Equal(t, 50, usage["output"])
	assert.Equal(t, 20, usage["cached_input"])
	assert.Equal(t, 10, usage["reasoning"])
}

func TestExtractUsageFromEvent_ResponseCompletedNestedUsage(t *testing.T) {
	event := []byte(`{
		"type": "response.completed",
		"response": {
			"usage": {
				"input_tokens": 12,
				"output_tokens": 4,
				"input_tokens_details": {"cached_tokens": 3},
				"output_tokens_details": {"reasoning_tokens": 2}
			}
		}
	}`)

	usage := ExtractUsageFromEvent(event)
	require.NotNil(t, usage)
	assert.Equal(t, 12, usage["input"])
	assert.Equal(t, 4, usage["output"])
	assert.Equal(t, 3, usage["cached_input"])
	assert.Equal(t, 2, usage["reasoning"])
}

func TestExtractUsageFromEvent_WrongType(t *testing.T) {
	event := []byte(`{"type": "response.created", "usage": {"input_tokens": 100}}`)
	assert.Nil(t, ExtractUsageFromEvent(event))
}

func TestExtractUsageFromEvent_NoUsage(t *testing.T) {
	event := []byte(`{"type": "response.completed"}`)
	assert.Nil(t, ExtractUsageFromEvent(event))
}

func TestExtractUsageFromEvent_InvalidJSON(t *testing.T) {
	assert.Nil(t, ExtractUsageFromEvent([]byte(`not json`)))
}

func TestExtractUsageFromEvent_PartialDetails(t *testing.T) {
	event := []byte(`{
		"type": "response.completed",
		"usage": {
			"input_tokens": 200,
			"output_tokens": 75
		}
	}`)

	usage := ExtractUsageFromEvent(event)
	require.NotNil(t, usage)
	assert.Equal(t, 200, usage["input"])
	assert.Equal(t, 75, usage["output"])
	_, hasCached := usage["cached_input"]
	assert.False(t, hasCached)
}

func TestExtractUsageFromJSON_Complete(t *testing.T) {
	body := []byte(`{
		"id": "resp_123",
		"usage": {
			"input_tokens": 500,
			"output_tokens": 200,
			"input_tokens_details": {"cached_tokens": 100},
			"output_tokens_details": {"reasoning_tokens": 50}
		}
	}`)

	usage := ExtractUsageFromJSON(body)
	require.NotNil(t, usage)
	assert.Equal(t, 500, usage["input"])
	assert.Equal(t, 200, usage["output"])
	assert.Equal(t, 100, usage["cached_input"])
	assert.Equal(t, 50, usage["reasoning"])
}

func TestExtractUsageFromJSON_ChatCompletionsShape(t *testing.T) {
	body := []byte(`{
		"id": "chatcmpl_123",
		"object": "chat.completion",
		"usage": {
			"prompt_tokens": 13,
			"completion_tokens": 8,
			"prompt_tokens_details": {"cached_tokens": 5},
			"completion_tokens_details": {"reasoning_tokens": 2}
		}
	}`)

	usage := ExtractUsageFromJSON(body)
	require.NotNil(t, usage)
	assert.Equal(t, 13, usage["input"])
	assert.Equal(t, 8, usage["output"])
	assert.Equal(t, 5, usage["cached_input"])
	assert.Equal(t, 2, usage["reasoning"])
}

func TestExtractUsageFromJSON_NoUsage(t *testing.T) {
	body := []byte(`{"id": "resp_123"}`)
	assert.Nil(t, ExtractUsageFromJSON(body))
}

func TestExtractUsageFromJSON_Invalid(t *testing.T) {
	assert.Nil(t, ExtractUsageFromJSON([]byte(`not json`)))
}

func TestExtractModel(t *testing.T) {
	body := []byte(`{"model": "gpt-4o", "messages": []}`)
	m := ExtractModel(body)
	require.NotNil(t, m)
	assert.Equal(t, "gpt-4o", *m)
}

func TestExtractModel_Empty(t *testing.T) {
	assert.Nil(t, ExtractModel([]byte(`{"messages": []}`)))
}

func TestExtractModelParams(t *testing.T) {
	body := []byte(`{
		"model": "o3",
		"reasoning_effort": "high",
		"reasoning": {"effort": "low"},
		"service_tier": "flex"
	}`)

	params := ExtractModelParams(body)
	require.NotNil(t, params)
	assert.Equal(t, "high", params["reasoning_effort"])
	assert.Equal(t, "flex", params["service_tier"])
}

func TestExtractModelParams_ReasoningObject(t *testing.T) {
	body := []byte(`{
		"model": "o3",
		"reasoning": {"effort": "high"}
	}`)

	params := ExtractModelParams(body)
	require.NotNil(t, params)
	assert.Equal(t, "high", params["reasoning_effort"])
}

func TestExtractModelParams_None(t *testing.T) {
	body := []byte(`{"model": "gpt-4o"}`)
	assert.Nil(t, ExtractModelParams(body))
}

func TestDetectResponseMode(t *testing.T) {
	assert.Equal(t, "sse", DetectResponseMode("text/event-stream"))
	assert.Equal(t, "sse", DetectResponseMode("text/event-stream; charset=utf-8"))
	assert.Equal(t, "json", DetectResponseMode("application/json"))
	assert.Equal(t, "json", DetectResponseMode(""))
}

func TestRequestWantsStreaming(t *testing.T) {
	assert.True(t, RequestWantsStreaming([]byte(`{"stream":true}`)))
	assert.False(t, RequestWantsStreaming([]byte(`{"stream":false}`)))
	assert.False(t, RequestWantsStreaming([]byte(`{"model":"gpt-5.4-mini"}`)))
	assert.False(t, RequestWantsStreaming([]byte(`not json`)))
}

func TestExtractErrorCode_WithCode(t *testing.T) {
	body := []byte(`{"error":{"code":"rate_limit_exceeded","type":"rate_limit_error","message":"too many requests"}}`)
	code := ExtractErrorCode(body)
	require.NotNil(t, code)
	assert.Equal(t, "rate_limit_exceeded", *code)
}

func TestExtractErrorCode_FallbackToType(t *testing.T) {
	body := []byte(`{"error":{"type":"invalid_request_error","message":"bad request"}}`)
	code := ExtractErrorCode(body)
	require.NotNil(t, code)
	assert.Equal(t, "invalid_request_error", *code)
}

func TestExtractErrorCode_ResponseNestedError(t *testing.T) {
	body := []byte(`{"response":{"error":{"code":"rate_limit_exceeded","message":"quota"}}}`)
	code := ExtractErrorCode(body)
	require.NotNil(t, code)
	assert.Equal(t, "rate_limit_exceeded", *code)
}

func TestExtractErrorCode_NoError(t *testing.T) {
	body := []byte(`{"id":"resp_123"}`)
	assert.Nil(t, ExtractErrorCode(body))
}

func TestExtractErrorCode_NilBody(t *testing.T) {
	assert.Nil(t, ExtractErrorCode(nil))
}

func TestExtractErrorCode_InvalidJSON(t *testing.T) {
	assert.Nil(t, ExtractErrorCode([]byte(`not json`)))
}

func TestExtractErrorCode_EmptyCodeAndType(t *testing.T) {
	body := []byte(`{"error":{"message":"something went wrong"}}`)
	assert.Nil(t, ExtractErrorCode(body))
}

func TestExtractUsageFromEvent_NilInputTokens(t *testing.T) {
	event := []byte(`{"type":"response.completed","usage":{"output_tokens":5,"input_tokens_details":{"cached_tokens":2},"output_tokens_details":{"reasoning_tokens":1}}}`)
	usage := ExtractUsageFromEvent(event)
	require.NotNil(t, usage)
	_, hasInput := usage["input"]
	assert.False(t, hasInput)
	assert.Equal(t, 5, usage["output"])
	assert.Equal(t, 2, usage["cached_input"])
	assert.Equal(t, 1, usage["reasoning"])
}

func TestExtractUsageFromEvent_NilOutputTokens(t *testing.T) {
	event := []byte(`{"type":"response.completed","usage":{"input_tokens":10}}`)
	usage := ExtractUsageFromEvent(event)
	require.NotNil(t, usage)
	assert.Equal(t, 10, usage["input"])
	_, hasOutput := usage["output"]
	assert.False(t, hasOutput)
}

func TestExtractUsageFromEvent_EmptyUsage(t *testing.T) {
	event := []byte(`{"type":"response.completed","usage":{}}`)
	assert.Nil(t, ExtractUsageFromEvent(event))
}

func TestExtractUsageFromJSON_NilInputTokens(t *testing.T) {
	body := []byte(`{"usage":{"output_tokens":5}}`)
	usage := ExtractUsageFromJSON(body)
	require.NotNil(t, usage)
	_, hasInput := usage["input"]
	assert.False(t, hasInput)
	assert.Equal(t, 5, usage["output"])
}

func TestExtractUsageFromJSON_EmptyUsage(t *testing.T) {
	body := []byte(`{"usage":{}}`)
	assert.Nil(t, ExtractUsageFromJSON(body))
}

func TestExtractModelParams_InvalidJSON(t *testing.T) {
	assert.Nil(t, ExtractModelParams([]byte(`not json`)))
}

func TestExtractModel_InvalidJSON(t *testing.T) {
	assert.Nil(t, ExtractModel([]byte(`not json`)))
}
