# Feature Spec: Codex Router MVP

**ID**: 001-codex-router-mvp
**Created**: 2026-04-15
**Updated**: 2026-04-25
**Status**: Ready

## Overview

one-llm-router provides a managed entry point for AI coding clients so teams no longer have to distribute and rotate upstream credentials client by client. The first launch focuses on Codex users, proving that a shared routing layer can improve reliability, operator control, and spend visibility while keeping a clear path to support additional protocols later.

**Current implementation amendment (2026-04-25)**: Feature 001's transparent `/v1/*` OpenAI Platform forwarding remains the contract for API-key upstream accounts. Feature 003 adds ChatGPT subscription OAuth accounts; those accounts are not generic OpenAI Platform keys, so `/v1/responses` and `/v1/responses/compact` are adapted to the ChatGPT Codex backend with `chatgpt-account-id` when present. OAuth `/v1/responses` forces upstream SSE because the ChatGPT Codex backend rejects `stream:false`, but downstream still follows the client stream flag: `stream:false` or omitted `stream` returns collected JSON, and `stream:true` returns SSE. Terminal upstream SSE events such as `response.failed` are returned to non-streaming clients as their terminal JSON payload; malformed or oversized upstream SSE returns the native router error `upstream_response_invalid`. Feature 002 relocated router-owned Admin JSON APIs from `/admin/*` to `/api/admin/*` and the SPA now owns `/admin/*`; the current request-history surface is `GET /api/admin/requests`, `GET /api/admin/requests/{id}`, and `GET /api/admin/requests/options`, with OpenAPI as the live route contract.

This is a **new system** — there is no migration from an existing service. Clients onboard directly to the router.

## User Scenarios

### US-1: Stable Codex Access (Priority: P0)

Internal client developers need a single managed Codex entry point that behaves consistently across accounts and failures.

**As a** internal client developer, **I want** to send Codex requests through one approved access path, **so that** I can keep using Codex without managing upstream accounts directly.

**Why this priority**: Without a stable access path, the platform does not replace direct account usage and delivers no user value.

**Acceptance Scenarios**:
1. **Given** an internal client developer and the platform has at least one active upstream account, **When** the developer sends a valid Codex request to any `/v1/*` path, **Then** the request is transparently proxied to an upstream account and the response is returned.
2. **Given** all upstream accounts are disabled or deleted, **When** the developer sends a request, **Then** the platform returns a capacity-unavailable error with a correlation identifier.

**Edge Cases**:
- What if the upstream returns an error? → The platform records the error in request history and returns the upstream error transparently.
- What if all upstream capacity is unavailable? → The platform returns a capacity error with a correlation identifier and does not expose upstream account details.

**Design Decisions**:
- For API-key accounts, the platform acts as a transparent reverse proxy for `/v1/*` paths. It does not maintain an explicit endpoint whitelist for that account type; any path under `/v1/` is forwarded to the account's upstream base URL. Feature 003 OAuth accounts are intentionally narrower: only Codex Responses paths are OAuth-eligible because ChatGPT subscription tokens are served by the ChatGPT Codex backend, not arbitrary OpenAI Platform endpoints.
- MVP does not validate client API keys. Authentication is deferred (the service runs on an internal network).
- MVP supports HTTP + SSE only. WebSocket support is deferred to a later phase.
- SSE streaming responses are parsed to extract token usage metadata before forwarding.

---

### US-2: Multi-Account Continuity (Priority: P0)

Platform operators need the router to keep traffic flowing across multiple upstream accounts without breaking ongoing sessions.

**As a** platform operator, **I want** the platform to select healthy upstream capacity and preserve session continuity, **so that** one unhealthy account does not take down the whole service.

**Why this priority**: Multi-account continuity is the core reason to run a router instead of sharing a single upstream account.

**Acceptance Scenarios**:
1. **Given** at least two active upstream accounts and one is disabled by an operator, **When** a new request arrives, **Then** the platform routes the request to another active account.
2. **Given** a Codex session is already active on a specific upstream account, **When** a follow-up request for that session arrives, **Then** the platform routes the follow-up request to the same account (deterministic via consistent hashing — no TTL, mapping is stable as long as the account set is unchanged).
3. **Given** the sticky account for a session has been disabled or deleted, **When** a follow-up request arrives, **Then** the platform automatically routes to a new active account (consistent hash ring rebuilt without the removed account).

**Edge Cases**:
- What if every upstream account is unavailable? → New requests are rejected with a capacity-unavailable result.
- What if an account fails after a session has already started responding? → The platform surfaces an interrupted-session result and preserves the failure details in request history.

**Design Decisions**:
- **Session identification**: Extracted from HTTP headers sent by Codex CLI, with precedence: `x-codex-session-id` > `x-codex-conversation-id`. The first non-empty value is used as the session key. When neither header is present, the request is treated as a new session (no stickiness).
- **Session stickiness**: Consistent hashing maps `session_key` to an active account deterministically. No storage required; the same session_key always routes to the same account as long as the account set is unchanged.
- **Sticky fallback (best-effort)**: When the pinned account becomes unavailable (disabled/deleted), the hash ring is rebuilt and the session is automatically reassigned to another active account. Note: this is **best-effort** — upstream conversation state (e.g. `previous_response_id`) is account-local and cannot be transferred. The client may need to restart the conversation on the new account.
- **Load balancing (MVP)**: Round-robin across active accounts for new sessions. Usage-weighted balancing is deferred to Phase 3.
- **No retry on upstream failure (MVP)**: If the upstream returns an error, the router returns it directly. Configurable retry policies are deferred to Phase 3.
- **Capacity unavailable**: Defined as "no upstream accounts with `active` status exist." If accounts are `active` but the upstream returns errors, the router transparently forwards the upstream error (it does not treat this as capacity unavailable).

---

### US-3: Operational Control (Priority: P0)

Platform operators need to manage accounts and request history without touching individual developer environments.

**As a** platform operator, **I want** to control upstream accounts from one place, **so that** I can contain incidents and change access quickly.

**Why this priority**: The service is not operationally useful unless operators can revoke bad capacity and inspect failures centrally.

**Acceptance Scenarios**:
1. **Given** an operator adds, enables, disables, or deletes an upstream account, **When** the change is saved, **Then** the change takes effect immediately for new requests (single-node, no cache).
2. **Given** an operator needs to investigate a problem, **When** the operator queries request history by time range, account, or outcome, **Then** the platform returns matching records with correlation identifier, selected account, session identifier when present, and final outcome.

**Edge Cases**:
- What if an operator disables an account that still has active sessions? → New sessions stop using that account immediately. Existing sticky sessions are re-routed to a new active account on the next request.
- What if request history retention is reached? → Expired records are removed according to the 30-day retention policy.

**Design Decisions**:
- **Account states**: `active`, `disabled`, `deleted` (soft delete).
- **API style**: Action-oriented POST endpoints, not PATCH.
  - `POST /api/admin/accounts` — create account
  - `POST /api/admin/accounts/{id}/enable` — enable
  - `POST /api/admin/accounts/{id}/disable` — disable
  - `POST /api/admin/accounts/{id}/delete` — soft delete
  - `GET /api/admin/accounts` — list (with status filter)
  - `GET /api/admin/requests` — query request history
  - `GET /api/admin/requests/{id}` — inspect one request/response record, including captured bodies when enabled
  - `GET /api/admin/requests/options` — discover dynamic request-log filter options
  - `GET /api/admin/sessions/resolve?session_key=xxx` — resolve a session key to its routed account (consistent hash lookup; no stored session list)
  - `GET /api/admin/health` — service status and account overview
- **No authentication (MVP)**: The Admin API is unauthenticated. The service runs on an internal network. RBAC is deferred to a later phase.
- **No client API key management (MVP)**: The router does not issue or validate client API keys in the MVP. Clients connect directly without credential checks.

---

### US-4: Usage Visibility (Priority: P2, deferred to Phase 3)

Platform operators need enough usage visibility to control spend and explain who is consuming shared capacity.

**As a** platform operator, **I want** aggregated usage by upstream account, **so that** I can spot abnormal consumption.

**Why this priority**: ~~P1~~ Downgraded to P2. The MVP can launch with request history for manual investigation. Aggregated usage summaries are deferred to Phase 3.

**MVP alternative**: Operators query request history directly. Each request record includes a `token_usage` JSON field containing `input`, `output`, `cached_input`, and `reasoning` token counts, enabling manual aggregation.

---

### US-5: Expansion Readiness (Priority: P2)

Protocol adapter maintainers need the first launch to avoid painting the platform into a Codex-only corner.

**As a** protocol adapter maintainer, **I want** the launch scope to preserve an additive path for future protocols, **so that** a later Claude Code or similar launch does not require replacing the whole platform.

**Why this priority**: This is important for product direction, but the first release can ship before a second protocol is implemented.

**Acceptance Scenarios**:
1. **Given** Codex is the only supported protocol at launch, **When** the product team decides to add a second client protocol later, **Then** that later launch can be delivered as additive scope without re-platforming.
2. **Given** a client attempts to use an unsupported protocol path (e.g. not under `/v1/`), **When** the request reaches the platform, **Then** the platform returns a 404 and Codex availability remains unaffected.

**Edge Cases**:
- What if the second protocol has different session behavior? → The future launch may define protocol-specific session rules without changing the promised behavior of existing Codex sessions.
- What if protocol expansion is delayed indefinitely? → The Codex launch remains fully usable on its own.

## Functional Requirements

- **FR-001**: The platform MUST provide a transparent reverse proxy for `/v1/*` paths to upstream OpenAI API when the selected account uses API-key authentication. OAuth account transport is defined by Feature 003 and is restricted to ChatGPT Codex Responses paths.
- **FR-002**: ~~The platform MUST authenticate every client request before it consumes upstream capacity.~~ **Deferred**: MVP does not validate client credentials (internal network).
- **FR-003**: The platform MUST route new work only to upstream accounts with `active` status.
- **FR-004**: The platform MUST preserve session continuity by extracting session identifiers from Codex CLI headers (`x-codex-session-id`, `x-codex-conversation-id`) and using consistent hashing to deterministically route sessions to accounts.
- **FR-005**: The platform MUST allow operators to add, enable, disable, and delete upstream accounts via the Admin API.
- **FR-006**: ~~The platform MUST allow operators to create, disable, rotate, and inspect issued API keys.~~ **Deferred**: No client API key management in MVP.
- **FR-007**: The platform MUST record a request history entry for every inbound request (including requests rejected before proxying, such as no-capacity errors).
- **FR-008**: The platform MUST expose request history through `GET /api/admin/requests` with cursor pagination and filters for time range, upstream account, outcome, model, response mode, and search text. The platform MUST expose `GET /api/admin/requests/{id}` for detail/body inspection and `GET /api/admin/requests/options` for dynamic filter options.
- **FR-009**: ~~The platform MUST provide grouped usage summaries.~~ **Deferred to Phase 3**.
- **FR-010**: The platform MUST extract token usage metadata (input_tokens, output_tokens, cached_input_tokens, reasoning_tokens) using these rules: (a) SSE streams: from the `usage` field of the final `response.completed` event; (b) non-streaming JSON responses: from the `usage` field in the response body; (c) failed or interrupted requests: token fields are recorded as null.
- **FR-011**: The platform MUST preserve additive compatibility for future protocol launches without breaking existing Codex clients.
- **FR-012**: The platform MUST support configurable request/response body logging (default off).
- **FR-013**: The platform MUST automatically re-route sticky sessions to a new active account when the pinned account is disabled or deleted.
- **FR-014**: The platform MUST return router-originated errors in a standard JSON envelope that is distinguishable from upstream errors (see Error Format section).

## Non-Functional Requirements

| Category | Requirement | Metric | Verification |
|----------|-------------|--------|--------------|
| Performance | Healthy requests are admitted quickly | P95 time from request receipt to routing decision under 200 ms at 200 concurrent routed requests | Load test |
| Performance | Operator history queries remain usable | P95 response time under 2 seconds for a 7-day query returning up to 500 records | Integration benchmark |
| Reliability | Healthy capacity failures do not take down the service | When at least one active upstream account exists, at least 99% of valid new requests are routed successfully (outcome != `router_error` and outcome != `no_available_account`) each calendar day. Upstream errors (4xx/5xx from OpenAI) do not count against this metric. | Production monitoring |
| Observability | Failures are diagnosable | 100% of requests have a correlation identifier and final outcome in retained history | Log audit |
| Retention | Operators can investigate recent incidents | Request history is retained for 30 days | Retention verification |

## Key Entities

### Upstream Account

A managed upstream OpenAI API key used to serve client traffic.

| Field | Type | Description |
|-------|------|-------------|
| `id` | bigserial | Auto-increment unique identifier |
| `name` | string | Human-readable alias |
| `provider` | string | Provider type: `openai`, `anthropic`, etc. Default: `openai` |
| `api_key` | string | Upstream API key (stored in plaintext for MVP) |
| `base_url` | string (nullable) | Override base URL. NULL = use provider default (`openai` → `https://api.openai.com`) |
| `status` | enum | `active`, `disabled`, `deleted` |
| `created_at` | timestamp | Creation time |
| `updated_at` | timestamp | Last modification time |

### Routing Session (in-process, no DB table)

Session stickiness is implemented via consistent hashing. No database table is needed.

Given a `session_key` (extracted from Codex CLI headers) and the current list of active accounts, the consistent hash function deterministically selects the same account every time. When accounts are added or removed, only ~1/N sessions are reassigned.

This is a stateless, in-process operation with no storage or TTL management.

### Request Record

The auditable outcome of one inbound request (including requests rejected before proxying).

| Field | Type | Description |
|-------|------|-------------|
| `id` | bigserial | Internal auto-increment ID |
| `request_id` | string | Human-readable correlation ID (e.g. `req_xxxxxxxx`) |
| `created_at` | timestamp | Request timestamp |
| `upstream_account_id` | bigint (nullable) | Routed-to account (null for pre-routing rejections) |
| `session_key` | string (nullable) | Session identifier if present |
| `method` | string | HTTP method |
| `path` | string | Request path (e.g. `/v1/responses`) |
| `status_code` | int | Response status code |
| `latency_ms` | int | End-to-end latency |
| `outcome` | enum | `success`, `upstream_error`, `no_available_account`, `router_error` |
| `error_code` | string (nullable) | Error code on failure |
| `model` | string (nullable) | Requested model |
| `model_params` | jsonb (nullable) | Model parameters (e.g. `{"reasoning_effort": "high", "service_tier": "default"}`) |
| `router_metadata` | jsonb (nullable) | Router-owned audit metadata, never client secrets or body bytes |
| `response_mode` | string | Response mode: `json`, `sse`, `websocket` (future) |
| `token_usage` | jsonb (nullable) | Token counts (e.g. `{"input": 100, "output": 50, "cached_input": 20, "reasoning": 10}`) |
| `client_request_body` | text (nullable) | Full client request body received by the router when client request body logging is enabled |
| `upstream_request_body` | text (nullable) | Full request body sent to the selected upstream when upstream request body logging is enabled |
| `upstream_response_body` | text (nullable) | Full upstream response body; for SSE: all events merged when upstream response body logging is enabled |

## Error Format

Router-originated errors use a standard JSON envelope distinguishable from upstream errors:

```json
{
  "error": {
    "type": "router_error",
    "code": "no_available_account",
    "message": "No active upstream accounts available",
    "request_id": "req_abc123"
  }
}
```

- `type` is always `"router_error"` for errors generated by the router.
- `code` identifies the specific error condition.
- `request_id` is the correlation ID stored in request history.
- Upstream errors are returned transparently (original HTTP status code and body, unmodified). Clients distinguish router errors from upstream errors by checking for `error.type == "router_error"`.

**MVP Router Error Codes**:

| code | HTTP Status | Scenario |
|------|-------------|----------|
| `no_available_account` | 503 | No upstream accounts with `active` status exists |
| `internal_error` | 500 | Router internal error |
| `upstream_connect_failed` | 502 | Cannot connect to upstream |
| `upstream_response_invalid` | 502 | Upstream returned malformed or oversized data that the router could not convert to the downstream contract |
| `upstream_timeout` | 504 | Upstream response timeout |

**SSE mid-stream failure**: Once SSE bytes have been sent, the router cannot send an HTTP error response. The router closes the connection. The failure is recorded in request history with `outcome = router_error` or `upstream_error` as appropriate.

## Success Criteria

- **SC-1**: Within 30 days of launch, at least 3 internal teams route Codex traffic through one-llm-router.
- **SC-2**: Operators can disable a misbehaving upstream account without causing more than 5 minutes of new-session disruption.
- **SC-3**: At least 90% of routed request failures can be explained from retained request history without needing direct upstream account access.
- **SC-4**: The platform can onboard a second protocol as a separately approved release without deprecating existing Codex access.

## Scope

### In Scope
- Transparent API-key reverse proxy for `/v1/*` paths (HTTP + SSE); Feature 003 OAuth Responses traffic uses ChatGPT Codex backend transport
- Multi-account routing with round-robin load balancing
- Session continuity via consistent hashing (stateless, deterministic)
- Operator management of upstream accounts (add/enable/disable/delete)
- Request history with searchable filters and 30-day retention
- SSE response parsing for token usage extraction
- Configurable request/response body logging
- Single-port deployment with path-prefix routing (`/v1/*` proxy, `/admin/*` control plane)
- Single-node Go binary + SQLite (default) or PostgreSQL

### Out of Scope
- Client API key issuance and validation (deferred — internal network)
- Admin API authentication and RBAC (deferred — internal network)
- Usage summary aggregation (deferred to Phase 3)
- WebSocket support (deferred to Phase 3)
- Automatic health detection and quarantine (deferred to Phase 3)
- Retry policies on upstream failure (deferred to Phase 3)
- Usage-weighted load balancing (deferred to Phase 3)
- Claude Code support
- Browser dashboard UI
- Predictive quota analytics, forecasting, or billing workflows
- Multi-region deployment and active-active failover
- Audio, transcription, and non-coding model workflows

## Architecture Overview

### Deployment

```
┌──────────────────────────────────────┐
│         one-llm-router (Go)          │
│                                      │
│  :8080                               │
│  ├── /v1/*    → Codex Proxy          │
│  └── /admin/* → Admin API            │
│                                      │
│  Codex Proxy:                        │
│  1. Extract session key from headers  │
│  2. Select upstream account           │
│     (consistent hash or round-robin) │
│  3. Replace API key in request        │
│  4. Forward to upstream               │
│  5. Parse SSE for token usage         │
│  6. Record request history            │
│                                      │
└──────────┬───────────────────────────┘
           │
           ▼
┌──────────────────────┐    ┌───────────────────────┐
│  SQLite / PostgreSQL │    │  api.openai.com       │
│                      │    │  (upstream)            │
│  - accounts          │    │                       │
│  - request_records   │    │                       │
└──────────────────────┘    └───────────────────────┘
```

### Configuration (Environment Variables)

| Variable | Default | Description |
|----------|---------|-------------|
| `ROUTER_DB_DRIVER` | `sqlite3` | Database driver: `sqlite3`, `postgres`, `mysql` (future) |
| `ROUTER_DB_URL` | `router.db` | Database connection string (SQLite file path or PostgreSQL DSN) |
| `ROUTER_LISTEN_ADDR` | `:8080` | Listen address |
| `ROUTER_LOG_LEVEL` | `info` | Log level |
| `ROUTER_DB_MAX_CONNS` | `10` | Maximum DB connection pool size |
| `ROUTER_DB_MIN_CONNS` | `2` | Minimum DB connection pool size |
| `ROUTER_LOG_CLIENT_REQUEST_BODY` | `false` | Enable recording of the client request body received by the router |
| `ROUTER_LOG_UPSTREAM_REQUEST_BODY` | `false` | Enable recording of the request body sent to the selected upstream |
| `ROUTER_LOG_UPSTREAM_RESPONSE_BODY` | `false` | Enable recording of the response body returned by the upstream |
| `ROUTER_LOG_RETENTION_DAYS` | `30` | Request history retention period in days |

Note: Upstream base URL is managed per account (via `base_url` field), not as a global config.

## Assumptions

- This is a new system. There is no migration from codex-lb or any other service.
- The first launch serves internal users on an internal network.
- Programmatic operator tooling (Admin API) is sufficient for the first release; a graphical dashboard can follow later.
- A single operating environment (single node) is acceptable for the initial launch.
- Upstream API keys are stored in plaintext in the database (internal network, acceptable risk for MVP).

## Clarifications

### Session 2026-04-15
- Q: Should the first launch include non-Codex protocols? → A: No. The first launch is Codex-only, while preserving additive expansion for future protocol support.
- Q: Should a browser dashboard be part of the first release? → A: No. The first release can rely on programmatic operator tooling and logs.

### Session 2026-04-16 (Product Discussion)
- Q: Should Usage Summary be in MVP? → A: No. Downgraded to P2, deferred to Phase 3. Operators use request history for manual investigation.
- Q: What does "multi-client" mean? → A: Multi-account (multiple upstream OpenAI API keys).
- Q: Account states? → A: `active`, `disabled`, `deleted` (soft delete).
- Q: Retry on upstream failure? → A: No retry in MVP. Configurable retry policies deferred to Phase 3.
- Q: Sticky account becomes disabled? → A: Automatically re-route to a new active account.
- Q: Session identification? → A: Extracted from Codex CLI headers (`x-codex-session-id`, `x-codex-conversation-id`). Studied from codex-lb source code.
- Q: Session TTL? → A: N/A — consistent hashing is stateless and deterministic. No TTL. Session mapping is stable as long as the active account set is unchanged.
- Q: Load balancing strategy? → A: Round-robin for MVP. Usage-weighted deferred to Phase 3.
- Q: API style? → A: Action-oriented POST endpoints (not PATCH). E.g. `POST /api/admin/accounts/{id}/disable`.
- Q: Admin authentication? → A: None for MVP (internal network).
- Q: Client API key validation? → A: None for MVP (internal network).
- Q: Request history storage? → A: Same database (SQLite default, PostgreSQL for scale). Updated: originally PostgreSQL-only, extended to multi-DB via XORM.
- Q: Request/response body logging? → A: Configurable, default off. Included in MVP.
- Q: API key masking in logs? → A: Not masked by default.
- Q: Token fields? → A: input_tokens, output_tokens, cached_input_tokens, reasoning_tokens.
- Q: Retention? → A: 30 days.
- Q: Proxy mode? → A: Transparent reverse proxy for API-key `/v1/*` paths. Feature 003 amends OAuth `/v1/responses` traffic to use ChatGPT Codex backend transport.
- Q: WebSocket support? → A: HTTP + SSE only for MVP. WebSocket deferred.
- Q: SSE handling? → A: Parse SSE events to extract token usage, then forward.
- Q: Upstream API key storage? → A: Plaintext in database (internal network). Updated: database is SQLite (default) or PostgreSQL.
- Q: Deployment? → A: Internal network. Single-node Go binary + SQLite (default) or PostgreSQL.
- Q: Port layout? → A: Single port, path-prefix routing (`/v1/*` proxy, `/api/admin/*` JSON control plane, `/admin/*` SPA HTML).
- Q: SC-1 target? → A: Changed from "80% of traffic" to "at least 3 internal teams" (more realistic for new system).

### Session 2026-04-16 (Codex Review Resolution)
- Q: Transparent proxy forwards audio/transcription requests? → A: Yes. The router does not filter by path. Upstream returns its own error for unsupported request types.
- Q: What happens when a sticky session is re-routed to a new account? → A: Best-effort. Upstream conversation state (`previous_response_id`) is account-local and cannot transfer. Client may need to restart the conversation.
- Q: Session key header precedence? → A: `x-codex-session-id` > `x-codex-conversation-id`. First non-empty value wins.
- Q: What does "capacity unavailable" mean in MVP? → A: No upstream accounts with `active` status. Active accounts returning upstream errors do not trigger capacity-unavailable.
- Q: Should Request Record include an outcome field? → A: Yes. Enum: `success`, `upstream_error`, `no_available_account`, `router_error`. Enables filtering by `GET /api/admin/requests?outcome=upstream_error`.
- Q: How to distinguish router errors from upstream errors? → A: Router errors use `error.type: "router_error"` in a standard JSON envelope. Upstream errors are returned transparently.
- Q: When do account changes take effect? → A: Immediately (single-node, no cache).
- Q: Token extraction rules? → A: SSE: from `response.completed` event's `usage` field. JSON: from response body's `usage` field. Failed/interrupted: null.
- Q: Should api_key be stored in Request Record? → A: No. Removed. Use `upstream_account_id` to look up the account if needed.
- Q: What does "routed successfully" mean in reliability NFR? → A: outcome != `router_error` and outcome != `no_available_account`. Upstream 4xx/5xx do not count against reliability.

### Session 2026-04-16 (Technical Design Review)
- Q: Token fields storage? → A: JSONB column `token_usage` instead of individual columns. Flexible for future token types.
- Q: Model parameters? → A: JSONB column `model_params` (reasoning_effort, service_tier, etc.); router-owned audit facts live in `router_metadata`.
- Q: Stream vs non-stream? → A: `response_mode` text field: `json`, `sse`, `websocket` (future).
- Q: Body logging? → A: Client request, upstream request, and upstream response bodies are stored independently when their runtime body-capture flags are enabled. SSE response events are merged into `upstream_response_body`.
- Q: ID type? → A: `bigserial` for all tables. `request_id` string (e.g. `req_xxxxxxxx`) for human-readable correlation.
- Q: Session storage? → A: Consistent hashing (zero storage). `SessionRouter` interface for future extension.
- Q: Upstream base URL? → A: Per-account `base_url` field (nullable, defaults to provider's standard URL). Global config removed.
- Q: Account provider type? → A: `provider` text field (not enum). Default `openai`. Future: `anthropic`, etc.
- Q: Retention config name? → A: `ROUTER_LOG_RETENTION_DAYS` (renamed for clarity).
- Q: Database engine? → A: XORM ORM for database-agnostic access. SQLite as default (zero-dependency), PostgreSQL for production scale. `modernc.org/sqlite` (pure Go, no CGO).
- Q: Session TTL? → A: N/A — replaced consistent hashing (stateless, deterministic). No TTL management needed. Session mapping is stable as long as the active account set is unchanged.
