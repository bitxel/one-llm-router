# Technical Design: Codex Router MVP

**Feature**: 001-codex-router-mvp
**Created**: 2026-04-16
**Status**: Draft

## 1. Architecture Overview

Single-node Go binary serving two HTTP path groups on one port:

```
:8080
├── /v1/*    → Provider-compatible Codex data plane
└── /admin/* → Operator control-plane API
```

Backing store: SQLite (default) or PostgreSQL 16 (2 tables: `upstream_accounts`, `request_records`). XORM ORM for database-agnostic access.

Session stickiness: in-process consistent hashing (zero storage).

## 2. Package Structure

```
cmd/one-llm-router/
  main.go              // process entrypoint
  config.go            // env loading + validation
  app.go               // dependency wiring, mux, lifecycle, shutdown

internal/
  domain/
    account.go         // UpstreamAccount, AccountStatus, ProviderDefaults enum
    request_record.go  // RequestRecord, RequestOutcome enum, TokenUsage
    errors.go          // sentinel/domain errors

  core/
    account_service.go  // CRUD operations on accounts
    account_selector.go // round-robin for new sessions
    session_router.go   // SessionRouter interface + ConsistentHashRouter
    request_recorder.go // async buffered recorder interface + worker
    request_service.go  // query service for request history
    health_service.go   // health check logic

  api/
    proxy.go           // /v1/* provider-compatible data-plane handler
    session.go         // session key extraction from Codex headers
    sse.go             // SSE event parser + inline forwarding
    errors.go          // router error JSON envelope
    admin/
      handler.go       // /admin/* handler implementations
      routes.go        // admin route registration

  provider/
    openai/
      client.go        // upstream HTTP client wrapper
      forwarder.go     // request cloning, auth replacement, response copy
      usage.go         // token usage extraction from JSON/SSE

  store/
    store.go           // Store struct, engine init, driver selection
    accounts.go        // AccountRepository implementation (XORM)
    records.go         // RequestRecordRepository implementation (XORM)
    migrations/
      postgres/
        000001_init.up.sql
        000001_init.down.sql
      sqlite/
        000001_init.up.sql
        000001_init.down.sql

  generated/
    adminapi/          // oapi-codegen output for /admin/*

openapi/
  admin.yaml           // OpenAPI 3.0.x spec for admin API
```

### Package Responsibilities

| Package | Responsibility | Knows About |
|---------|---------------|-------------|
| `domain` | Pure data entities, enums, invariants. XORM struct tags are metadata only — no XORM import required. | Nothing external |
| `core` | Business logic, interfaces, orchestration | `domain` only |
| `api` | HTTP handlers, request validation, response formatting | `core`, `domain` |
| `api/admin` | Admin API handlers | `core`, `domain`, `generated/adminapi` |
| `provider/openai` | Upstream HTTP client, SSE parsing, usage extraction | `domain` |
| `store` | Database access via XORM (single implementation for all DB backends) | `core`, `domain` |
| `cmd/one-llm-router` | Bootstrap, config, dependency wiring | Everything |

## 3. Database Schema

### ORM: XORM

XORM provides database-agnostic access. One set of Go code works with SQLite, PostgreSQL, and MySQL.

JSON columns (`token_usage`, `model_params`, `router_metadata`) use XORM's `json` tag — auto-marshaled via `encoding/json`. Storage varies by dialect: `jsonb` in PostgreSQL, `TEXT` in SQLite. XORM handles this transparently.

### XORM Models

```go
type UpstreamAccount struct {
    ID        int64     `xorm:"pk autoincr 'id'"`
    Name      string    `xorm:"not null 'name'"`
    Provider  string    `xorm:"not null default('openai') 'provider'"`
    APIKey    string    `xorm:"not null 'api_key'"`
    BaseURL   *string   `xorm:"'base_url'"`
    Status    string    `xorm:"not null default('active') 'status'"`
    CreatedAt time.Time `xorm:"created not null 'created_at'"`
    UpdatedAt time.Time `xorm:"updated not null 'updated_at'"`
}

type RequestRecord struct {
    ID                int64    `xorm:"pk autoincr 'id'"`
    RequestID         string   `xorm:"not null unique 'request_id'"`
    CreatedAt         time.Time `xorm:"created not null index 'created_at'"`
    UpstreamAccountID *int64   `xorm:"index 'upstream_account_id'"`
    SessionKey        *string  `xorm:"index 'session_key'"`
    Method            string   `xorm:"not null 'method'"`
    Path              string   `xorm:"not null 'path'"`
    StatusCode        int      `xorm:"not null 'status_code'"`
    LatencyMs         int      `xorm:"not null 'latency_ms'"`
    Outcome           string   `xorm:"not null index 'outcome'"`
    ErrorCode         *string  `xorm:"'error_code'"`
    Model             *string  `xorm:"'model'"`
    ModelParams       JSONMap  `xorm:"json 'model_params'"`
    RouterMetadata    JSONMap  `xorm:"json 'router_metadata'"`
    ResponseMode      string   `xorm:"not null default('json') 'response_mode'"`
    TokenUsage        JSONMap  `xorm:"json 'token_usage'"`
    ClientRequestBody    *string  `xorm:"'client_request_body'"`
    UpstreamRequestBody  *string  `xorm:"'upstream_request_body'"`
    UpstreamResponseBody *string  `xorm:"'upstream_response_body'"`
}

type JSONMap map[string]interface{}
```

### Migration Strategy

Use `golang-migrate` for versioned migrations (not XORM Sync2), maintaining expand/contract principle.

Separate migration files per dialect:

```
store/migrations/
  postgres/
    000001_init.up.sql    # PostgreSQL-specific DDL
    000001_init.down.sql
  sqlite/
    000001_init.up.sql    # SQLite-specific DDL
    000001_init.down.sql
```

### Database Drivers

| Driver | DB | Package |
|--------|-----|---------|
| SQLite (default) | SQLite 3 | `modernc.org/sqlite` (pure Go, no CGO) |
| PostgreSQL | PG 16 | `github.com/lib/pq` |
| MySQL (future) | MySQL 8+ | `github.com/go-sql-driver/mysql` |

### Configuration

```bash
# SQLite (default, zero-dependency)
ROUTER_DB_DRIVER=sqlite3
ROUTER_DB_URL=router.db

# PostgreSQL
ROUTER_DB_DRIVER=postgres
ROUTER_DB_URL=postgres://user:pass@localhost/router
```

### SQLite Pragmas

Applied on connection for SQLite:
- `PRAGMA journal_mode=WAL;`
- `PRAGMA busy_timeout=5000;`
- `PRAGMA foreign_keys=ON;`

### Notes
- `request_id` (e.g. `req_xxxxxxxx`) is the human-readable correlation ID used in logs, error responses, and admin queries.
- `token_usage` is JSON: `{"input": 100, "output": 50, "cached_input": 20, "reasoning": 10}`. Flexible for future token types.
- `model_params` is JSON: `{"reasoning_effort": "high", "service_tier": "default"}`.
- `router_metadata` is JSON for router-owned audit metadata and never stores client secrets or body bytes.
- `response_mode` is `json`, `sse`, or `websocket` (future).
- `upstream_response_body` stores the full upstream response: for JSON the raw body, for SSE all `data:` lines merged.
- `client_request_body`, `upstream_request_body`, and `upstream_response_body` are recorded only when their corresponding runtime body-capture flags are enabled.
- No `routing_sessions` table — session stickiness uses in-process consistent hashing.
- No PostgreSQL ENUM types — use text fields with application-level validation for cross-DB compatibility.

## 4. Core Interfaces

```go
// core/session_router.go
type SessionRouter interface {
    Route(sessionKey string, activeAccounts []domain.UpstreamAccount) (domain.UpstreamAccount, error)
}

// MVP implementation: ConsistentHashRouter
// Future: MemoryMapRouter, PostgresRouter, etc.
```

```go
// core/account_selector.go
type AccountSelector struct {
    repo          AccountRepository
    sessionRouter SessionRouter
    counter       atomic.Uint64 // round-robin counter
}

func (s *AccountSelector) Select(ctx context.Context, sessionKey string) (domain.UpstreamAccount, error)
```

Selection logic:
1. Load all active accounts from DB.
2. If no active accounts → return `ErrNoCapacity`.
3. If `sessionKey` is non-empty → use `SessionRouter.Route(sessionKey, activeAccounts)`.
4. If `sessionKey` is empty → round-robin via atomic counter.

```go
// core/request_recorder.go
type RequestRecorder struct {
    repo    RequestRecordRepository
    ch      chan domain.RequestRecord
    wg      sync.WaitGroup
    logger  *slog.Logger
}

func (r *RequestRecorder) Record(ctx context.Context, rec domain.RequestRecord)
func (r *RequestRecorder) Close(ctx context.Context) error
```

Buffered channel (size 1024) + single worker goroutine. If channel is full, synchronous insert fallback (never drop records).

## 5. Request Flow

### Data Plane (/v1/*)

```
1. Inbound HTTP request to /v1/*
2. Generate request ID (e.g. req_a1b2c3d4)
3. Extract session key:
   - Priority: x-codex-session-id > x-codex-conversation-id
   - First non-empty value wins; both empty = new session
4. AccountSelector.Select(ctx, sessionKey)
   - No active accounts → return 503 router error, record outcome=no_available_account
5. Build account-specific upstream request:
   - API-key accounts: clone request to upstream URL (account's base_url or provider default), replace Authorization header with account's API key
   - OAuth accounts (Feature 003): route `/v1/responses` and `/v1/responses/compact` to the ChatGPT Codex backend with the OAuth bearer and `chatgpt-account-id` when present. For `/v1/responses`, force upstream SSE but preserve the client's downstream stream mode.
6. Forward to upstream via provider/openai.Forwarder
7. Detect response type:
   a. Content-Type: text/event-stream → SSE path
   b. Content-Type: application/json → JSON path
   c. Other → transparent passthrough
8. SSE path: read-and-forward each event, parse response.completed for usage; used for client `stream:true`.
9. JSON path: read body once, extract usage, write to client; used for API-key JSON responses and OAuth client `stream:false`/omitted `stream` after Codex upstream SSE has been collected into JSON.
10. Build RequestRecord with timing, tokens, outcome
11. Send to RequestRecorder (async)
```

### Control Plane (/admin/*)

```
1. Inbound HTTP request to /admin/*
2. Route to AdminHandler via generated oapi-codegen server
3. Handler validates request against OpenAPI schema
4. Handler calls core service (AccountService, RequestService, etc.)
5. Core service operates on database via repository (XORM)
6. Return JSON response
```

## 6. SSE Parsing Strategy

Stream-and-parse approach (zero buffering of full response):

1. Detect `Content-Type: text/event-stream` from upstream response.
2. Set response headers, begin streaming to client.
3. Read upstream body line by line with `bufio.Scanner`.
4. Forward each line to client immediately, flush after blank lines (event boundaries).
5. Accumulate `data:` lines for the current event.
6. On event boundary (blank line), decode JSON from accumulated data.
7. If event has `"type": "response.completed"`, extract:
   - `usage.input_tokens`
   - `usage.output_tokens`
   - `usage.input_tokens_details.cached_tokens` → `cached_input_tokens`
   - `usage.output_tokens_details.reasoning_tokens` → `reasoning_tokens`
8. Continue until stream ends or connection drops.
9. For non-streaming JSON: buffer full body, extract `usage`, write original bytes to client. OAuth `/v1/responses` non-streaming requests are a special upstream-transport adaptation: the router asks ChatGPT Codex for SSE, collects the terminal response into complete JSON, then sends that JSON downstream and records the same JSON in `upstream_response_body` when response-body logging is enabled.

## 7. Error Handling

### Router Error Envelope

> **Updated 2026-04-18 PM (002 follow-up)**: 001 admin JSON endpoints moved under `/api/admin/*` and adopt the project-wide envelope `{code:int, msg:string, data:any}` — see `AGENTS.md §HTTP API Style` and `docs/error-codes.md`. The SPA owns `/admin/*` HTML paths. **`/v1/*` is explicitly excluded from the Admin API envelope** — the JSON shape below is the router-generated fallback that is only emitted when the router short-circuits before forwarding (e.g. `no_available_account` at 503, internal timeouts). 001's router-error shape is preserved on `/v1/*` for backward compatibility with existing Codex clients.
>
> **Updated 2026-04-25 (003 follow-up)**: setup-done upstream transport is account-specific. API-key accounts keep the 001/002 Platform-compatible pass-through behavior. OAuth accounts are ChatGPT/Codex accounts and use the ChatGPT Codex backend for `/v1/responses` and `/v1/responses/compact`, including `chatgpt-account-id` when available.
>
> **Scope summary**:
> - `/v1/*` router-generated errors → shape below (001 MVP contract preserved)
> - `/api/admin/*` (including `/api/admin/health`, all 001 admin JSON endpoints) → new envelope `{code,msg,data}`, HTTP 200 for business errors, HTTP 500 for system errors. 001 codes are backfilled into `docs/error-codes.md` in the 1000–1999 range.

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

### Error Mapping

| Scenario | HTTP | code | outcome |
|----------|------|------|---------|
| No active accounts exist (`/v1/*`) | 503 | `no_available_account` | `no_available_account` |
| Router internal error (`/v1/*`) | 500 | `internal_error` | `router_error` |
| Cannot connect to upstream (`/v1/*`) | 502 | `upstream_connect_failed` | `router_error` |
| Upstream response timeout (`/v1/*`) | 504 | `upstream_timeout` | `router_error` |
| Upstream returns error (`/v1/*`) | upstream's code | — | `upstream_error` |
| Upstream returns success (`/v1/*`) | upstream's code | — | `success` |

For 001's admin endpoints (including `/api/admin/health`), the envelope introduced by 002 applies — see `docs/error-codes.md` for the 1000-range registry and `specs/002-.../contracts/admin-api.md` for the full shape.

### SSE Mid-Stream Failure

Once SSE bytes have been sent, the router cannot send an HTTP error. It closes the connection. The failure is recorded in request history.

## 8. Configuration

```go
type Config struct {
    ListenAddr         string        // ROUTER_LISTEN_ADDR, default ":8080"
    DBDriver           string        // ROUTER_DB_DRIVER, default "sqlite3" (also: "postgres")
    DBURL              string        // ROUTER_DB_URL, default "router.db" (SQLite file)
    DBMaxConns         int32         // ROUTER_DB_MAX_CONNS, default 10 (ignored for SQLite)
    DBMinConns         int32         // ROUTER_DB_MIN_CONNS, default 2 (ignored for SQLite)
    LogLevel           slog.Level    // ROUTER_LOG_LEVEL, default info
    LogClientRequestBody    bool     // ROUTER_LOG_CLIENT_REQUEST_BODY, default false
    LogUpstreamRequestBody  bool     // ROUTER_LOG_UPSTREAM_REQUEST_BODY, default false
    LogUpstreamResponseBody bool     // ROUTER_LOG_UPSTREAM_RESPONSE_BODY, default false
    LogRetentionDays   int           // ROUTER_LOG_RETENTION_DAYS, default 30
}
```

Note: `UpstreamBaseURL` is no longer a global config. Each account has its own `base_url` field (nullable; defaults to provider's standard URL).

Provider default URLs (hardcoded):
- `openai` → `https://api.openai.com`
- `anthropic` → `https://api.anthropic.com`

## 9. Background Tasks

### Request Record Cleanup

Single goroutine, runs every hour:

```go
engine.Where("created_at < ?", time.Now().AddDate(0, 0, -retentionDays)).Limit(1000).Delete(&RequestRecord{})
```

Uses XORM query builder for database-agnostic cleanup. Uses `ROUTER_LOG_RETENTION_DAYS` config value. Deletes in batches of 1000 to avoid long-running transactions.

## 10. Admin API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| POST | /api/admin/accounts | Create upstream account |
| GET | /api/admin/accounts | List accounts (with status filter) |
| GET | /api/admin/accounts/{id} | Get account details |
| POST | /api/admin/accounts/{id}/enable | Enable account |
| POST | /api/admin/accounts/{id}/disable | Disable account |
| POST | /api/admin/accounts/{id}/delete | Soft delete account |
| GET | /api/admin/requests | Query request history (time, account, outcome, model, response-mode, search filters) |
| GET | /api/admin/requests/{id} | Inspect one request/response record, including captured bodies when enabled |
| GET | /api/admin/requests/options | Return dynamic request-log filter options |
| GET | /api/admin/sessions/resolve | Resolve session key to account (consistent hash lookup) |
| GET | /api/admin/health | Service health + account summary (wrapped in `{code,msg,data}` envelope from 002 onwards — see `docs/error-codes.md`; code `1000 ok`) |

### Pagination

Request history uses cursor-based pagination:
- `?limit=50` (default 50, max 200)
- `?before=<id>` (cursor: records before this bigint ID)

### Account Create Request

```json
{
  "name": "team-alpha-key-1",
  "api_key": "sk-...",
  "provider": "openai",
  "base_url": null
}
```

`provider` defaults to `"openai"` if omitted. `base_url` defaults to `null` (uses provider default URL) if omitted.

### Request History Query Parameters

| Param | Type | Description |
|-------|------|-------------|
| `start` | ISO 8601 | Start time (required) |
| `end` | ISO 8601 | End time (default: now) |
| `account_id` | int64 | Filter by upstream account ID |
| `outcome` | string | Filter by outcome (`success`, `upstream_error`, `no_available_account`, `router_error`) |
| `limit` | int | Page size (default 50, max 200) |
| `before` | int64 | Cursor for pagination (record ID) |

## 11. Testing Strategy

### Integration Tests (priority)

SQLite in-memory (default) or PostgreSQL (testcontainers-go) + httptest upstream:

1. **Proxy happy path**: request proxied, response returned, record created with outcome=success
2. **SSE streaming**: response.completed event parsed, tokens extracted
3. **Non-streaming JSON**: usage extracted from response body
4. **Round-robin**: N requests distributed across active accounts
5. **Consistent hash stickiness**: same session_key routes to same account
6. **Sticky fallback**: disable pinned account, next request routes elsewhere
7. **No capacity**: all accounts disabled → 503 with router error envelope
8. **Upstream error passthrough**: upstream 429 → same 429 to client, outcome=upstream_error
9. **Admin CRUD lifecycle**: create → list → disable → enable → delete
10. **Request history filtering**: filter by time, account, outcome, model, response mode, and search text; detail and option endpoints are covered by OpenAPI admin contract tests
11. **Request body logging**: enabled vs disabled
12. **Retention cleanup**: old records deleted by janitor

### Unit Tests

- Session key extraction (header precedence)
- Consistent hash determinism and stability
- SSE event parser
- JSON usage parser
- Router error envelope construction
- Config loading and validation

## 12. Graceful Shutdown

1. Stop accepting new connections.
2. Wait for in-flight requests (30s timeout).
3. Drain RequestRecorder channel.
4. Close DB pool.

## 13. Traceability Matrix

| Spec Requirement | Implementation |
|-----------------|----------------|
| FR-001 (API-key transparent proxy; OAuth amendment in 003) | `api/proxy.go` |
| FR-003 (route to active only) | `core/account_selector.go` |
| FR-004 (session continuity) | `core/session_router.go` + `api/session.go` |
| FR-005 (account management) | `api/admin/handler.go` + `core/account_service.go` |
| FR-007 (request history) | `core/request_recorder.go` + `store/records.go` |
| FR-008 (history filters) | `internal/api/adminapi/request_logs.go` GET `/api/admin/requests`, `/api/admin/requests/{id}`, `/api/admin/requests/options` |
| FR-010 (token extraction) | `provider/openai/usage.go` + `api/sse.go` |
| FR-011 (additive compat) | Package structure separates protocol from core |
| FR-012 (body logging) | `Config.LogClientRequestBody`, `Config.LogUpstreamRequestBody`, `Config.LogUpstreamResponseBody` |
| FR-013 (sticky fallback) | `core/account_selector.go` + consistent hash rebuild |
| FR-014 (error format) | `api/errors.go` |
