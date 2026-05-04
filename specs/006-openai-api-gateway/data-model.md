# Data Model: Full OpenAI API Gateway

**Feature**: 006-openai-api-gateway
**Spec**: `specs/006-openai-api-gateway/spec.md`
**Created**: 2026-04-26
**Status**: Updated 2026-05-02 for request client IP observability

The original planned 006 scope did not require a persistent schema migration. The implemented request-log observability scope now adds one request-record schema expansion: `request_records.client_ip`, storing the router-observed downstream peer IP for new request records.

## Entity: Gateway Route

Pure in-process classification result for one inbound data-plane request.

| Attribute | Type | Constraints | Description |
|---|---|---|---|
| `method` | string | HTTP method or `WS` pseudo-method | Client-facing method |
| `path_pattern` | string | Must match one 006 matrix row or unsupported family | Normalized route pattern |
| `client_path` | string | Required | Actual request path for recording and upstream direct-forward |
| `route_kind` | enum | `platform_direct`, `codex_mapping`, `chat_adapter`, `unsupported`, `blocked` | Decides handler behavior |
| `priority` | enum | `P0`, `P1`, `Deferred`, `Blocked`, `Unsupported` | Trace back to spec matrix |
| `allowed_credentials` | set | `api_key`, `oauth`, or empty | Account classes eligible for selection |
| `upstream_path` | string | Required for provider routes | Exact provider path for mapping; never derived by wildcard for OAuth |
| `response_mode` | enum | `json`, `sse`, `websocket` | Expected response or connection mechanics; multipart request bodies are represented by `body_policy` |
| `body_policy` | enum | `json_capture_allowed`, `capture_disabled`, `websocket_no_body` | Controls body read/capture/logging |

### Invariants

- Classification runs before account selection.
- Unsupported, deferred, and blocked routes never attempt upstream and never select an account.
- OAuth routes must be explicitly listed. OAuth accounts are never eligible for arbitrary `/v1/*`, `/backend-api/*`, or `/api/*`.
- Router-local usage observability is not part of Gateway Route classification; it is served by the Admin API.

## Entity: Gateway Request Record

Existing `request_records` row, reused for every routed 006 data-plane request.

| Existing Column | 006 Use |
|---|---|
| `request_id` | Router correlation id, also returned as `X-Request-Id` |
| `client_ip` | Router-observed downstream peer IP extracted from Go `net/http` `Request.RemoteAddr`; empty string means unavailable, legacy, or unparsable peer address |
| `upstream_account_id` | Selected account id for provider routes; null for unsupported routes |
| `session_key` | Existing Codex session affinity key where present |
| `method` | HTTP method, or `WS` for WebSocket records |
| `path` | Exact client-facing data-plane path, including selected `/backend-api/*` |
| `status_code` | Provider status, router-native error status, or WebSocket close-derived status |
| `latency_ms` | End-to-end elapsed time |
| `ttft_ms` | SSE first text delta timing when observable; null otherwise |
| `outcome` | Existing outcome strings plus router outcomes for unsupported/blocked/WebSocket errors |
| `error_code` | Provider error code or router-native symbol such as `unsupported_endpoint` |
| `model` | Extracted from JSON request body when safe and available |
| `model_params` | Safe client/model parameters and adapter-derived model metadata |
| `router_metadata` | Router-owned audit metadata such as route kind, credential class, and operation bridge details |
| `response_mode` | `json`, `sse`, or `websocket`; `/backend-api/transcribe` records the provider-compatible response mode, normally `json` |
| `token_usage` | Extracted usage map when provider response has usage |
| `client_request_body` | Captured only for safe JSON and only when runtime toggle allows |
| `upstream_request_body` | Captured only for safe JSON upstream bodies and only when runtime toggle allows |
| `upstream_response_body` | Captured only for safe JSON/SSE aggregates and only when runtime toggle allows |

### Body-Capture Invariants

- Multipart, audio, binary, and WebSocket payloads are not captured.
- API keys, bearer tokens, OAuth tokens, `auth.json` fields, cookies, and authorization headers are never persisted.
- Unsupported-route records may include route metadata but never request body bytes.
- Unsupported, deferred, and blocked routes are classified before any business request-body read; server-level connection/body caps may still reject pathological requests before routing.
- `response_mode` value `websocket` requires Admin request-log OpenAPI enum regeneration so request logs can render and filter WebSocket rows.
- `client_ip` is the direct TCP peer IP observed by this router process. `X-Forwarded-For`, `X-Real-IP`, and other forwarded headers are ignored until a future trusted-proxy configuration explicitly defines which hops may be trusted.

## Entity: Admin Usage Summary

Envelope `data` object for `GET /api/admin/usage`.

| Attribute | Type | Constraints | Description |
|---|---|---|---|
| `request_count` | integer | Non-negative | Count of retained data-plane request records in the computed scope |
| `total_tokens` | integer | Non-negative | Sum of known input/output tokens; missing usage contributes zero |
| `cached_input_tokens` | integer | Non-negative | Sum of known cached input tokens |
| `total_cost_usd` | number | Non-negative; `0` when unknown | Current router has no pricing table, so 006 does not fabricate cost |
| `limits` | array | Empty unless future client-key quota feature provides real limits | Router-local quota/limit facts when real local rows exist |

### Invariants

- The operation is a router-owned Admin API and must use the `{code,msg,data}` envelope.
- In MVP without client-key identity, scope is global retained data-plane request records.
- Missing usage fields are absence, not zero-usage facts for a single request.
- Cost is `0` only because no cost model exists; implementation must not estimate cost.

## Entity: Admin Codex Account Usage Status

Nested object in `GET /api/admin/usage` for local Codex account status.

| Attribute | Type | Constraints | Description |
|---|---|---|---|
| `plan_type` | string | Required | `guest`, a single active OAuth plan type, `mixed`, or `unknown` |
| `rate_limit` | object or null | Null when no real ChatGPT quota window is known | Local rate-limit status when real data exists |
| `credits` | object or null | Null when no credit balance source exists | Local credit status when real data exists |
| `additional_rate_limits` | array | Empty when no additional limits are known | Codex-lb-compatible extension field |

### Invariants

- The Admin API must not fetch ChatGPT usage in 006.
- Null/empty quota fields are preferred over synthetic limits.
- The data is returned inside the Admin API envelope.

## Entity: WebSocket Relay Session

Transient in-process relay for `WS /v1/responses` and `WS /backend-api/codex/responses`.

| Attribute | Type | Constraints | Description |
|---|---|---|---|
| `request_id` | string | Required | Router correlation id |
| `turn_state` | string | Required; reused from inbound header or generated | Codex turn-state affinity value |
| `account_id` | integer | Required after selection | OAuth account used for upstream socket |
| `client_path` | string | Required | `/v1/responses` or `/backend-api/codex/responses` |
| `upstream_path` | string | Required | ChatGPT Codex WebSocket path |
| `started_at` | timestamp | Required | Used for latency |
| `close_code` | integer | Optional | Final close code when available |
| `outcome` | enum | success, upstream_error, router_error, cancelled, no_available_account | Terminal relay outcome |

### Invariants

- WebSocket sessions do not store frame payloads.
- Upstream OAuth bearer and `chatgpt-account-id` are never reflected downstream.
- Client abort closes upstream promptly; upstream close is propagated downstream.

## Entity: OAuth Chat Completions Adapter

Transient adapter model for `POST /v1/chat/completions` when an OAuth account is selected.

| Attribute | Type | Constraints | Description |
|---|---|---|---|
| `request_model` | string | Required | Original Chat Completions model |
| `messages` | array | Required | Converted into Codex Responses input/instructions |
| `stream` | boolean | Optional | Controls downstream Chat chunk vs object response |
| `stream_options.include_usage` | boolean | Optional | Controls terminal usage chunk when supported by adapter |
| `unsupported_fields` | array | Optional | Non-empty list rejects before upstream |

### Invariants

- API-key Chat Completions are direct pass-through and do not use this adapter.
- OAuth adapter rejects unsupported fields with native router error rather than silently dropping client intent.
- Adapter output must be Chat Completions-compatible enough for SDK parsing tests.

## Migration Strategy

- **Forward**: No database migration.
- **Rollback**: No database rollback.
- **Compatibility**: Existing request history rows remain valid. New `response_mode` string value `websocket` fits the existing text column, but Admin request-log row/filter APIs must expand their generated enum to accept it.

If later implementation proves quota windows or client-key usage scoping require persistence, that is a separate spec change because it affects auth/client-key scope and possibly migrations.
