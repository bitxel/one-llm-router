package provider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/one-llm-router/internal/domain"
)

type stubBridge struct {
	id         BridgeID
	opID       OpID
	credential CredentialClass
}

func (b stubBridge) ID() BridgeID {
	return b.id
}

func (b stubBridge) OpID() OpID {
	return b.opID
}

func (b stubBridge) CredentialClass() CredentialClass {
	return b.credential
}

func (b stubBridge) ClientContract() Contract {
	return "contract.test.client"
}

func (b stubBridge) UpstreamContract() Contract {
	return "contract.test.upstream"
}

func (b stubBridge) DecodeClientRequest(context.Context, DecodeInput) (ClientRequest, error) {
	return ClientRequest{ContractValue: b.ClientContract()}, nil
}

func (b stubBridge) BuildUpstreamRequest(context.Context, BuildInput) (UpstreamRequest, ClientResponseAdapter, error) {
	return UpstreamRequest{ContractValue: b.UpstreamContract()}, nil, nil
}

func TestBridgeRegistryResolveUsesOperationAndCredential(t *testing.T) {
	t.Parallel()

	apiKeyBridge := stubBridge{id: "bridge.test.api_key", opID: "op.test.create", credential: CredentialClassAPIKey}
	oauthBridge := stubBridge{id: "bridge.test.oauth", opID: "op.test.create", credential: CredentialClassOAuth}
	registry := NewBridgeRegistry(nil, apiKeyBridge, oauthBridge)

	got, ok := registry.Resolve("op.test.create", CredentialClassOAuth)
	require.True(t, ok)
	assert.Equal(t, BridgeID("bridge.test.oauth"), got.ID())

	got, ok = registry.Resolve("op.test.create", CredentialClassAPIKey)
	require.True(t, ok)
	assert.Equal(t, BridgeID("bridge.test.api_key"), got.ID())
}

func TestBridgeRegistryResolveNilRegistry(t *testing.T) {
	t.Parallel()

	var registry *BridgeRegistry
	got, ok := registry.Resolve("op.test.create", CredentialClassAPIKey)
	assert.False(t, ok)
	assert.Nil(t, got)
}

func TestBridgeRegistryPanicsOnDuplicateRegistration(t *testing.T) {
	t.Parallel()

	bridge := stubBridge{id: "bridge.test.one", opID: "op.test.create", credential: CredentialClassAPIKey}
	duplicate := stubBridge{id: "bridge.test.two", opID: "op.test.create", credential: CredentialClassAPIKey}

	assert.PanicsWithValue(
		t,
		`duplicate operation bridge registration: op_id="op.test.create" credential="api_key"`,
		func() { NewBridgeRegistry(bridge, duplicate) },
	)
}

func TestBridgeMetadataSafeMap(t *testing.T) {
	t.Parallel()

	metadata := BridgeMetadata{
		OpID:             "op.test.create",
		BridgeID:         "bridge.test.direct",
		ClientContract:   "contract.test.client",
		UpstreamContract: "contract.test.upstream",
		CredentialClass:  CredentialClassAPIKey,
	}

	assert.Equal(t, domain.JSONMap{
		"op_id":             "op.test.create",
		"bridge_id":         "bridge.test.direct",
		"client_contract":   "contract.test.client",
		"upstream_contract": "contract.test.upstream",
		"upstream_endpoint": "",
		"credential_class":  "api_key",
	}, metadata.SafeMap())
}

func TestMergeBridgeRouterMetadataDoesNotMutateBase(t *testing.T) {
	t.Parallel()

	base := domain.JSONMap{
		"route_kind":            "supported",
		RouterMetadataBridgeKey: domain.JSONMap{"old": "value"},
	}
	metadata := BridgeMetadata{
		OpID:             "op.test.create",
		BridgeID:         "bridge.test.direct",
		ClientContract:   "contract.test.client",
		UpstreamContract: "contract.test.upstream",
		CredentialClass:  CredentialClassAPIKey,
	}

	got := MergeBridgeRouterMetadata(base, metadata)

	assert.Equal(t, "supported", got["route_kind"])
	assert.Equal(t, metadata.SafeMap(), got[RouterMetadataBridgeKey])
	assert.Equal(t, domain.JSONMap{"old": "value"}, base[RouterMetadataBridgeKey])
}

func TestBridgeMetadataSafeMapIncludesUpstreamEndpointWhenKnown(t *testing.T) {
	t.Parallel()

	metadata := BridgeMetadata{
		OpID:             "op.test.create",
		BridgeID:         "bridge.test.to_upstream",
		ClientContract:   "contract.test.client",
		UpstreamContract: "contract.test.upstream",
		UpstreamEndpoint: "/upstream/responses",
		CredentialClass:  CredentialClassOAuth,
	}

	assert.Equal(t, domain.JSONMap{
		"op_id":             "op.test.create",
		"bridge_id":         "bridge.test.to_upstream",
		"client_contract":   "contract.test.client",
		"upstream_contract": "contract.test.upstream",
		"upstream_endpoint": "/upstream/responses",
		"credential_class":  "oauth",
	}, metadata.SafeMap())
}
