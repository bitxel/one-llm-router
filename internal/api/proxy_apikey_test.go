package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/provider/openai"
	"github.com/user/one-llm-router/internal/store"
)

func TestProxyAPIKeyDirectSupportedAllowlist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		method         string
		target         string
		body           string
		acceptEncoding string
	}{
		{name: "responses retrieve", method: http.MethodGet, target: "/v1/responses/resp_123?include[]=usage"},
		{name: "responses delete", method: http.MethodDelete, target: "/v1/responses/resp_123"},
		{name: "responses cancel", method: http.MethodPost, target: "/v1/responses/resp_123/cancel", body: `{}`},
		{name: "responses input items", method: http.MethodGet, target: "/v1/responses/resp_123/input_items?limit=20"},
		{name: "responses input tokens", method: http.MethodPost, target: "/v1/responses/input_tokens", body: `{"model":"gpt-4.1","input":"hello"}`},
		{name: "conversation create", method: http.MethodPost, target: "/v1/conversations", body: `{"metadata":{"k":"v"}}`},
		{name: "conversation retrieve", method: http.MethodGet, target: "/v1/conversations/conv_123"},
		{name: "conversation update", method: http.MethodPost, target: "/v1/conversations/conv_123", body: `{"metadata":{"k":"v2"}}`},
		{name: "conversation delete", method: http.MethodDelete, target: "/v1/conversations/conv_123"},
		{name: "conversation item create", method: http.MethodPost, target: "/v1/conversations/conv_123/items", body: `{"items":[]}`},
		{name: "conversation item list", method: http.MethodGet, target: "/v1/conversations/conv_123/items?limit=20"},
		{name: "conversation item retrieve", method: http.MethodGet, target: "/v1/conversations/conv_123/items/item_123"},
		{name: "conversation item delete", method: http.MethodDelete, target: "/v1/conversations/conv_123/items/item_123"},
		{name: "chat completion create", method: http.MethodPost, target: "/v1/chat/completions", body: `{"model":"gpt-4.1","messages":[]}`},
		{name: "chat completion list", method: http.MethodGet, target: "/v1/chat/completions"},
		{name: "chat completion retrieve", method: http.MethodGet, target: "/v1/chat/completions/chatcmpl_123"},
		{name: "chat completion update", method: http.MethodPost, target: "/v1/chat/completions/chatcmpl_123", body: `{"metadata":{"k":"v"}}`},
		{name: "chat completion delete", method: http.MethodDelete, target: "/v1/chat/completions/chatcmpl_123"},
		{name: "chat completion messages", method: http.MethodGet, target: "/v1/chat/completions/chatcmpl_123/messages?limit=5"},
		{name: "models list", method: http.MethodGet, target: "/v1/models", acceptEncoding: "gzip"},
		{name: "model retrieve", method: http.MethodGet, target: "/v1/models/gpt-4.1"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var upstreamCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamCalls.Add(1)
				expectedPath, expectedQuery, _ := strings.Cut(tc.target, "?")
				assert.Equal(t, expectedPath, r.URL.Path)
				assert.Equal(t, expectedQuery, r.URL.RawQuery)
				assert.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))
				if tc.acceptEncoding != "" {
					assert.Equal(t, tc.acceptEncoding, r.Header.Get("Accept-Encoding"))
				}
				data, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				assert.Equal(t, tc.body, string(data))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{"path": r.URL.Path})
			}))
			t.Cleanup(upstream.Close)

			handler, _ := setupProxyTest(t, upstream)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			if tc.acceptEncoding != "" {
				req.Header.Set("Accept-Encoding", tc.acceptEncoding)
			}

			handler.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, int32(1), upstreamCalls.Load())
		})
	}
}

func TestProxyAPIKeyDirectPreservesProviderErrors(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models/missing-model", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"model_not_found","message":"missing"}}`))
	}))
	t.Cleanup(upstream.Close)

	handler, _ := setupProxyTest(t, upstream)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models/missing-model", nil)

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.JSONEq(t, `{"error":{"code":"model_not_found","message":"missing"}}`, rec.Body.String())
}

func TestProxyAPIKeyRecordsBridgeMetadata(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl_123","object":"chat.completion","choices":[]}`))
	}))
	t.Cleanup(upstream.Close)

	handler, st := setupProxyTest(t, upstream)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5.4-mini","messages":[],"service_tier":"priority"}`))

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	recordRepo := store.NewRequestRecordRepo(st.Engine())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		require.NoError(c, err)
		require.Len(c, records, 1)
		assertRecordBridgeMetadata(c, records[0], openai.BridgeMetadata{
			OpID:             openai.OpOpenAIChatCompletionsCreate,
			BridgeID:         openai.BridgeOpenAIChatCompletionsDirect,
			ClientContract:   openai.ContractOpenAIV1ChatCompletions,
			UpstreamContract: openai.ContractOpenAIV1ChatCompletions,
			CredentialClass:  openai.CredentialClassAPIKey,
		})
		require.NotNil(c, records[0].Model)
		assert.Equal(c, "gpt-5.4-mini", *records[0].Model)
		assert.Equal(c, "priority", records[0].ModelParams["service_tier"])
	}, time.Second, 10*time.Millisecond)
}

func TestProxyUnsupportedRouteOmitsBridgeMetadata(t *testing.T) {
	t.Parallel()

	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		upstreamCalls.Add(1)
	}))
	t.Cleanup(upstream.Close)

	handler, st := setupProxyTest(t, upstream)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", strings.NewReader("unread"))

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	assert.Equal(t, int32(0), upstreamCalls.Load())
	recordRepo := store.NewRequestRecordRepo(st.Engine())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		require.NoError(c, err)
		require.Len(c, records, 1)
		assert.NotContains(c, records[0].RouterMetadata, openai.RouterMetadataBridgeKey)
	}, time.Second, 10*time.Millisecond)
}

func TestProxyAPIKeyDoesNotWildcardForwardDeferredV1Routes(t *testing.T) {
	t.Parallel()

	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		upstreamCalls.Add(1)
	}))
	t.Cleanup(upstream.Close)

	handler, _ := setupProxyTest(t, upstream)
	body := &readTrackingBody{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", body)

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, int32(0), upstreamCalls.Load())
	assert.Equal(t, int32(0), body.reads.Load(), "deferred route must reject before body read")

	var envelope RouterErrorEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	assert.Equal(t, ErrCodeUnsupportedEndpoint, envelope.Error.Code)
}
