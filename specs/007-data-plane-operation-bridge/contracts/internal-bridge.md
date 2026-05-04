# Contract: Internal Data-Plane Operation Bridge

**Feature**: 007-data-plane-operation-bridge
**Status**: Implemented

This is an internal implementation contract. It does not add public HTTP endpoints. External data-plane behavior remains governed by Feature 006 contracts.

The provider-agnostic bridge boundary and safe `router_metadata.bridge` helpers live in `internal/provider`. Concrete OpenAI Platform and ChatGPT Codex operation constants and bridge implementations live in `internal/provider/openai`.

## Identifier Namespaces

| Type | Prefix | Example | Meaning |
|---|---|---|---|
| `OpID` | `op.` | `op.openai.chat_completions.create` | Stable client-facing operation identity |
| `BridgeID` | `bridge.` | `bridge.openai.chat_completions.to_codex` | Selected executable bridge identity |
| `Contract` | `contract.` | `contract.openai.v1.chat_completions` | Request/response/stream contract family name |

Identifier values are stable test and audit names. They are not user-visible API paths.

Contract granularity is wire-shape family, not one contract per URL. `OpID` owns exact method/path identity; `Contract` owns request/response/stream shape. Separate contracts are required when decoding, upstream building, or client response adaptation semantics differ, such as compact, WebSocket, native Codex, Chat Completions, and Models shapes.

## Bridge Interface Semantics

Required public boundary:

```go
type OperationBridge interface {
	ID() BridgeID
	OpID() OpID
	CredentialClass() CredentialClass

	ClientContract() Contract
	UpstreamContract() Contract

	DecodeClientRequest(ctx context.Context, in DecodeInput) (ClientRequest, error)
	BuildUpstreamRequest(ctx context.Context, in BuildInput) (UpstreamRequest, ClientResponseAdapter, error)
}
```

## Bridge I/O Types

The exact Go structs may evolve during implementation, but they MUST preserve these ownership boundaries.

### DecodeInput

Required contents:

- Original HTTP request metadata: method, path, query, headers, content length, and upgrade state.
- Classified operation metadata: OpID, path pattern, response mode, body policy, credential eligibility, and provider route kind.
- Request body access according to body policy:
  - Safe JSON routes may receive bounded captured bytes.
  - Multipart routes may receive the bounded body stream and must not force JSON decoding.
  - WebSocket routes receive no body.
- Request id for error and trace correlation.

Forbidden contents:

- Selected upstream account.
- OAuth access token or API key.
- Provider URL decisions.

### ClientRequest

Required behavior:

- Exposes its `Contract`.
- Exposes client stream preference when the operation has a stream mode.
- Preserves raw request semantics for direct pass-through, multipart, and WebSocket operations.
- Carries only safe decoded metadata needed by the selected bridge.

### BuildInput

Required contents:

- The decoded `ClientRequest`.
- Selected operation metadata.
- Selected credential class and provider-neutral selected-account metadata.
- Ephemeral credential material needed to construct the upstream request, if the bridge owns header construction.
- Request id through `ClientRequest` for trace correlation.

Forbidden contents:

- Persisted credential copies.
- Provider-specific account fields on the provider-generic struct; these must be passed as safe account metadata and interpreted inside the provider package.
- Admin usage responses, because they are outside data-plane bridge resolution.
- Client body bytes for unsupported/deferred/blocked routes, because those routes never reach bridge selection.

### UpstreamRequest

Required behavior:

- Represents a provider HTTP/WebSocket request.
- Carries final method, URL/path, sanitized headers, body stream/bytes, content length when known, and response/capture policy.
- Keeps direct API-key pass-through bodies raw unless the operation contract explicitly defines normalization.
- Keeps OAuth/Codex upstream requests on the ChatGPT Codex backend contract; OAuth tokens must not be used for OpenAI Platform `/v1/*`.

### ClientResponseAdapter

Required behavior:

- Returns the upstream result in the client-facing contract.
- Makes collection behavior explicit: passthrough, facade JSON collection, facade SSE conversion, native Codex passthrough, or WebSocket relay.
- Does not persist response bodies beyond existing bounded capture policy.
- Does not log or persist WebSocket frame payloads.

### DecodeClientRequest

Responsibilities:

- Parse or preserve the inbound request according to the client contract.
- Validate client request shape for router-owned compatibility bridges.
- Preserve raw body semantics for direct pass-through and non-JSON operations.
- Return errors for invalid client requests: malformed syntax, wrong field type, missing required client-contract fields, or fields outside the client contract.
- Preserve syntactically valid client intent for `BuildUpstreamRequest` to accept, transform, or reject according to the selected upstream contract.

Non-responsibilities:

- It must not select accounts.
- It must not perform upstream HTTP calls.
- It must not mutate global route state.

### BuildUpstreamRequest

Responsibilities:

- Convert a valid `ClientRequest` into an `UpstreamRequest`.
- Apply provider-specific normalization inside the selected bridge.
- Reject syntactically valid client intent that the selected upstream contract cannot honestly support.
- Rewrite or remove credentials and hop-by-hop headers according to existing rules.
- Select a `ClientResponseAdapter`.
- Return errors that mean "cannot build a valid upstream request for this selected bridge".
- Fail fast when the selected account credential class does not match the bridge credential class.

Non-responsibilities:

- It must not silently fabricate successful provider responses.
- It must not use provider-wide global policy flags for operation-specific behavior.
- It must not read or persist secrets beyond existing request execution needs.

## Contract Mismatch Rule

```text
ClientContract == UpstreamContract
  -> direct pass-through or operation-defined normalization only

ClientContract != UpstreamContract
  -> explicit bridge transformation and ClientResponseAdapter are mandatory
```

Examples:

| Scenario | ClientContract | UpstreamContract | Bridge Requirement |
|---|---|---|---|
| API-key `/v1/chat/completions` | `contract.openai.v1.chat_completions` | `contract.openai.v1.chat_completions` | Direct forwarding |
| OAuth `/v1/chat/completions` | `contract.openai.v1.chat_completions` | `contract.chatgpt.backend_api.codex.responses` | Chat-to-Codex request bridge and Chat response adapter |
| OAuth `/backend-api/codex/responses` | `contract.chatgpt.backend_api.codex.responses` | `contract.chatgpt.backend_api.codex.responses` | Native Codex normalization and passthrough response |

## Error Boundaries

| Error Class | Meaning | Expected Data-Plane Outcome |
|---|---|---|
| Invalid client request | The client body/headers do not satisfy the client contract | `400 invalid_request` native data-plane error |
| Unsupported client intent | Request is syntactically valid but the selected bridge cannot honestly support it | `400 invalid_request` or documented native unsupported error |
| No eligible bridge/account | Operation is supported but no selected credential class can handle it | Existing no-available-account behavior |
| Upstream build failure | Internal failure while constructing upstream request | Native router error, no fabricated provider success |
| Upstream response invalid | Adapter cannot safely parse/convert upstream output | Existing upstream-response-invalid behavior |

## Required Bridge Families

| BridgeID | Required Behavior |
|---|---|
| `bridge.openai.responses.direct` | API-key direct forwarding for supported non-compact HTTP Responses operations |
| `bridge.openai.responses.compact.direct` | API-key direct pass-through for `POST /v1/responses/compact` when the configured upstream supports it |
| `bridge.openai.conversations.direct` | API-key direct forwarding for supported Conversations operations |
| `bridge.openai.chat_completions.direct` | API-key direct forwarding for supported Chat Completions operations |
| `bridge.openai.models.direct` | API-key direct forwarding for supported Models operations |
| `bridge.openai.responses.to_codex` | OAuth Responses facade over Codex responses |
| `bridge.openai.responses.websocket.to_codex` | OAuth `/v1/responses` WebSocket bridge to Codex WebSocket |
| `bridge.openai.responses.compact.to_codex` | OAuth compact responses bridge |
| `bridge.openai.chat_completions.to_codex` | OAuth Chat Completions facade over Codex responses |
| `bridge.openai.models.from_codex` | OAuth OpenAI model list facade over Codex models |
| `bridge.codex_native.responses.direct` | OAuth native Codex responses |
| `bridge.codex_native.responses.websocket.direct` | OAuth native Codex WebSocket |
| `bridge.codex_native.responses.compact.direct` | OAuth native Codex compact |
| `bridge.codex_native.models.direct` | OAuth native Codex models |
| `bridge.codex_native.transcribe.direct` | OAuth native transcribe multipart |

## Audit Metadata Contract

Safe metadata keys:

```json
{
  "op_id": "op.openai.chat_completions.create",
  "bridge_id": "bridge.openai.chat_completions.to_codex",
  "client_contract": "contract.openai.v1.chat_completions",
  "upstream_contract": "contract.chatgpt.backend_api.codex.responses",
  "credential_class": "oauth"
}
```

Storage location:

```json
{
  "router_metadata": {
    "bridge": {
      "op_id": "op.openai.chat_completions.create",
      "bridge_id": "bridge.openai.chat_completions.to_codex",
      "client_contract": "contract.openai.v1.chat_completions",
      "upstream_contract": "contract.chatgpt.backend_api.codex.responses",
      "upstream_endpoint": "/codex/responses",
      "credential_class": "oauth"
    }
  }
}
```

The implementation MUST store this object under `router_metadata.bridge`. `model_params` must not be used for router-owned bridge audit facts; it remains available for client/model parameters and adapter-derived model metadata.

Forbidden metadata:

- Authorization headers
- API keys
- OAuth access, refresh, or id tokens
- Cookies
- auth.json token fields
- Raw request bodies
- Raw upstream bodies
- Raw response bodies
- WebSocket frame payloads

## Compatibility Requirements

- Feature 006 route support matrix is unchanged.
- API-key direct bridges do not apply OAuth/Codex normalization.
- OAuth/Codex bridges do not send OAuth credentials to OpenAI Platform `/v1/*`.
- Native Codex bridges do not adapt responses into OpenAI facades.
- `GET /api/admin/usage` remains a router-owned Admin API envelope endpoint and is not assigned an OpID or BridgeID.
- `/api/codex/*` remains outside data-plane bridge resolution.
- Unsupported/deferred/blocked routes do not select bridges.
