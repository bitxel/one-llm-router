# Feature Spec: Data-Plane Operation Bridge

**ID**: 007-data-plane-operation-bridge
**Created**: 2026-04-27
**Updated**: 2026-04-27
**Status**: Implemented

## Overview

Feature 007 makes the data-plane gateway architecture explicit around operation-level bridges. The router already supports a mixed data plane: OpenAI-compatible `/v1/*` paths, selected ChatGPT Codex-native `/backend-api/*` paths, direct API-key forwarding, and OAuth-backed Codex compatibility adapters. Router-local usage observability remains a separate Admin API concern at `GET /api/admin/usage`, outside data-plane bridge selection. The data-plane mix is useful, but the current behavior must remain easy to audit, test, and extend without hiding provider-specific transformations inside broad forwarding logic.

The feature requires every supported client-facing data-plane operation to declare what operation it represents, what client contract it accepts, what upstream contract it will call after account selection, and what client response shape it returns. When the client contract and selected upstream contract differ, the bridge must explicitly adapt the request and response. When they match, the bridge may pass through or normalize only as documented by that operation. This preserves Feature 006 behavior while making future provider additions safer.

This is a refactoring and architecture hardening feature. It must not add new endpoint families, change account eligibility, weaken unsupported-route behavior, or turn ChatGPT OAuth accounts into general OpenAI Platform credentials.

## User Scenarios

### US-1: Audit Data-Plane Behavior Per Operation (P0)

As a platform operator, I want each data-plane request to resolve to a stable operation and bridge, so that I can audit why a request was allowed, rejected, transformed, or forwarded.

**Acceptance Scenarios**:
1. **Given** a supported client-facing data-plane request, **when** the router handles it, **then** the request resolves to exactly one stable operation id and one selected bridge id before any upstream call.
2. **Given** an unsupported, deferred, or blocked data-plane path, **when** the router handles it, **then** no bridge is selected and no upstream account is contacted.
3. **Given** a request record is created for a supported bridge, **when** an operator inspects the record, **then** it identifies the client-facing operation family, selected account class, response mode, and router outcome without exposing secrets.

**Edge Cases**:
- Removed compatibility aliases such as `/api/codex/usage` and `/api/codex/usage/` → remain outside data-plane bridge selection rather than reappearing as wildcard forwarding.
- WebSocket upgrade requests → resolve before request-body processing because they have no JSON body.
- Admin usage route `GET /api/admin/usage` → remains an enveloped router-owned Admin API and never claims a provider or bridge result.

### US-2: Preserve Existing Client Contracts While Routing to Different Upstreams (P0)

As an internal client developer, I want OpenAI-compatible and Codex-native clients to keep their current request and response contracts, so that Feature 007 does not require SDK, CLI, or integration changes.

**Acceptance Scenarios**:
1. **Given** an API-key account is selected for an OpenAI-compatible operation, **when** the client contract and upstream contract match, **then** the router preserves the provider-compatible method, path, query, request body, status, content type, and response body except for credential substitution and documented header filtering.
2. **Given** a ChatGPT OAuth account is selected for an OpenAI-compatible operation backed by Codex, **when** the client contract and upstream contract differ, **then** the bridge explicitly adapts the request and returns the original client-facing response contract.
3. **Given** a Codex-native client calls a supported `/backend-api/*` operation, **when** the upstream contract is Codex-native, **then** the response remains Codex-native and is not converted into an OpenAI facade.
4. **Given** a client uses streaming or non-streaming mode on a supported facade operation, **when** the selected upstream requires streaming transport, **then** the bridge preserves the downstream client contract by either streaming or collecting according to the operation contract.

**Edge Cases**:
- Upstream Codex transport may require `stream:true` even when the client requested non-streaming → the bridge must adapt downstream output rather than exposing upstream transport details.
- Native `/backend-api/codex/responses` requests with non-streaming-looking payloads → keep Codex-native response semantics instead of collecting into OpenAI JSON.
- Provider errors → return the client-facing data-plane error/status contract, not an admin envelope.

### US-3: Keep Provider-Specific Rules Local to Bridges (P0)

As a protocol adapter maintainer, I want provider-specific request and response rules to live inside the selected bridge, so that adding or modifying a provider does not spread provider exceptions across route matching, account selection, logging, and response copying.

**Acceptance Scenarios**:
1. **Given** a provider has operation-specific field or streaming rules, **when** a bridge handles that provider, **then** those rules are implemented by the bridge selected for that operation and not as provider-wide global flags.
2. **Given** an OpenAI-compatible operation uses API-key direct forwarding, **when** a provider-specific Codex rule exists for the same client path under OAuth, **then** the API-key bridge is unaffected.
3. **Given** a future provider supports an OpenAI-compatible contract with minor differences, **when** that provider is added, **then** it can supply its own bridge behavior without changing the client-facing operation matrix.

**Edge Cases**:
- A provider may support Chat Completions but not Responses → bridges are declared per operation, not per provider-wide capability.
- A provider may require request normalization only for one endpoint → that rule must not affect unrelated operations.
- Tool or field support may differ by operation → bridge validation must fail visibly instead of silently dropping unsupported client intent.

### US-4: Make Compatibility Tests Traceable to Bridges (P0)

As a protocol adapter maintainer, I want tests to map directly to operations and bridges, so that regressions in direct forwarding, facade conversion, native Codex passthrough, unsupported-route rejection, and streaming behavior are caught independently.

**Acceptance Scenarios**:
1. **Given** the compatibility test suite runs, **when** it covers a supported operation, **then** it asserts the operation id, selected bridge id, client contract, upstream contract, response adapter behavior, and no-upstream behavior where applicable.
2. **Given** a bridge converts between different contracts, **when** tests run, **then** they cover successful conversion, invalid client input, unsupported upstream intent, provider error propagation, and streaming/non-streaming response adaptation.
3. **Given** a supported native Codex operation is tested, **when** tests run, **then** they assert that native responses are not converted into OpenAI facade responses.
4. **Given** a direct API-key operation is tested, **when** tests run, **then** they assert that router-owned compatibility normalization is not applied.

**Edge Cases**:
- Unsupported routes with unreadable or oversized bodies → fail before bridge selection and before business body reads.
- WebSocket tests → assert bridge selection and token-safe logging without persisting frame payloads.
- Live smoke tests → remain opt-in and only cover bridge behavior that cannot be proven by deterministic local tests.

### US-5: Preserve 006 Scope and Rollback Safety (P1)

As a platform operator, I want this architecture change to preserve the 006 supported operation matrix and remain easy to roll back, so that structural cleanup does not expand production blast radius.

**Acceptance Scenarios**:
1. **Given** the 006 operation-level coverage matrix, **when** Feature 007 is implemented, **then** every supported, deferred, blocked, and unsupported route keeps the same external eligibility and response behavior.
2. **Given** Feature 007 changes internal architecture only, **when** it ships, **then** no database migration, new admin API, new provider credential type, or new default live network dependency is required.
3. **Given** a rollback is needed, **when** the previous data-plane forwarding architecture is restored, **then** persisted request records and existing configuration remain compatible.

**Edge Cases**:
- Existing historical request records without bridge metadata → remain readable in admin request logs.
- Existing live smoke commands → continue to work without new required environment variables.
- Existing binary deployments → require only the normal rebuild/restart process documented in AGENTS.md.

## Functional Requirements

- **FR-001**: Every supported data-plane request MUST resolve to one stable operation id with the prefix `op.` before account selection or upstream forwarding.
- **FR-002**: Every selected bridge MUST expose one stable bridge id with the prefix `bridge.`.
- **FR-003**: Every selected bridge MUST declare a client contract and an upstream contract using the `contract.` prefix namespace.
- **FR-004**: The router MUST explicitly adapt the request and response when the selected bridge's client contract and upstream contract differ.
- **FR-005**: The router MUST preserve direct provider-compatible pass-through behavior when the selected bridge's client contract and upstream contract match and the operation contract does not define normalization.
- **FR-006**: Native Codex `/backend-api/*` operations MUST remain allowlisted by exact operation and MUST NOT become arbitrary `/backend-api/*` proxying.
- **FR-007**: OpenAI-compatible Responses and Chat Completions facade operations MUST continue to use operation-specific validation and unsupported-field handling where the upstream cannot honestly support client intent.
- **FR-008**: API-key OpenAI-compatible operations MUST NOT inherit OAuth/Codex-specific normalization or field stripping.
- **FR-009**: Router-local usage observability MUST remain outside data-plane bridge resolution and MUST continue to use the Admin API envelope at `GET /api/admin/usage`.
- **FR-010**: Unsupported, deferred, and blocked data-plane routes MUST fail before bridge selection, business request-body reads, account selection, OAuth refresh, or upstream calls.
- **FR-011**: Request records for supported bridge traffic MUST include enough safe metadata to identify the operation family, selected bridge family, credential class, response mode, and outcome without storing credentials or WebSocket frame payloads.
- **FR-012**: Existing external route behavior from Feature 006 MUST remain unchanged unless a future spec explicitly changes the operation matrix.
- **FR-013**: Tests MUST prove direct forwarding, OpenAI-to-Codex facade bridging, Codex-native passthrough, unsupported-route rejection, SSE, WebSocket behavior, and the Admin usage boundary separately.
- **FR-014**: Feature 007 MUST NOT introduce new endpoint groups, new database migrations, new admin APIs, new third-party dependencies, or new default live network calls.

## Key Entities

| Entity | Description |
|---|---|
| Operation | Stable client-facing data-plane action such as creating a response, creating a chat completion, listing models, or calling native Codex responses. |
| Bridge | Selected operation-specific path from a client contract to an upstream contract for a concrete account class. |
| Contract | Named wire-shape family describing request, response, and streaming semantics for the client-facing side or upstream side. OpID remains the method/path-level identity; contracts split only when the wire shape or response adaptation semantics differ, such as compact, WebSocket, native Codex, Chat Completions, or Models shapes. |
| Client Request | Parsed client-facing request in the contract accepted by the selected operation. |
| Upstream Request | Provider-facing request produced by the selected bridge. |
| Client Response Adapter | Behavior that converts or passes through upstream output into the client-facing response contract. |
| Bridge Metadata | Safe request-record metadata used for audit and test traceability. |

## Non-Functional Requirements

| ID | Requirement | Target |
|---|---|---|
| NFR-001 | Compatibility preservation | 100% of Feature 006 supported/deferred/blocked route expectations remain unchanged in deterministic tests. |
| NFR-002 | Pre-body rejection | 100% of unsupported/deferred/blocked route tests assert no business body read before rejection. |
| NFR-003 | Secret safety | 0 API keys, OAuth tokens, cookies, auth.json token fields, or WebSocket frame payloads appear in request records or test logs. |
| NFR-004 | Test traceability | Each P0 bridge family has at least one deterministic test that asserts operation id and bridge id selection. |
| NFR-005 | Performance overhead | Added bridge selection and metadata construction add no more than 1 ms median overhead in local handler benchmarks or equivalent unit timing checks. |
| NFR-006 | Streaming behavior | Supported SSE and WebSocket paths avoid full-response buffering except where a facade operation explicitly requires collection for a non-streaming client response. |
| NFR-007 | Rollback safety | No schema migration and no config file format migration are introduced. |
| NFR-008 | Live test safety | Live smoke remains opt-in and performs 0 network calls when its documented enable flag is absent. |

## Assumptions

- Feature 007 is an internal architecture and compatibility-hardening feature; external data-plane support remains the 006 matrix.
- `op.`, `bridge.`, and `contract.` identifier prefixes are accepted as stable internal namespaces.
- `ClientContract` and `UpstreamContract` are clearer names than `WireContract` for this codebase.
- `ClientResponseAdapter` is the preferred name for response-side adaptation because it describes the response shape returned to the downstream client.
- Existing API-key and OAuth account classes remain unchanged.
- Existing body logging toggles remain unchanged, with bridge metadata subject to the same secret-safety rules as existing request records.

## Out of Scope

- Adding support for new OpenAI endpoint groups such as embeddings, images, files, uploads, audio `/v1/audio/transcriptions`, OpenAI Realtime, or Administration APIs.
- Turning ChatGPT OAuth accounts into general OpenAI Platform credentials.
- Adding client authentication, admin authentication, RBAC, quota enforcement, or billing.
- Adding new provider implementations such as OpenRouter, Doubao, or Qwen; this feature only makes their future addition safer.
- Adding new third-party dependencies.
- Changing the Admin API envelope, generated Admin OpenAPI surface, or frontend navigation.
- Rewriting all existing provider code in one step if behavior-preserving incremental migration can meet the acceptance criteria.

## Success Criteria

- 100% of existing 006 deterministic data-plane compatibility tests still pass after the bridge architecture is introduced.
- The test suite contains operation/bridge selection assertions for direct API-key forwarding, OpenAI-to-Codex facade bridging, Codex-native passthrough, unsupported routes, SSE, WebSocket, and the Admin usage non-bridge boundary.
- A maintainer can identify the selected operation id, bridge id, client contract, and upstream contract for each supported route from deterministic tests and new request-record metadata without reading provider forwarding internals.
- No new database migration, admin API, third-party dependency, or endpoint family is introduced by this feature.
- Live smoke remains opt-in and skips without network calls when credentials or enable flags are absent.

## Clarifications

No unresolved clarification markers remain. Naming decisions resolved during the 2026-04-27 architecture discussion:

- Use `OpID` / `op.*` for client-facing operation identifiers.
- Use `BridgeID` / `bridge.*` for selected bridge identifiers.
- Use `Contract` with `ClientContract` and `UpstreamContract` accessors/fields.
- Use `DecodeClientRequest`, `BuildUpstreamRequest`, `ClientRequest`, and `ClientResponseAdapter`.
