package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/domain"
)

func TestForwardGatewayRequest_OAuthCodexExplicitMappings(t *testing.T) {
	t.Parallel()

	chatGPTAccountID := "chatgpt-account-123"
	tests := []struct {
		name         string
		method       string
		clientPath   string
		upstreamPath string
		body         string
		wantJSON     string
	}{
		{
			name:         "backend codex responses",
			method:       http.MethodPost,
			clientPath:   "/backend-api/codex/responses",
			upstreamPath: "/codex/responses",
			body:         `{"model":"gpt-5.4-mini","instructions":"be brief","input":"hello","stream":false,"store":false,"temperature":0.3,"max_output_tokens":128,"prompt_cache_retention":"24h","safety_identifier":"safe","reasoningEffort":"medium","verbosity":"low","service_tier":"fast"}`,
			wantJSON:     `{"model":"gpt-5.4-mini","instructions":"be brief","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],"stream":true,"store":false,"reasoning":{"effort":"medium"},"text":{"verbosity":"low"},"service_tier":"priority"}`,
		},
		{
			name:         "backend codex compact",
			method:       http.MethodPost,
			clientPath:   "/backend-api/codex/responses/compact",
			upstreamPath: "/codex/responses/compact",
			body:         `{"model":"gpt-5.4-mini","instructions":"summarize","input":"hello","store":false,"temperature":0.3,"max_output_tokens":128}`,
			wantJSON:     `{"model":"gpt-5.4-mini","instructions":"summarize","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]}`,
		},
		{
			name:         "backend codex models",
			method:       http.MethodGet,
			clientPath:   "/backend-api/codex/models?client_version=" + CodexClientVersion,
			upstreamPath: "/codex/models",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var upstreamBody []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, tc.upstreamPath, r.URL.Path)
				_, expectedQuery, _ := strings.Cut(tc.clientPath, "?")
				assert.Equal(t, expectedQuery, r.URL.RawQuery)
				assert.Equal(t, "Bearer oauth-access", r.Header.Get("Authorization"))
				assert.Equal(t, chatGPTAccountID, r.Header.Get("chatgpt-account-id"))
				assert.Equal(t, CodexCLIUserAgent, r.Header.Get("User-Agent"))
				var err error
				upstreamBody, err = io.ReadAll(r.Body)
				require.NoError(t, err)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			t.Cleanup(srv.Close)

			c := NewClient(30 * time.Second)
			c.codexBackendBaseURL = srv.URL
			orig := httptest.NewRequest(tc.method, "http://router"+tc.clientPath, strings.NewReader(tc.body))
			resp, capture, err := c.ForwardGatewayRequestWithCapture(
				context.Background(),
				oauthAccount(chatGPTAccountID),
				[]byte("oauth-access"),
				orig,
				GatewayRoute{OAuthUpstreamPath: tc.upstreamPath, OAuthBehavior: GatewayOAuthBehaviorCodexMapping},
			)
			require.NoError(t, err)
			require.NotNil(t, resp)
			t.Cleanup(func() { _ = resp.Body.Close() })
			if tc.wantJSON == "" {
				assert.Equal(t, []byte(tc.body), upstreamBody)
				assert.Equal(t, []byte(tc.body), capture.UpstreamRequestBody)
				return
			}
			assert.JSONEq(t, tc.wantJSON, string(upstreamBody))
			assert.JSONEq(t, tc.wantJSON, string(capture.UpstreamRequestBody))
			assert.NotContains(t, string(upstreamBody), "temperature")
			assert.NotContains(t, string(upstreamBody), "max_output_tokens")
			assert.NotContains(t, string(upstreamBody), "prompt_cache_retention")
			assert.NotContains(t, string(upstreamBody), "safety_identifier")
		})
	}
}

func TestForwardGatewayRequest_OAuthNativeCodexResponsesDoesNotCollectSSE(t *testing.T) {
	t.Parallel()

	const sseBody = "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\n"
	var upstreamBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/codex/responses", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&upstreamBody))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sseBody))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	orig := httptest.NewRequest(http.MethodPost, "http://router/backend-api/codex/responses", strings.NewReader(`{"model":"gpt-5.4-mini","instructions":"","input":"hello","stream":false}`))
	resp, capture, err := c.ForwardGatewayRequestWithCapture(
		context.Background(),
		oauthAccount("chatgpt-account-123"),
		[]byte("oauth-access"),
		orig,
		GatewayRoute{OAuthUpstreamPath: "/codex/responses", OAuthBehavior: GatewayOAuthBehaviorCodexMapping},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })

	assert.Equal(t, true, upstreamBody["stream"])
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, sseBody, string(data))
	assert.Empty(t, capture.UpstreamResponseBody)
}

func TestForwardGatewayRequest_OAuthModelsFacadeUsesCodexModels(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/codex/models", r.URL.Path)
		assert.Equal(t, "Bearer oauth-access", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"models": [
				{
					"slug": "gpt-5.4-codex",
					"display_name": "GPT 5.4 Codex",
					"description": "Codex model",
					"context_window": 200000,
					"input_modalities": ["text"],
					"owned_by": "codex-real-owner",
					"created": 1710000000
				}
			]
		}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	orig := httptest.NewRequest(http.MethodGet, "http://router/v1/models", nil)
	resp, _, err := c.ForwardGatewayRequestWithCapture(
		context.Background(),
		oauthAccount("acct-1"),
		[]byte("oauth-access"),
		orig,
		GatewayRoute{OAuthUpstreamPath: "/codex/models", OAuthBehavior: GatewayOAuthBehaviorModelsFacade},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "list", body["object"])
	data, ok := body["data"].([]any)
	require.True(t, ok)
	require.Len(t, data, 1)
	item, ok := data[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "gpt-5.4-codex", item["id"])
	assert.Equal(t, "model", item["object"])
	assert.Equal(t, "codex-real-owner", item["owned_by"])
	assert.Equal(t, float64(1710000000), item["created"])
	metadata, ok := item["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "GPT 5.4 Codex", metadata["display_name"])
	assert.Equal(t, float64(200000), metadata["context_window"])
}

func TestForwardGatewayRequest_OAuthTranscribePreservesMultipartAndDisablesCapture(t *testing.T) {
	t.Parallel()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	filePart, err := writer.CreateFormFile("file", "sample.wav")
	require.NoError(t, err)
	_, err = filePart.Write([]byte("RIFF-audio-bytes"))
	require.NoError(t, err)
	require.NoError(t, writer.WriteField("prompt", "meeting notes"))
	require.NoError(t, writer.Close())

	var upstreamContentType string
	var upstreamHeaders http.Header
	var upstreamBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/transcribe", r.URL.Path)
		upstreamContentType = r.Header.Get("Content-Type")
		upstreamHeaders = r.Header.Clone()
		var err error
		upstreamBody, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hello"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	orig := httptest.NewRequest(http.MethodPost, "http://router/backend-api/transcribe", bytes.NewReader(body.Bytes()))
	orig.Header.Set("Content-Type", writer.FormDataContentType())
	orig.Header.Set("Accept", "application/json")
	orig.Header.Set("X-Request-Id", "client-request-id")
	orig.Header.Set("chatgpt-account-id", "client-controlled-account")
	orig.Header.Set("X-Forwarded-For", "203.0.113.10")
	orig.Header.Set("X-OpenAI-Client", "codex")
	orig.Header.Set("X-Codex-Trace", "trace-1")
	resp, capture, err := c.ForwardGatewayRequestWithCapture(
		context.Background(),
		oauthAccount("acct-1"),
		[]byte("oauth-access"),
		orig,
		GatewayRoute{
			OAuthUpstreamPath: "/transcribe",
			OAuthBehavior:     GatewayOAuthBehaviorCodexMapping,
			BodyPolicy:        GatewayBodyPolicyCaptureDisabled,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })

	assert.True(t, strings.HasPrefix(upstreamContentType, "multipart/form-data; boundary="))
	assert.Contains(t, string(upstreamBody), "RIFF-audio-bytes")
	assert.Equal(t, "Bearer oauth-access", upstreamHeaders.Get("Authorization"))
	assert.Equal(t, CodexCLIUserAgent, upstreamHeaders.Get("User-Agent"))
	assert.Equal(t, "acct-1", upstreamHeaders.Get("chatgpt-account-id"))
	assert.Empty(t, upstreamHeaders.Get("Accept"))
	assert.Empty(t, upstreamHeaders.Get("X-Request-Id"))
	assert.Empty(t, upstreamHeaders.Get("X-Forwarded-For"))
	assert.Equal(t, "codex", upstreamHeaders.Get("X-OpenAI-Client"))
	assert.Equal(t, "trace-1", upstreamHeaders.Get("X-Codex-Trace"))
	assert.Empty(t, capture.UpstreamRequestBody)
}

func TestForwardGatewayRequest_OAuthDropsClientChatGPTAccountID(t *testing.T) {
	t.Parallel()

	var upstreamHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(30 * time.Second)
	c.codexBackendBaseURL = srv.URL
	orig := httptest.NewRequest(http.MethodPost, "http://router/backend-api/codex/responses", strings.NewReader(`{"model":"gpt","input":"hi"}`))
	orig.Header.Set("chatgpt-account-id", "client-controlled-account")
	resp, _, err := c.ForwardGatewayRequestWithCapture(
		context.Background(),
		domain.UpstreamAccount{
			ID:         20,
			Provider:   domain.ProviderOpenAI,
			AuthMethod: domain.AuthMethodOAuthBrowser,
		},
		[]byte("oauth-access"),
		orig,
		GatewayRoute{OAuthUpstreamPath: "/codex/responses", OAuthBehavior: GatewayOAuthBehaviorCodexMapping},
	)
	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assert.Empty(t, upstreamHeaders.Get("chatgpt-account-id"))
}

func oauthAccount(chatGPTAccountID string) domain.UpstreamAccount {
	return domain.UpstreamAccount{
		ID:               20,
		Provider:         domain.ProviderOpenAI,
		AuthMethod:       domain.AuthMethodOAuthBrowser,
		ChatGPTAccountID: &chatGPTAccountID,
	}
}
