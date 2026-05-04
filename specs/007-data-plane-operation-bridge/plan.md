# Implementation Plan: Data-Plane Operation Bridge

**Feature**: 007-data-plane-operation-bridge
**Spec**: `specs/007-data-plane-operation-bridge/spec.md`
**Created**: 2026-04-27
**Status**: Implemented
**Complexity**: complex
**Complexity Reason**: The feature refactors at least two backend modules (`internal/api` and `internal/provider/openai`) and must preserve JSON, SSE, WebSocket, native Codex, direct API-key, OAuth facade, and Admin usage boundary behavior.
**Complexity Override**: none

## Summary

Feature 007 introduces an explicit operation bridge architecture for the data plane. Each supported request resolves to an `OpID`, selected `BridgeID`, `ClientContract`, `UpstreamContract`, `ClientRequest`, `UpstreamRequest`, and `ClientResponseAdapter`, while preserving the external Feature 006 route matrix.

This is a behavior-preserving internal refactor. It must reduce ambiguity in the current route classifier and provider forwarder without adding endpoints, dependencies, database migrations, or admin APIs.

## Source Inputs

| Source | Result |
|---|---|
| `specs/007-data-plane-operation-bridge/spec.md` | User value, acceptance criteria, naming decisions, no-scope-expansion constraints |
| `AGENTS.md` | Data-plane envelope exclusions, fail-fast rules, test-first requirement, minimal dependency rule |
| `specs/sdd/constitution.md` | Spec-first, simplicity, test-first, traceability, reversibility gates |
| `specs/006-openai-api-gateway/spec.md` | Existing supported/deferred/blocked operation matrix that must remain unchanged |
| `specs/006-openai-api-gateway/contracts/*.md` | Current data-plane contracts for OpenAI-compatible, Codex-native, and unsupported-route behavior |
| `internal/api/proxy_routes.go` | Current route classifier, credential eligibility, route kind, body policy, response mode |
| `internal/api/proxy.go` | Current proxy orchestration: classify, WebSocket, body handling, account selection, forwarding, recording |
| `internal/provider/openai/forwarder.go` | Current direct API-key forwarding, OAuth Codex mapping, models facade, route-aware body handling |
| `internal/provider/openai/codex_request.go` | Current Codex request normalization and native/facade distinction |
| `internal/provider/openai/chat_adapter.go` | Current OAuth Chat Completions-to-Codex bridge behavior |

## Technical Context

- **Language/Version**: Go `1.25.0`, verified in `go.mod`.
- **HTTP Framework**: `net/http` built-in routing, per `AGENTS.md`.
- **Database**: Existing SQLite/PostgreSQL/MySQL support via `xorm`; no schema change planned.
- **Backend Dependencies**: Existing direct dependencies only. No new dependency is planned for 007.
- **Frontend**: No production frontend code is planned. Existing frontend stack is React 19, Vite 6, TypeScript strict, verified in `frontend/package.json`; Playwright data-plane test specs may be updated if bridge metadata is asserted there.
- **Testing**: Go `go test ./...`; existing Playwright compatibility tests remain the data-plane E2E surface.
- **Target Platform**: Existing single binary deployment; normal rebuild/restart rules from `AGENTS.md` apply.

## Constitution Check

### First-Principles Gate

- [x] Starts from the actual problem: mixed data-plane behavior currently spans route classification, provider forwarding, request normalization, and response adaptation.
- [x] Motivation is clear: make supported behavior auditable and future provider work safer without changing 006 scope.
- [x] No extra endpoint or provider scope is added.
- [x] Shortest safe path is incremental extraction of explicit bridge boundaries from existing behavior.

### Simplicity Gate

- [x] Uses the existing backend service only.
- [x] No new deployable sub-project.
- [x] No new third-party dependency.
- [x] No speculative provider implementation.

### Anti-Abstraction Gate

- [x] Avoids a configuration DSL such as provider-wide `RequestPolicy` fields.
- [x] Uses code-owned bridge implementations for provider-specific logic.
- [x] Keeps a single selected bridge per request.
- [x] Does not introduce a global canonical model for matching direct pass-through operations.

### Integration-First Gate

- [x] Internal bridge contract is defined in `contracts/internal-bridge.md`.
- [x] Existing 006 compatibility contracts remain authoritative for external behavior.
- [x] Deterministic tests must prove route and bridge behavior before implementation.

### Test-First Gate

- [x] Red tests start with operation/bridge resolution and behavior preservation.
- [x] Acceptance scenarios map to unit, integration, and compatibility tests.
- [x] Live smoke remains opt-in and is not a substitute for deterministic tests.

## Brownfield Findings

| Area | Current State | 007 Implication |
|---|---|---|
| Route identity | `gatewayRoute.Pattern` identifies path pattern but not stable operation id | Add `OpID` so tests and records can refer to a stable semantic operation |
| Bridge identity | Route kind values such as `platform_direct`, `codex_mapping`, `chat_adapter`, `models_facade` partially describe behavior | Add `BridgeID` so direct, facade, and native behavior are explicit |
| Contract identity | Current route metadata does not name client/upstream contracts | Add `ClientContract` and `UpstreamContract` to the selected bridge |
| Request decoding | Direct forwarding, Codex normalization, compact normalization, and Chat adapter decode in different places | Move decode responsibility behind `DecodeClientRequest` while preserving direct pass-through |
| Upstream building | `ForwardGatewayRequestWithCapture` switches by account type and `OAuthBehavior` | Move provider-specific request construction behind `BuildUpstreamRequest` |
| Response adaptation | SSE collection, Chat Completions reconstruction, models facade, and native passthrough live in provider functions | Make `ClientResponseAdapter` explicit as the response-side contract |
| Provider-specific rules | Codex rules are mixed into generic route and forwarding code | Keep Codex-specific behavior inside Codex bridges |
| Unsupported routes | Current classifier rejects unsupported/deferred/blocked before selection | Preserve this as a hard invariant before bridge selection |
| Request records | Records do not have first-class bridge metadata today | Add safe router-owned metadata through the current baseline request-record schema; no compatibility migration |

## Architecture

### Module Boundaries

| Module | Responsibility | Change Type |
|---|---|---|
| `internal/api/proxy_routes.go` | Match inbound method/path/upgrade to data-plane operation and support status | Modified |
| `internal/api/proxy.go` | Orchestrate request lifecycle around operation resolution, bridge selection, forwarding, response adaptation, and recording | Modified |
| `internal/provider/bridge.go` | Define provider-agnostic bridge DTOs, credential class, `OperationBridge`, `ClientResponseAdapter`, bridge registry, and safe `router_metadata.bridge` helpers | New |
| `internal/provider/openai/bridge.go` | Define OpenAI/Codex bridge registry and bridge implementations | New |
| `internal/provider/openai/contracts.go` | Define OpenAI/Codex OpID, BridgeID, and Contract constants over provider-generic types | New |
| `internal/provider/openai/forwarder.go` | Keep low-level HTTP send/header helpers; remove high-level behavior switching where bridges replace it | Modified |
| `internal/provider/openai/codex_request.go` | Reuse Codex normalization inside Codex bridge implementations | Modified |
| `internal/provider/openai/chat_adapter.go` | Reuse Chat Completions conversion inside Chat-to-Codex bridge implementation | Modified |
| `internal/provider/openai/*_test.go` | Add bridge-level tests and preserve existing adapter/normalizer tests | Modified |
| `internal/api/*proxy*_test.go` | Add operation/bridge selection and behavior-preservation tests | Modified |
| `frontend/tests/e2e/data-plane-compat.spec.ts` | Test-only update if deterministic compatibility assertions include bridge metadata | Modified if needed |

### Naming Model

The selected names are part of the 007 design:

```go
type OpID string
type BridgeID string
type Contract string
```

Identifier values use stable prefixes:

```text
op.openai.responses.create
bridge.openai.responses.direct
contract.openai.v1.responses
```

Contract granularity is wire-shape family, not one contract per URL. `OpID` owns exact method/path identity; `Contract` owns request/response/stream shape. Split contracts only when the wire shape or response adaptation differs enough to affect decoding/building/adaptation, such as Responses compact, WebSocket, native Codex, Chat Completions, and Models shapes.

The authoritative bridge interface contract is `contracts/internal-bridge.md`. The summary below shows the intended public boundary with direction-specific accessors:

```go
type OperationBridge interface {
	ID() BridgeID
	OpID() OpID

	ClientContract() Contract
	UpstreamContract() Contract

	DecodeClientRequest(ctx context.Context, in DecodeInput) (ClientRequest, error)
	BuildUpstreamRequest(ctx context.Context, in BuildInput) (UpstreamRequest, ClientResponseAdapter, error)
}
```

`DecodeClientRequest` and `BuildUpstreamRequest` remain separate because they produce clearer error boundaries and finer tests. Bridge implementations may still share private helpers internally.

`contracts/internal-bridge.md#bridge-io-types` defines the required ownership boundaries for `DecodeInput`, `ClientRequest`, `BuildInput`, `UpstreamRequest`, and `ClientResponseAdapter`. `sdd-tasks` must decompose implementation work against those boundaries rather than inventing provider-wide policy structs.

### Operation and Bridge Resolution

Primary flow:

```text
HTTP request
 -> classify method/path/upgrade into OpID or unsupported/blocked
 -> if unsupported/blocked: native data-plane error, no body business-read, no bridge
 -> select eligible account class for the OpID
 -> resolve OperationBridge for OpID + selected account/provider behavior
 -> DecodeClientRequest
 -> BuildUpstreamRequest
 -> send upstream
 -> ClientResponseAdapter adapts provider output to client contract
 -> record request with safe operation/bridge metadata
```

Implementation note (2026-04-27): the data-plane proxy executes this flow through `OperationBridge.DecodeClientRequest` and `OperationBridge.BuildUpstreamRequest` for HTTP and WebSocket operations. HTTP sends the bridge-produced `UpstreamRequest` through `Client.ForwardBridgeRequestWithCapture` and applies the bridge-selected `ClientResponseAdapter`; WebSocket dials the bridge-produced upstream URL/headers through `Client.DialBridgeWebSocket`. Older high-level forwarder entrypoints remain for existing non-bridge callers.

Contract mismatch rule:

```text
ClientContract == UpstreamContract
  -> direct pass-through or operation-defined normalization only

ClientContract != UpstreamContract
  -> bridge must explicitly transform request and adapt response
```

The bridge may use a canonical intent internally, but the framework does not force a global canonical DTO. The invariant is explicit handling of the contract gap.

### Initial OpID Set

The implementation MUST define OpIDs for all currently supported Feature 006 rows. The names below are the planned baseline.

| OpID | Client-Facing Operation |
|---|---|
| `op.openai.responses.create` | `POST /v1/responses` |
| `op.openai.responses.websocket` | `WS /v1/responses` |
| `op.openai.responses.retrieve` | `GET /v1/responses/{response_id}` |
| `op.openai.responses.delete` | `DELETE /v1/responses/{response_id}` |
| `op.openai.responses.cancel` | `POST /v1/responses/{response_id}/cancel` |
| `op.openai.responses.input_items.list` | `GET /v1/responses/{response_id}/input_items` |
| `op.openai.responses.input_tokens.create` | `POST /v1/responses/input_tokens` |
| `op.openai.responses.compact` | `POST /v1/responses/compact` |
| `op.openai.conversations.create` | `POST /v1/conversations` |
| `op.openai.conversations.retrieve` | `GET /v1/conversations/{conversation_id}` |
| `op.openai.conversations.update` | `POST /v1/conversations/{conversation_id}` |
| `op.openai.conversations.delete` | `DELETE /v1/conversations/{conversation_id}` |
| `op.openai.conversations.items.create` | `POST /v1/conversations/{conversation_id}/items` |
| `op.openai.conversations.items.list` | `GET /v1/conversations/{conversation_id}/items` |
| `op.openai.conversations.items.retrieve` | `GET /v1/conversations/{conversation_id}/items/{item_id}` |
| `op.openai.conversations.items.delete` | `DELETE /v1/conversations/{conversation_id}/items/{item_id}` |
| `op.openai.chat_completions.create` | `POST /v1/chat/completions` |
| `op.openai.chat_completions.list` | `GET /v1/chat/completions` |
| `op.openai.chat_completions.retrieve` | `GET /v1/chat/completions/{completion_id}` |
| `op.openai.chat_completions.update` | `POST /v1/chat/completions/{completion_id}` |
| `op.openai.chat_completions.delete` | `DELETE /v1/chat/completions/{completion_id}` |
| `op.openai.chat_completions.messages.list` | `GET /v1/chat/completions/{completion_id}/messages` |
| `op.openai.models.list` | `GET /v1/models` |
| `op.openai.models.retrieve` | `GET /v1/models/{model}` |
| `op.codex_native.responses.create` | `POST /backend-api/codex/responses` |
| `op.codex_native.responses.websocket` | `WS /backend-api/codex/responses` |
| `op.codex_native.responses.compact` | `POST /backend-api/codex/responses/compact` |
| `op.codex_native.models.list` | `GET /backend-api/codex/models` |
| `op.codex_native.transcribe` | `POST /backend-api/transcribe` |

Deferred, blocked, and unsupported routes are not assigned supported OpIDs. They resolve to unsupported or blocked classifier outcomes before bridge selection.

### Initial Bridge Families

| BridgeID | ClientContract | UpstreamContract | Applies To |
|---|---|---|---|
| `bridge.openai.responses.direct` | `contract.openai.v1.responses` | `contract.openai.v1.responses` | API-key non-compact HTTP Responses operations |
| `bridge.openai.responses.compact.direct` | `contract.openai.v1.responses.compact` | `contract.openai.v1.responses.compact` | API-key `POST /v1/responses/compact` pass-through |
| `bridge.openai.conversations.direct` | `contract.openai.v1.conversations` | `contract.openai.v1.conversations` | API-key Conversations operations |
| `bridge.openai.chat_completions.direct` | `contract.openai.v1.chat_completions` | `contract.openai.v1.chat_completions` | API-key Chat Completions operations |
| `bridge.openai.models.direct` | `contract.openai.v1.models` | `contract.openai.v1.models` | API-key Models operations |
| `bridge.openai.responses.to_codex` | `contract.openai.v1.responses` | `contract.chatgpt.backend_api.codex.responses` | OAuth `POST /v1/responses` |
| `bridge.openai.responses.websocket.to_codex` | `contract.openai.v1.responses.websocket` | `contract.chatgpt.backend_api.codex.responses.websocket` | OAuth `WS /v1/responses` |
| `bridge.openai.responses.compact.to_codex` | `contract.openai.v1.responses.compact` | `contract.chatgpt.backend_api.codex.responses.compact` | OAuth `POST /v1/responses/compact` |
| `bridge.openai.chat_completions.to_codex` | `contract.openai.v1.chat_completions` | `contract.chatgpt.backend_api.codex.responses` | OAuth `POST /v1/chat/completions` |
| `bridge.openai.models.from_codex` | `contract.openai.v1.models` | `contract.chatgpt.backend_api.codex.models` | OAuth `GET /v1/models` |
| `bridge.codex_native.responses.direct` | `contract.chatgpt.backend_api.codex.responses` | `contract.chatgpt.backend_api.codex.responses` | OAuth native Codex responses |
| `bridge.codex_native.responses.websocket.direct` | `contract.chatgpt.backend_api.codex.responses.websocket` | `contract.chatgpt.backend_api.codex.responses.websocket` | OAuth native Codex WebSocket |
| `bridge.codex_native.responses.compact.direct` | `contract.chatgpt.backend_api.codex.responses.compact` | `contract.chatgpt.backend_api.codex.responses.compact` | OAuth native compact |
| `bridge.codex_native.models.direct` | `contract.chatgpt.backend_api.codex.models` | `contract.chatgpt.backend_api.codex.models` | OAuth native models |
| `bridge.codex_native.transcribe.direct` | `contract.chatgpt.backend_api.transcribe` | `contract.chatgpt.backend_api.transcribe` | OAuth native transcribe |

Direct bridges may share implementation where behavior is truly identical, but they must stay identifiable by bridge id.

### Credential-Class Bridge Matrix

This matrix is the implementation and test source for credential-specific bridge selection. "No bridge" means the route may still be a supported client-facing operation for another credential class, but this credential class must not select an account or attempt upstream.

| OpID / Group | API-Key Behavior | OAuth Behavior | Response Mode | Body Policy |
|---|---|---|---|---|
| `op.openai.responses.create` | `bridge.openai.responses.direct` | `bridge.openai.responses.to_codex` | JSON or SSE | JSON capture allowed |
| `op.openai.responses.websocket` | No bridge | `bridge.openai.responses.websocket.to_codex` | WebSocket | WebSocket no body |
| `op.openai.responses.retrieve`, `op.openai.responses.delete`, `op.openai.responses.cancel`, `op.openai.responses.input_items.list`, `op.openai.responses.input_tokens.create` | `bridge.openai.responses.direct` | No bridge | JSON | JSON capture allowed; no body expected for GET/DELETE |
| `op.openai.responses.compact` | `bridge.openai.responses.compact.direct` | `bridge.openai.responses.compact.to_codex` | JSON | JSON capture allowed |
| `op.openai.conversations.*` | `bridge.openai.conversations.direct` | No bridge | JSON | JSON capture allowed; no body expected for GET/DELETE |
| `op.openai.chat_completions.create` | `bridge.openai.chat_completions.direct` | `bridge.openai.chat_completions.to_codex` | JSON or SSE | JSON capture allowed |
| `op.openai.chat_completions.list`, `op.openai.chat_completions.retrieve`, `op.openai.chat_completions.update`, `op.openai.chat_completions.delete`, `op.openai.chat_completions.messages.list` | `bridge.openai.chat_completions.direct` | No bridge | JSON | JSON capture allowed; no body expected for GET/DELETE |
| `op.openai.models.list` | `bridge.openai.models.direct` | `bridge.openai.models.from_codex` | JSON | JSON capture allowed; no body expected |
| `op.openai.models.retrieve` | `bridge.openai.models.direct` | No bridge | JSON | JSON capture allowed; no body expected |
| `op.codex_native.responses.create` | No bridge | `bridge.codex_native.responses.direct` | JSON or SSE | JSON capture allowed |
| `op.codex_native.responses.websocket` | No bridge | `bridge.codex_native.responses.websocket.direct` | WebSocket | WebSocket no body |
| `op.codex_native.responses.compact` | No bridge | `bridge.codex_native.responses.compact.direct` | JSON | JSON capture allowed |
| `op.codex_native.models.list` | No bridge | `bridge.codex_native.models.direct` | JSON | JSON capture allowed; no body expected |
| `op.codex_native.transcribe` | No bridge | `bridge.codex_native.transcribe.direct` | JSON | Capture disabled |

The test suite MUST include negative credential-class assertions derived from this matrix, not only route-level positive classification.

### ClientResponseAdapter

`ClientResponseAdapter` names the downstream response behavior. Planned adapter families:

- `PassthroughClientResponseAdapter`
- `OpenAIResponsesJSONAdapter`
- `OpenAIResponsesSSEAdapter`
- `ChatCompletionsJSONAdapter`
- `ChatCompletionsSSEAdapter`
- `OpenAIModelsListAdapter`
- `CodexNativePassthroughAdapter`
- `WebSocketRelayAdapter`

The adapter is selected by the bridge during `BuildUpstreamRequest` because downstream shape depends on client stream preference, operation kind, and upstream contract.

## Project Structure

Planned new or modified files only:

```text
internal/api/
├── proxy.go                         # modified orchestration around bridge plan
├── proxy_routes.go                  # modified classifier with OpID and support outcomes
├── proxy_routes_test.go             # modified operation id matrix tests
├── proxy_oauth_test.go              # modified bridge assertion tests
├── proxy_apikey_test.go             # modified direct bridge tests
└── websocket_proxy_test.go          # modified WS bridge tests if metadata is asserted

internal/provider/
├── bridge.go                        # new provider-agnostic bridge DTOs, registry, and router_metadata.bridge helpers
└── openai/
    ├── bridge.go                    # new OpenAI/Codex bridge implementations
    ├── contracts.go                 # new OpenAI/Codex OpID, BridgeID, Contract constants
    ├── forwarder.go                 # modified low-level send helpers
    ├── codex_request.go             # modified/reused by Codex bridges
    ├── chat_adapter.go              # modified/reused by Chat-to-Codex bridge
    ├── bridge_test.go               # new bridge resolution and contract tests
    ├── forwarder_codex_test.go      # modified to assert bridge behavior
└── chat_adapter_test.go             # modified if helper boundaries move
```

OpenAPI admin codegen is required because request-log rows expose the new `router_metadata` audit field.

## Contracts

External data-plane contracts remain the Feature 006 contract files. Feature 007 adds an internal contract:

- `contracts/internal-bridge.md`

That contract defines:

- identifier namespace rules for `op.*`, `bridge.*`, and `contract.*`;
- bridge interface semantics;
- required error boundaries;
- direct/facade/native behavior expectations;
- audit metadata expectations.

## Data Model

No new versioned migration is planned. Because this project has not shipped the schema yet, Feature 007 updates the current baseline DDL directly instead of adding a compatibility migration.

Feature 007 adds transient in-process architecture entities plus additive bridge metadata on new request records:

- Operation Resolution
- Operation Bridge
- Client Request
- Upstream Request
- Client Response Adapter
- Bridge Audit Metadata

Safe operation and bridge metadata MUST be stored for supported bridge traffic under the request-record `router_metadata.bridge` JSON namespace. `model_params` remains reserved for client/model request parameters and adapter-derived model metadata.

Required shape:

```json
{
  "router_metadata": {
    "bridge": {
      "op_id": "op.openai.chat_completions.create",
      "bridge_id": "bridge.openai.chat_completions.to_codex",
      "client_contract": "contract.openai.v1.chat_completions",
      "upstream_contract": "contract.chatgpt.backend_api.codex.responses",
      "credential_class": "oauth"
    }
  }
}
```

Historical rows without this metadata remain readable.

## Implementation Phases

1. **Red tests for operation and bridge resolution**
   - Add table tests proving supported 006 routes resolve to expected OpID and bridge family for API-key and OAuth paths.
   - Add tests proving unsupported/deferred/blocked routes resolve before body reads and have no bridge.
2. **Introduce identifiers and classifier metadata**
   - Add `OpID`, `BridgeID`, and `Contract` constants.
   - Extend route classification with OpID and bridge-eligible account metadata without changing external behavior.
3. **Introduce bridge interfaces and direct bridges**
   - Add provider-generic `OperationBridge`, `ClientRequest`, `UpstreamRequest`, and `ClientResponseAdapter` under `internal/provider`, with OpenAI/Codex implementations under `internal/provider/openai`.
   - Implement API-key direct bridges using existing low-level forwarding behavior.
4. **Move OAuth Codex mappings behind bridges**
   - Implement Responses-to-Codex, compact-to-Codex, models-from-Codex, and native Codex direct bridges over existing normalizers and forwarders.
5. **Move Chat Completions facade behind a bridge**
   - Keep existing validation and conversion behavior, but expose it as `bridge.openai.chat_completions.to_codex`.
   - Preserve JSON and SSE response adaptation.
6. **Move WebSocket behavior behind bridges**
   - Preserve Codex WebSocket relay behavior while constructing upstream URL/header decisions through explicit bridge ids and contracts.
7. **Recording and observability**
   - Add safe operation/bridge metadata to request recording through existing JSON metadata fields; do not introduce a schema migration.
   - Ensure secret and frame-payload redaction remains intact.
8. **Compatibility verification**
   - Run existing 006 deterministic compatibility tests.
   - Run full backend tests and targeted E2E/live smoke only as documented.

## Test Plan

Tests are written before implementation changes for each phase.

### Contract and Classifier Tests

- All supported Feature 006 operations resolve to the expected OpID.
- API-key selected routes resolve to direct bridges.
- OAuth selected routes resolve to Codex facade/native bridges.
- Unsupported/deferred/blocked routes resolve to no bridge and no account selection.
- `/backend-api/*` remains exact allowlist only.
- `/api/codex/*` remains outside data-plane bridge selection.
- `GET /api/admin/usage` remains an Admin API envelope endpoint and is not assigned an OpID or BridgeID.
- The bridge matrix tests MUST cover both positive and negative credential eligibility for every supported OpID: API-key bridge or no-eligible-account outcome, OAuth bridge or no-eligible-account outcome, response mode, and body policy. Required negative examples include API-key `WS /v1/responses`, API-key `/backend-api/*`, OAuth Conversations, OAuth stored Chat Completions, OAuth `GET /v1/models/{model}`, and both credential classes for deferred `/v1/audio/transcriptions`.

### Bridge Unit Tests

- `DecodeClientRequest` returns client request objects with the expected contract.
- `BuildUpstreamRequest` returns the expected upstream contract, target path, header behavior, body behavior, and response adapter.
- Direct API-key bridges preserve raw body/path/query and do not apply Codex normalization.
- OAuth Responses-to-Codex bridge normalizes `store`, `stream`, unsupported upstream fields, and native/facade collection rules as currently implemented.
- OAuth Chat Completions bridge preserves existing allowlist/unsupported-field behavior and maps stream/non-stream response adapters correctly.
- Native Codex bridges preserve native response semantics.
- WebSocket bridge never exposes or records frame payloads.
- Bridge selection and metadata construction have a benchmark or deterministic timing check that demonstrates the NFR-005 overhead target is not exceeded under local test conditions.
- The NFR-005 check should reuse the existing data-plane compatibility/local mock environment where possible and assert a median added bridge-selection overhead of <= 1 ms; if runtime noise makes the measurement unstable, the task must document the measured value and keep the check non-flaky while preserving the NFR target for review.

### Integration Tests

- Existing `ProxyHandler` tests for API-key direct forwarding continue to pass.
- Existing OAuth proxy tests continue to pass, with bridge assertions added.
- Existing Codex normalizer tests continue to pass.
- Existing Chat adapter tests continue to pass.
- Existing WebSocket proxy tests continue to pass.
- Existing Admin usage tests continue to pass, but are not bridge-selection tests.

### E2E and Smoke

- `make data-plane-compat` remains the primary deterministic compatibility gate.
- `make live-upstream-compat` remains opt-in only and must skip without live credentials.
- Live smoke does not replace local tests for bridge selection or unsupported-route no-upstream behavior.

## Risk Assessment

| Risk | P | I | Mitigation | Verification |
|---|---|---|---|---|
| Refactor changes route eligibility | M | H | Start with classifier/bridge golden matrix from 006 | Route matrix tests before implementation |
| Direct API-key paths accidentally inherit Codex normalization | M | H | Separate direct bridges and Codex bridges | API-key body preservation tests |
| Native backend-api responses are accidentally collected into OpenAI facade JSON | M | H | Separate native Codex bridges and ClientResponseAdapter tests | Native SSE passthrough tests |
| Chat Completions facade loses unsupported-field validation | M | M | Reuse existing adapter tests and add bridge-level tests | Unknown/unsupported field tests |
| Request records leak bridge metadata with secrets | L | H | Metadata is ids/classes only, no headers/body/token values | Log scrub and record assertions |
| New abstraction becomes too broad | M | M | No provider-wide policy DSL; bridges own code behavior | Code review against internal contract |
| Rollback becomes hard | L | M | No DB/config migrations; keep low-level forwarding helpers intact | Rollback review and test suite |

## Security Considerations

- **Authentication**: No new downstream client authentication or admin authentication is introduced.
- **Authorization**: Existing account selection and setup gate behavior remain unchanged.
- **Input validation**: Existing request validation remains operation-specific; unsupported routes reject before business body reads.
- **Sensitive data**: Bridge metadata may include operation id, bridge id, contract ids, credential class, and response mode only. It must not include API keys, OAuth tokens, cookies, auth.json contents, request body bytes, or WebSocket frame payloads.
- **SSRF/egress**: Bridge implementations must use existing account base URL and ChatGPT Codex base URL logic. No client-provided upstream host is introduced.
- **Provider boundary**: OAuth/Codex bridges must not forward ChatGPT OAuth tokens to OpenAI Platform `/v1/*` upstreams.

## Quality Gates

- `gofmt -w` on changed Go files
- `golangci-lint run`
- `go test ./...`
- `go build ./...`
- `make data-plane-compat`
- `bash scripts/log-scrub.sh test-output/` after compatibility runs when logs are produced
- Bridge-selection overhead check for NFR-005, implemented as a benchmark or deterministic local timing check
- Opt-in only: `make live-upstream-compat`

## Complexity Tracking

No constitution deviation is accepted.

The feature is complex because it modifies multiple backend modules and touches several transport shapes. It remains bounded because it introduces no new endpoint families, dependencies, database migrations, admin APIs, or frontend screens.
