// Package provider defines provider-agnostic data-plane bridge contracts.
package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/user/one-llm-router/internal/domain"
)

type OpID string
type BridgeID string
type Contract string
type CredentialClass string

const (
	CredentialClassAPIKey CredentialClass = "api_key"
	CredentialClassOAuth  CredentialClass = "oauth"
)

const RouterMetadataBridgeKey = "bridge"
const RouterMetadataBridgeUpstreamEndpointKey = "upstream_endpoint"

type BridgeMetadata struct {
	OpID             OpID
	BridgeID         BridgeID
	ClientContract   Contract
	UpstreamContract Contract
	UpstreamEndpoint string
	CredentialClass  CredentialClass
}

func (m BridgeMetadata) SafeMap() domain.JSONMap {
	out := domain.JSONMap{
		"op_id":             string(m.OpID),
		"bridge_id":         string(m.BridgeID),
		"client_contract":   string(m.ClientContract),
		"upstream_contract": string(m.UpstreamContract),
		"upstream_endpoint": m.UpstreamEndpoint,
		"credential_class":  string(m.CredentialClass),
	}
	return out
}

func MergeBridgeRouterMetadata(base domain.JSONMap, metadata BridgeMetadata) domain.JSONMap {
	out := domain.JSONMap{}
	for key, value := range base {
		out[key] = value
	}
	out[RouterMetadataBridgeKey] = metadata.SafeMap()
	return out
}

type OperationBridge interface {
	ID() BridgeID
	OpID() OpID
	CredentialClass() CredentialClass
	ClientContract() Contract
	UpstreamContract() Contract
	DecodeClientRequest(context.Context, DecodeInput) (ClientRequest, error)
	BuildUpstreamRequest(context.Context, BuildInput) (UpstreamRequest, ClientResponseAdapter, error)
}

type DecodeInput struct {
	OpID          OpID
	Method        string
	Path          string
	Pattern       string
	RawQuery      string
	RawBody       []byte
	Body          io.Reader
	Headers       http.Header
	ContentLength int64
	WebSocket     bool
	ResponseMode  string
	BodyPolicy    string
	RequestID     string
	Metadata      domain.JSONMap
}

type ClientRequest struct {
	ContractValue   Contract
	Method          string
	Path            string
	Pattern         string
	RawQuery        string
	RawBody         []byte
	Body            io.Reader
	Headers         http.Header
	ContentLength   int64
	WebSocket       bool
	StreamRequested bool
	RequestID       string
	Metadata        domain.JSONMap
}

func (r ClientRequest) Contract() Contract {
	return r.ContractValue
}

type BuildInput struct {
	ClientRequest   ClientRequest
	Credential      CredentialClass
	CredentialValue string
	// AccountMetadata carries non-secret selected-account facts for provider
	// bridges. Provider-specific packages decide which keys they understand.
	AccountMetadata domain.JSONMap
	UpstreamBaseURL string
}

type UpstreamRequest struct {
	ContractValue Contract
	OpID          OpID
	BridgeID      BridgeID
	Method        string
	URL           string
	Path          string
	Pattern       string
	RawQuery      string
	RawBody       []byte
	Body          io.Reader
	ContentLength int64
	Headers       http.Header
	Metadata      domain.JSONMap
}

func (r UpstreamRequest) Contract() Contract {
	return r.ContractValue
}

type ClientResponseAdapter interface {
	Kind() string
	ClientContract() Contract
}

type BridgeRegistry struct {
	bridges map[bridgeRegistryKey]OperationBridge
}

type bridgeRegistryKey struct {
	opID       OpID
	credential CredentialClass
}

func NewBridgeRegistry(bridges ...OperationBridge) *BridgeRegistry {
	registry := &BridgeRegistry{bridges: map[bridgeRegistryKey]OperationBridge{}}
	for _, bridge := range bridges {
		if bridge == nil {
			continue
		}
		key := bridgeRegistryKey{
			opID:       bridge.OpID(),
			credential: bridge.CredentialClass(),
		}
		if _, exists := registry.bridges[key]; exists {
			panic(fmt.Sprintf("duplicate operation bridge registration: op_id=%q credential=%q", key.opID, key.credential))
		}
		registry.bridges[key] = bridge
	}
	return registry
}

func (r *BridgeRegistry) Resolve(opID OpID, credential CredentialClass) (OperationBridge, bool) {
	if r == nil {
		return nil, false
	}
	bridge, ok := r.bridges[bridgeRegistryKey{opID: opID, credential: credential}]
	return bridge, ok
}
