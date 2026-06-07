package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/provider"
)

type clientResponseAdapter struct {
	kind           string
	clientContract Contract
	model          string
	includeUsage   bool
}

func (a clientResponseAdapter) Kind() string {
	return a.kind
}

func (a clientResponseAdapter) ClientContract() Contract {
	return a.clientContract
}

const (
	ClientResponseAdapterPassthrough         = "passthrough"
	ClientResponseAdapterOpenAIResponsesJSON = "openai_responses_json"
	ClientResponseAdapterOpenAIResponsesSSE  = "openai_responses_sse"
	ClientResponseAdapterChatCompletionsJSON = "chat_completions_json"
	ClientResponseAdapterChatCompletionsSSE  = "chat_completions_sse"
	ClientResponseAdapterOpenAIModelsList    = "openai_models_list"
	ClientResponseAdapterCodexNative         = "codex_native_passthrough"
	ClientResponseAdapterWebSocketRelay      = "websocket_relay"
)

func DefaultBridgeRegistry() *BridgeRegistry {
	return provider.NewBridgeRegistry(defaultOperationBridges()...)
}

type operationBridge struct {
	id               BridgeID
	opID             OpID
	credential       CredentialClass
	clientContract   Contract
	upstreamContract Contract
	upstreamPath     string
	adapterKind      string
}

func (b operationBridge) ID() BridgeID {
	return b.id
}

func (b operationBridge) OpID() OpID {
	return b.opID
}

func (b operationBridge) CredentialClass() CredentialClass {
	return b.credential
}

func (b operationBridge) ClientContract() Contract {
	return b.clientContract
}

func (b operationBridge) UpstreamContract() Contract {
	return b.upstreamContract
}

func (b operationBridge) DecodeClientRequest(_ context.Context, in DecodeInput) (ClientRequest, error) {
	if in.OpID != "" && in.OpID != b.opID {
		return ClientRequest{}, errors.New("decode input op id does not match bridge")
	}
	return ClientRequest{
		ContractValue: b.clientContract,
		Method:        in.Method,
		Path:          in.Path,
		Pattern:       in.Pattern,
		RawQuery:      in.RawQuery,
		RawBody:       append([]byte(nil), in.RawBody...),
		Body:          in.Body,
		Headers:       in.Headers.Clone(),
		ContentLength: in.ContentLength,
		WebSocket:     in.WebSocket,
		StreamRequested: streamRequestedFromClientInput(
			in.RawBody,
			in.WebSocket,
		),
		RequestID: in.RequestID,
		Metadata:  cloneJSONMap(in.Metadata),
	}, nil
}

func (b operationBridge) BuildUpstreamRequest(_ context.Context, in BuildInput) (UpstreamRequest, ClientResponseAdapter, error) {
	if in.ClientRequest.Contract() != b.clientContract {
		return UpstreamRequest{}, nil, errors.New("client request contract does not match bridge")
	}
	if in.Credential != "" && in.Credential != b.credential {
		return UpstreamRequest{}, nil, fmt.Errorf("%w: credential class %q does not match bridge %q", ErrInvalidUpstreamRequest, in.Credential, b.id)
	}
	upstreamBody, bodyReader, contentLength, adapter, err := b.upstreamBodyAndAdapter(in.ClientRequest)
	if err != nil {
		return UpstreamRequest{}, nil, fmt.Errorf("build upstream request body: %w", err)
	}
	upstream := UpstreamRequest{
		ContractValue: b.upstreamContract,
		OpID:          b.opID,
		BridgeID:      b.id,
		Method:        in.ClientRequest.Method,
		URL:           b.upstreamURL(in.ClientRequest, in.UpstreamBaseURL),
		Path:          b.resolvedUpstreamPath(in.ClientRequest),
		Pattern:       in.ClientRequest.Pattern,
		RawQuery:      in.ClientRequest.RawQuery,
		RawBody:       upstreamBody,
		Body:          bodyReader,
		ContentLength: contentLength,
		Headers:       b.upstreamHeaders(in.ClientRequest, in.CredentialValue, in.AccountMetadata),
		Metadata:      cloneJSONMap(in.ClientRequest.Metadata),
	}
	return upstream, adapter, nil
}

func (b operationBridge) upstreamURL(clientReq ClientRequest, baseURL string) string {
	if baseURL == "" {
		if b.credential == CredentialClassOAuth {
			baseURL = ChatGPTBackendBaseURL
		} else {
			return ""
		}
	}
	target := strings.TrimRight(baseURL, "/") + b.resolvedUpstreamPath(clientReq)
	if clientReq.RawQuery != "" {
		target += "?" + clientReq.RawQuery
	}
	return target
}

func (b operationBridge) upstreamHeaders(clientReq ClientRequest, credentialValue string, accountMetadata domain.JSONMap) http.Header {
	headers := http.Header{}
	if b.id == BridgeCodexNativeTranscribeDirect {
		copyTranscribeHeaders(headers, clientReq.Headers)
	} else {
		copyForwardHeaders(headers, clientReq.Headers)
	}
	if credentialValue != "" {
		headers.Set("Authorization", "Bearer "+credentialValue)
	}
	if b.credential == CredentialClassOAuth {
		headers.Set("User-Agent", CodexCLIUserAgent)
		if !clientReq.WebSocket && b.id != BridgeCodexNativeTranscribeDirect && b.hasJSONUpstreamBody(clientReq) {
			headers.Set("Content-Type", "application/json")
		}
		if !clientReq.WebSocket && b.requiresIdentityAcceptEncoding(clientReq) {
			forceIdentityAcceptEncoding(headers)
		}
		if !clientReq.WebSocket && b.resolvedUpstreamPath(clientReq) == "/codex/responses" {
			headers.Set("Accept", "text/event-stream")
			forceIdentityAcceptEncoding(headers)
		}
		chatGPTAccountID := chatGPTAccountIDFromMetadata(accountMetadata)
		if chatGPTAccountID != "" {
			headers.Set("chatgpt-account-id", chatGPTAccountID)
		}
	}
	return headers
}

func chatGPTAccountIDFromMetadata(metadata domain.JSONMap) string {
	if len(metadata) == 0 {
		return ""
	}
	value, ok := metadata["chatgpt_account_id"].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func (b operationBridge) requiresIdentityAcceptEncoding(clientReq ClientRequest) bool {
	switch b.resolvedUpstreamPath(clientReq) {
	case "/codex/responses", "/codex/responses/compact":
		return true
	}
	switch b.adapterKind {
	case ClientResponseAdapterOpenAIResponsesJSON,
		ClientResponseAdapterOpenAIModelsList,
		ClientResponseAdapterChatCompletionsJSON,
		ClientResponseAdapterChatCompletionsSSE:
		return true
	default:
		return false
	}
}

func (b operationBridge) hasJSONUpstreamBody(clientReq ClientRequest) bool {
	if b.adapterKind == ClientResponseAdapterWebSocketRelay {
		return false
	}
	if len(clientReq.RawBody) > 0 {
		return true
	}
	if clientReq.Body != nil && clientReq.ContentLength != 0 {
		return true
	}
	return false
}

func (b operationBridge) resolvedUpstreamPath(clientReq ClientRequest) string {
	if b.upstreamPath != "" {
		return b.upstreamPath
	}
	// API-key bridge: client path like /v1/chat/completions → /chat/completions
	// because base_url already carries /v1.
	return stripV1Prefix(clientReq.Path)
}

// stripV1Prefix strips a leading "/v1" from path so it can be appended to a
// base_url that already contains "/v1" (e.g. "https://api.openai.com/v1").
func stripV1Prefix(path string) string {
	if strings.HasPrefix(path, "/v1/") {
		return path[3:]
	}
	if path == "/v1" {
		return "/"
	}
	return path
}

func (b operationBridge) upstreamBodyAndAdapter(clientReq ClientRequest) ([]byte, io.Reader, int64, clientResponseAdapter, error) {
	adapter := clientResponseAdapter{kind: b.adapterKind, clientContract: b.clientContract}
	bodyFromBytes := func(body []byte) ([]byte, io.Reader, int64, clientResponseAdapter, error) {
		return body, bytes.NewReader(body), int64(len(body)), adapter, nil
	}

	switch b.id {
	case BridgeOpenAIResponsesToCodex:
		body, collectSSE, err := normalizeCodexResponsesBody(clientReq.RawBody)
		if err != nil {
			return nil, nil, 0, clientResponseAdapter{}, err
		}
		if !collectSSE {
			adapter.kind = ClientResponseAdapterOpenAIResponsesSSE
		}
		return body, bytes.NewReader(body), int64(len(body)), adapter, nil
	case BridgeCodexNativeResponsesDirect:
		body, err := normalizeNativeCodexResponsesBody(clientReq.RawBody)
		if err != nil {
			return nil, nil, 0, clientResponseAdapter{}, err
		}
		return bodyFromBytes(body)
	case BridgeOpenAIResponsesCompactToCodex:
		body, err := normalizeCodexResponsesCompactBody(clientReq.RawBody)
		if err != nil {
			return nil, nil, 0, clientResponseAdapter{}, err
		}
		return bodyFromBytes(body)
	case BridgeCodexNativeResponsesCompactDirect:
		body, err := normalizeNativeCodexResponsesCompactBody(clientReq.RawBody)
		if err != nil {
			return nil, nil, 0, clientResponseAdapter{}, err
		}
		return bodyFromBytes(body)
	case BridgeOpenAIChatCompletionsToCodex:
		req, err := http.NewRequest(clientReq.Method, "http://router"+clientReq.Path, bytes.NewReader(clientReq.RawBody))
		if err != nil {
			return nil, nil, 0, clientResponseAdapter{}, err
		}
		req.Header = clientReq.Headers.Clone()
		adapted, err := buildChatAdapterRequest(req)
		if err != nil {
			return nil, nil, 0, clientResponseAdapter{}, err
		}
		adapterKind := ClientResponseAdapterChatCompletionsJSON
		if adapted.stream {
			adapterKind = ClientResponseAdapterChatCompletionsSSE
		}
		adapter.kind = adapterKind
		adapter.model = adapted.model
		adapter.includeUsage = adapted.includeUsage
		return adapted.upstreamBody, bytes.NewReader(adapted.upstreamBody), int64(len(adapted.upstreamBody)), adapter, nil
	case BridgeCodexNativeTranscribeDirect:
		if clientReq.Body != nil {
			return nil, clientReq.Body, clientReq.ContentLength, adapter, nil
		}
		return bodyFromBytes(clientReq.RawBody)
	default:
		body := append([]byte(nil), clientReq.RawBody...)
		return bodyFromBytes(body)
	}
}

func defaultOperationBridges() []OperationBridge {
	type bridgeSpec struct {
		credential       CredentialClass
		opIDs            []OpID
		bridgeID         BridgeID
		clientContract   Contract
		upstreamContract Contract
		upstreamPath     string
		adapterKind      string
	}

	var bridges []OperationBridge
	add := func(spec bridgeSpec) {
		for _, opID := range spec.opIDs {
			bridges = append(bridges, operationBridge{
				id:               spec.bridgeID,
				opID:             opID,
				credential:       spec.credential,
				clientContract:   spec.clientContract,
				upstreamContract: spec.upstreamContract,
				upstreamPath:     spec.upstreamPath,
				adapterKind:      spec.adapterKind,
			})
		}
	}

	for _, spec := range []bridgeSpec{
		{
			credential:       CredentialClassAPIKey,
			opIDs:            []OpID{OpOpenAIResponsesCreate, OpOpenAIResponsesRetrieve, OpOpenAIResponsesDelete, OpOpenAIResponsesCancel, OpOpenAIResponsesInputItemsList, OpOpenAIResponsesInputTokens},
			bridgeID:         BridgeOpenAIResponsesDirect,
			clientContract:   ContractOpenAIV1Responses,
			upstreamContract: ContractOpenAIV1Responses,
			adapterKind:      ClientResponseAdapterPassthrough,
		},
		{
			credential:       CredentialClassAPIKey,
			opIDs:            []OpID{OpOpenAIResponsesCompact},
			bridgeID:         BridgeOpenAIResponsesCompactDirect,
			clientContract:   ContractOpenAIV1ResponsesCompact,
			upstreamContract: ContractOpenAIV1ResponsesCompact,
			adapterKind:      ClientResponseAdapterPassthrough,
		},
		{
			credential:       CredentialClassAPIKey,
			opIDs:            []OpID{OpOpenAIConversationsCreate, OpOpenAIConversationsRetrieve, OpOpenAIConversationsUpdate, OpOpenAIConversationsDelete, OpOpenAIConversationsItemsCreate, OpOpenAIConversationsItemsList, OpOpenAIConversationsItemsRetrieve, OpOpenAIConversationsItemsDelete},
			bridgeID:         BridgeOpenAIConversationsDirect,
			clientContract:   ContractOpenAIV1Conversations,
			upstreamContract: ContractOpenAIV1Conversations,
			adapterKind:      ClientResponseAdapterPassthrough,
		},
		{
			credential:       CredentialClassAPIKey,
			opIDs:            []OpID{OpOpenAIChatCompletionsCreate, OpOpenAIChatCompletionsList, OpOpenAIChatCompletionsRetrieve, OpOpenAIChatCompletionsUpdate, OpOpenAIChatCompletionsDelete, OpOpenAIChatCompletionsMessagesList},
			bridgeID:         BridgeOpenAIChatCompletionsDirect,
			clientContract:   ContractOpenAIV1ChatCompletions,
			upstreamContract: ContractOpenAIV1ChatCompletions,
			adapterKind:      ClientResponseAdapterPassthrough,
		},
		{
			credential:       CredentialClassAPIKey,
			opIDs:            []OpID{OpOpenAIModelsList, OpOpenAIModelsRetrieve},
			bridgeID:         BridgeOpenAIModelsDirect,
			clientContract:   ContractOpenAIV1Models,
			upstreamContract: ContractOpenAIV1Models,
			adapterKind:      ClientResponseAdapterPassthrough,
		},
		{
			credential:       CredentialClassOAuth,
			opIDs:            []OpID{OpOpenAIResponsesCreate},
			bridgeID:         BridgeOpenAIResponsesToCodex,
			clientContract:   ContractOpenAIV1Responses,
			upstreamContract: ContractChatGPTBackendAPICodexResponses,
			upstreamPath:     "/codex/responses",
			adapterKind:      ClientResponseAdapterOpenAIResponsesJSON,
		},
		{
			credential:       CredentialClassOAuth,
			opIDs:            []OpID{OpOpenAIResponsesWebSocket},
			bridgeID:         BridgeOpenAIResponsesWebSocketToCodex,
			clientContract:   ContractOpenAIV1ResponsesWS,
			upstreamContract: ContractChatGPTBackendAPICodexResponsesWS,
			upstreamPath:     "/codex/responses",
			adapterKind:      ClientResponseAdapterWebSocketRelay,
		},
		{
			credential:       CredentialClassOAuth,
			opIDs:            []OpID{OpOpenAIResponsesCompact},
			bridgeID:         BridgeOpenAIResponsesCompactToCodex,
			clientContract:   ContractOpenAIV1ResponsesCompact,
			upstreamContract: ContractChatGPTBackendAPICodexResponsesCompact,
			upstreamPath:     "/codex/responses/compact",
			adapterKind:      ClientResponseAdapterOpenAIResponsesJSON,
		},
		{
			credential:       CredentialClassOAuth,
			opIDs:            []OpID{OpOpenAIChatCompletionsCreate},
			bridgeID:         BridgeOpenAIChatCompletionsToCodex,
			clientContract:   ContractOpenAIV1ChatCompletions,
			upstreamContract: ContractChatGPTBackendAPICodexResponses,
			upstreamPath:     "/codex/responses",
			adapterKind:      ClientResponseAdapterChatCompletionsJSON,
		},
		{
			credential:       CredentialClassOAuth,
			opIDs:            []OpID{OpOpenAIModelsList},
			bridgeID:         BridgeOpenAIModelsFromCodex,
			clientContract:   ContractOpenAIV1Models,
			upstreamContract: ContractChatGPTBackendAPICodexModels,
			upstreamPath:     "/codex/models",
			adapterKind:      ClientResponseAdapterOpenAIModelsList,
		},
		{
			credential:       CredentialClassOAuth,
			opIDs:            []OpID{OpCodexNativeResponsesCreate},
			bridgeID:         BridgeCodexNativeResponsesDirect,
			clientContract:   ContractChatGPTBackendAPICodexResponses,
			upstreamContract: ContractChatGPTBackendAPICodexResponses,
			upstreamPath:     "/codex/responses",
			adapterKind:      ClientResponseAdapterCodexNative,
		},
		{
			credential:       CredentialClassOAuth,
			opIDs:            []OpID{OpCodexNativeResponsesWebSocket},
			bridgeID:         BridgeCodexNativeResponsesWebSocketDirect,
			clientContract:   ContractChatGPTBackendAPICodexResponsesWS,
			upstreamContract: ContractChatGPTBackendAPICodexResponsesWS,
			upstreamPath:     "/codex/responses",
			adapterKind:      ClientResponseAdapterWebSocketRelay,
		},
		{
			credential:       CredentialClassOAuth,
			opIDs:            []OpID{OpCodexNativeResponsesCompact},
			bridgeID:         BridgeCodexNativeResponsesCompactDirect,
			clientContract:   ContractChatGPTBackendAPICodexResponsesCompact,
			upstreamContract: ContractChatGPTBackendAPICodexResponsesCompact,
			upstreamPath:     "/codex/responses/compact",
			adapterKind:      ClientResponseAdapterCodexNative,
		},
		{
			credential:       CredentialClassOAuth,
			opIDs:            []OpID{OpCodexNativeModelsList},
			bridgeID:         BridgeCodexNativeModelsDirect,
			clientContract:   ContractChatGPTBackendAPICodexModels,
			upstreamContract: ContractChatGPTBackendAPICodexModels,
			upstreamPath:     "/codex/models",
			adapterKind:      ClientResponseAdapterCodexNative,
		},
		{
			credential:       CredentialClassOAuth,
			opIDs:            []OpID{OpCodexNativeTranscribe},
			bridgeID:         BridgeCodexNativeTranscribeDirect,
			clientContract:   ContractChatGPTBackendAPITranscribe,
			upstreamContract: ContractChatGPTBackendAPITranscribe,
			upstreamPath:     "/transcribe",
			adapterKind:      ClientResponseAdapterCodexNative,
		},
	} {
		add(spec)
	}

	return bridges
}

func cloneJSONMap(in domain.JSONMap) domain.JSONMap {
	if in == nil {
		return nil
	}
	out := domain.JSONMap{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func streamRequestedFromClientInput(rawBody []byte, websocket bool) bool {
	if websocket {
		return true
	}
	if len(rawBody) == 0 {
		return false
	}
	var payload struct {
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		return false
	}
	return payload.Stream
}
