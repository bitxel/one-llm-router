package systemone

import (
	"context"
	"encoding/json"
	"github.com/user/one-llm-router/internal/provider"
	"github.com/user/one-llm-router/internal/provider/openai"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestContract(t *testing.T) {
	for _, body := range []string{
		`{"model":"m","state":{},"questions":{"q":{"type":"choice","criteria":{"one":"first"}}}}`,
		`{"model":"m","state":[],"questions":{"q":{"type":"score","criteria":["one"]}},"extension":true}`,
		`{"model":"m","state":"text","questions":{"q":{"type":"noul"}}}`,
	} {
		require.NoError(t, ValidateRequest([]byte(body)))
	}
	for _, body := range []string{
		`{}`, `null`, `[]`, `{"model":1}`, `{"model":" ","state":{},"questions":{}}`,
		`{"model":"m","state":null,"questions":{"q":{"type":"noul"}}}`,
		`{"model":"m","state":"s","questions":null}`,
		`{"model":"m","state":"s","questions":{"q":null}}`,
		`{"model":"m","state":"s","questions":{"q":{"type":"box"}}}`,
		`{"model":"m","state":"s","questions":{"q":{"type":"noul"}},"assets":{}}`,
		`{"model":"m","state":"s","questions":{"q":{"type":"noul"}},"images":[]}`,
		string([]byte{'"', 0xff, '"'}),
	} {
		assert.Error(t, ValidateRequest([]byte(body)), body)
	}
	headers := http.Header{"Content-Type": []string{"application/json; charset=utf-8"}}
	require.NoError(t, ValidateHeaders(headers, ""))
	assert.Error(t, ValidateHeaders(headers, "flag=true"))
	headers.Set("Content-Encoding", "zstd")
	assert.ErrorIs(t, ValidateHeaders(headers, ""), ErrUnsupportedMedia)
}

func TestResponseContract(t *testing.T) {
	good := `{"model":"version","answers":{"q":{"type":"choice","choice":"a","confidence":0.8,"probabilities":{"a":0.8,"b":0.2}}},"usage":{"input_tokens":10,"output_tokens":0}}`
	usage, err := ValidateResponse([]byte(good))
	require.NoError(t, err)
	assert.EqualValues(t, 10, usage["input"])
	assert.EqualValues(t, 0, usage["output"])
	for _, body := range []string{
		`{}`, `null`, `{"model":"m","answers":{},"usage":{"input_tokens":1,"output_tokens":2}}`,
		`{"model":"m","answers":{"q":{"type":"noul"}},"usage":{"input_tokens":-1,"output_tokens":2}}`,
		`{"model":"m","answers":{"q":{"type":"noul"}},"usage":{"input_tokens":1.5,"output_tokens":2}}`,
		`{"model":"m","answers":{"q":{"type":"other"}},"usage":{"input_tokens":1,"output_tokens":2}}`,
		string([]byte{'"', 0xff, '"'}),
	} {
		_, err := ValidateResponse([]byte(body))
		assert.Error(t, err, body)
	}
}

func TestDecodedRequestPreservesWireBytes(t *testing.T) {
	body := []byte(`{ "model":" m ", "state":{"n":9007199254740993}, "questions":{"q":{"type":"noul"}} }`)
	in := provider.DecodeInput{Headers: http.Header{"Content-Type": []string{"application/json"}}, RawBody: body, Path: "/v1/systemone", RequestID: "req-test"}
	full, err := (Bridge{}).DecodeClientRequest(context.Background(), in)
	require.NoError(t, err)
	require.NoError(t, ValidateHeaders(in.Headers, in.RawQuery))
	early, err := (Bridge{}).DecodeRequestBody(in)
	require.NoError(t, err)
	require.Equal(t, full, early)
	require.Equal(t, body, early.RawBody)
	in.Headers.Set("Content-Encoding", "gzip")
	_, err = (Bridge{}).DecodeClientRequest(context.Background(), in)
	require.ErrorIs(t, err, ErrUnsupportedMedia)
}

func TestValidatedUsagePreservesRecordedFields(t *testing.T) {
	for _, usage := range []string{
		`{"input_tokens":12,"output_tokens":0}`,
		`{"input_tokens":12,"output_tokens":3,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":2}}`,
		`{"input_tokens":12,"output_tokens":3,"prompt_tokens":5,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":1}}`,
		`{"input_tokens":12,"output_tokens":3,"input_tokens_details":"invalid"}`,
		`{"input_tokens":12,"output_tokens":3,"prompt_tokens":"invalid"}`,
	} {
		body := []byte(`{"model":"m","answers":{"q":{"type":"noul"}},"usage":` + usage + `}`)
		got, err := ValidateResponse(body)
		require.NoError(t, err)
		// JSON comparison ignores Go int/int64 representation differences.
		actual, err := json.Marshal(got)
		require.NoError(t, err)
		expected, err := json.Marshal(openai.ExtractUsageFromJSON(body))
		require.NoError(t, err)
		require.JSONEq(t, string(expected), string(actual))
	}
}
