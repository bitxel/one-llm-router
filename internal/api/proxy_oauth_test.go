package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/core"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/oauth"
	"github.com/user/one-llm-router/internal/provider/openai"
	"github.com/user/one-llm-router/internal/store"
)

func TestProxyOAuthCodexExplicitMappings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		method             string
		clientPath         string
		upstreamPath       string
		body               string
		wantBody           string
		wantAcceptEncoding string
		wantMetadata       openai.BridgeMetadata
	}{
		{
			name:               "backend codex responses",
			method:             http.MethodPost,
			clientPath:         "/backend-api/codex/responses",
			upstreamPath:       "/codex/responses",
			body:               `{"model":"gpt-5.4-mini","instructions":"be brief","input":"hello","stream":false,"temperature":0.3,"max_output_tokens":128}`,
			wantBody:           `{"model":"gpt-5.4-mini","instructions":"be brief","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],"stream":true,"store":false}`,
			wantAcceptEncoding: "identity",
			wantMetadata: openai.BridgeMetadata{
				OpID:             openai.OpCodexNativeResponsesCreate,
				BridgeID:         openai.BridgeCodexNativeResponsesDirect,
				ClientContract:   openai.ContractChatGPTBackendAPICodexResponses,
				UpstreamContract: openai.ContractChatGPTBackendAPICodexResponses,
				CredentialClass:  openai.CredentialClassOAuth,
			},
		},
		{
			name:               "backend codex compact",
			method:             http.MethodPost,
			clientPath:         "/backend-api/codex/responses/compact",
			upstreamPath:       "/codex/responses/compact",
			body:               `{"model":"gpt-5.4-mini","instructions":"summarize","input":"hello","temperature":0.3,"max_output_tokens":128}`,
			wantBody:           `{"model":"gpt-5.4-mini","instructions":"summarize","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]}`,
			wantAcceptEncoding: "identity",
			wantMetadata: openai.BridgeMetadata{
				OpID:             openai.OpCodexNativeResponsesCompact,
				BridgeID:         openai.BridgeCodexNativeResponsesCompactDirect,
				ClientContract:   openai.ContractChatGPTBackendAPICodexResponsesCompact,
				UpstreamContract: openai.ContractChatGPTBackendAPICodexResponsesCompact,
				CredentialClass:  openai.CredentialClassOAuth,
			},
		},
		{
			name:         "backend codex models",
			method:       http.MethodGet,
			clientPath:   "/backend-api/codex/models",
			upstreamPath: "/codex/models",
			wantMetadata: openai.BridgeMetadata{
				OpID:             openai.OpCodexNativeModelsList,
				BridgeID:         openai.BridgeCodexNativeModelsDirect,
				ClientContract:   openai.ContractChatGPTBackendAPICodexModels,
				UpstreamContract: openai.ContractChatGPTBackendAPICodexModels,
				CredentialClass:  openai.CredentialClassOAuth,
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var upstreamCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamCalls.Add(1)
				assert.Equal(t, tc.upstreamPath, r.URL.Path)
				assert.Equal(t, "Bearer oauth-access", r.Header.Get("Authorization"))
				assert.Equal(t, "acct-oauth-access", r.Header.Get("chatgpt-account-id"))
				assert.Equal(t, openai.CodexCLIUserAgent, r.Header.Get("User-Agent"))
				assert.Equal(t, tc.wantAcceptEncoding, r.Header.Get("Accept-Encoding"))
				data, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				if tc.wantBody == "" {
					assert.Equal(t, tc.body, string(data))
				} else {
					assert.JSONEq(t, tc.wantBody, string(data))
					assert.NotContains(t, string(data), "temperature")
					assert.NotContains(t, string(data), "max_output_tokens")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			t.Cleanup(upstream.Close)

			h := newOAuthProxyHarness(t, upstream.URL)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.clientPath, strings.NewReader(tc.body))

			h.handler.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, int32(1), upstreamCalls.Load())
			recordRepo := store.NewRequestRecordRepo(h.store.Engine())
			require.EventuallyWithT(t, func(c *assert.CollectT) {
				records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
				require.NoError(c, err)
				require.Len(c, records, 1)
				assertRecordBridgeMetadata(c, records[0], tc.wantMetadata)
				assertRecordBridgeUpstreamEndpoint(c, records[0], tc.upstreamPath)
			}, time.Second, 10*time.Millisecond)
		})
	}
}

func TestProxyOAuthCodexNormalizationRejectsInvalidBeforeUpstream(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		path         string
		body         string
		wantMetadata openai.BridgeMetadata
	}{
		{
			name: "backend codex responses store true",
			path: "/backend-api/codex/responses",
			body: `{"model":"gpt-5.4-mini","input":"hello","store":true}`,
			wantMetadata: openai.BridgeMetadata{
				OpID:             openai.OpCodexNativeResponsesCreate,
				BridgeID:         openai.BridgeCodexNativeResponsesDirect,
				ClientContract:   openai.ContractChatGPTBackendAPICodexResponses,
				UpstreamContract: openai.ContractChatGPTBackendAPICodexResponses,
				CredentialClass:  openai.CredentialClassOAuth,
			},
		},
		{
			name: "backend codex compact store true",
			path: "/backend-api/codex/responses/compact",
			body: `{"model":"gpt-5.4-mini","input":"hello","store":true}`,
			wantMetadata: openai.BridgeMetadata{
				OpID:             openai.OpCodexNativeResponsesCompact,
				BridgeID:         openai.BridgeCodexNativeResponsesCompactDirect,
				ClientContract:   openai.ContractChatGPTBackendAPICodexResponsesCompact,
				UpstreamContract: openai.ContractChatGPTBackendAPICodexResponsesCompact,
				CredentialClass:  openai.CredentialClassOAuth,
			},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var upstreamCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				upstreamCalls.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(upstream.Close)

			h := newOAuthProxyHarness(t, upstream.URL)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))

			h.handler.ServeHTTP(rec, req)

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Equal(t, int32(0), upstreamCalls.Load())
			var envelope RouterErrorEnvelope
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
			assert.Equal(t, ErrCodeInvalidRequest, envelope.Error.Code)
			assert.Equal(t, "router_error", envelope.Error.Type)

			recordRepo := store.NewRequestRecordRepo(h.store.Engine())
			require.EventuallyWithT(t, func(c *assert.CollectT) {
				records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
				require.NoError(c, err)
				require.Len(c, records, 1)
				assert.Nil(c, records[0].UpstreamRequestBody)
				assert.Nil(c, records[0].UpstreamResponseBody)
				require.NotNil(c, records[0].ErrorCode)
				assert.Equal(c, ErrCodeInvalidRequest, *records[0].ErrorCode)
				assertRecordBridgeMetadata(c, records[0], tc.wantMetadata)
			}, time.Second, 10*time.Millisecond)
		})
	}
}

func TestProxyOAuthChatCompletionsBridgeIgnoresUnsupportedBeforeUpstream(t *testing.T) {
	t.Parallel()

	var upstreamCalls atomic.Int32
	var upstreamBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		var err error
		upstreamBody, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","delta":"ok"}`,
			``,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"id":"resp_ignore","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
			``,
		}, "\n")))
	}))
	t.Cleanup(upstream.Close)

	h := newOAuthProxyHarness(t, upstream.URL)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"gpt-5.4-mini",
		"messages":[{"role":"user","content":"hi"}],
		"logprobs": true,
		"reasoning_effort": "low",
		"reasoning": {"effort": "high"}
	}`))

	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, int32(1), upstreamCalls.Load())
	assert.NotContains(t, string(upstreamBody), "logprobs")
	assert.Contains(t, string(upstreamBody), `"reasoning":{"effort":"low"}`)

	recordRepo := store.NewRequestRecordRepo(h.store.Engine())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		require.NoError(c, err)
		require.Len(c, records, 1)
		assert.Nil(c, records[0].ErrorCode)
		assert.Equal(c, "low", records[0].ModelParams["reasoning_effort"])
		assertRecordBridgeMetadata(c, records[0], openai.BridgeMetadata{
			OpID:             openai.OpOpenAIChatCompletionsCreate,
			BridgeID:         openai.BridgeOpenAIChatCompletionsToCodex,
			ClientContract:   openai.ContractOpenAIV1ChatCompletions,
			UpstreamContract: openai.ContractChatGPTBackendAPICodexResponses,
			CredentialClass:  openai.CredentialClassOAuth,
		})
	}, time.Second, 10*time.Millisecond)
}

func TestProxyOAuthModelsFacadeUsesCodexModels(t *testing.T) {
	t.Parallel()

	var upstreamPath string
	var upstreamAcceptEncoding string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPath = r.URL.Path
		upstreamAcceptEncoding = r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-5.4-codex","display_name":"GPT 5.4 Codex","description":"Codex model","context_window":200000,"owned_by":"codex-real-owner","created":1710000000}]}`))
	}))
	t.Cleanup(upstream.Close)

	h := newOAuthProxyHarness(t, upstream.URL)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)

	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "/codex/models", upstreamPath)
	assert.Equal(t, "identity", upstreamAcceptEncoding)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "list", body["object"])
	data, ok := body["data"].([]any)
	require.True(t, ok)
	require.Len(t, data, 1)
	model, ok := data[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "gpt-5.4-codex", model["id"])
	assert.Equal(t, "model", model["object"])
	assert.Equal(t, "codex-real-owner", model["owned_by"])
}

func TestProxyOAuthRecordsBridgeMetadata(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/codex/responses", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_123","output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(upstream.Close)

	h := newOAuthProxyHarness(t, upstream.URL)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.4-mini","input":"hello","stream":false}`))

	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	recordRepo := store.NewRequestRecordRepo(h.store.Engine())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		require.NoError(c, err)
		require.Len(c, records, 1)
		assertRecordBridgeMetadata(c, records[0], openai.BridgeMetadata{
			OpID:             openai.OpOpenAIResponsesCreate,
			BridgeID:         openai.BridgeOpenAIResponsesToCodex,
			ClientContract:   openai.ContractOpenAIV1Responses,
			UpstreamContract: openai.ContractChatGPTBackendAPICodexResponses,
			CredentialClass:  openai.CredentialClassOAuth,
		})
		assertRecordBridgeUpstreamEndpoint(c, records[0], "/codex/responses")
	}, time.Second, 10*time.Millisecond)
}

func TestProxyOAuthTranscribeDisablesBodyCapture(t *testing.T) {
	t.Parallel()

	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	part, err := writer.CreateFormFile("file", "sample.wav")
	require.NoError(t, err)
	_, err = part.Write([]byte("RIFF-audio-bytes"))
	require.NoError(t, err)
	require.NoError(t, writer.WriteField("prompt", "meeting notes"))
	require.NoError(t, writer.Close())

	var upstreamContentType string
	var upstreamBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/transcribe", r.URL.Path)
		upstreamContentType = r.Header.Get("Content-Type")
		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		upstreamBody = string(data)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hello"}`))
	}))
	t.Cleanup(upstream.Close)

	h := newOAuthProxyHarness(t, upstream.URL)
	h.handler.SetBodyLogFunc(func() (bool, bool, bool) { return true, true, true })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/backend-api/transcribe", bytes.NewReader(form.Bytes()))
	req.Header.Set("Content-Type", writer.FormDataContentType())

	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, strings.HasPrefix(upstreamContentType, "multipart/form-data; boundary="))
	assert.Contains(t, upstreamBody, "RIFF-audio-bytes")

	recordRepo := store.NewRequestRecordRepo(h.store.Engine())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		require.NoError(c, err)
		require.Len(c, records, 1)
		assert.Nil(c, records[0].ClientRequestBody)
		assert.Nil(c, records[0].UpstreamRequestBody)
		require.NotNil(c, records[0].UpstreamResponseBody)
		assert.JSONEq(c, `{"text":"hello"}`, *records[0].UpstreamResponseBody)
		assert.NotContains(c, *records[0].UpstreamResponseBody, "RIFF-audio-bytes")
	}, time.Second, 10*time.Millisecond)
}

func TestProxyOAuthTranscribeRejectsOversizeBeforeUpstream(t *testing.T) {
	t.Parallel()

	var upstreamHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	h := newOAuthProxyHarness(t, upstream.URL)
	h.handler.maxRequestBody = 16

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/backend-api/transcribe", strings.NewReader(strings.Repeat("x", 17)))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=fixture")

	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
	assert.Equal(t, int32(0), upstreamHits.Load())
	var envelope RouterErrorEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	assert.Equal(t, ErrCodeInvalidRequest, envelope.Error.Code)
	assert.Equal(t, "router_error", envelope.Error.Type)

	recordRepo := store.NewRequestRecordRepo(h.store.Engine())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		require.NoError(c, err)
		require.Len(c, records, 1)
		assert.Nil(c, records[0].ClientRequestBody)
		assert.Nil(c, records[0].UpstreamRequestBody)
		assert.Nil(c, records[0].UpstreamResponseBody)
		require.NotNil(c, records[0].ErrorCode)
		assert.Equal(c, ErrCodeInvalidRequest, *records[0].ErrorCode)
	}, time.Second, 10*time.Millisecond)
}

func TestProxyOAuthChatCompletionStreamMapsResponsesSSEAndRecordsUsage(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/codex/responses", r.URL.Path)
		assert.Equal(t, "identity", r.Header.Get("Accept-Encoding"))
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
	t.Cleanup(upstream.Close)

	h := newOAuthProxyHarness(t, upstream.URL)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"gpt-stream",
		"messages":[{"role":"user","content":"hi"}],
		"stream": true,
		"stream_options": {"include_usage": true}
	}`))

	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	body := rec.Body.String()
	assert.Contains(t, body, `"object":"chat.completion.chunk"`)
	assert.Contains(t, body, `"id":"resp_stream"`)
	assert.Contains(t, body, `"content":"Hi"`)
	assert.Contains(t, body, "data: [DONE]")
	assert.NotContains(t, body, `"id":"chatcmpl_stream"`)
	assert.NotContains(t, body, "response.output_text.delta")

	recordRepo := store.NewRequestRecordRepo(h.store.Engine())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		require.NoError(c, err)
		require.Len(c, records, 1)
		assert.Equal(c, domain.ResponseModeSSE, records[0].ResponseMode)
		assert.EqualValues(c, 3, records[0].TokenUsage["input"])
		assert.EqualValues(c, 4, records[0].TokenUsage["output"])
	}, time.Second, 10*time.Millisecond)
}

func TestProxyOAuthChatCompletionStreamMapsResponsesJSONFallbackAndRecordsUsage(t *testing.T) {
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
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/codex/responses", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&upstreamBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(upstreamJSON))
	}))
	t.Cleanup(upstream.Close)

	h := newOAuthProxyHarness(t, upstream.URL)
	h.handler.SetBodyLogFunc(func() (bool, bool, bool) {
		return true, true, true
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"gpt-stream-json",
		"messages":[{"role":"user","content":"hi"}],
		"stream": true,
		"stream_options": {"include_usage": true}
	}`))

	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	assert.Equal(t, true, upstreamBody["stream"])
	assert.Equal(t, false, upstreamBody["store"])
	body := rec.Body.String()
	assert.Contains(t, body, `"object":"chat.completion.chunk"`)
	assert.Contains(t, body, `"content":"Hi from JSON fallback"`)
	assert.Contains(t, body, `"choices":[]`)
	assert.Contains(t, body, `"prompt_tokens":3`)
	assert.Contains(t, body, `"completion_tokens":4`)
	assert.Contains(t, body, "data: [DONE]")

	recordRepo := store.NewRequestRecordRepo(h.store.Engine())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		require.NoError(c, err)
		require.Len(c, records, 1)
		assert.Equal(c, domain.OutcomeSuccess, records[0].Outcome)
		assert.Equal(c, domain.ResponseModeSSE, records[0].ResponseMode)
		assert.EqualValues(c, 3, records[0].TokenUsage["input"])
		assert.EqualValues(c, 4, records[0].TokenUsage["output"])
		assert.EqualValues(c, 1, records[0].TokenUsage["cached_input"])
		assert.EqualValues(c, 2, records[0].TokenUsage["reasoning"])
		require.NotNil(c, records[0].UpstreamResponseBody)
		assert.JSONEq(c, upstreamJSON, *records[0].UpstreamResponseBody)
	}, time.Second, 10*time.Millisecond)
}

func TestProxyOAuthChatCompletionStreamMapsResponsesSSEMislabeledAsJSONAndRecordsUsage(t *testing.T) {
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
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/codex/responses", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&upstreamBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(upstreamSSE))
	}))
	t.Cleanup(upstream.Close)

	h := newOAuthProxyHarness(t, upstream.URL)
	h.handler.SetBodyLogFunc(func() (bool, bool, bool) {
		return true, true, true
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"gpt-stream-mislabel",
		"messages":[{"role":"user","content":"hi"}],
		"stream": true,
		"stream_options": {"include_usage": true}
	}`))

	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	assert.Equal(t, true, upstreamBody["stream"])
	assert.Equal(t, false, upstreamBody["store"])
	body := rec.Body.String()
	assert.Contains(t, body, `"object":"chat.completion.chunk"`)
	assert.Contains(t, body, `"id":"resp_mislabel_stream"`)
	assert.Contains(t, body, `"content":"Hi"`)
	assert.Contains(t, body, `"prompt_tokens":3`)
	assert.Contains(t, body, `"completion_tokens":4`)
	assert.Contains(t, body, "data: [DONE]")
	assert.NotContains(t, body, `"id":"chatcmpl_stream"`)

	recordRepo := store.NewRequestRecordRepo(h.store.Engine())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		require.NoError(c, err)
		require.Len(c, records, 1)
		assert.Equal(c, domain.OutcomeSuccess, records[0].Outcome)
		assert.Equal(c, domain.ResponseModeSSE, records[0].ResponseMode)
		assert.EqualValues(c, 3, records[0].TokenUsage["input"])
		assert.EqualValues(c, 4, records[0].TokenUsage["output"])
		require.NotNil(c, records[0].UpstreamResponseBody)
		assert.JSONEq(c, `{"id":"resp_mislabel_stream","output_text":"Hi","usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}}`, *records[0].UpstreamResponseBody)
	}, time.Second, 10*time.Millisecond)
}

func TestProxyOAuthChatCompletionStreamRecordsUsageWhenClientOmitsUsageChunk(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/codex/responses", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.output_text.delta`,
			`data: {"type":"response.output_text.delta","delta":"Hi"}`,
			``,
			`event: response.completed`,
			`data: {"type":"response.completed","response":{"id":"resp_stream","usage":{"input_tokens":3,"output_tokens":4,"input_tokens_details":{"cached_tokens":1},"output_tokens_details":{"reasoning_tokens":2}}}}`,
			``,
		}, "\n")))
	}))
	t.Cleanup(upstream.Close)

	h := newOAuthProxyHarness(t, upstream.URL)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"gpt-stream",
		"messages":[{"role":"user","content":"hi"}],
		"stream": true
	}`))

	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	body := rec.Body.String()
	assert.Contains(t, body, `"content":"Hi"`)
	assert.NotContains(t, body, `"usage"`)

	recordRepo := store.NewRequestRecordRepo(h.store.Engine())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		require.NoError(c, err)
		require.Len(c, records, 1)
		assert.Equal(c, domain.ResponseModeSSE, records[0].ResponseMode)
		assert.EqualValues(c, 3, records[0].TokenUsage["input"])
		assert.EqualValues(c, 4, records[0].TokenUsage["output"])
		assert.EqualValues(c, 1, records[0].TokenUsage["cached_input"])
		assert.EqualValues(c, 2, records[0].TokenUsage["reasoning"])
		assert.Equal(c, true, records[0].ModelParams["chat_adapter_generated_id"])
		protocolID, ok := records[0].ModelParams["chat_adapter_protocol_id"].(string)
		require.True(c, ok)
		assert.True(c, strings.HasPrefix(protocolID, "chatcmpl_req_"), "protocolID=%q", protocolID)
	}, time.Second, 10*time.Millisecond)
}

func TestProxyOAuthChatCompletionStreamFailureRecordsRouterError(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/codex/responses", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`event: response.failed`,
			`data: {"type":"response.failed","response":{"id":"resp_fail","error":{"code":"server_error","message":"failed","type":"server_error"}}}`,
			``,
		}, "\n")))
	}))
	t.Cleanup(upstream.Close)

	h := newOAuthProxyHarness(t, upstream.URL)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{
		"model":"gpt-stream-fail",
		"messages":[{"role":"user","content":"hi"}],
		"stream": true
	}`))

	h.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"error"`)
	assert.Contains(t, rec.Body.String(), "server_error")
	assert.Contains(t, rec.Body.String(), "data: [DONE]")

	recordRepo := store.NewRequestRecordRepo(h.store.Engine())
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := recordRepo.Query(context.Background(), core.QueryParams{Limit: 10})
		require.NoError(c, err)
		require.Len(c, records, 1)
		assert.Equal(c, domain.ResponseModeSSE, records[0].ResponseMode)
		assert.Equal(c, domain.OutcomeRouterError, records[0].Outcome)
		require.NotNil(c, records[0].ErrorCode)
		assert.Equal(c, ErrCodeUpstreamRespInvalid, *records[0].ErrorCode)
		assert.Nil(c, records[0].TokenUsage)
	}, time.Second, 10*time.Millisecond)
}

func newOAuthProxyHarness(t *testing.T, codexBaseURL string) *proxyHarness {
	t.Helper()

	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	h := newProxyHarness(t, 5*time.Second)
	h.client.SetCodexBackendBaseURLForTest(codexBaseURL)
	insertOAuthProxyAccount(t, h.repo, codexBaseURL, now, "oauth-access", "oauth-refresh", now.Add(2*time.Hour))
	coord := oauth.NewCoordinatorWithClock(oauth.NewFakeClock(now), &proxyRefreshProvider{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	coord.SetAccountStore(h.repo)
	h.selector.PreForward = coord.RefreshIfStale
	return h
}

func TestProxyOAuthOnly_ModelRetrieveReturnsNoCapacity(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	var oauthHits atomic.Int32
	oauthUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		oauthHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(oauthUpstream.Close)

	h := newProxyHarness(t, 5*time.Second)
	h.client.SetCodexBackendBaseURLForTest(oauthUpstream.URL)
	insertOAuthProxyAccount(t, h.repo, oauthUpstream.URL, now, "oauth-access", "oauth-refresh", now.Add(2*time.Hour))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/models/gpt-4o-mini", nil)
	h.handler.ServeHTTP(w, r)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, int32(0), oauthHits.Load())
	assert.Contains(t, w.Body.String(), ErrCodeNoAvailableAccount)
}
