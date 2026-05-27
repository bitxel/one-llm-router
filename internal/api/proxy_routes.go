package api

import (
	"net/http"
	"strings"

	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/provider"
	"github.com/user/one-llm-router/internal/provider/openai"
)

type gatewayRouteKind string

const (
	gatewayRouteKindSupported      gatewayRouteKind = "supported"
	gatewayRouteKindPlatformDirect gatewayRouteKind = "platform_direct"
	gatewayRouteKindCodexMapping   gatewayRouteKind = "codex_mapping"
	gatewayRouteKindChatAdapter    gatewayRouteKind = "chat_adapter"
	gatewayRouteKindModelsFacade   gatewayRouteKind = "models_facade"
	gatewayRouteKindUnsupported    gatewayRouteKind = "unsupported"
	gatewayRouteKindBlocked        gatewayRouteKind = "blocked"
)

type gatewayBodyPolicy string

const (
	gatewayBodyPolicyJSONCaptureAllowed gatewayBodyPolicy = "json_capture_allowed"
	gatewayBodyPolicyCaptureDisabled    gatewayBodyPolicy = "capture_disabled"
	gatewayBodyPolicyWebSocketNoBody    gatewayBodyPolicy = "websocket_no_body"
)

type gatewayCredentialRoute struct {
	Eligible     bool
	Kind         gatewayRouteKind
	UpstreamPath string
}

type gatewayRoute struct {
	Kind         gatewayRouteKind
	Pattern      string
	OpID         provider.OpID
	ResponseMode string
	BodyPolicy   gatewayBodyPolicy
	APIKey       gatewayCredentialRoute
	OAuth        gatewayCredentialRoute
	StatusCode   int
	ErrorCode    string
}

var defaultGatewayBridgeRegistry = openai.DefaultBridgeRegistry()

func classifyGatewayRoute(method, rawPath string, websocket bool) gatewayRoute {
	path := normalizeGatewayPath(rawPath)

	if isBlockedGatewayPath(path) {
		return blockedGatewayRoute(path)
	}

	if route, ok := classifySupportedGatewayRoute(method, path, websocket); ok {
		return route
	}

	return unsupportedGatewayRoute(unsupportedGatewayPattern(method, path, websocket))
}

func selectGatewayBridge(route gatewayRoute, credential provider.CredentialClass) (provider.OperationBridge, bool) {
	return selectGatewayBridgeFromRegistry(defaultGatewayBridgeRegistry, route, credential)
}

func (h *ProxyHandler) selectGatewayBridge(route gatewayRoute, credential provider.CredentialClass) (provider.OperationBridge, bool) {
	if h == nil || h.bridgeRegistry == nil {
		return selectGatewayBridge(route, credential)
	}
	return selectGatewayBridgeFromRegistry(h.bridgeRegistry, route, credential)
}

func selectGatewayBridgeFromRegistry(registry *provider.BridgeRegistry, route gatewayRoute, credential provider.CredentialClass) (provider.OperationBridge, bool) {
	if route.Kind != gatewayRouteKindSupported || route.OpID == "" {
		return nil, false
	}
	switch credential {
	case provider.CredentialClassAPIKey:
		if !route.APIKey.Eligible {
			return nil, false
		}
	case provider.CredentialClassOAuth:
		if !route.OAuth.Eligible {
			return nil, false
		}
	default:
		return nil, false
	}
	return registry.Resolve(route.OpID, credential)
}

func credentialClassForAccount(account domain.UpstreamAccount) provider.CredentialClass {
	if account.IsOAuth() {
		return provider.CredentialClassOAuth
	}
	return provider.CredentialClassAPIKey
}

func classifySupportedGatewayRoute(method, path string, websocket bool) (gatewayRoute, bool) {
	switch {
	case websocket && method == http.MethodGet && path == "/v1/responses":
		return oauthCodexRoute(openai.OpOpenAIResponsesWebSocket, "/v1/responses", domain.ResponseModeWebSocket, gatewayBodyPolicyWebSocketNoBody, "/codex/responses"), true
	case websocket && method == http.MethodGet && path == "/backend-api/codex/responses":
		return oauthCodexRoute(openai.OpCodexNativeResponsesWebSocket, "/backend-api/codex/responses", domain.ResponseModeWebSocket, gatewayBodyPolicyWebSocketNoBody, "/codex/responses"), true
	case websocket:
		return gatewayRoute{}, false
	}

	switch path {
	case "/v1/responses":
		if method == http.MethodPost {
			return platformAndOAuthCodexRoute(openai.OpOpenAIResponsesCreate, path, "/codex/responses"), true
		}
	case "/v1/responses/input_tokens":
		if method == http.MethodPost {
			return platformDirectRoute(openai.OpOpenAIResponsesInputTokens, path), true
		}
	case "/v1/responses/compact":
		if method == http.MethodPost {
			return platformAndOAuthCodexRoute(openai.OpOpenAIResponsesCompact, path, "/codex/responses/compact"), true
		}
	case "/v1/conversations":
		if method == http.MethodPost {
			return platformDirectRoute(openai.OpOpenAIConversationsCreate, path), true
		}
	case "/v1/chat/completions":
		switch method {
		case http.MethodPost:
			route := platformDirectRoute(openai.OpOpenAIChatCompletionsCreate, path)
			route.OAuth = gatewayCredentialRoute{
				Eligible:     true,
				Kind:         gatewayRouteKindChatAdapter,
				UpstreamPath: "/codex/responses",
			}
			return route, true
		case http.MethodGet:
			return platformDirectRoute(openai.OpOpenAIChatCompletionsList, path), true
		}
	case "/v1/models":
		if method == http.MethodGet {
			// GET /v1/models is handled as a router-level strict union before
			// normal selected-account forwarding.
			route := platformDirectRoute(openai.OpOpenAIModelsList, path)
			route.OAuth = gatewayCredentialRoute{
				Eligible:     true,
				Kind:         gatewayRouteKindModelsFacade,
				UpstreamPath: "/codex/models",
			}
			return route, true
		}
	case "/backend-api/codex/responses":
		if method == http.MethodPost {
			return oauthCodexRoute(openai.OpCodexNativeResponsesCreate, path, domain.ResponseModeJSON, gatewayBodyPolicyJSONCaptureAllowed, "/codex/responses"), true
		}
	case "/backend-api/codex/responses/compact":
		if method == http.MethodPost {
			return oauthCodexRoute(openai.OpCodexNativeResponsesCompact, path, domain.ResponseModeJSON, gatewayBodyPolicyJSONCaptureAllowed, "/codex/responses/compact"), true
		}
	case "/backend-api/codex/models":
		if method == http.MethodGet {
			return oauthCodexRoute(openai.OpCodexNativeModelsList, path, domain.ResponseModeJSON, gatewayBodyPolicyJSONCaptureAllowed, "/codex/models"), true
		}
	case "/backend-api/transcribe":
		if method == http.MethodPost {
			return oauthCodexRoute(openai.OpCodexNativeTranscribe, path, domain.ResponseModeJSON, gatewayBodyPolicyCaptureDisabled, "/transcribe"), true
		}
	}

	if pattern, opID, ok := platformOnlyDynamicPattern(method, path); ok {
		return platformDirectRoute(opID, pattern), true
	}

	return gatewayRoute{}, false
}

func platformOnlyDynamicPattern(method, path string) (string, provider.OpID, bool) {
	segments := gatewayPathSegments(path)
	if len(segments) < 3 {
		return "", "", false
	}

	if segments[0] != "v1" {
		return "", "", false
	}

	switch segments[1] {
	case "responses":
		if len(segments) == 3 {
			switch method {
			case http.MethodGet:
				return "/v1/responses/{response_id}", openai.OpOpenAIResponsesRetrieve, true
			case http.MethodDelete:
				return "/v1/responses/{response_id}", openai.OpOpenAIResponsesDelete, true
			}
		}
		if len(segments) == 4 {
			switch {
			case method == http.MethodPost && segments[3] == "cancel":
				return "/v1/responses/{response_id}/cancel", openai.OpOpenAIResponsesCancel, true
			case method == http.MethodGet && segments[3] == "input_items":
				return "/v1/responses/{response_id}/input_items", openai.OpOpenAIResponsesInputItemsList, true
			}
		}
	case "conversations":
		if len(segments) == 3 {
			switch method {
			case http.MethodGet:
				return "/v1/conversations/{conversation_id}", openai.OpOpenAIConversationsRetrieve, true
			case http.MethodPost:
				return "/v1/conversations/{conversation_id}", openai.OpOpenAIConversationsUpdate, true
			case http.MethodDelete:
				return "/v1/conversations/{conversation_id}", openai.OpOpenAIConversationsDelete, true
			}
		}
		if len(segments) == 4 && segments[3] == "items" {
			switch method {
			case http.MethodPost:
				return "/v1/conversations/{conversation_id}/items", openai.OpOpenAIConversationsItemsCreate, true
			case http.MethodGet:
				return "/v1/conversations/{conversation_id}/items", openai.OpOpenAIConversationsItemsList, true
			}
		}
		if len(segments) == 5 && segments[3] == "items" {
			switch method {
			case http.MethodGet:
				return "/v1/conversations/{conversation_id}/items/{item_id}", openai.OpOpenAIConversationsItemsRetrieve, true
			case http.MethodDelete:
				return "/v1/conversations/{conversation_id}/items/{item_id}", openai.OpOpenAIConversationsItemsDelete, true
			}
		}
	case "chat":
		if len(segments) >= 3 && segments[2] == "completions" {
			if len(segments) == 4 {
				switch method {
				case http.MethodGet:
					return "/v1/chat/completions/{completion_id}", openai.OpOpenAIChatCompletionsRetrieve, true
				case http.MethodPost:
					return "/v1/chat/completions/{completion_id}", openai.OpOpenAIChatCompletionsUpdate, true
				case http.MethodDelete:
					return "/v1/chat/completions/{completion_id}", openai.OpOpenAIChatCompletionsDelete, true
				}
			}
			if len(segments) == 5 && method == http.MethodGet && segments[4] == "messages" {
				return "/v1/chat/completions/{completion_id}/messages", openai.OpOpenAIChatCompletionsMessagesList, true
			}
		}
	case "models":
		if len(segments) == 3 && method == http.MethodGet {
			return "/v1/models/{model}", openai.OpOpenAIModelsRetrieve, true
		}
	}

	return "", "", false
}

func platformDirectRoute(opID provider.OpID, pattern string) gatewayRoute {
	route := supportedGatewayRoute(opID, pattern, domain.ResponseModeJSON, gatewayBodyPolicyJSONCaptureAllowed)
	route.APIKey = gatewayCredentialRoute{
		Eligible:     true,
		Kind:         gatewayRouteKindPlatformDirect,
		UpstreamPath: pattern,
	}
	return route
}

func platformAndOAuthCodexRoute(opID provider.OpID, pattern, oauthUpstreamPath string) gatewayRoute {
	route := platformDirectRoute(opID, pattern)
	route.OAuth = gatewayCredentialRoute{
		Eligible:     true,
		Kind:         gatewayRouteKindCodexMapping,
		UpstreamPath: oauthUpstreamPath,
	}
	return route
}

func oauthCodexRoute(opID provider.OpID, pattern, responseMode string, bodyPolicy gatewayBodyPolicy, upstreamPath string) gatewayRoute {
	route := supportedGatewayRoute(opID, pattern, responseMode, bodyPolicy)
	route.OAuth = gatewayCredentialRoute{
		Eligible:     true,
		Kind:         gatewayRouteKindCodexMapping,
		UpstreamPath: upstreamPath,
	}
	return route
}

func supportedGatewayRoute(opID provider.OpID, pattern, responseMode string, bodyPolicy gatewayBodyPolicy) gatewayRoute {
	return gatewayRoute{
		Kind:         gatewayRouteKindSupported,
		Pattern:      pattern,
		OpID:         opID,
		ResponseMode: responseMode,
		BodyPolicy:   bodyPolicy,
	}
}

func unsupportedGatewayRoute(pattern string) gatewayRoute {
	return gatewayRoute{
		Kind:         gatewayRouteKindUnsupported,
		Pattern:      pattern,
		ResponseMode: domain.ResponseModeJSON,
		BodyPolicy:   gatewayBodyPolicyCaptureDisabled,
		StatusCode:   http.StatusNotFound,
		ErrorCode:    ErrCodeUnsupportedEndpoint,
	}
}

func blockedGatewayRoute(path string) gatewayRoute {
	return gatewayRoute{
		Kind:         gatewayRouteKindBlocked,
		Pattern:      blockedGatewayPattern(path),
		ResponseMode: domain.ResponseModeJSON,
		BodyPolicy:   gatewayBodyPolicyCaptureDisabled,
		StatusCode:   http.StatusForbidden,
		ErrorCode:    ErrCodeBlockedEndpoint,
	}
}

func normalizeGatewayPath(path string) string {
	if path == "" {
		return "/"
	}
	return path
}

func gatewayPathSegments(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func isBlockedGatewayPath(path string) bool {
	return strings.HasPrefix(path, "/v1/organization") || strings.HasPrefix(path, "/v1/projects/")
}

func blockedGatewayPattern(path string) string {
	switch {
	case strings.HasPrefix(path, "/v1/organization"):
		return "/v1/organization*"
	case strings.HasPrefix(path, "/v1/projects/"):
		return "/v1/projects/*"
	default:
		return path
	}
}

func unsupportedGatewayPattern(method, path string, websocket bool) string {
	switch {
	case strings.HasPrefix(path, "/v1/audio/"):
		return "/v1/audio/*"
	case path == "/v1/embeddings" || strings.HasPrefix(path, "/v1/embeddings/"):
		return "/v1/embeddings"
	case path == "/v1/moderations" || strings.HasPrefix(path, "/v1/moderations/"):
		return "/v1/moderations"
	case strings.HasPrefix(path, "/v1/images/"):
		return "/v1/images/*"
	case path == "/v1/videos" || strings.HasPrefix(path, "/v1/videos/"):
		return "/v1/videos*"
	case path == "/v1/files" || strings.HasPrefix(path, "/v1/files/"):
		return "/v1/files*"
	case path == "/v1/uploads" || strings.HasPrefix(path, "/v1/uploads/"):
		return "/v1/uploads*"
	case path == "/v1/vector_stores" || strings.HasPrefix(path, "/v1/vector_stores/"):
		return "/v1/vector_stores*"
	case path == "/v1/batches" || strings.HasPrefix(path, "/v1/batches/"):
		return "/v1/batches*"
	case path == "/v1/fine_tuning" || strings.HasPrefix(path, "/v1/fine_tuning/"):
		return "/v1/fine_tuning*"
	case path == "/v1/evals" || strings.HasPrefix(path, "/v1/evals/"):
		return "/v1/evals*"
	case path == "/v1/realtime" || strings.HasPrefix(path, "/v1/realtime/") || websocket && strings.HasPrefix(path, "/v1/realtime"):
		return "/v1/realtime*"
	case path == "/v1/assistants" || strings.HasPrefix(path, "/v1/assistants/"):
		return "/v1/assistants*"
	case path == "/v1/threads" || strings.HasPrefix(path, "/v1/threads/"):
		return "/v1/threads*"
	case path == "/v1/completions":
		return "/v1/completions"
	case path == "/v1/chatkit" || strings.HasPrefix(path, "/v1/chatkit/"):
		return "/v1/chatkit*"
	case path == "/v1/containers" || strings.HasPrefix(path, "/v1/containers/"):
		return "/v1/containers*"
	case path == "/v1/skills" || strings.HasPrefix(path, "/v1/skills/"):
		return "/v1/skills*"
	case method == http.MethodDelete && strings.HasPrefix(path, "/v1/models/"):
		return "/v1/models/{model}"
	case path == "/backend-api":
		return "/backend-api"
	case strings.HasPrefix(path, "/backend-api/"):
		return "/backend-api/*"
	case strings.HasPrefix(path, "/v1/"):
		return "/v1/*"
	default:
		return path
	}
}

func isGatewayWebSocketUpgrade(r *http.Request) bool {
	if r == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return false
	}
	for _, raw := range r.Header.Values("Connection") {
		for _, token := range strings.Split(raw, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
				return true
			}
		}
	}
	return false
}
