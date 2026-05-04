package openai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeNativeCodexResponsesBodyMatchesCodexLBInputNormalization(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"model": "gpt-5.4-mini",
		"instructions": "",
		"input": [
			{"role":"assistant","content":"hello","reasoning_content":"hidden"},
			{"role":"assistant","content":[{"type":"input_text","text":"old"},{"type":"reasoning","text":"hidden"},{"type":"refusal","refusal":"no"}]},
			{"role":"tool","toolCallId":"call_1","content":[{"type":"text","text":"tool "},{"type":"refusal","refusal":"denied"}]},
			{"role":"user","content":[{"type":"reasoning","text":"hidden"},{"type":"input_text","text":"keep"}]}
		],
		"tools": [
			{"type":"function","function":{"name":"z"}},
			{"type":"web_search_preview"},
			{"type":"function","function":{"name":"a"}}
		],
		"stream": false,
		"temperature": 1
	}`)

	encoded, err := normalizeNativeCodexResponsesBody(body)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"model": "gpt-5.4-mini",
		"instructions": "",
		"input": [
			{"role":"assistant","content":[{"type":"output_text","text":"hello"}]},
			{"role":"assistant","content":[{"type":"output_text","text":"old"},{"type":"output_text","text":"no"}]},
			{"type":"function_call_output","call_id":"call_1","output":"tool denied"},
			{"role":"user","content":[{"type":"input_text","text":"keep"}]}
		],
		"tools": [
			{"type":"web_search"},
			{"type":"function","function":{"name":"a"}},
			{"type":"function","function":{"name":"z"}}
		],
		"store": false,
		"stream": true
	}`, string(encoded))
	assert.NotContains(t, string(encoded), "temperature")
	assert.NotContains(t, string(encoded), "reasoning_content")
}

func TestNormalizeNativeCodexRequestsRejectInvalidBodies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		run     func([]byte) error
		wantErr string
	}{
		{
			name: "responses store true",
			body: `{"model":"gpt-5.4-mini","input":"hello","store":true}`,
			run: func(data []byte) error {
				_, err := normalizeNativeCodexResponsesBody(data)
				return err
			},
			wantErr: "responses request store must be false",
		},
		{
			name: "responses null body",
			body: `null`,
			run: func(data []byte) error {
				_, err := normalizeNativeCodexResponsesBody(data)
				return err
			},
			wantErr: "responses request body must be an object",
		},
		{
			name: "responses missing input",
			body: `{"model":"gpt-5.4-mini","instructions":""}`,
			run: func(data []byte) error {
				_, err := normalizeNativeCodexResponsesBody(data)
				return err
			},
			wantErr: "responses request input is required",
		},
		{
			name: "responses tool output missing id",
			body: `{"model":"gpt-5.4-mini","input":[{"role":"tool","content":"ok"}]}`,
			run: func(data []byte) error {
				_, err := normalizeNativeCodexResponsesBody(data)
				return err
			},
			wantErr: "tool input items must include 'tool_call_id'",
		},
		{
			name: "responses invalid reasoning",
			body: `{"model":"gpt-5.4-mini","input":"hello","reasoning":"medium"}`,
			run: func(data []byte) error {
				_, err := normalizeNativeCodexResponsesBody(data)
				return err
			},
			wantErr: "responses request reasoning must be an object",
		},
		{
			name: "responses invalid text format",
			body: `{"model":"gpt-5.4-mini","input":"hello","text":{"format":"json_object"}}`,
			run: func(data []byte) error {
				_, err := normalizeNativeCodexResponsesBody(data)
				return err
			},
			wantErr: "responses request text.format must be an object",
		},
		{
			name: "compact store true",
			body: `{"model":"gpt-5.4-mini","input":"hello","store":true}`,
			run: func(data []byte) error {
				_, err := normalizeNativeCodexResponsesCompactBody(data)
				return err
			},
			wantErr: "compact request store must be false",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.run([]byte(tc.body))
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}
