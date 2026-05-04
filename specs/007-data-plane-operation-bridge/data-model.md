# Data Model: Data-Plane Operation Bridge

**Feature**: 007-data-plane-operation-bridge
**Spec**: `specs/007-data-plane-operation-bridge/spec.md`
**Created**: 2026-04-27
**Status**: Implemented

No persistent schema migration is required.

Feature 007 introduces in-process architecture entities and writes safe operation/bridge metadata for new supported bridge traffic into existing request-record JSON metadata. Existing `request_records`, upstream account tables, config files, and admin API response shapes remain compatible.

## Entity: Operation Resolution

Pure in-process classification result for a client-facing data-plane request.

| Attribute | Type | Constraints | Description |
|---|---|---|---|
| `op_id` | string | Required for supported routes; prefix `op.` | Stable client-facing operation identity |
| `method` | string | Required | HTTP method or `WS` pseudo-method |
| `path_pattern` | string | Required | Normalized supported path pattern |
| `response_mode` | enum | `json`, `sse`, `websocket` | Expected downstream response mechanics |
| `body_policy` | enum | Existing gateway body policy values | Controls business body reads and capture |
| `api_key_eligible` | boolean | Required | Whether API-key accounts can handle the operation |
| `oauth_eligible` | boolean | Required | Whether OAuth/Codex accounts can handle the operation |

### Invariants

- Unsupported, deferred, and blocked routes do not receive supported OpIDs.
- Operation resolution happens before account selection and before business request-body reads.
- `/backend-api/*` remains an exact allowlist.
- `/api/codex/*` remains outside data-plane operation resolution.

## Entity: Operation Bridge

Executable bridge selected after operation resolution and account-class behavior selection.

| Attribute | Type | Constraints | Description |
|---|---|---|---|
| `bridge_id` | string | Required; prefix `bridge.` | Stable bridge implementation identity |
| `op_id` | string | Required | Operation the bridge handles |
| `client_contract` | string | Required; prefix `contract.` | Client-facing request/response contract |
| `upstream_contract` | string | Required; prefix `contract.` | Provider upstream contract |
| `credential_class` | enum | `api_key`, `oauth` | Selection class |
| `response_adapter` | string | Required | Client response adaptation family |

### Invariants

- Exactly one bridge is selected for a supported provider request after account-class selection.
- When `client_contract != upstream_contract`, the bridge must explicitly transform request and response.
- Bridge identity is safe to log; raw credentials and frame payloads are not.

## Entity: Client Request

Parsed request in the client-facing contract accepted by the selected operation.

| Attribute | Type | Constraints | Description |
|---|---|---|---|
| `contract` | string | Required | Must equal the bridge client contract |
| `stream_requested` | boolean | Required when applicable | Downstream stream preference |
| `raw_body` | bytes or stream | Bounded or disabled by body policy | Raw request body for direct paths or decoding |
| `metadata` | object | Safe fields only | Model, route family, or validation metadata when safe |

### Invariants

- Direct API-key requests may remain shallow/raw client requests.
- Multipart and WebSocket operations must not force JSON decoding.
- Invalid client contract errors are distinguishable from unsupported upstream intent.

## Entity: Upstream Request

Provider-facing execution plan produced by a bridge.

| Attribute | Type | Constraints | Description |
|---|---|---|---|
| `contract` | string | Required | Must equal the bridge upstream contract |
| `method` | string | Required for provider calls | Upstream method |
| `url` | string | Required for provider calls | Final upstream URL |
| `headers` | object | Sanitized | Forwardable provider headers after rewrite/drop rules |
| `body` | bytes or stream | Bounded by operation | Upstream body |

### Invariants

- OAuth/Codex bridges do not send OAuth tokens to OpenAI Platform `/v1/*` upstreams.
- API-key direct bridges do not apply OAuth/Codex-only normalization.
- Admin usage is not represented as an Upstream Request because it is outside data-plane bridge resolution.

## Entity: Client Response Adapter

Response-side behavior that returns the correct client-facing contract.

| Attribute | Type | Constraints | Description |
|---|---|---|---|
| `adapter_kind` | string | Required | Passthrough, facade JSON, facade SSE, native, or WebSocket |
| `client_contract` | string | Required | Contract returned to the downstream client |
| `streaming` | boolean | Required | Whether it streams downstream |
| `collects_upstream` | boolean | Required | Whether upstream SSE is collected for non-streaming facade output |

### Invariants

- Native Codex responses are not collected into OpenAI facade JSON.
- OpenAI facade non-streaming responses may collect upstream SSE only when the operation contract requires it.
- WebSocket adapters do not persist frame payloads.

## Entity: Bridge Audit Metadata

Safe metadata attached to request records and test traces.

| Attribute | Type | Constraints | Description |
|---|---|---|---|
| `router_metadata.bridge.op_id` | string | Required for new supported bridge traffic; safe | Supported operation id |
| `router_metadata.bridge.bridge_id` | string | Required for new supported bridge traffic; safe | Selected bridge id |
| `router_metadata.bridge.client_contract` | string | Required for new supported bridge traffic; safe | Client contract name |
| `router_metadata.bridge.upstream_contract` | string | Required for new supported bridge traffic; safe | Upstream contract name |
| `router_metadata.bridge.upstream_endpoint` | string | Present after an upstream request is built; safe | Actual downstream path sent from the router to the LLM server, e.g. `/codex/responses`. This is not the client entry path stored in `request_records.path`. |
| `router_metadata.bridge.credential_class` | string | Required for new supported bridge traffic; safe | API-key or OAuth |

### Invariants

- Metadata must not include tokens, cookies, authorization headers, auth.json fields, request body bytes, upstream body bytes, response body bytes, or WebSocket frame payloads.
- Metadata must be stored under `router_metadata.bridge`; `model_params` must remain reserved for model/client parameters and adapter-derived model metadata.
- Existing historical request records without metadata remain valid and readable.

## Migration Strategy

- **Forward**: No new migration version. Update the current baseline DDL and domain model to include `request_records.router_metadata`, then write safe bridge metadata for new supported bridge traffic into that field.
- **Rollback**: Revert code to previous route/forwarder architecture. Existing request records remain readable.
- **Data compatibility**: This project has not shipped the baseline schema yet, so no compatibility migration is required. Existing accounts and config are unchanged. Historical test/dev rows may omit `router_metadata`.
