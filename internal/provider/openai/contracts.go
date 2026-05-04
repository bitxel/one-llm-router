package openai

import (
	"github.com/user/one-llm-router/internal/domain"
	"github.com/user/one-llm-router/internal/provider"
)

type OpID = provider.OpID
type BridgeID = provider.BridgeID
type Contract = provider.Contract
type CredentialClass = provider.CredentialClass
type OperationBridge = provider.OperationBridge
type DecodeInput = provider.DecodeInput
type ClientRequest = provider.ClientRequest
type BuildInput = provider.BuildInput
type UpstreamRequest = provider.UpstreamRequest
type ClientResponseAdapter = provider.ClientResponseAdapter
type BridgeRegistry = provider.BridgeRegistry
type BridgeMetadata = provider.BridgeMetadata

func NewBridgeRegistry(bridges ...OperationBridge) *BridgeRegistry {
	return provider.NewBridgeRegistry(bridges...)
}

const RouterMetadataBridgeKey = provider.RouterMetadataBridgeKey
const RouterMetadataBridgeUpstreamEndpointKey = provider.RouterMetadataBridgeUpstreamEndpointKey

const (
	CredentialClassAPIKey CredentialClass = provider.CredentialClassAPIKey
	CredentialClassOAuth  CredentialClass = provider.CredentialClassOAuth
)

const (
	OpOpenAIResponsesCreate         OpID = "op.openai.responses.create"
	OpOpenAIResponsesWebSocket      OpID = "op.openai.responses.websocket"
	OpOpenAIResponsesRetrieve       OpID = "op.openai.responses.retrieve"
	OpOpenAIResponsesDelete         OpID = "op.openai.responses.delete"
	OpOpenAIResponsesCancel         OpID = "op.openai.responses.cancel"
	OpOpenAIResponsesInputItemsList OpID = "op.openai.responses.input_items.list"
	OpOpenAIResponsesInputTokens    OpID = "op.openai.responses.input_tokens.create"
	OpOpenAIResponsesCompact        OpID = "op.openai.responses.compact"

	OpOpenAIConversationsCreate        OpID = "op.openai.conversations.create"
	OpOpenAIConversationsRetrieve      OpID = "op.openai.conversations.retrieve"
	OpOpenAIConversationsUpdate        OpID = "op.openai.conversations.update"
	OpOpenAIConversationsDelete        OpID = "op.openai.conversations.delete"
	OpOpenAIConversationsItemsCreate   OpID = "op.openai.conversations.items.create"
	OpOpenAIConversationsItemsList     OpID = "op.openai.conversations.items.list"
	OpOpenAIConversationsItemsRetrieve OpID = "op.openai.conversations.items.retrieve"
	OpOpenAIConversationsItemsDelete   OpID = "op.openai.conversations.items.delete"

	OpOpenAIChatCompletionsCreate       OpID = "op.openai.chat_completions.create"
	OpOpenAIChatCompletionsList         OpID = "op.openai.chat_completions.list"
	OpOpenAIChatCompletionsRetrieve     OpID = "op.openai.chat_completions.retrieve"
	OpOpenAIChatCompletionsUpdate       OpID = "op.openai.chat_completions.update"
	OpOpenAIChatCompletionsDelete       OpID = "op.openai.chat_completions.delete"
	OpOpenAIChatCompletionsMessagesList OpID = "op.openai.chat_completions.messages.list"

	OpOpenAIModelsList     OpID = "op.openai.models.list"
	OpOpenAIModelsRetrieve OpID = "op.openai.models.retrieve"

	OpCodexNativeResponsesCreate    OpID = "op.codex_native.responses.create"
	OpCodexNativeResponsesWebSocket OpID = "op.codex_native.responses.websocket"
	OpCodexNativeResponsesCompact   OpID = "op.codex_native.responses.compact"
	OpCodexNativeModelsList         OpID = "op.codex_native.models.list"
	OpCodexNativeTranscribe         OpID = "op.codex_native.transcribe"
)

const (
	BridgeOpenAIResponsesDirect        BridgeID = "bridge.openai.responses.direct"
	BridgeOpenAIResponsesCompactDirect BridgeID = "bridge.openai.responses.compact.direct"
	BridgeOpenAIConversationsDirect    BridgeID = "bridge.openai.conversations.direct"
	BridgeOpenAIChatCompletionsDirect  BridgeID = "bridge.openai.chat_completions.direct"
	BridgeOpenAIModelsDirect           BridgeID = "bridge.openai.models.direct"

	BridgeOpenAIResponsesToCodex          BridgeID = "bridge.openai.responses.to_codex"
	BridgeOpenAIResponsesWebSocketToCodex BridgeID = "bridge.openai.responses.websocket.to_codex"
	BridgeOpenAIResponsesCompactToCodex   BridgeID = "bridge.openai.responses.compact.to_codex"
	BridgeOpenAIChatCompletionsToCodex    BridgeID = "bridge.openai.chat_completions.to_codex"
	BridgeOpenAIModelsFromCodex           BridgeID = "bridge.openai.models.from_codex"

	BridgeCodexNativeResponsesDirect          BridgeID = "bridge.codex_native.responses.direct"
	BridgeCodexNativeResponsesWebSocketDirect BridgeID = "bridge.codex_native.responses.websocket.direct"
	BridgeCodexNativeResponsesCompactDirect   BridgeID = "bridge.codex_native.responses.compact.direct"
	BridgeCodexNativeModelsDirect             BridgeID = "bridge.codex_native.models.direct"
	BridgeCodexNativeTranscribeDirect         BridgeID = "bridge.codex_native.transcribe.direct"
)

const (
	ContractOpenAIV1Responses        Contract = "contract.openai.v1.responses"
	ContractOpenAIV1ResponsesCompact Contract = "contract.openai.v1.responses.compact"
	ContractOpenAIV1ResponsesWS      Contract = "contract.openai.v1.responses.websocket"
	ContractOpenAIV1Conversations    Contract = "contract.openai.v1.conversations"
	ContractOpenAIV1ChatCompletions  Contract = "contract.openai.v1.chat_completions"
	ContractOpenAIV1Models           Contract = "contract.openai.v1.models"

	ContractChatGPTBackendAPICodexResponses        Contract = "contract.chatgpt.backend_api.codex.responses"
	ContractChatGPTBackendAPICodexResponsesCompact Contract = "contract.chatgpt.backend_api.codex.responses.compact"
	ContractChatGPTBackendAPICodexResponsesWS      Contract = "contract.chatgpt.backend_api.codex.responses.websocket"
	ContractChatGPTBackendAPICodexModels           Contract = "contract.chatgpt.backend_api.codex.models"
	ContractChatGPTBackendAPITranscribe            Contract = "contract.chatgpt.backend_api.transcribe"
)

func MergeBridgeRouterMetadata(base domain.JSONMap, metadata BridgeMetadata) domain.JSONMap {
	return provider.MergeBridgeRouterMetadata(base, metadata)
}

func AllOpIDs() []OpID {
	return []OpID{
		OpOpenAIResponsesCreate,
		OpOpenAIResponsesWebSocket,
		OpOpenAIResponsesRetrieve,
		OpOpenAIResponsesDelete,
		OpOpenAIResponsesCancel,
		OpOpenAIResponsesInputItemsList,
		OpOpenAIResponsesInputTokens,
		OpOpenAIResponsesCompact,
		OpOpenAIConversationsCreate,
		OpOpenAIConversationsRetrieve,
		OpOpenAIConversationsUpdate,
		OpOpenAIConversationsDelete,
		OpOpenAIConversationsItemsCreate,
		OpOpenAIConversationsItemsList,
		OpOpenAIConversationsItemsRetrieve,
		OpOpenAIConversationsItemsDelete,
		OpOpenAIChatCompletionsCreate,
		OpOpenAIChatCompletionsList,
		OpOpenAIChatCompletionsRetrieve,
		OpOpenAIChatCompletionsUpdate,
		OpOpenAIChatCompletionsDelete,
		OpOpenAIChatCompletionsMessagesList,
		OpOpenAIModelsList,
		OpOpenAIModelsRetrieve,
		OpCodexNativeResponsesCreate,
		OpCodexNativeResponsesWebSocket,
		OpCodexNativeResponsesCompact,
		OpCodexNativeModelsList,
		OpCodexNativeTranscribe,
	}
}

func AllBridgeIDs() []BridgeID {
	return []BridgeID{
		BridgeOpenAIResponsesDirect,
		BridgeOpenAIResponsesCompactDirect,
		BridgeOpenAIConversationsDirect,
		BridgeOpenAIChatCompletionsDirect,
		BridgeOpenAIModelsDirect,
		BridgeOpenAIResponsesToCodex,
		BridgeOpenAIResponsesWebSocketToCodex,
		BridgeOpenAIResponsesCompactToCodex,
		BridgeOpenAIChatCompletionsToCodex,
		BridgeOpenAIModelsFromCodex,
		BridgeCodexNativeResponsesDirect,
		BridgeCodexNativeResponsesWebSocketDirect,
		BridgeCodexNativeResponsesCompactDirect,
		BridgeCodexNativeModelsDirect,
		BridgeCodexNativeTranscribeDirect,
	}
}

func AllContracts() []Contract {
	return []Contract{
		ContractOpenAIV1Responses,
		ContractOpenAIV1ResponsesCompact,
		ContractOpenAIV1ResponsesWS,
		ContractOpenAIV1Conversations,
		ContractOpenAIV1ChatCompletions,
		ContractOpenAIV1Models,
		ContractChatGPTBackendAPICodexResponses,
		ContractChatGPTBackendAPICodexResponsesCompact,
		ContractChatGPTBackendAPICodexResponsesWS,
		ContractChatGPTBackendAPICodexModels,
		ContractChatGPTBackendAPITranscribe,
	}
}
