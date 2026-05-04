package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/one-llm-router/internal/domain"
)

func TestBridgeIdentifierPrefixes(t *testing.T) {
	t.Parallel()

	for _, opID := range AllOpIDs() {
		assert.Truef(t, strings.HasPrefix(string(opID), "op."), "OpID %q must start with op.", opID)
		assert.NotContains(t, string(opID), "/api/codex/usage")
		assert.NotContains(t, string(opID), "/v1/usage")
	}

	for _, bridgeID := range AllBridgeIDs() {
		assert.Truef(t, strings.HasPrefix(string(bridgeID), "bridge."), "BridgeID %q must start with bridge.", bridgeID)
		assert.NotContains(t, string(bridgeID), "/api/codex/usage")
		assert.NotContains(t, string(bridgeID), "/v1/usage")
	}
}

func TestContractPrefixes(t *testing.T) {
	t.Parallel()

	for _, contract := range AllContracts() {
		assert.Truef(t, strings.HasPrefix(string(contract), "contract."), "Contract %q must start with contract.", contract)
		assert.NotContains(t, string(contract), "/api/codex/usage")
		assert.NotContains(t, string(contract), "/v1/usage")
	}
}

func TestBridgeMetadataSafeMap(t *testing.T) {
	t.Parallel()

	metadata := BridgeMetadata{
		OpID:             OpOpenAIChatCompletionsCreate,
		BridgeID:         BridgeOpenAIChatCompletionsToCodex,
		ClientContract:   ContractOpenAIV1ChatCompletions,
		UpstreamContract: ContractChatGPTBackendAPICodexResponses,
		CredentialClass:  CredentialClassOAuth,
	}

	got := metadata.SafeMap()

	require.Equal(t, domain.JSONMap{
		"op_id":             string(OpOpenAIChatCompletionsCreate),
		"bridge_id":         string(BridgeOpenAIChatCompletionsToCodex),
		"client_contract":   string(ContractOpenAIV1ChatCompletions),
		"upstream_contract": string(ContractChatGPTBackendAPICodexResponses),
		"upstream_endpoint": "",
		"credential_class":  string(CredentialClassOAuth),
	}, got)
	assertForbiddenBridgeMetadataKeysAbsent(t, got)
}

func TestMergeBridgeRouterMetadataPreservesExistingRouterMetadata(t *testing.T) {
	t.Parallel()

	base := domain.JSONMap{
		"route_kind": "supported",
		"nested": domain.JSONMap{
			"keep": true,
		},
	}
	metadata := BridgeMetadata{
		OpID:             OpOpenAIResponsesCreate,
		BridgeID:         BridgeOpenAIResponsesDirect,
		ClientContract:   ContractOpenAIV1Responses,
		UpstreamContract: ContractOpenAIV1Responses,
		CredentialClass:  CredentialClassAPIKey,
	}

	got := MergeBridgeRouterMetadata(base, metadata)

	assert.Equal(t, "supported", got["route_kind"])
	assert.Equal(t, domain.JSONMap{"keep": true}, got["nested"])
	require.Contains(t, got, RouterMetadataBridgeKey)
	assert.Equal(t, metadata.SafeMap(), got[RouterMetadataBridgeKey])
	assert.NotContains(t, base, RouterMetadataBridgeKey, "merge must not mutate caller-owned map")
	assertForbiddenBridgeMetadataKeysAbsent(t, got)
}

func TestMergeBridgeRouterMetadataReplacesOnlyBridgeKey(t *testing.T) {
	t.Parallel()

	base := domain.JSONMap{
		RouterMetadataBridgeKey: domain.JSONMap{"old": "value"},
		"route_kind":            "supported",
		"nested": domain.JSONMap{
			"keep": true,
		},
	}
	metadata := BridgeMetadata{
		OpID:             OpOpenAIResponsesCreate,
		BridgeID:         BridgeOpenAIResponsesDirect,
		ClientContract:   ContractOpenAIV1Responses,
		UpstreamContract: ContractOpenAIV1Responses,
		CredentialClass:  CredentialClassAPIKey,
	}

	got := MergeBridgeRouterMetadata(base, metadata)

	assert.Equal(t, "supported", got["route_kind"])
	assert.Equal(t, metadata.SafeMap(), got[RouterMetadataBridgeKey])
	assert.Equal(t, domain.JSONMap{"old": "value"}, base[RouterMetadataBridgeKey])
	assertForbiddenBridgeMetadataKeysAbsent(t, got)
}

func assertForbiddenBridgeMetadataKeysAbsent(t *testing.T, value any) {
	t.Helper()

	data, err := json.Marshal(value)
	require.NoError(t, err)
	text := strings.ToLower(string(data))
	for _, forbidden := range []string{
		"authorization",
		"cookie",
		"access_token",
		"refresh_token",
		"id_token",
		"request_body",
		"response_body",
		"upstream_body",
		"frame",
	} {
		assert.NotContains(t, text, forbidden)
	}
}
