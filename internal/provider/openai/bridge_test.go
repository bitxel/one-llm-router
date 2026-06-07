package openai

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/domain"
)

func TestBridgeRegistryResolvesCredentialSpecificBridge(t *testing.T) {
	t.Parallel()

	registry := DefaultBridgeRegistry()

	tests := []struct {
		name             string
		opID             OpID
		credential       CredentialClass
		bridgeID         BridgeID
		clientContract   Contract
		upstreamContract Contract
	}{
		{
			name:             "api key responses create direct",
			opID:             OpOpenAIResponsesCreate,
			credential:       CredentialClassAPIKey,
			bridgeID:         BridgeOpenAIResponsesDirect,
			clientContract:   ContractOpenAIV1Responses,
			upstreamContract: ContractOpenAIV1Responses,
		},
		{
			name:             "oauth responses create to codex",
			opID:             OpOpenAIResponsesCreate,
			credential:       CredentialClassOAuth,
			bridgeID:         BridgeOpenAIResponsesToCodex,
			clientContract:   ContractOpenAIV1Responses,
			upstreamContract: ContractChatGPTBackendAPICodexResponses,
		},
		{
			name:             "api key chat completions direct",
			opID:             OpOpenAIChatCompletionsCreate,
			credential:       CredentialClassAPIKey,
			bridgeID:         BridgeOpenAIChatCompletionsDirect,
			clientContract:   ContractOpenAIV1ChatCompletions,
			upstreamContract: ContractOpenAIV1ChatCompletions,
		},
		{
			name:             "oauth chat completions to codex",
			opID:             OpOpenAIChatCompletionsCreate,
			credential:       CredentialClassOAuth,
			bridgeID:         BridgeOpenAIChatCompletionsToCodex,
			clientContract:   ContractOpenAIV1ChatCompletions,
			upstreamContract: ContractChatGPTBackendAPICodexResponses,
		},
		{
			name:             "oauth native codex responses direct",
			opID:             OpCodexNativeResponsesCreate,
			credential:       CredentialClassOAuth,
			bridgeID:         BridgeCodexNativeResponsesDirect,
			clientContract:   ContractChatGPTBackendAPICodexResponses,
			upstreamContract: ContractChatGPTBackendAPICodexResponses,
		},
		{
			name:             "oauth native codex transcribe direct",
			opID:             OpCodexNativeTranscribe,
			credential:       CredentialClassOAuth,
			bridgeID:         BridgeCodexNativeTranscribeDirect,
			clientContract:   ContractChatGPTBackendAPITranscribe,
			upstreamContract: ContractChatGPTBackendAPITranscribe,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bridge, ok := registry.Resolve(tc.opID, tc.credential)

			require.True(t, ok)
			assert.Equal(t, tc.bridgeID, bridge.ID())
			assert.Equal(t, tc.opID, bridge.OpID())
			assert.Equal(t, tc.credential, bridge.CredentialClass())
			assert.Equal(t, tc.clientContract, bridge.ClientContract())
			assert.Equal(t, tc.upstreamContract, bridge.UpstreamContract())
		})
	}
}

func TestBridgeRegistryRejectsIneligibleOperations(t *testing.T) {
	t.Parallel()

	registry := DefaultBridgeRegistry()

	tests := []struct {
		name       string
		opID       OpID
		credential CredentialClass
	}{
		{name: "api key websocket v1 responses has no bridge", opID: OpOpenAIResponsesWebSocket, credential: CredentialClassAPIKey},
		{name: "oauth responses retrieve has no bridge", opID: OpOpenAIResponsesRetrieve, credential: CredentialClassOAuth},
		{name: "oauth conversations create has no bridge", opID: OpOpenAIConversationsCreate, credential: CredentialClassOAuth},
		{name: "api key native codex responses has no bridge", opID: OpCodexNativeResponsesCreate, credential: CredentialClassAPIKey},
		{name: "oauth model retrieve has no bridge", opID: OpOpenAIModelsRetrieve, credential: CredentialClassOAuth},
		{name: "unknown op has no bridge", opID: OpID(""), credential: CredentialClassAPIKey},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bridge, ok := registry.Resolve(tc.opID, tc.credential)

			assert.False(t, ok)
			assert.Nil(t, bridge)
		})
	}
}

func TestBridgeDecodeAndBuildExposeContractsWithoutNetwork(t *testing.T) {
	t.Parallel()

	bridge, ok := DefaultBridgeRegistry().Resolve(OpOpenAIChatCompletionsCreate, CredentialClassOAuth)
	require.True(t, ok)

	clientReq, err := bridge.DecodeClientRequest(context.Background(), DecodeInput{
		OpID:       OpOpenAIChatCompletionsCreate,
		Method:     "POST",
		Path:       "/v1/chat/completions",
		RawBody:    []byte(`{"model":"gpt-5.4-mini","messages":[{"role":"user","content":"hi"}]}`),
		RawQuery:   "debug=true",
		RequestID:  "req_test",
		Pattern:    "/v1/chat/completions",
		BodyPolicy: string(GatewayBodyPolicyJSONCaptureAllowed),
	})
	require.NoError(t, err)
	assert.Equal(t, ContractOpenAIV1ChatCompletions, clientReq.Contract())
	assert.Equal(t, []byte(`{"model":"gpt-5.4-mini","messages":[{"role":"user","content":"hi"}]}`), clientReq.RawBody)
	assert.False(t, clientReq.StreamRequested)

	upstreamReq, adapter, err := bridge.BuildUpstreamRequest(context.Background(), BuildInput{
		ClientRequest: clientReq,
		Credential:    CredentialClassOAuth,
	})
	require.NoError(t, err)

	assert.Equal(t, ContractChatGPTBackendAPICodexResponses, upstreamReq.Contract())
	assert.Equal(t, BridgeOpenAIChatCompletionsToCodex, upstreamReq.BridgeID)
	require.NotNil(t, adapter)
	assert.Equal(t, ContractOpenAIV1ChatCompletions, adapter.ClientContract())
}

func TestBridgeDecodeClientRequestPopulatesStreamIntent(t *testing.T) {
	t.Parallel()

	bridge, ok := DefaultBridgeRegistry().Resolve(OpOpenAIResponsesCreate, CredentialClassAPIKey)
	require.True(t, ok)

	clientReq, err := bridge.DecodeClientRequest(context.Background(), DecodeInput{
		OpID:    OpOpenAIResponsesCreate,
		Method:  http.MethodPost,
		Path:    "/v1/responses",
		RawBody: []byte(`{"model":"gpt-5.4-mini","input":"hi","stream":true}`),
	})
	require.NoError(t, err)
	assert.True(t, clientReq.StreamRequested)

	wsBridge, ok := DefaultBridgeRegistry().Resolve(OpCodexNativeResponsesWebSocket, CredentialClassOAuth)
	require.True(t, ok)
	wsReq, err := wsBridge.DecodeClientRequest(context.Background(), DecodeInput{
		OpID:      OpCodexNativeResponsesWebSocket,
		Method:    http.MethodGet,
		Path:      "/backend-api/codex/responses",
		WebSocket: true,
	})
	require.NoError(t, err)
	assert.True(t, wsReq.StreamRequested)
}

func TestBridgeBuildUpstreamRequestRejectsCredentialMismatch(t *testing.T) {
	t.Parallel()

	bridge, ok := DefaultBridgeRegistry().Resolve(OpOpenAIResponsesCreate, CredentialClassOAuth)
	require.True(t, ok)

	clientReq, err := bridge.DecodeClientRequest(context.Background(), DecodeInput{
		OpID:    OpOpenAIResponsesCreate,
		Method:  http.MethodPost,
		Path:    "/v1/responses",
		RawBody: []byte(`{"model":"gpt-5.4-mini","input":"hi","store":false}`),
	})
	require.NoError(t, err)

	_, _, err = bridge.BuildUpstreamRequest(context.Background(), BuildInput{
		ClientRequest: clientReq,
		Credential:    CredentialClassAPIKey,
	})
	require.ErrorIs(t, err, ErrInvalidUpstreamRequest)
}

func TestDirectBridgeBuildUpstreamRequestPreservesRawRequest(t *testing.T) {
	t.Parallel()

	bridge, ok := DefaultBridgeRegistry().Resolve(OpOpenAIChatCompletionsCreate, CredentialClassAPIKey)
	require.True(t, ok)

	headers := http.Header{}
	headers.Set("Authorization", "Bearer client-secret")
	headers.Set("Cookie", "admin_session=secret")
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept-Encoding", "gzip")
	clientReq, err := bridge.DecodeClientRequest(context.Background(), DecodeInput{
		OpID:      OpOpenAIChatCompletionsCreate,
		Method:    http.MethodPost,
		Path:      "/v1/chat/completions",
		RawQuery:  "trace=true",
		RawBody:   []byte(`{"model":"gpt-5.4-mini","messages":[]}`),
		Headers:   headers,
		RequestID: "req_direct",
	})
	require.NoError(t, err)

	upstreamReq, adapter, err := bridge.BuildUpstreamRequest(context.Background(), BuildInput{
		ClientRequest:   clientReq,
		Credential:      CredentialClassAPIKey,
		CredentialValue: "sk-direct",
		UpstreamBaseURL: "https://api.example.test/root/",
	})
	require.NoError(t, err)

	assert.Equal(t, "https://api.example.test/root/chat/completions?trace=true", upstreamReq.URL)
	assert.Equal(t, http.MethodPost, upstreamReq.Method)
	assert.Equal(t, []byte(`{"model":"gpt-5.4-mini","messages":[]}`), upstreamReq.RawBody)
	assert.Equal(t, "Bearer sk-direct", upstreamReq.Headers.Get("Authorization"))
	assert.Equal(t, "application/json", upstreamReq.Headers.Get("Content-Type"))
	assert.Equal(t, "gzip", upstreamReq.Headers.Get("Accept-Encoding"))
	assert.Empty(t, upstreamReq.Headers.Get("Cookie"))
	require.NotNil(t, adapter)
	assert.Equal(t, ClientResponseAdapterPassthrough, adapter.Kind())
}

func TestCodexBridgeBuildUpstreamRequestNormalizesResponses(t *testing.T) {
	t.Parallel()

	bridge, ok := DefaultBridgeRegistry().Resolve(OpOpenAIResponsesCreate, CredentialClassOAuth)
	require.True(t, ok)

	clientReq, err := bridge.DecodeClientRequest(context.Background(), DecodeInput{
		OpID:     OpOpenAIResponsesCreate,
		Method:   http.MethodPost,
		Path:     "/v1/responses",
		RawQuery: "trace=true",
		RawBody:  []byte(`{"model":"gpt-5.4-mini","input":"hello","store":false,"stream":false,"temperature":0.8,"max_output_tokens":128}`),
	})
	require.NoError(t, err)

	upstreamReq, adapter, err := bridge.BuildUpstreamRequest(context.Background(), BuildInput{
		ClientRequest:   clientReq,
		Credential:      CredentialClassOAuth,
		CredentialValue: "oauth-access",
		AccountMetadata: domain.JSONMap{"chatgpt_account_id": "acct-123"},
		UpstreamBaseURL: "https://chatgpt.example.test/backend-api",
	})
	require.NoError(t, err)

	assert.Equal(t, "https://chatgpt.example.test/backend-api/codex/responses?trace=true", upstreamReq.URL)
	assert.Equal(t, "Bearer oauth-access", upstreamReq.Headers.Get("Authorization"))
	assert.Equal(t, CodexCLIUserAgent, upstreamReq.Headers.Get("User-Agent"))
	assert.Equal(t, "application/json", upstreamReq.Headers.Get("Content-Type"))
	assert.Equal(t, "text/event-stream", upstreamReq.Headers.Get("Accept"))
	assert.Equal(t, "identity", upstreamReq.Headers.Get("Accept-Encoding"))
	assert.Equal(t, "acct-123", upstreamReq.Headers.Get("chatgpt-account-id"))
	assert.JSONEq(t, `{"model":"gpt-5.4-mini","instructions":"","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],"store":false,"stream":true}`, string(upstreamReq.RawBody))
	assert.NotContains(t, string(upstreamReq.RawBody), "temperature")
	assert.NotContains(t, string(upstreamReq.RawBody), "max_output_tokens")
	require.NotNil(t, adapter)
	assert.Equal(t, ClientResponseAdapterOpenAIResponsesJSON, adapter.Kind())
}

func TestCodexNativeBridgeBuildUpstreamRequestKeepsNativeContract(t *testing.T) {
	t.Parallel()

	bridge, ok := DefaultBridgeRegistry().Resolve(OpCodexNativeResponsesCreate, CredentialClassOAuth)
	require.True(t, ok)

	clientReq, err := bridge.DecodeClientRequest(context.Background(), DecodeInput{
		OpID:    OpCodexNativeResponsesCreate,
		Method:  http.MethodPost,
		Path:    "/backend-api/codex/responses",
		RawBody: []byte(`{"model":"gpt-5.4-mini","input":"hello","stream":false,"temperature":0.8}`),
	})
	require.NoError(t, err)

	upstreamReq, adapter, err := bridge.BuildUpstreamRequest(context.Background(), BuildInput{
		ClientRequest:   clientReq,
		Credential:      CredentialClassOAuth,
		CredentialValue: "oauth-access",
		UpstreamBaseURL: "https://chatgpt.example.test/backend-api",
	})
	require.NoError(t, err)

	assert.Equal(t, ContractChatGPTBackendAPICodexResponses, upstreamReq.Contract())
	assert.Equal(t, "https://chatgpt.example.test/backend-api/codex/responses", upstreamReq.URL)
	assert.JSONEq(t, `{"model":"gpt-5.4-mini","instructions":"","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],"store":false,"stream":true}`, string(upstreamReq.RawBody))
	require.NotNil(t, adapter)
	assert.Equal(t, ClientResponseAdapterCodexNative, adapter.Kind())
}

func TestCodexCompactBridgeBuildUpstreamRequestNormalizesCompact(t *testing.T) {
	t.Parallel()

	bridge, ok := DefaultBridgeRegistry().Resolve(OpOpenAIResponsesCompact, CredentialClassOAuth)
	require.True(t, ok)

	clientReq, err := bridge.DecodeClientRequest(context.Background(), DecodeInput{
		OpID:    OpOpenAIResponsesCompact,
		Method:  http.MethodPost,
		Path:    "/v1/responses/compact",
		RawBody: []byte(`{"model":"gpt-5.4-mini","input":"hello","store":false,"temperature":0.8}`),
	})
	require.NoError(t, err)

	upstreamReq, adapter, err := bridge.BuildUpstreamRequest(context.Background(), BuildInput{
		ClientRequest:   clientReq,
		Credential:      CredentialClassOAuth,
		CredentialValue: "oauth-access",
		UpstreamBaseURL: "https://chatgpt.example.test/backend-api",
	})
	require.NoError(t, err)

	assert.Equal(t, "https://chatgpt.example.test/backend-api/codex/responses/compact", upstreamReq.URL)
	assert.Equal(t, "identity", upstreamReq.Headers.Get("Accept-Encoding"))
	assert.JSONEq(t, `{"model":"gpt-5.4-mini","instructions":"","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]}`, string(upstreamReq.RawBody))
	require.NotNil(t, adapter)
	assert.Equal(t, ClientResponseAdapterOpenAIResponsesJSON, adapter.Kind())
}

func TestCodexNativeCompactBridgeBuildUpstreamRequestForcesIdentityEncoding(t *testing.T) {
	t.Parallel()

	bridge, ok := DefaultBridgeRegistry().Resolve(OpCodexNativeResponsesCompact, CredentialClassOAuth)
	require.True(t, ok)

	clientReq, err := bridge.DecodeClientRequest(context.Background(), DecodeInput{
		OpID:    OpCodexNativeResponsesCompact,
		Method:  http.MethodPost,
		Path:    "/backend-api/codex/responses/compact",
		RawBody: []byte(`{"model":"gpt-5.4-mini","input":"hello"}`),
	})
	require.NoError(t, err)

	upstreamReq, adapter, err := bridge.BuildUpstreamRequest(context.Background(), BuildInput{
		ClientRequest:   clientReq,
		Credential:      CredentialClassOAuth,
		CredentialValue: "oauth-access",
		UpstreamBaseURL: "https://chatgpt.example.test/backend-api",
	})
	require.NoError(t, err)

	assert.Equal(t, "https://chatgpt.example.test/backend-api/codex/responses/compact", upstreamReq.URL)
	assert.Equal(t, "identity", upstreamReq.Headers.Get("Accept-Encoding"))
	require.NotNil(t, adapter)
	assert.Equal(t, ClientResponseAdapterCodexNative, adapter.Kind())
}

func TestCodexModelsBridgeBuildUpstreamRequestUsesModelsFacadeAdapter(t *testing.T) {
	t.Parallel()

	bridge, ok := DefaultBridgeRegistry().Resolve(OpOpenAIModelsList, CredentialClassOAuth)
	require.True(t, ok)

	clientReq, err := bridge.DecodeClientRequest(context.Background(), DecodeInput{
		OpID:   OpOpenAIModelsList,
		Method: http.MethodGet,
		Path:   "/v1/models",
	})
	require.NoError(t, err)

	upstreamReq, adapter, err := bridge.BuildUpstreamRequest(context.Background(), BuildInput{
		ClientRequest:   clientReq,
		Credential:      CredentialClassOAuth,
		CredentialValue: "oauth-access",
		UpstreamBaseURL: "https://chatgpt.example.test/backend-api",
	})
	require.NoError(t, err)

	assert.Equal(t, ContractChatGPTBackendAPICodexModels, upstreamReq.Contract())
	assert.Equal(t, "https://chatgpt.example.test/backend-api/codex/models", upstreamReq.URL)
	assert.Empty(t, upstreamReq.RawBody)
	assert.Equal(t, "identity", upstreamReq.Headers.Get("Accept-Encoding"))
	assert.Empty(t, upstreamReq.Headers.Get("Content-Type"))
	require.NotNil(t, adapter)
	assert.Equal(t, ClientResponseAdapterOpenAIModelsList, adapter.Kind())
}

func TestChatCompletionsBridgeBuildUpstreamRequestSelectsAdapters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		body        string
		wantAdapter string
	}{
		{
			name:        "non-stream chat facade",
			body:        `{"model":"gpt-5.4-mini","messages":[{"role":"user","content":"hi"}],"stream":false}`,
			wantAdapter: ClientResponseAdapterChatCompletionsJSON,
		},
		{
			name:        "stream chat facade",
			body:        `{"model":"gpt-5.4-mini","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true}}`,
			wantAdapter: ClientResponseAdapterChatCompletionsSSE,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bridge, ok := DefaultBridgeRegistry().Resolve(OpOpenAIChatCompletionsCreate, CredentialClassOAuth)
			require.True(t, ok)
			clientReq, err := bridge.DecodeClientRequest(context.Background(), DecodeInput{
				OpID:    OpOpenAIChatCompletionsCreate,
				Method:  http.MethodPost,
				Path:    "/v1/chat/completions",
				RawBody: []byte(tc.body),
			})
			require.NoError(t, err)

			upstreamReq, adapter, err := bridge.BuildUpstreamRequest(context.Background(), BuildInput{
				ClientRequest:   clientReq,
				Credential:      CredentialClassOAuth,
				CredentialValue: "oauth-access",
				UpstreamBaseURL: "https://chatgpt.example.test/backend-api",
			})
			require.NoError(t, err)

			assert.Equal(t, "https://chatgpt.example.test/backend-api/codex/responses", upstreamReq.URL)
			assert.Equal(t, "identity", upstreamReq.Headers.Get("Accept-Encoding"))
			assert.JSONEq(t, `{"model":"gpt-5.4-mini","instructions":"","input":[{"role":"user","content":"hi"}],"store":false,"stream":true}`, string(upstreamReq.RawBody))
			require.NotNil(t, adapter)
			assert.Equal(t, tc.wantAdapter, adapter.Kind())
			assert.Equal(t, ContractOpenAIV1ChatCompletions, adapter.ClientContract())
		})
	}
}

func TestChatCompletionsBridgeIgnoresUnsupportedFields(t *testing.T) {
	t.Parallel()

	bridge, ok := DefaultBridgeRegistry().Resolve(OpOpenAIChatCompletionsCreate, CredentialClassOAuth)
	require.True(t, ok)
	clientReq, err := bridge.DecodeClientRequest(context.Background(), DecodeInput{
		OpID:    OpOpenAIChatCompletionsCreate,
		Method:  http.MethodPost,
		Path:    "/v1/chat/completions",
		RawBody: []byte(`{"model":"gpt-5.4-mini","messages":[{"role":"user","content":"hi"}],"logprobs":true}`),
	})
	require.NoError(t, err)

	upstreamReq, _, err := bridge.BuildUpstreamRequest(context.Background(), BuildInput{
		ClientRequest:   clientReq,
		Credential:      CredentialClassOAuth,
		CredentialValue: "oauth-access",
		UpstreamBaseURL: "https://chatgpt.example.test/backend-api",
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"model":"gpt-5.4-mini","instructions":"","input":[{"role":"user","content":"hi"}],"store":false,"stream":true}`, string(upstreamReq.RawBody))
}
