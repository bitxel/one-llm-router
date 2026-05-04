package api

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForwardSSE_TokenExtraction(t *testing.T) {
	stream := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_123"}}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","usage":{"input_tokens":100,"output_tokens":50,"input_tokens_details":{"cached_tokens":20},"output_tokens_details":{"reasoning_tokens":10}}}`,
		"",
	}, "\n")

	w := httptest.NewRecorder()
	result := ForwardSSE(w, strings.NewReader(stream), false)

	require.NotNil(t, result.TokenUsage)
	assert.Equal(t, 100, result.TokenUsage["input"])
	assert.Equal(t, 50, result.TokenUsage["output"])
	assert.Equal(t, 20, result.TokenUsage["cached_input"])
	assert.Equal(t, 10, result.TokenUsage["reasoning"])
	assert.Empty(t, result.UpstreamResponseBody)
}

func TestForwardSSE_MultiLineDataJoin(t *testing.T) {
	// SSE spec: multiple data: lines in one event are joined with '\n'.
	// For JSON usage payloads OpenAI always emits a single data line, but
	// upstream correctness demands we preserve newlines between fragments.
	stream := strings.Join([]string{
		"event: response.completed",
		`data: {"type":"response.completed","usage":`,
		`data: {"input_tokens":7,"output_tokens":3}}`,
		"",
	}, "\n")

	w := httptest.NewRecorder()
	result := ForwardSSE(w, strings.NewReader(stream), false)

	require.NotNil(t, result.TokenUsage)
	assert.Equal(t, 7, result.TokenUsage["input"])
	assert.Equal(t, 3, result.TokenUsage["output"])
}

func TestForwardSSE_BodyCapture(t *testing.T) {
	stream := strings.Join([]string{
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","delta":"OK"}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","response":{"id":"resp_123","usage":{"input_tokens":2,"output_tokens":1}}}`,
		"",
	}, "\n")

	w := httptest.NewRecorder()
	result := ForwardSSE(w, strings.NewReader(stream), true)

	assert.NotEmpty(t, result.UpstreamResponseBody)
	assert.JSONEq(t, `{"id":"resp_123","output_text":"OK","usage":{"input_tokens":2,"output_tokens":1}}`, result.UpstreamResponseBody)
	assert.NotContains(t, result.UpstreamResponseBody, "event:")
}

func TestForwardSSE_EmptyStream(t *testing.T) {
	w := httptest.NewRecorder()
	result := ForwardSSE(w, strings.NewReader(""), false)

	assert.Nil(t, result.TokenUsage)
	assert.Nil(t, result.TTFTMs)
	assert.Empty(t, result.UpstreamResponseBody)
}

func TestForwardSSE_CapturesTTFTOnFirstTextDelta(t *testing.T) {
	stream := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_123"}}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","delta":"H"}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","delta":"i"}`,
		"",
	}, "\n")

	w := httptest.NewRecorder()
	result := ForwardSSE(w, strings.NewReader(stream), false)

	require.NotNil(t, result.TTFTMs)
	assert.GreaterOrEqual(t, *result.TTFTMs, 0)
}

func TestForwardSSE_TTFTIgnoresLifecycleAndEmptyDeltaEvents(t *testing.T) {
	stream := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_123"}}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","delta":""}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","usage":{"input_tokens":2,"output_tokens":0}}`,
		"",
	}, "\n")

	w := httptest.NewRecorder()
	result := ForwardSSE(w, strings.NewReader(stream), false)

	assert.Nil(t, result.TTFTMs)
}

type noFlushWriter struct {
	buf bytes.Buffer
}

func (w *noFlushWriter) Header() http.Header         { return http.Header{} }
func (w *noFlushWriter) Write(b []byte) (int, error) { return w.buf.Write(b) }
func (w *noFlushWriter) WriteHeader(int)             {}

func TestForwardSSE_NoFlusher(t *testing.T) {
	stream := "data: {\"type\":\"response.completed\",\"usage\":{\"input_tokens\":5,\"output_tokens\":3}}\n\n"

	w := &noFlushWriter{}
	result := ForwardSSE(w, strings.NewReader(stream), false)

	require.NotNil(t, result.TokenUsage)
	assert.Equal(t, 5, result.TokenUsage["input"])
	assert.Contains(t, w.buf.String(), "response.completed")
}

func TestForwardSSE_ForwardsContent(t *testing.T) {
	stream := "data: hello\n\ndata: world\n\n"
	w := httptest.NewRecorder()
	_ = ForwardSSE(w, strings.NewReader(stream), false)

	body := w.Body.String()
	assert.Contains(t, body, "data: hello")
	assert.Contains(t, body, "data: world")
}

// WHATWG SSE: "data:foo" (no space after colon) is valid and equivalent to
// "data: foo". codex-lb follows this; we must too.
func TestForwardSSE_NoSpaceAfterColon(t *testing.T) {
	stream := "event: response.completed\n" +
		`data:{"type":"response.completed","usage":{"input_tokens":9,"output_tokens":4}}` + "\n\n"

	w := httptest.NewRecorder()
	result := ForwardSSE(w, strings.NewReader(stream), false)

	require.NotNil(t, result.TokenUsage, "usage must be extracted even when upstream omits the space after data:")
	assert.Equal(t, 9, result.TokenUsage["input"])
	assert.Equal(t, 4, result.TokenUsage["output"])
}

// WHATWG SSE: lines starting with ':' are comments / keep-alive pings and
// MUST NOT contribute to field parsing.
func TestForwardSSE_IgnoresCommentLines(t *testing.T) {
	stream := strings.Join([]string{
		": upstream keepalive",
		"event: response.completed",
		`data: {"type":"response.completed","usage":{"input_tokens":1,"output_tokens":2}}`,
		"",
	}, "\n")

	w := httptest.NewRecorder()
	result := ForwardSSE(w, strings.NewReader(stream), false)

	require.NotNil(t, result.TokenUsage)
	assert.Equal(t, 1, result.TokenUsage["input"])

	body := w.Body.String()
	assert.Contains(t, body, ": upstream keepalive", "comment lines must still be forwarded verbatim for client keepalive")
}

// Reject false-positive matches: a field whose name *starts* with "data"
// (e.g. "datapoint") must not be treated as a data field.
func TestForwardSSE_FieldPrefixGuard(t *testing.T) {
	stream := "datapoint: 42\n\n"
	w := httptest.NewRecorder()
	result := ForwardSSE(w, strings.NewReader(stream), false)
	assert.Nil(t, result.TokenUsage, "non-data field must not be parsed as data")
}

func TestForwardSSE_EventDataTooLargeDoesNotBufferUnboundedUsage(t *testing.T) {
	original := maxSSEEventBytes
	maxSSEEventBytes = 8
	t.Cleanup(func() { maxSSEEventBytes = original })

	stream := strings.Join([]string{
		"event: response.completed",
		`data: {"type":"response.completed",`,
		`data: "usage":{"input_tokens":1,"output_tokens":2}}`,
		"",
	}, "\n")

	w := httptest.NewRecorder()
	result := ForwardSSE(w, strings.NewReader(stream), false)

	assert.Error(t, result.Err)
	assert.True(t, errors.Is(result.Err, ErrSSEEventTooLarge))
	assert.Nil(t, result.TokenUsage)
	assert.Contains(t, w.Body.String(), "response.completed")
}
