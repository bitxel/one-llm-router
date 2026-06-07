package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/domain"
)

func TestNewClient(t *testing.T) {
	timeout := 7 * time.Second
	c := NewClient(timeout)
	require.NotNil(t, c)
	require.NotNil(t, c.httpClient)

	assert.Equal(t, time.Duration(0), c.httpClient.Timeout, "full request timeout must be zero for long SSE streams")

	transport, ok := c.httpClient.Transport.(*http.Transport)
	require.True(t, ok, "expected *http.Transport")
	assert.Equal(t, timeout, transport.ResponseHeaderTimeout)
	assert.Equal(t, 10*time.Second, transport.TLSHandshakeTimeout)
	assert.Equal(t, 100, transport.MaxIdleConns)
	assert.Equal(t, 10, transport.MaxIdleConnsPerHost)
	assert.Equal(t, 90*time.Second, transport.IdleConnTimeout)
	assert.True(t, transport.DisableCompression)

	redirectErr := c.httpClient.CheckRedirect(nil, nil)
	assert.Equal(t, http.ErrUseLastResponse, redirectErr)
}

func TestForwardRequest_Success(t *testing.T) {
	const apiKey = "upstream-secret"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/chat/completions", r.URL.Path)
		assert.Equal(t, "stream=true&foo=bar", r.URL.RawQuery)
		assert.Equal(t, "Bearer "+apiKey, r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "extra-value", r.Header.Get("X-Extra"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Equal(t, `{"model":"gpt-4"}`, string(body))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	orig := httptest.NewRequest(http.MethodPost, "http://downstream.example/v1/chat/completions?stream=true&foo=bar", strings.NewReader(`{"model":"gpt-4"}`))
	orig.Header.Set("Authorization", "Bearer client-should-not-leak")
	orig.Header.Set("Host", "downstream.example")
	orig.Header.Set("Content-Type", "application/json")
	orig.Header.Set("X-Extra", "extra-value")

	resp, err := c.ForwardRequest(context.Background(), srv.URL+"/", apiKey, orig)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, `{"ok":true}`, string(body))
}

func TestForwardAccountRequest_APIKeyKeepsPlatformPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/responses", r.URL.Path)
		assert.Empty(t, r.Header.Get("chatgpt-account-id"))
		assert.Equal(t, "Bearer sk-platform", r.Header.Get("Authorization"))
		assert.Equal(t, "curl/8.7.1", r.Header.Get("User-Agent"))
		assert.Empty(t, r.Header.Get("Cookie"))
		assert.Empty(t, r.Header.Get("X-Admin-Token"))
		assert.Empty(t, r.Header.Get("X-CSRF-Token"))
		assert.Empty(t, r.Header.Get("X-XSRF-Token"))
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "hello", body["input"])
		assert.Equal(t, float64(16), body["max_output_tokens"])
		assert.Equal(t, false, body["stream"])
		assert.NotContains(t, body, "instructions")
		assert.NotContains(t, body, "store")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	orig := httptest.NewRequest(http.MethodPost, "http://downstream/v1/responses", strings.NewReader(`{"model":"gpt-4o-mini","input":"hello","max_output_tokens":16,"stream":false}`))
	orig.Header.Set("Cookie", "admin_session=secret")
	orig.Header.Set("User-Agent", "curl/8.7.1")
	orig.Header.Set("X-Admin-Token", "admin-secret")
	orig.Header.Set("X-CSRF-Token", "csrf-secret")
	orig.Header.Set("X-XSRF-Token", "xsrf-secret")
	resp, capture, err := c.ForwardAccountRequestWithCapture(context.Background(), domain.UpstreamAccount{
		ID:         1,
		Provider:   domain.ProviderOpenAI,
		AuthMethod: domain.AuthMethodAPIKey,
		BaseURL:    &srv.URL,
	}, []byte("sk-platform"), orig)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.JSONEq(t, `{"model":"gpt-4o-mini","input":"hello","max_output_tokens":16,"stream":false}`, string(capture.UpstreamRequestBody))
}

func TestForwardAccountRequest_OAuthStreamingPassesThroughSSE(t *testing.T) {
	chatGPTAccountID := "chatgpt-account-123"
	sseBody := strings.Join([]string{
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":"OK"}`,
		``,
	}, "\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/codex/responses", r.URL.Path)
		assert.Equal(t, chatGPTAccountID, r.Header.Get("chatgpt-account-id"))
		assert.Equal(t, CodexCLIUserAgent, r.Header.Get("User-Agent"))
		assert.Equal(t, "text/event-stream", r.Header.Get("Accept"))
		assert.Equal(t, "identity", r.Header.Get("Accept-Encoding"))
		assert.Empty(t, r.Header.Get("Cookie"))
		assert.Empty(t, r.Header.Get("X-Admin-Token"))

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, true, body["stream"])

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseBody))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	orig := httptest.NewRequest(http.MethodPost, "http://downstream/v1/responses", strings.NewReader(`{"model":"gpt-5.4-mini","input":"hello","stream":true}`))
	orig.Header.Set("Cookie", "admin_session=secret")
	orig.Header.Set("User-Agent", "curl/8.7.1")
	orig.Header.Set("X-Admin-Token", "admin-secret")
	resp, err := c.ForwardAccountRequest(context.Background(), domain.UpstreamAccount{
		ID:               2,
		Provider:         domain.ProviderOpenAI,
		AuthMethod:       domain.AuthMethodOAuthBrowser,
		ChatGPTAccountID: &chatGPTAccountID,
	}, []byte("oauth-access"), orig)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, sseBody, string(out))
}

func TestForwardAccountRequest_OAuthNonStreamingReturnsCollectedJSON(t *testing.T) {
	chatGPTAccountID := "chatgpt-account-123"
	customBaseURL := "https://custom-oauth-base.invalid"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/codex/responses", r.URL.Path)
		assert.Equal(t, "Bearer oauth-access", r.Header.Get("Authorization"))
		assert.Equal(t, chatGPTAccountID, r.Header.Get("chatgpt-account-id"))
		assert.Equal(t, CodexCLIUserAgent, r.Header.Get("User-Agent"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "identity", r.Header.Get("Accept-Encoding"))
		assert.Empty(t, r.Header.Get("X-Forwarded-For"))

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "gpt-5.4-mini", body["model"])
		assert.Equal(t, "", body["instructions"])
		assert.Equal(t, false, body["store"])
		assert.Equal(t, true, body["stream"])
		assert.NotContains(t, body, "max_output_tokens")
		assert.NotContains(t, body, "temperature")
		input, ok := body["input"].([]any)
		require.True(t, ok)
		require.Len(t, input, 1)
		item, ok := input[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "user", item["role"])

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Set-Cookie", "chatgpt=secret")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","delta":"OK"}`,
			``,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":3,"output_tokens":1}}}`,
			``,
		}, "\n")))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	body := `{"model":"gpt-5.4-mini","input":"hello","max_output_tokens":128,"temperature":0.3}`
	orig := httptest.NewRequest(http.MethodPost, "http://downstream/v1/responses", strings.NewReader(body))
	orig.Header.Set("User-Agent", "curl/8.7.1")
	orig.Header.Set("X-Forwarded-For", "client")
	resp, capture, err := c.ForwardAccountRequestWithCapture(context.Background(), domain.UpstreamAccount{
		ID:               2,
		Provider:         domain.ProviderOpenAI,
		AuthMethod:       domain.AuthMethodOAuthBrowser,
		BaseURL:          &customBaseURL,
		ChatGPTAccountID: &chatGPTAccountID,
	}, []byte("oauth-access"), orig)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Content-Encoding"))
	assert.Empty(t, resp.Header.Get("Set-Cookie"))
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"resp_1","output_text":"OK","usage":{"input_tokens":3,"output_tokens":1}}`, string(out))
	assert.JSONEq(t, `{"model":"gpt-5.4-mini","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],"instructions":"","store":false,"stream":true}`, string(capture.UpstreamRequestBody))
	assert.JSONEq(t, `{"id":"resp_1","output_text":"OK","usage":{"input_tokens":3,"output_tokens":1}}`, string(capture.UpstreamResponseBody))
}

func TestForwardAccountRequest_OAuthExplicitStreamFalseReturnsCollectedJSON(t *testing.T) {
	chatGPTAccountID := "chatgpt-account-123"
	var upstreamBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/codex/responses", r.URL.Path)
		assert.Equal(t, CodexCLIUserAgent, r.Header.Get("User-Agent"))
		var err error
		upstreamBody, err = io.ReadAll(r.Body)
		require.NoError(t, err)

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","delta":"OK"}`,
			``,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"id":"resp_false"}}`,
			``,
		}, "\n")))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	orig := httptest.NewRequest(http.MethodPost, "http://downstream/v1/responses", strings.NewReader(`{"model":"gpt-5.4-mini","input":"hello","stream":false}`))
	orig.Header.Set("User-Agent", "curl/8.7.1")
	resp, capture, err := c.ForwardAccountRequestWithCapture(context.Background(), domain.UpstreamAccount{
		ID:               2,
		Provider:         domain.ProviderOpenAI,
		AuthMethod:       domain.AuthMethodOAuthBrowser,
		ChatGPTAccountID: &chatGPTAccountID,
	}, []byte("oauth-access"), orig)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"resp_false","output_text":"OK"}`, string(out))
	assert.Contains(t, string(upstreamBody), `"stream":true`)
	assert.JSONEq(t, `{"id":"resp_false","output_text":"OK"}`, string(capture.UpstreamResponseBody))
}

func TestForwardAccountRequest_OAuthCompactMapsToCodexCompact(t *testing.T) {
	chatGPTAccountID := "chatgpt-account-123"
	var upstreamBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/codex/responses/compact", r.URL.Path)
		assert.Equal(t, "Bearer oauth-access", r.Header.Get("Authorization"))
		assert.Equal(t, chatGPTAccountID, r.Header.Get("chatgpt-account-id"))
		assert.Equal(t, CodexCLIUserAgent, r.Header.Get("User-Agent"))
		var err error
		upstreamBody, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"compact":"ok"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	orig := httptest.NewRequest(http.MethodPost, "http://downstream/v1/responses/compact", strings.NewReader(`{
		"model":"gpt-5.4-mini",
		"instructions":"Be concise.",
		"messages":[
			{"role":"system","content":"Use JSON."},
			{"role":"user","content":"hello"}
		]
	}`))
	orig.Header.Set("User-Agent", "curl/8.7.1")
	resp, capture, err := c.ForwardAccountRequestWithCapture(context.Background(), domain.UpstreamAccount{
		ID:               2,
		Provider:         domain.ProviderOpenAI,
		AuthMethod:       domain.AuthMethodOAuthBrowser,
		ChatGPTAccountID: &chatGPTAccountID,
	}, []byte("oauth-access"), orig)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assert.JSONEq(t, `{
		"model":"gpt-5.4-mini",
		"instructions":"Be concise.\n\nUse JSON.",
		"input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]
	}`, string(upstreamBody))
	assert.JSONEq(t, string(upstreamBody), string(capture.UpstreamRequestBody))
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"compact":"ok"}`, string(out))
}

func TestForwardAccountRequest_OAuthInvalidResponsesJSONFails(t *testing.T) {
	chatGPTAccountID := "chatgpt-account-123"
	c := NewClient(30 * time.Second)
	orig := httptest.NewRequest(http.MethodPost, "http://downstream/v1/responses", strings.NewReader(`{"model":`))
	resp, err := c.ForwardAccountRequest(context.Background(), domain.UpstreamAccount{
		ID:               2,
		Provider:         domain.ProviderOpenAI,
		AuthMethod:       domain.AuthMethodOAuthBrowser,
		ChatGPTAccountID: &chatGPTAccountID,
	}, []byte("oauth-access"), orig)
	if resp != nil {
		t.Cleanup(func() { _ = resp.Body.Close() })
	}
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidUpstreamRequest)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "decode codex upstream request body")
}

func TestCollectCodexSSEBytes(t *testing.T) {
	body := []byte(strings.Join([]string{
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":"O"}`,
		``,
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":"K"}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":3,"output_tokens":1}}}`,
		``,
	}, "\n"))

	got, err := collectCodexSSEBytes(body)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"id": "resp_1",
		"output_text": "OK",
		"usage": {"input_tokens":3,"output_tokens":1}
	}`, string(got))
}

func TestCollectCodexSSEBytes_UsesDoneText(t *testing.T) {
	body := []byte(strings.Join([]string{
		`event: response.output_text.delta`,
		`data:{"type":"response.output_text.delta","delta":"partial"}`,
		``,
		`event: response.output_text.done`,
		`data: {"type":"response.output_text.done","text":"final"}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_1"}}`,
		``,
	}, "\n"))

	got, err := collectCodexSSEBytes(body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"resp_1","output_text":"final"}`, string(got))
}

func TestCollectCodexSSEBytes_IgnoresDoneSentinel(t *testing.T) {
	body := []byte(strings.Join([]string{
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_1"}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n"))

	got, err := collectCodexSSEBytes(body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"resp_1","output_text":""}`, string(got))
}

func TestCollectCodexSSEBytes_ReturnsTerminalFailedResponse(t *testing.T) {
	body := []byte(strings.Join([]string{
		`event: response.failed`,
		`data: {"type":"response.failed","response":{"id":"resp_failed","status":"failed","error":{"code":"rate_limit_exceeded","message":"quota"}}}`,
		``,
	}, "\n"))

	got, err := collectCodexSSEBytes(body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"resp_failed","status":"failed","output_text":"","error":{"code":"rate_limit_exceeded","message":"quota"}}`, string(got))
}

func TestCollectCodexSSEBytes_FailsMalformedEvent(t *testing.T) {
	body := []byte(strings.Join([]string{
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":`,
		``,
	}, "\n"))

	got, err := collectCodexSSEBytes(body)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "decode codex SSE event")
}

func TestCollectCodexSSEBytes_FailsMissingCompleted(t *testing.T) {
	body := []byte(strings.Join([]string{
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":"OK"}`,
		``,
	}, "\n"))

	got, err := collectCodexSSEBytes(body)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "missing terminal event")
}

func TestCollectCodexSSEBytes_FailsCompletedWithoutResponse(t *testing.T) {
	body := []byte(strings.Join([]string{
		`event: response.completed`,
		`data: {"type":"response.completed"}`,
		``,
	}, "\n"))

	got, err := collectCodexSSEBytes(body)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "missing response")
}

func TestCollectCodexSSEResponse_FailsOversized(t *testing.T) {
	got, err := collectCodexSSEResponse(io.LimitReader(
		strings.NewReader(strings.Repeat("x", codexSSECollectLimit+1)),
		codexSSECollectLimit+1,
	))
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "too large")
}

func TestForwardRequest_SSEResponse(t *testing.T) {
	sseBody := "data: {\"type\":\"done\"}\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseBody))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	orig := httptest.NewRequest(http.MethodGet, "http://downstream/v1/responses", nil)

	resp, err := c.ForwardRequest(context.Background(), srv.URL, "k", orig)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, sseBody, string(out))
}

func TestForwardRequest_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	orig := httptest.NewRequest(http.MethodGet, "http://downstream/v1/models", nil)

	resp, err := c.ForwardRequest(context.Background(), srv.URL, "k", orig)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestForwardRequest_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No headers until after client ResponseHeaderTimeout (50ms) elapses.
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(50 * time.Millisecond)
	orig := httptest.NewRequest(http.MethodGet, "http://downstream/v1/slow", nil)

	// ForwardRequest guarantees resp == nil when err != nil (see
	// forwarder.go); nothing to close.
	resp, err := c.ForwardRequest(context.Background(), srv.URL, "k", orig) //nolint:bodyclose
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, ErrUpstreamTimeout)
}

func TestForwardRequest_ConnectionFailed(t *testing.T) {
	c := NewClient(2 * time.Second)
	// Nothing listens on 127.0.0.1:1 — connection refused.
	orig := httptest.NewRequest(http.MethodGet, "http://downstream/v1/models", nil)

	// ForwardRequest guarantees resp == nil when err != nil (see
	// forwarder.go); nothing to close.
	resp, err := c.ForwardRequest(context.Background(), "http://127.0.0.1:1", "k", orig) //nolint:bodyclose
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, ErrUpstreamConnectFailed)
}

func TestIsTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "net.Error Timeout true",
			err:  mockNetErr{timeout: true},
			want: true,
		},
		{
			name: "net.Error Timeout false",
			err:  mockNetErr{timeout: false},
			want: false,
		},
		{
			name: "context.DeadlineExceeded",
			err:  context.DeadlineExceeded,
			want: true,
		},
		{
			name: "wrapped context.DeadlineExceeded",
			err:  errors.New("outer: " + context.DeadlineExceeded.Error()),
			want: false,
		},
		{
			name: "plain error",
			err:  errors.New("some failure"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isTimeout(tt.err))
		})
	}

	t.Run("wrapped deadline with %w", func(t *testing.T) {
		t.Parallel()
		wrapped := fmt.Errorf("upstream: %w", context.DeadlineExceeded)
		assert.True(t, isTimeout(wrapped))
	})
}

// mockNetErr implements net.Error for table tests.
type mockNetErr struct {
	timeout bool
}

func (e mockNetErr) Error() string   { return "mock net error" }
func (e mockNetErr) Timeout() bool   { return e.timeout }
func (e mockNetErr) Temporary() bool { return false }

// TestForwardRequest_StripsHopByHopHeaders asserts RFC 7230 §6.1 compliance:
// hop-by-hop headers (and client-listed Connection-tokens) must never reach
// upstream. Mirrors codex-lb's _HOP_BY_HOP_HEADER_NAMES behaviour.
func TestForwardRequest_StripsHopByHopHeaders(t *testing.T) {
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	orig := httptest.NewRequest(http.MethodGet, "http://downstream/v1/models", nil)
	// RFC 7230 canonical hop-by-hop headers.
	orig.Header.Set("Connection", "close, X-Client-Hop")
	orig.Header.Set("Keep-Alive", "timeout=5")
	orig.Header.Set("Proxy-Authorization", "Basic secret")
	orig.Header.Set("Proxy-Connection", "Keep-Alive")
	orig.Header.Set("Te", "trailers")
	orig.Header.Set("Trailer", "Expires")
	orig.Header.Set("Transfer-Encoding", "chunked")
	orig.Header.Set("Upgrade", "h2c")
	// Dynamic hop token advertised by the client via Connection.
	orig.Header.Set("X-Client-Hop", "please-drop-me")
	// A regular header that MUST survive.
	orig.Header.Set("X-Keep-Me", "alive")

	resp, err := c.ForwardRequest(context.Background(), srv.URL, "k", orig)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	for _, h := range []string{
		"Connection", "Keep-Alive", "Proxy-Authorization", "Proxy-Connection",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade", "X-Client-Hop",
	} {
		assert.Empty(t, gotHeaders.Get(h), "%s must be stripped before reaching upstream", h)
	}
	assert.Equal(t, "alive", gotHeaders.Get("X-Keep-Me"))
	assert.Equal(t, "Bearer k", gotHeaders.Get("Authorization"))
}

func TestFetchUsageParsesNestedRateLimitUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/wham/usage", r.URL.Path)
		assert.Equal(t, "Bearer access-token", r.Header.Get("Authorization"))
		assert.Equal(t, CodexCLIUserAgent, r.Header.Get("User-Agent"))
		assert.Equal(t, "acct-123", r.Header.Get("chatgpt-account-id"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"user_id":"user-1",
			"rate_limit":{
				"primary_window":{"used_percent":28},
				"secondary_window":null
			}
		}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL

	usage, err := c.FetchUsage(context.Background(), "access-token", "acct-123")
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.NotNil(t, usage.RateLimit)
	require.NotNil(t, usage.RateLimit.PrimaryWindow)
	assert.Equal(t, 28.0, usage.RateLimit.PrimaryWindow.UsedPercent)
	assert.Nil(t, usage.RateLimit.SecondaryWindow)
}
