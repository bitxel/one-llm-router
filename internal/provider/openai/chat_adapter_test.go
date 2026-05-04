package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatAdapterNonStreamMapsRequestAndResponse(t *testing.T) {
	t.Parallel()

	var upstreamBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/codex/responses", r.URL.Path)
		assert.Equal(t, "Bearer oauth-access", r.Header.Get("Authorization"))
		assert.Equal(t, "text/event-stream", r.Header.Get("Accept"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&upstreamBody))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","delta":"Hello "}`,
			``,
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","delta":"world"}`,
			``,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"id":"resp_chat_123","usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7,"input_tokens_details":{"cached_tokens":1},"output_tokens_details":{"reasoning_tokens":1}}}}`,
			``,
		}, "\n")))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	reqBody := `{
		"model":"gpt-5.4-mini",
		"messages":[
			{"role":"system","content":"You are concise."},
			{"role":"developer","content":[{"type":"text","text":"Use JSON."}]},
			{"role":"user","content":"hello"}
		],
		"max_tokens": 32,
		"temperature": 0.2,
		"tools": [{"type":"function","function":{"name":"lookup","description":"Lookup","parameters":{"type":"object"}}}],
		"tool_choice": {"type":"function","function":{"name":"lookup"}},
		"response_format": {"type":"json_object"},
		"n": 1
	}`
	orig := httptest.NewRequest(http.MethodPost, "http://router/v1/chat/completions", strings.NewReader(reqBody))
	resp, capture, err := c.ForwardGatewayRequestWithCapture(
		context.Background(),
		oauthAccount("acct-1"),
		[]byte("oauth-access"),
		orig,
		GatewayRoute{OAuthUpstreamPath: "/codex/responses", OAuthBehavior: GatewayOAuthBehaviorChatAdapter},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })

	assert.Equal(t, "gpt-5.4-mini", upstreamBody["model"])
	assert.Equal(t, true, upstreamBody["stream"])
	assert.NotContains(t, upstreamBody, "max_output_tokens")
	assert.NotContains(t, upstreamBody, "temperature")
	assert.NotContains(t, upstreamBody, "messages")
	assert.NotContains(t, upstreamBody, "max_tokens")
	assert.Equal(t, false, upstreamBody["store"])
	assert.NotContains(t, upstreamBody, "n")
	assert.Contains(t, upstreamBody["instructions"], "You are concise.")
	assert.Contains(t, upstreamBody["instructions"], "Use JSON.")
	require.NotEmpty(t, upstreamBody["input"])
	assert.NotEmpty(t, upstreamBody["tools"])
	assert.Equal(t, map[string]any{"type": "json_object"}, upstreamBody["text"].(map[string]any)["format"])
	assert.NotEmpty(t, capture.UpstreamRequestBody)

	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Equal(t, "resp_chat_123", out["id"])
	assert.Equal(t, "chat.completion", out["object"])
	assert.Equal(t, "gpt-5.4-mini", out["model"])
	choices := out["choices"].([]any)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	assert.Equal(t, "assistant", message["role"])
	assert.Equal(t, "Hello world", message["content"])
	assert.Equal(t, "stop", choices[0].(map[string]any)["finish_reason"])
	usage := out["usage"].(map[string]any)
	assert.Equal(t, float64(5), usage["prompt_tokens"])
	assert.Equal(t, float64(2), usage["completion_tokens"])
	assert.Equal(t, float64(7), usage["total_tokens"])
	assert.Equal(t, float64(1), usage["prompt_tokens_details"].(map[string]any)["cached_tokens"])
	assert.Equal(t, float64(1), usage["completion_tokens_details"].(map[string]any)["reasoning_tokens"])
}

func TestChatAdapterValidateRejectsUnsupportedFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "unknown top level", body: `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"unknown":true}`},
		{name: "store true", body: `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"store":true}`},
		{name: "n greater than one", body: `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"n":2}`},
		{name: "conflicting token limits", body: `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"max_tokens":4,"max_completion_tokens":5}`},
		{name: "unsupported response format", body: `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"xml"}}`},
		{name: "legacy function call", body: `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"function_call":"auto"}`},
		{name: "logprobs", body: `{"model":"gpt","messages":[{"role":"user","content":"hi"}],"logprobs":true}`},
		{name: "empty messages", body: `{"model":"gpt","messages":[]}`},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var upstreamCalls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				upstreamCalls.Add(1)
			}))
			t.Cleanup(srv.Close)

			c := NewClient(30 * time.Second)
			c.codexBackendBaseURL = srv.URL
			orig := httptest.NewRequest(http.MethodPost, "http://router/v1/chat/completions", strings.NewReader(tc.body))
			resp, capture, err := c.ForwardGatewayRequestWithCapture(
				context.Background(),
				oauthAccount("acct-1"),
				[]byte("oauth-access"),
				orig,
				GatewayRoute{OAuthUpstreamPath: "/codex/responses", OAuthBehavior: GatewayOAuthBehaviorChatAdapter},
			)
			if resp != nil {
				t.Cleanup(func() { _ = resp.Body.Close() })
			}

			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrInvalidUpstreamRequest))
			assert.Nil(t, resp)
			assert.Empty(t, capture.UpstreamRequestBody)
			assert.Equal(t, int32(0), upstreamCalls.Load())
		})
	}
}

func TestChatAdapterNonStreamFailedResponseIsNotSuccess(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.failed`,
			`data: {"type":"response.failed","response":{"id":"resp_fail","error":{"code":"server_error","message":"failed","type":"server_error"}}}`,
			``,
		}, "\n")))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	orig := httptest.NewRequest(http.MethodPost, "http://router/v1/chat/completions", strings.NewReader(`{"model":"gpt","messages":[{"role":"user","content":"hi"}]}`))
	resp, _, err := c.ForwardGatewayRequestWithCapture(
		context.Background(),
		oauthAccount("acct-1"),
		[]byte("oauth-access"),
		orig,
		GatewayRoute{OAuthUpstreamPath: "/codex/responses", OAuthBehavior: GatewayOAuthBehaviorChatAdapter},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.Contains(t, string(data), "server_error")
	assert.NotContains(t, string(data), "chat.completion")
}

func TestChatAdapterNonStreamFailedJSONResponseIsNotSuccess(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_fail_json","status":"failed","error":{"code":"server_error","message":"failed","type":"server_error"}}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	orig := httptest.NewRequest(http.MethodPost, "http://router/v1/chat/completions", strings.NewReader(`{"model":"gpt","messages":[{"role":"user","content":"hi"}]}`))
	resp, _, err := c.ForwardGatewayRequestWithCapture(
		context.Background(),
		oauthAccount("acct-1"),
		[]byte("oauth-access"),
		orig,
		GatewayRoute{OAuthUpstreamPath: "/codex/responses", OAuthBehavior: GatewayOAuthBehaviorChatAdapter},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.Contains(t, string(data), "server_error")
	assert.NotContains(t, string(data), "chat.completion")
}

func TestChatAdapterNonStreamMalformedJSONResponseFails(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	orig := httptest.NewRequest(http.MethodPost, "http://router/v1/chat/completions", strings.NewReader(`{"model":"gpt","messages":[{"role":"user","content":"hi"}]}`))
	resp, _, err := c.ForwardGatewayRequestWithCapture(
		context.Background(),
		oauthAccount("acct-1"),
		[]byte("oauth-access"),
		orig,
		GatewayRoute{OAuthUpstreamPath: "/codex/responses", OAuthBehavior: GatewayOAuthBehaviorChatAdapter},
	)
	if resp != nil {
		t.Cleanup(func() { _ = resp.Body.Close() })
	}

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUpstreamResponseInvalid), "err=%v", err)
	assert.Nil(t, resp)
}

func TestChatAdapterStreamProviderErrorPreservesProviderBody(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"rate_limit_exceeded","message":"slow down","type":"rate_limit_error"}}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	orig := httptest.NewRequest(http.MethodPost, "http://router/v1/chat/completions", strings.NewReader(`{
		"model":"gpt",
		"messages":[{"role":"user","content":"hi"}],
		"stream": true
	}`))
	resp, _, err := c.ForwardGatewayRequestWithCapture(
		context.Background(),
		oauthAccount("acct-1"),
		[]byte("oauth-access"),
		orig,
		GatewayRoute{OAuthUpstreamPath: "/codex/responses", OAuthBehavior: GatewayOAuthBehaviorChatAdapter},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
	assert.JSONEq(t, `{"error":{"code":"rate_limit_exceeded","message":"slow down","type":"rate_limit_error"}}`, string(data))
}

func TestChatAdapterNonStreamMapsToolCallsAndRefusal(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.refusal.delta`,
			`data: {"type":"response.refusal.delta","delta":"I cannot"}`,
			``,
			`event: response.output_item.done`,
			`data: {"type":"response.output_item.done","item":{"id":"call_1","type":"function_call","name":"lookup","arguments":"{\"q\":\"abc\"}"}}`,
			``,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"id":"resp_tools","usage":{"input_tokens":3,"output_tokens":1}}}`,
			``,
		}, "\n")))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	orig := httptest.NewRequest(http.MethodPost, "http://router/v1/chat/completions", strings.NewReader(`{
		"model":"gpt-tools",
		"messages":[{"role":"user","content":"hi"}]
	}`))
	resp, _, err := c.ForwardGatewayRequestWithCapture(
		context.Background(),
		oauthAccount("acct-1"),
		[]byte("oauth-access"),
		orig,
		GatewayRoute{OAuthUpstreamPath: "/codex/responses", OAuthBehavior: GatewayOAuthBehaviorChatAdapter},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })

	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	choice := out["choices"].([]any)[0].(map[string]any)
	message := choice["message"].(map[string]any)
	assert.Nil(t, message["content"])
	assert.Equal(t, "I cannot", message["refusal"])
	assert.Equal(t, "tool_calls", choice["finish_reason"])
	toolCalls := message["tool_calls"].([]any)
	require.Len(t, toolCalls, 1)
	call := toolCalls[0].(map[string]any)
	assert.Equal(t, "call_1", call["id"])
	assert.Equal(t, "function", call["type"])
	fn := call["function"].(map[string]any)
	assert.Equal(t, "lookup", fn["name"])
	assert.Equal(t, `{"q":"abc"}`, fn["arguments"])
}

func TestChatAdapterStreamMapsResponsesSSE(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var upstreamBody map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&upstreamBody))
		assert.Equal(t, true, upstreamBody["stream"])
		assert.Equal(t, false, upstreamBody["store"])
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.created`,
			`data: {"type":"response.created","response":{"id":"resp_stream"}}`,
			``,
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","delta":"Hi"}`,
			``,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"id":"resp_stream","usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}}}`,
			``,
		}, "\n")))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	orig := httptest.NewRequest(http.MethodPost, "http://router/v1/chat/completions", strings.NewReader(`{
		"model":"gpt-stream",
		"messages":[{"role":"user","content":"hi"}],
		"stream": true,
		"stream_options": {"include_usage": true}
	}`))
	resp, _, err := c.ForwardGatewayRequestWithCapture(
		context.Background(),
		oauthAccount("acct-1"),
		[]byte("oauth-access"),
		orig,
		GatewayRoute{OAuthUpstreamPath: "/codex/responses", OAuthBehavior: GatewayOAuthBehaviorChatAdapter},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })

	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	body := string(data)
	assert.Contains(t, body, `"object":"chat.completion.chunk"`)
	assert.Contains(t, body, `"id":"resp_stream"`)
	assert.Contains(t, body, `"content":"Hi"`)
	assert.Contains(t, body, `"finish_reason":"stop"`)
	assert.Contains(t, body, `"usage":{"completion_tokens":4,"prompt_tokens":3,"total_tokens":7}`)
	assert.Contains(t, body, "data: [DONE]")
	assert.NotContains(t, body, `"id":"chatcmpl_stream"`)
	assert.NotContains(t, body, "response.output_text.delta")
}

func TestChatAdapterStreamMapsResponsesSSEMislabeledAsJSON(t *testing.T) {
	t.Parallel()

	const upstreamSSE = `: heartbeat

event: response.created
data: {"type":"response.created","response":{"id":"resp_mislabel_stream"}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"Hi"}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_mislabel_stream","usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}}}

`
	var upstreamBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&upstreamBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(upstreamSSE))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	orig := httptest.NewRequest(http.MethodPost, "http://router/v1/chat/completions", strings.NewReader(`{
		"model":"gpt-stream-mislabel",
		"messages":[{"role":"user","content":"hi"}],
		"stream": true,
		"stream_options": {"include_usage": true}
	}`))
	resp, capture, err := c.ForwardGatewayRequestWithCapture(
		context.Background(),
		oauthAccount("acct-1"),
		[]byte("oauth-access"),
		orig,
		GatewayRoute{OAuthUpstreamPath: "/codex/responses", OAuthBehavior: GatewayOAuthBehaviorChatAdapter},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, capture.TokenUsage)
	t.Cleanup(func() { _ = resp.Body.Close() })

	assert.Equal(t, true, upstreamBody["stream"])
	assert.Equal(t, false, upstreamBody["store"])
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	body := string(data)
	assert.Contains(t, body, `"object":"chat.completion.chunk"`)
	assert.Contains(t, body, `"id":"resp_mislabel_stream"`)
	assert.Contains(t, body, `"content":"Hi"`)
	assert.Contains(t, body, `"finish_reason":"stop"`)
	assert.Contains(t, body, `"prompt_tokens":3`)
	assert.Contains(t, body, `"completion_tokens":4`)
	assert.Contains(t, body, "data: [DONE]")
	assert.NotContains(t, body, `"id":"chatcmpl_stream"`)

	usage := capture.TokenUsage()
	require.NotNil(t, usage)
	assert.EqualValues(t, 3, usage["input"])
	assert.EqualValues(t, 4, usage["output"])
	require.NotNil(t, capture.ModelParams)
	assert.Nil(t, capture.ModelParams())
	require.NotNil(t, capture.UpstreamResponseBodySnapshot)
	assert.JSONEq(t, `{"id":"resp_mislabel_stream","output_text":"Hi","usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}}`, string(capture.UpstreamResponseBodySnapshot()))
}

func TestChatAdapterStreamMislabeledSSEDoesNotBufferUntilTerminal(t *testing.T) {
	t.Parallel()

	firstHeartbeatSent := make(chan struct{})
	releaseTerminal := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, ":\n\n")
		flusher.Flush()
		close(firstHeartbeatSent)
		<-releaseTerminal
		_, _ = io.WriteString(w, strings.Join([]string{
			`event: response.created`,
			`data: {"type":"response.created","response":{"id":"resp_streaming_mislabel"}}`,
			``,
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","delta":"Hi"}`,
			``,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"id":"resp_streaming_mislabel","usage":{"input_tokens":1,"output_tokens":1}}}`,
			``,
		}, "\n")+"\n")
	}))
	t.Cleanup(func() {
		select {
		case <-releaseTerminal:
		default:
			close(releaseTerminal)
		}
		srv.Close()
	})

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	orig := httptest.NewRequest(http.MethodPost, "http://router/v1/chat/completions", strings.NewReader(`{
		"model":"gpt-stream-mislabel",
		"messages":[{"role":"user","content":"hi"}],
		"stream": true
	}`))

	type result struct {
		resp    *http.Response
		capture ForwardCapture
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		//nolint:bodyclose // The receiving test owns resp.Body so it can assert streaming flush behavior.
		resp, capture, err := c.ForwardGatewayRequestWithCapture(
			context.Background(),
			oauthAccount("acct-1"),
			[]byte("oauth-access"),
			orig,
			GatewayRoute{OAuthUpstreamPath: "/codex/responses", OAuthBehavior: GatewayOAuthBehaviorChatAdapter},
		)
		resultCh <- result{resp: resp, capture: capture, err: err}
	}()

	select {
	case <-firstHeartbeatSent:
	case <-time.After(time.Second):
		t.Fatal("upstream did not send first SSE heartbeat")
	}

	var got result
	select {
	case got = <-resultCh:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("chat adapter buffered mislabeled SSE until terminal event")
	}
	require.NoError(t, got.err)
	require.NotNil(t, got.resp)
	t.Cleanup(func() { _ = got.resp.Body.Close() })

	close(releaseTerminal)
	reader := bufio.NewReader(got.resp.Body)
	firstEvent, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(firstEvent, "data: "), "firstEvent=%q", firstEvent)
	assert.Contains(t, firstEvent, `"id":"resp_streaming_mislabel"`)
	assert.Contains(t, firstEvent, `"content":"Hi"`)

	rest, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Contains(t, string(rest), `"finish_reason":"stop"`)
	assert.Contains(t, string(rest), "data: [DONE]")
	require.NotNil(t, got.capture.UpstreamResponseBodySnapshot)
	assert.JSONEq(t, `{"id":"resp_streaming_mislabel","output_text":"Hi","usage":{"input_tokens":1,"output_tokens":1}}`, string(got.capture.UpstreamResponseBodySnapshot()))
}

func TestChatAdapterStreamMapsResponsesJSONFallback(t *testing.T) {
	t.Parallel()

	const upstreamJSON = `{
		"id":"resp_json_stream",
		"status":"completed",
		"output":[
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hi from JSON fallback"}]}
		],
		"usage":{
			"input_tokens":3,
			"output_tokens":4,
			"total_tokens":7,
			"input_tokens_details":{"cached_tokens":1},
			"output_tokens_details":{"reasoning_tokens":2}
		}
	}`
	var upstreamBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&upstreamBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(upstreamJSON))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	orig := httptest.NewRequest(http.MethodPost, "http://router/v1/chat/completions", strings.NewReader(`{
		"model":"gpt-stream-json",
		"messages":[{"role":"user","content":"hi"}],
		"stream": true,
		"stream_options": {"include_usage": true}
	}`))
	resp, capture, err := c.ForwardGatewayRequestWithCapture(
		context.Background(),
		oauthAccount("acct-1"),
		[]byte("oauth-access"),
		orig,
		GatewayRoute{OAuthUpstreamPath: "/codex/responses", OAuthBehavior: GatewayOAuthBehaviorChatAdapter},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, capture.TokenUsage)
	t.Cleanup(func() { _ = resp.Body.Close() })

	assert.Equal(t, true, upstreamBody["stream"])
	assert.Equal(t, false, upstreamBody["store"])
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	assert.JSONEq(t, upstreamJSON, string(capture.UpstreamResponseBody))

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	body := string(data)
	assert.Contains(t, body, `"id":"resp_json_stream"`)
	assert.Contains(t, body, `"object":"chat.completion.chunk"`)
	assert.Contains(t, body, `"content":"Hi from JSON fallback"`)
	assert.Contains(t, body, `"finish_reason":"stop"`)
	assert.Contains(t, body, `"choices":[]`)
	assert.Contains(t, body, `"prompt_tokens":3`)
	assert.Contains(t, body, `"completion_tokens":4`)
	assert.Contains(t, body, "data: [DONE]")

	usage := capture.TokenUsage()
	require.NotNil(t, usage)
	assert.EqualValues(t, 3, usage["input"])
	assert.EqualValues(t, 4, usage["output"])
	assert.EqualValues(t, 1, usage["cached_input"])
	assert.EqualValues(t, 2, usage["reasoning"])
}

func TestChatAdapterStreamCapturesUsageWhenClientDoesNotRequestUsageChunk(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","delta":"Hi"}`,
			``,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"id":"resp_usage_capture","usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7,"input_tokens_details":{"cached_tokens":1},"output_tokens_details":{"reasoning_tokens":2}}}}`,
			``,
		}, "\n")))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	orig := httptest.NewRequest(http.MethodPost, "http://router/v1/chat/completions", strings.NewReader(`{
		"model":"gpt-stream",
		"messages":[{"role":"user","content":"hi"}],
		"stream": true
	}`))
	resp, capture, err := c.ForwardGatewayRequestWithCapture(
		context.Background(),
		oauthAccount("acct-1"),
		[]byte("oauth-access"),
		orig,
		GatewayRoute{OAuthUpstreamPath: "/codex/responses", OAuthBehavior: GatewayOAuthBehaviorChatAdapter},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, capture.TokenUsage)
	t.Cleanup(func() { _ = resp.Body.Close() })

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	body := string(data)
	assert.Contains(t, body, `"content":"Hi"`)
	assert.NotContains(t, body, `"usage"`)
	require.NotNil(t, capture.ModelParams)
	metadata := capture.ModelParams()
	require.NotNil(t, metadata)
	assert.Equal(t, true, metadata["chat_adapter_generated_id"])
	protocolID, ok := metadata["chat_adapter_protocol_id"].(string)
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(protocolID, "chatcmpl_req_"), "protocolID=%q", protocolID)
	assert.Contains(t, body, `"id":"`+protocolID+`"`)
	assert.NotContains(t, body, `"id":"resp_usage_capture"`)

	usage := capture.TokenUsage()
	require.NotNil(t, usage)
	assert.EqualValues(t, 3, usage["input"])
	assert.EqualValues(t, 4, usage["output"])
	assert.EqualValues(t, 1, usage["cached_input"])
	assert.EqualValues(t, 2, usage["reasoning"])
}

func TestChatAdapterStreamFailedResponseReturnsReadError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.failed`,
			`data: {"type":"response.failed","response":{"id":"resp_fail","error":{"code":"server_error","message":"failed","type":"server_error"}}}`,
			``,
		}, "\n")))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	orig := httptest.NewRequest(http.MethodPost, "http://router/v1/chat/completions", strings.NewReader(`{
		"model":"gpt-stream-fail",
		"messages":[{"role":"user","content":"hi"}],
		"stream": true
	}`))
	resp, _, err := c.ForwardGatewayRequestWithCapture(
		context.Background(),
		oauthAccount("acct-1"),
		[]byte("oauth-access"),
		orig,
		GatewayRoute{OAuthUpstreamPath: "/codex/responses", OAuthBehavior: GatewayOAuthBehaviorChatAdapter},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })

	data, readErr := io.ReadAll(resp.Body)
	require.Error(t, readErr)
	assert.True(t, errors.Is(readErr, errChatAdapterStreamFailed), "readErr=%v", readErr)
	body := string(data)
	assert.Contains(t, body, `"error"`)
	assert.Contains(t, body, "server_error")
	assert.Contains(t, body, "data: [DONE]")
}

func TestChatAdapterStreamJSONFailedResponseReturnsReadError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_fail_json","status":"failed","error":{"code":"server_error","message":"failed","type":"server_error"}}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	orig := httptest.NewRequest(http.MethodPost, "http://router/v1/chat/completions", strings.NewReader(`{
		"model":"gpt-stream-fail-json",
		"messages":[{"role":"user","content":"hi"}],
		"stream": true
	}`))
	resp, _, err := c.ForwardGatewayRequestWithCapture(
		context.Background(),
		oauthAccount("acct-1"),
		[]byte("oauth-access"),
		orig,
		GatewayRoute{OAuthUpstreamPath: "/codex/responses", OAuthBehavior: GatewayOAuthBehaviorChatAdapter},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })

	data, readErr := io.ReadAll(resp.Body)
	require.Error(t, readErr)
	assert.True(t, errors.Is(readErr, errChatAdapterStreamFailed), "readErr=%v", readErr)
	body := string(data)
	assert.Contains(t, body, `"error"`)
	assert.Contains(t, body, "server_error")
	assert.Contains(t, body, "data: [DONE]")
}

func TestChatAdapterStreamMissingTerminalReturnsReadError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","delta":"partial"}`,
			``,
		}, "\n")))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	orig := httptest.NewRequest(http.MethodPost, "http://router/v1/chat/completions", strings.NewReader(`{
		"model":"gpt-stream-malformed",
		"messages":[{"role":"user","content":"hi"}],
		"stream": true
	}`))
	resp, _, err := c.ForwardGatewayRequestWithCapture(
		context.Background(),
		oauthAccount("acct-1"),
		[]byte("oauth-access"),
		orig,
		GatewayRoute{OAuthUpstreamPath: "/codex/responses", OAuthBehavior: GatewayOAuthBehaviorChatAdapter},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })

	data, readErr := io.ReadAll(resp.Body)
	require.Error(t, readErr)
	assert.Contains(t, readErr.Error(), "missing terminal event")
	assert.Contains(t, string(data), `"content":"partial"`)
	assert.NotContains(t, string(data), "data: [DONE]")
}

func TestChatAdapterStreamMapsToolCallsAndRefusal(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.refusal.delta`,
			`data: {"type":"response.refusal.delta","delta":"No"}`,
			``,
			`event: response.output_tool_call.delta`,
			`data: {"type":"response.output_tool_call.delta","call_id":"call_1","name":"lookup","arguments":"{\"q\":"}`,
			``,
			`event: response.output_tool_call.delta`,
			`data: {"type":"response.output_tool_call.delta","call_id":"call_1","arguments":"\"abc\"}"}`,
			``,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"id":"resp_stream_tools","usage":{"input_tokens":3,"output_tokens":4}}}`,
			``,
		}, "\n")))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	orig := httptest.NewRequest(http.MethodPost, "http://router/v1/chat/completions", strings.NewReader(`{
		"model":"gpt-stream-tools",
		"messages":[{"role":"user","content":"hi"}],
		"stream": true
	}`))
	resp, _, err := c.ForwardGatewayRequestWithCapture(
		context.Background(),
		oauthAccount("acct-1"),
		[]byte("oauth-access"),
		orig,
		GatewayRoute{OAuthUpstreamPath: "/codex/responses", OAuthBehavior: GatewayOAuthBehaviorChatAdapter},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	body := string(data)
	assert.Contains(t, body, `"refusal":"No"`)
	assert.Contains(t, body, `"tool_calls":[{"function":{"arguments":"{\"q\":","name":"lookup"},"id":"call_1","index":0,"type":"function"}]`)
	assert.Contains(t, body, `"tool_calls":[{"function":{"arguments":"\"abc\"}"},"index":0,"type":"function"}]`)
	assert.Contains(t, body, `"finish_reason":"tool_calls"`)
	assert.Contains(t, body, "data: [DONE]")
}
