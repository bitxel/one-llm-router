package openai

import (
	"context"
	"encoding/json"
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

func TestForwardAccountRequest_APIKeyDirectPreservesSupportedPlatformPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{name: "responses retrieve", method: http.MethodGet, target: "/v1/responses/resp_123?include[]=usage"},
		{name: "responses delete", method: http.MethodDelete, target: "/v1/responses/resp_123"},
		{name: "responses cancel", method: http.MethodPost, target: "/v1/responses/resp_123/cancel", body: `{}`},
		{name: "responses input items", method: http.MethodGet, target: "/v1/responses/resp_123/input_items?limit=20"},
		{name: "responses input tokens", method: http.MethodPost, target: "/v1/responses/input_tokens", body: `{"model":"gpt-4.1","input":"hello"}`},
		{name: "responses compact", method: http.MethodPost, target: "/v1/responses/compact", body: `{"items":[]}`},
		{name: "conversation create", method: http.MethodPost, target: "/v1/conversations", body: `{"metadata":{"k":"v"}}`},
		{name: "conversation retrieve", method: http.MethodGet, target: "/v1/conversations/conv_123"},
		{name: "conversation update", method: http.MethodPost, target: "/v1/conversations/conv_123", body: `{"metadata":{"k":"v2"}}`},
		{name: "conversation delete", method: http.MethodDelete, target: "/v1/conversations/conv_123"},
		{name: "conversation items create", method: http.MethodPost, target: "/v1/conversations/conv_123/items", body: `{"items":[]}`},
		{name: "conversation items list", method: http.MethodGet, target: "/v1/conversations/conv_123/items?limit=20"},
		{name: "conversation item retrieve", method: http.MethodGet, target: "/v1/conversations/conv_123/items/item_123"},
		{name: "conversation item delete", method: http.MethodDelete, target: "/v1/conversations/conv_123/items/item_123"},
		{name: "stored chat list", method: http.MethodGet, target: "/v1/chat/completions"},
		{name: "stored chat retrieve", method: http.MethodGet, target: "/v1/chat/completions/chatcmpl_123"},
		{name: "stored chat update", method: http.MethodPost, target: "/v1/chat/completions/chatcmpl_123", body: `{"metadata":{"k":"v"}}`},
		{name: "stored chat delete", method: http.MethodDelete, target: "/v1/chat/completions/chatcmpl_123"},
		{name: "chat messages", method: http.MethodGet, target: "/v1/chat/completions/chatcmpl_123/messages?limit=1"},
		{name: "model retrieve", method: http.MethodGet, target: "/v1/models/gpt-4.1"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotPath, gotQuery, gotBody, gotAuth, gotCookie string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotQuery = r.URL.RawQuery
				gotAuth = r.Header.Get("Authorization")
				gotCookie = r.Header.Get("Cookie")
				data, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				gotBody = string(data)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			}))
			t.Cleanup(srv.Close)

			orig := httptest.NewRequest(tc.method, "http://router"+tc.target, strings.NewReader(tc.body))
			orig.Header.Set("Cookie", "admin_session=secret")
			resp, capture, err := NewClient(30*time.Second).ForwardAccountRequestWithCapture(
				context.Background(),
				domain.UpstreamAccount{
					ID:         10,
					Provider:   domain.ProviderOpenAI,
					AuthMethod: domain.AuthMethodAPIKey,
					BaseURL:    &srv.URL,
				},
				[]byte("sk-direct"),
				orig,
			)
			require.NoError(t, err)
			require.NotNil(t, resp)
			t.Cleanup(func() { _ = resp.Body.Close() })

			expectedPath, expectedQuery, _ := strings.Cut(tc.target, "?")
			assert.Equal(t, expectedPath, gotPath)
			assert.Equal(t, expectedQuery, gotQuery)
			assert.Equal(t, "Bearer sk-direct", gotAuth)
			assert.Empty(t, gotCookie)
			assert.Equal(t, tc.body, gotBody)
			assert.Equal(t, []byte(tc.body), capture.UpstreamRequestBody)
		})
	}
}

func TestForwardAccountRequest_APIKeyDirectPreservesClientAcceptEncoding(t *testing.T) {
	t.Parallel()

	var gotAcceptEncoding string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAcceptEncoding = r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	t.Cleanup(srv.Close)

	orig := httptest.NewRequest(http.MethodGet, "http://router/v1/models", nil)
	orig.Header.Set("Accept-Encoding", "gzip")

	resp, _, err := NewClient(30*time.Second).ForwardAccountRequestWithCapture(
		context.Background(),
		domain.UpstreamAccount{
			ID:         10,
			Provider:   domain.ProviderOpenAI,
			AuthMethod: domain.AuthMethodAPIKey,
			BaseURL:    &srv.URL,
		},
		[]byte("sk-direct"),
		orig,
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })

	assert.Equal(t, "gzip", gotAcceptEncoding)
}

func TestForwardAccountRequest_APIKeyDirectDoesNotInjectAcceptEncoding(t *testing.T) {
	t.Parallel()

	var gotAcceptEncoding string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAcceptEncoding = r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	t.Cleanup(srv.Close)

	orig := httptest.NewRequest(http.MethodGet, "http://router/v1/models", nil)

	resp, _, err := NewClient(30*time.Second).ForwardAccountRequestWithCapture(
		context.Background(),
		domain.UpstreamAccount{
			ID:         10,
			Provider:   domain.ProviderOpenAI,
			AuthMethod: domain.AuthMethodAPIKey,
			BaseURL:    &srv.URL,
		},
		[]byte("sk-direct"),
		orig,
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })

	assert.Empty(t, gotAcceptEncoding)
}
