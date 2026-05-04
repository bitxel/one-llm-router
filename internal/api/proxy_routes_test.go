package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/provider/openai"
)

func TestGatewayRouteClassifier_SupportedOperations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		method         string
		path           string
		websocket      bool
		pattern        string
		responseMode   string
		bodyPolicy     gatewayBodyPolicy
		opID           openai.OpID
		apiKeyEligible bool
		apiKeyKind     gatewayRouteKind
		oauthEligible  bool
		oauthKind      gatewayRouteKind
		oauthUpstream  string
	}{
		{
			name:           "responses create supports api key direct and oauth codex",
			method:         http.MethodPost,
			path:           "/v1/responses",
			pattern:        "/v1/responses",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIResponsesCreate,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
			oauthEligible:  true,
			oauthKind:      gatewayRouteKindCodexMapping,
			oauthUpstream:  "/codex/responses",
		},
		{
			name:           "responses websocket is oauth codex only",
			method:         http.MethodGet,
			path:           "/v1/responses",
			websocket:      true,
			pattern:        "/v1/responses",
			responseMode:   domain.ResponseModeWebSocket,
			bodyPolicy:     gatewayBodyPolicyWebSocketNoBody,
			opID:           openai.OpOpenAIResponsesWebSocket,
			apiKeyEligible: false,
			oauthEligible:  true,
			oauthKind:      gatewayRouteKindCodexMapping,
			oauthUpstream:  "/codex/responses",
		},
		{
			name:           "responses retrieve is api key only",
			method:         http.MethodGet,
			path:           "/v1/responses/resp_123",
			pattern:        "/v1/responses/{response_id}",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIResponsesRetrieve,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
		},
		{
			name:           "responses delete is api key only",
			method:         http.MethodDelete,
			path:           "/v1/responses/resp_123",
			pattern:        "/v1/responses/{response_id}",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIResponsesDelete,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
		},
		{
			name:           "responses cancel is api key only",
			method:         http.MethodPost,
			path:           "/v1/responses/resp_123/cancel",
			pattern:        "/v1/responses/{response_id}/cancel",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIResponsesCancel,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
		},
		{
			name:           "responses input items list is api key only",
			method:         http.MethodGet,
			path:           "/v1/responses/resp_123/input_items",
			pattern:        "/v1/responses/{response_id}/input_items",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIResponsesInputItemsList,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
		},
		{
			name:           "responses input tokens is api key only",
			method:         http.MethodPost,
			path:           "/v1/responses/input_tokens",
			pattern:        "/v1/responses/input_tokens",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIResponsesInputTokens,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
		},
		{
			name:           "responses compact supports api key passthrough and oauth codex",
			method:         http.MethodPost,
			path:           "/v1/responses/compact",
			pattern:        "/v1/responses/compact",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIResponsesCompact,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
			oauthEligible:  true,
			oauthKind:      gatewayRouteKindCodexMapping,
			oauthUpstream:  "/codex/responses/compact",
		},
		{
			name:           "conversation create is api key only",
			method:         http.MethodPost,
			path:           "/v1/conversations",
			pattern:        "/v1/conversations",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIConversationsCreate,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
		},
		{
			name:           "conversation retrieve is api key only",
			method:         http.MethodGet,
			path:           "/v1/conversations/conv_123",
			pattern:        "/v1/conversations/{conversation_id}",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIConversationsRetrieve,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
		},
		{
			name:           "conversation update is api key only",
			method:         http.MethodPost,
			path:           "/v1/conversations/conv_123",
			pattern:        "/v1/conversations/{conversation_id}",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIConversationsUpdate,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
		},
		{
			name:           "conversation delete is api key only",
			method:         http.MethodDelete,
			path:           "/v1/conversations/conv_123",
			pattern:        "/v1/conversations/{conversation_id}",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIConversationsDelete,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
		},
		{
			name:           "conversation item create is api key only",
			method:         http.MethodPost,
			path:           "/v1/conversations/conv_123/items",
			pattern:        "/v1/conversations/{conversation_id}/items",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIConversationsItemsCreate,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
		},
		{
			name:           "conversation item list is api key only",
			method:         http.MethodGet,
			path:           "/v1/conversations/conv_123/items",
			pattern:        "/v1/conversations/{conversation_id}/items",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIConversationsItemsList,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
		},
		{
			name:           "conversation item retrieve is api key only",
			method:         http.MethodGet,
			path:           "/v1/conversations/conv_123/items/item_123",
			pattern:        "/v1/conversations/{conversation_id}/items/{item_id}",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIConversationsItemsRetrieve,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
		},
		{
			name:           "conversation item delete is api key only",
			method:         http.MethodDelete,
			path:           "/v1/conversations/conv_123/items/item_123",
			pattern:        "/v1/conversations/{conversation_id}/items/{item_id}",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIConversationsItemsDelete,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
		},
		{
			name:           "chat completions create supports api key direct and oauth adapter",
			method:         http.MethodPost,
			path:           "/v1/chat/completions",
			pattern:        "/v1/chat/completions",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIChatCompletionsCreate,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
			oauthEligible:  true,
			oauthKind:      gatewayRouteKindChatAdapter,
			oauthUpstream:  "/codex/responses",
		},
		{
			name:           "stored chat completions list is api key only",
			method:         http.MethodGet,
			path:           "/v1/chat/completions",
			pattern:        "/v1/chat/completions",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIChatCompletionsList,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
		},
		{
			name:           "stored chat completion retrieve is api key only",
			method:         http.MethodGet,
			path:           "/v1/chat/completions/chatcmpl_123",
			pattern:        "/v1/chat/completions/{completion_id}",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIChatCompletionsRetrieve,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
		},
		{
			name:           "stored chat completion update is api key only",
			method:         http.MethodPost,
			path:           "/v1/chat/completions/chatcmpl_123",
			pattern:        "/v1/chat/completions/{completion_id}",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIChatCompletionsUpdate,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
		},
		{
			name:           "stored chat completion delete is api key only",
			method:         http.MethodDelete,
			path:           "/v1/chat/completions/chatcmpl_123",
			pattern:        "/v1/chat/completions/{completion_id}",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIChatCompletionsDelete,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
		},
		{
			name:           "stored chat completion messages is api key only",
			method:         http.MethodGet,
			path:           "/v1/chat/completions/chatcmpl_123/messages",
			pattern:        "/v1/chat/completions/{completion_id}/messages",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIChatCompletionsMessagesList,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
		},
		{
			name:           "models list supports api key and oauth facade",
			method:         http.MethodGet,
			path:           "/v1/models",
			pattern:        "/v1/models",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIModelsList,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
			oauthEligible:  true,
			oauthKind:      gatewayRouteKindModelsFacade,
			oauthUpstream:  "/codex/models",
		},
		{
			name:           "model retrieve is api key only",
			method:         http.MethodGet,
			path:           "/v1/models/gpt-4.1",
			pattern:        "/v1/models/{model}",
			responseMode:   domain.ResponseModeJSON,
			bodyPolicy:     gatewayBodyPolicyJSONCaptureAllowed,
			opID:           openai.OpOpenAIModelsRetrieve,
			apiKeyEligible: true,
			apiKeyKind:     gatewayRouteKindPlatformDirect,
		},
		{
			name:          "backend codex responses is oauth only",
			method:        http.MethodPost,
			path:          "/backend-api/codex/responses",
			pattern:       "/backend-api/codex/responses",
			responseMode:  domain.ResponseModeJSON,
			bodyPolicy:    gatewayBodyPolicyJSONCaptureAllowed,
			opID:          openai.OpCodexNativeResponsesCreate,
			oauthEligible: true,
			oauthKind:     gatewayRouteKindCodexMapping,
			oauthUpstream: "/codex/responses",
		},
		{
			name:          "backend codex websocket is oauth only",
			method:        http.MethodGet,
			path:          "/backend-api/codex/responses",
			websocket:     true,
			pattern:       "/backend-api/codex/responses",
			responseMode:  domain.ResponseModeWebSocket,
			bodyPolicy:    gatewayBodyPolicyWebSocketNoBody,
			opID:          openai.OpCodexNativeResponsesWebSocket,
			oauthEligible: true,
			oauthKind:     gatewayRouteKindCodexMapping,
			oauthUpstream: "/codex/responses",
		},
		{
			name:          "backend codex compact is oauth only",
			method:        http.MethodPost,
			path:          "/backend-api/codex/responses/compact",
			pattern:       "/backend-api/codex/responses/compact",
			responseMode:  domain.ResponseModeJSON,
			bodyPolicy:    gatewayBodyPolicyJSONCaptureAllowed,
			opID:          openai.OpCodexNativeResponsesCompact,
			oauthEligible: true,
			oauthKind:     gatewayRouteKindCodexMapping,
			oauthUpstream: "/codex/responses/compact",
		},
		{
			name:          "backend codex models is oauth only",
			method:        http.MethodGet,
			path:          "/backend-api/codex/models",
			pattern:       "/backend-api/codex/models",
			responseMode:  domain.ResponseModeJSON,
			bodyPolicy:    gatewayBodyPolicyJSONCaptureAllowed,
			opID:          openai.OpCodexNativeModelsList,
			oauthEligible: true,
			oauthKind:     gatewayRouteKindCodexMapping,
			oauthUpstream: "/codex/models",
		},
		{
			name:          "backend transcribe is oauth only capture disabled",
			method:        http.MethodPost,
			path:          "/backend-api/transcribe",
			pattern:       "/backend-api/transcribe",
			responseMode:  domain.ResponseModeJSON,
			bodyPolicy:    gatewayBodyPolicyCaptureDisabled,
			opID:          openai.OpCodexNativeTranscribe,
			oauthEligible: true,
			oauthKind:     gatewayRouteKindCodexMapping,
			oauthUpstream: "/transcribe",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			route := classifyGatewayRoute(tc.method, tc.path, tc.websocket)

			require.Equal(t, gatewayRouteKindSupported, route.Kind, "overall route kind")
			assert.Equal(t, tc.pattern, route.Pattern)
			assert.Equal(t, tc.opID, route.OpID)
			assert.Equal(t, tc.responseMode, route.ResponseMode)
			assert.Equal(t, tc.bodyPolicy, route.BodyPolicy)

			assert.Equal(t, tc.apiKeyEligible, route.APIKey.Eligible, "api key eligibility")
			assert.Equal(t, tc.apiKeyKind, route.APIKey.Kind, "api key kind")
			assert.Equal(t, tc.oauthEligible, route.OAuth.Eligible, "oauth eligibility")
			assert.Equal(t, tc.oauthKind, route.OAuth.Kind, "oauth kind")
			assert.Equal(t, tc.oauthUpstream, route.OAuth.UpstreamPath, "oauth upstream path")
		})
	}
}

func TestGatewayRouteClassifier_DeferredBlockedAndUnsupportedOperations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     string
		path       string
		websocket  bool
		kind       gatewayRouteKind
		statusCode int
		errorCode  string
		pattern    string
	}{
		{
			name:       "audio transcription remains unsupported",
			method:     http.MethodPost,
			path:       "/v1/audio/transcriptions",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/v1/audio/*",
		},
		{
			name:       "embeddings are deferred",
			method:     http.MethodPost,
			path:       "/v1/embeddings",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/v1/embeddings",
		},
		{
			name:       "moderations are deferred",
			method:     http.MethodPost,
			path:       "/v1/moderations",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/v1/moderations",
		},
		{
			name:       "images are deferred",
			method:     http.MethodPost,
			path:       "/v1/images/generations",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/v1/images/*",
		},
		{
			name:       "videos are deferred",
			method:     http.MethodPost,
			path:       "/v1/videos",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/v1/videos*",
		},
		{
			name:       "files are deferred",
			method:     http.MethodPost,
			path:       "/v1/files",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/v1/files*",
		},
		{
			name:       "uploads are deferred",
			method:     http.MethodPost,
			path:       "/v1/uploads",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/v1/uploads*",
		},
		{
			name:       "vector stores are deferred",
			method:     http.MethodPost,
			path:       "/v1/vector_stores",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/v1/vector_stores*",
		},
		{
			name:       "batches are deferred",
			method:     http.MethodPost,
			path:       "/v1/batches",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/v1/batches*",
		},
		{
			name:       "fine tuning is deferred",
			method:     http.MethodPost,
			path:       "/v1/fine_tuning/jobs",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/v1/fine_tuning*",
		},
		{
			name:       "evals are deferred",
			method:     http.MethodPost,
			path:       "/v1/evals",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/v1/evals*",
		},
		{
			name:       "realtime websocket is deferred",
			method:     http.MethodGet,
			path:       "/v1/realtime",
			websocket:  true,
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/v1/realtime*",
		},
		{
			name:       "assistants are deferred",
			method:     http.MethodGet,
			path:       "/v1/assistants",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/v1/assistants*",
		},
		{
			name:       "threads are deferred",
			method:     http.MethodPost,
			path:       "/v1/threads",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/v1/threads*",
		},
		{
			name:       "chatkit is deferred",
			method:     http.MethodPost,
			path:       "/v1/chatkit/sessions",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/v1/chatkit*",
		},
		{
			name:       "containers are deferred",
			method:     http.MethodPost,
			path:       "/v1/containers",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/v1/containers*",
		},
		{
			name:       "skills are deferred",
			method:     http.MethodPost,
			path:       "/v1/skills",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/v1/skills*",
		},
		{
			name:       "model delete is deferred",
			method:     http.MethodDelete,
			path:       "/v1/models/gpt-4.1",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/v1/models/{model}",
		},
		{
			name:       "organization admin is blocked",
			method:     http.MethodGet,
			path:       "/v1/organization/usage/completions",
			kind:       gatewayRouteKindBlocked,
			statusCode: http.StatusForbidden,
			errorCode:  ErrCodeBlockedEndpoint,
			pattern:    "/v1/organization*",
		},
		{
			name:       "v1 usage local alias removed",
			method:     http.MethodGet,
			path:       "/v1/usage",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/v1/*",
		},
		{
			name:       "project admin is blocked",
			method:     http.MethodPost,
			path:       "/v1/projects/proj_123/api_keys",
			kind:       gatewayRouteKindBlocked,
			statusCode: http.StatusForbidden,
			errorCode:  ErrCodeBlockedEndpoint,
			pattern:    "/v1/projects/*",
		},
		{
			name:       "bare backend api is unsupported after setup",
			method:     http.MethodGet,
			path:       "/backend-api",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/backend-api",
		},
		{
			name:       "arbitrary backend api is unsupported",
			method:     http.MethodGet,
			path:       "/backend-api/accounts/check/v4-2023-04-27",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/backend-api/*",
		},
		{
			name:       "bare api codex is not a data-plane route",
			method:     http.MethodGet,
			path:       "/api/codex",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/api/codex",
		},
		{
			name:       "arbitrary api codex is not a data-plane route",
			method:     http.MethodGet,
			path:       "/api/codex/unknown",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/api/codex/unknown",
		},
		{
			name:       "api codex usage local alias removed",
			method:     http.MethodGet,
			path:       "/api/codex/usage",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/api/codex/usage",
		},
		{
			name:       "api codex usage trailing slash local alias removed",
			method:     http.MethodGet,
			path:       "/api/codex/usage/",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/api/codex/usage/",
		},
		{
			name:       "unknown v1 path is unsupported",
			method:     http.MethodPost,
			path:       "/v1/unknown",
			kind:       gatewayRouteKindUnsupported,
			statusCode: http.StatusNotFound,
			errorCode:  ErrCodeUnsupportedEndpoint,
			pattern:    "/v1/*",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			route := classifyGatewayRoute(tc.method, tc.path, tc.websocket)

			assert.Equal(t, tc.kind, route.Kind)
			assert.Equal(t, tc.statusCode, route.StatusCode)
			assert.Equal(t, tc.errorCode, route.ErrorCode)
			assert.Equal(t, tc.pattern, route.Pattern)
			assert.Empty(t, route.OpID)
			assert.False(t, route.APIKey.Eligible)
			assert.False(t, route.OAuth.Eligible)
			assert.Equal(t, gatewayBodyPolicyCaptureDisabled, route.BodyPolicy)
		})
	}
}

func TestGatewayRouteBridgeSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     string
		path       string
		websocket  bool
		credential openai.CredentialClass
		wantBridge openai.BridgeID
		wantOK     bool
	}{
		{
			name:       "api key chat completions create uses direct bridge",
			method:     http.MethodPost,
			path:       "/v1/chat/completions",
			credential: openai.CredentialClassAPIKey,
			wantBridge: openai.BridgeOpenAIChatCompletionsDirect,
			wantOK:     true,
		},
		{
			name:       "oauth chat completions create uses codex facade bridge",
			method:     http.MethodPost,
			path:       "/v1/chat/completions",
			credential: openai.CredentialClassOAuth,
			wantBridge: openai.BridgeOpenAIChatCompletionsToCodex,
			wantOK:     true,
		},
		{
			name:       "oauth backend codex responses uses native direct bridge",
			method:     http.MethodPost,
			path:       "/backend-api/codex/responses",
			credential: openai.CredentialClassOAuth,
			wantBridge: openai.BridgeCodexNativeResponsesDirect,
			wantOK:     true,
		},
		{
			name:       "api key v1 responses websocket has no bridge",
			method:     http.MethodGet,
			path:       "/v1/responses",
			websocket:  true,
			credential: openai.CredentialClassAPIKey,
		},
		{
			name:       "oauth conversations create has no bridge",
			method:     http.MethodPost,
			path:       "/v1/conversations",
			credential: openai.CredentialClassOAuth,
		},
		{
			name:       "admin usage route has no data-plane bridge",
			method:     http.MethodGet,
			path:       "/api/admin/usage",
			credential: openai.CredentialClassAPIKey,
		},
		{
			name:       "api codex usage alias has no data-plane bridge",
			method:     http.MethodGet,
			path:       "/api/codex/usage",
			credential: openai.CredentialClassOAuth,
		},
		{
			name:       "api codex usage trailing slash alias has no data-plane bridge",
			method:     http.MethodGet,
			path:       "/api/codex/usage/",
			credential: openai.CredentialClassOAuth,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			route := classifyGatewayRoute(tc.method, tc.path, tc.websocket)
			bridge, ok := selectGatewayBridge(route, tc.credential)

			assert.Equal(t, tc.wantOK, ok)
			if !tc.wantOK {
				assert.Nil(t, bridge)
				return
			}
			require.NotNil(t, bridge)
			assert.Equal(t, tc.wantBridge, bridge.ID())
			assert.Equal(t, route.OpID, bridge.OpID())
		})
	}
}
