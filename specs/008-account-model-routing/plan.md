# Account Model Routing — Technical Design

## Problem

Currently one-llm-router does not filter upstream accounts by the requested model. If both an OAuth account and an API-key account exist for `POST /v1/chat/completions`, the router may route a `deepseek-v4-flash-free` request to the OAuth account (ChatGPT Codex backend), which does not support that model. The model string is treated as opaque application data — it only flows through model rename and request recording, never participates in routing decisions.

## Goals

1. Each account (API-key and OAuth) can declare which models it supports.
2. Models can be auto-discovered from the upstream `/v1/models` endpoint.
3. Models can be manually added/removed via the admin portal.
4. At routing time, filter eligible accounts by the requested model.
5. If no account supports the model, return a clear business error.

## Design Decisions (Confirmed)

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Empty model list | Reject all requests | Forces explicit configuration; no silent "catch-all" behavior |
| Model matching | Exact match (`model_id`) | Simple, predictable; no glob/regex complexity |
| Model not found | 400 + native MVP shape `model_not_supported` | Client error (bad model name), not server capacity. Fires on data-plane path (`/v1/chat/completions`), so uses native MVP shape `{"error":{"type":"invalid_request","code":"model_not_supported","message":"..."}}` — never the Admin API envelope. Error code 8001 is used in operator logs, not in the envelope body. |
| OAuth model source | Only upstream returned models on refresh | `deepseek-v4-flash-free` and similar won't appear in Codex's model list; user manually adds them |
| Refresh semantics | Replace `source=upstream` rows; preserve `source=manual` | Manual additions survive refresh |
| Error code range | 8xxx (Feature 008 segment) | 001-006 allocated; 7xxx reserved for 007 |
| Auth on admin endpoints | WARN-level audit logs; no auth enforcement | Follows existing MVP pattern (declared in OpenAPI, not enforced) |
| GET /v1/models | Intersect upstream union with `account_models` | Prevents clients from seeing unsupported models |
| Domain layer isolation | No `HasModel()` on `UpstreamAccount` struct | Domain layer is pure data (no store imports); model check lives in the eligibility closure |

## Data Model

### New Table: `account_models`

```sql
CREATE TABLE account_models (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id  INTEGER NOT NULL REFERENCES upstream_accounts(id) ON DELETE CASCADE,
    model_id    TEXT    NOT NULL,
    source      TEXT    NOT NULL DEFAULT 'manual' CHECK(source IN ('manual', 'upstream')),
    created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(account_id, model_id)
);
CREATE INDEX idx_account_models_account_id ON account_models(account_id);
CREATE INDEX idx_account_models_model_id   ON account_models(model_id);
```

**Why independent table (not JSON column on accounts):**
- Per-row source tracking (`manual` vs `upstream`)
- Indexed lookup for `HasModel(accountID, modelID)` at routing time
- Extensible: future columns (rate-limit override, quota, etc.) per model
- Consistent with relational pattern in the codebase (no JSON column complexity)

### Domain Struct

```go
// AccountModel lives in domain layer — pure data, no store dependency.
type AccountModel struct {
    ID        int64     `xorm:"pk autoincr 'id'" json:"id"`
    AccountID int64     `xorm:"not null 'account_id'" json:"account_id"`
    ModelID   string    `xorm:"not null 'model_id'" json:"model_id"`
    Source    string    `xorm:"not null default('manual') 'source'" json:"source"`
    CreatedAt time.Time `xorm:"created not null 'created_at'" json:"created_at"`
    UpdatedAt time.Time `xorm:"updated not null 'updated_at'" json:"updated_at"`
}
```

### UpstreamAccount Changes

**No new methods or fields on `UpstreamAccount`.** The domain layer stays pure (no store imports). Model filtering is implemented entirely in the api layer's eligibility closure.

### Sentinels

```go
var ErrModelNotFound = errors.New("model not found on any active account")
```

## Core Layer Interface

New repository interface in `internal/core/interfaces.go`:

```go
type AccountModelRepository interface {
    HasModel(ctx context.Context, accountID int64, modelID string) (bool, error)
    ListByAccount(ctx context.Context, accountID int64) ([]domain.AccountModel, error)
    Insert(ctx context.Context, accountID int64, modelID string, source string) error
    Delete(ctx context.Context, accountID int64, modelID string) error
    ReplaceUpstreamModels(ctx context.Context, accountID int64, modelIDs []string) (added int, keptManual int, err error)
    AccountsWithModel(ctx context.Context, modelID string) ([]int64, error)
    DistinctModelsForAccounts(ctx context.Context, accountIDs []int64) ([]string, error)
}
```

## API Changes

### Internal: Admin API (4 new endpoints)

```
POST   /api/admin/accounts/{id}/models/add     body: {model_id: string}
POST   /api/admin/accounts/{id}/models/remove   body: {model_id: string}
POST   /api/admin/accounts/{id}/models/refresh
GET    /api/admin/accounts/{id}/models
```

All follow the existing admin envelope pattern (`{code, msg, data}`).

- **add**: Insert with `source='manual'`; error if duplicate (code 8002).
- **remove**: Delete row regardless of source.
- **Account not found**: All model endpoints return HTTP 200 + envelope `code=1001 account_not_found` (reuse existing 001 code), NOT HTTP 404. Follows existing admin pattern.
- **refresh**: 
  1. Resolve upstream URL:
     - API-key accounts: `account.BaseURL + /v1/models` (OpenAI Platform-compatible)
     - OAuth accounts: `CodexBackendBaseURL() + /codex/models` (ChatGPT Codex backend)
  2. Call upstream, parse `{object:"list", data:[{id:...}]}` response
  3. Delete all `source='upstream'` rows for this account (in a transaction)
  4. Insert current upstream models as `source='upstream'`
  5. Keep `source='manual'` rows intact
  6. Return counts: `{added: N, manual_kept: M}`
- **list**: Return all rows for account, ordered by model_id.

### Internal: Data Plane (eligibility change)

The eligibility function in `proxy.go` gains a `model` parameter and a store reference:

```go
func proxyAccountEligible(
    route gatewayRoute,
    model string,
    accountModelRepo core.AccountModelRepository,
) func(context.Context, domain.UpstreamAccount) bool
```

When `model != ""`, after existing capability checks, calls `accountModelRepo.HasModel(ctx, account.ID, model)`. If false, the account is filtered out.

**Performance: Batch lookup (no N+1)**

For routing, we avoid the N+1 pattern by pre-computing the set of account IDs that have the requested model:

```go
// In proxy.go or websocket_proxy.go, before account selection:
var eligibleAccountIDs map[int64]struct{}
if model != "" {
    ids, err := accountModelRepo.AccountsWithModel(ctx, model)
    // Convert to map for O(1) lookup
    eligibleAccountIDs = make(map[int64]struct{}, len(ids))
    for _, id := range ids {
        eligibleAccountIDs[id] = struct{}{}
    }
}

// The eligibility closure then uses the pre-fetched set:
func proxyAccountEligible(
    route gatewayRoute,
    eligibleAccountIDs map[int64]struct{},
) func(domain.UpstreamAccount) bool {
    return func(account domain.UpstreamAccount) bool {
        if account.IsOAuth() {
            if !route.OAuth.Eligible { return false }
        } else {
            if !route.APIKey.Eligible { return false }
            if route.OpID != "" && !account.HasCapabilityFor(string(route.OpID)) {
                return false
            }
        }
        // Model filter: skip if model is "" (WebSocket) or no filter set
        if eligibleAccountIDs != nil {
            if _, ok := eligibleAccountIDs[account.ID]; !ok {
                return false
            }
        }
        return true
    }
}
```

This means:
- 1 query: `SELECT account_id FROM account_models WHERE model_id = ?` (via `AccountsWithModel`)
- 1 query: `SELECT * FROM upstream_accounts WHERE status = 'active'` (via `ListActive`)
- Total: 2 queries per routing decision, regardless of account count.

**Note**: The eligibility closure signature changes from `func(domain.UpstreamAccount) bool` to `func(context.Context, domain.UpstreamAccount) bool` — but this is NOT needed because the batch lookup happens before the closure is called. The closure just checks map membership. No signature change required.

### WebSocket Handling

WebSocket requests have no body (model is `""`). The eligibility closure skips model filtering when `model == ""`:
- `eligibleAccountIDs` is `nil` → closure returns `true` for all eligible accounts
- This is the existing behavior for WebSocket routes

### GET /v1/models Interaction

The `GET /v1/models` endpoint currently returns the union of all upstream model lists from ALL active eligible accounts. After this feature:

The union should be **filtered to only include model IDs that exist in at least one active account's `account_models` table**. This prevents clients from enumerating models that will immediately return 400.

Implementation approach in `proxy_models.go`:
1. After building the union from upstream, call `DistinctModelsForAccounts(ctx, activeAccountIDs)` to get the set of declared model IDs
2. Filter the union to only include models in that set

### OpenAPI Spec

New paths under `/api/admin/accounts/{id}/models/`:
```yaml
/api/admin/accounts/{accountId}/models:
  get:
    summary: List models for an account
    operationId: accountModelsList
    parameters:
      - name: accountId
        in: path
        required: true
        schema: {type: integer}
    responses:
      '200': {$ref: '#/components/responses/AccountModelsListResponse'}

/api/admin/accounts/{accountId}/models/add:
  post:
    operationId: accountModelAdd
    requestBody:
      content:
        application/json:
          schema:
            type: object
            properties:
              model_id: {type: string}
            required: [model_id]
    responses:
      '200': {$ref: '#/components/responses/AccountModelAddResponse'}

/api/admin/accounts/{accountId}/models/remove:
  post:
    operationId: accountModelRemove
    requestBody:
      content:
        application/json:
          schema:
            type: object
            properties:
              model_id: {type: string}
            required: [model_id]
    responses:
      '200': {$ref: '#/components/responses/AccountModelRemoveResponse'}

/api/admin/accounts/{accountId}/models/refresh:
  post:
    operationId: accountModelsRefresh
    responses:
      '200': {$ref: '#/components/responses/AccountModelsRefreshResponse'}
```

Error codes:
- `8xxx` range for this feature (Feature 008)

### Audit Logging

Every model mutation generates a structured WARN-level log event:

| Event | Log message | Payload |
|-------|-------------|---------|
| Model added | `account_model_added` | `account_id`, `model_id`, `source` |
| Model removed | `account_model_removed` | `account_id`, `model_id` |
| Models refreshed | `account_models_refreshed` | `account_id`, `added_count`, `kept_manual_count` |
| Model not supported | `model_not_supported` | `model_id`, `requested_account_count`, `eligible_account_count` |

No authentication enforcement on admin endpoints (follows MVP pattern — declared in OpenAPI with `adminAuth` but not enforced).

### Frontend: Account Detail Page

- Add "Models" section to the account detail/edit page
- Table: model_id | source | actions (remove)
- "Add Model" button → inline input → POST add
- "Refresh from upstream" button → confirm dialog → POST refresh
- Show loading/error states

## Implementation Plan

### Phase 1: DB + Domain + Store

**Migration** (3 dialects):

```
internal/store/migrations/sqlite/000008_account_models.up.sql
internal/store/migrations/sqlite/000008_account_models.down.sql
internal/store/migrations/postgres/000008_account_models.up.sql
internal/store/migrations/postgres/000008_account_models.down.sql
internal/store/migrations/mysql/000008_account_models.up.sql
internal/store/migrations/mysql/000008_account_models.down.sql
```

**Files to create:**
- `internal/domain/account_model.go` — struct + sentinel errors (pure data, no store)
- `internal/store/account_models.go` — `AccountModelRepo` implementing `core.AccountModelRepository`

### Phase 2: Core Interface

- Add `AccountModelRepository` interface to `internal/core/interfaces.go`
- Wire into `AccountSelector` or `ProxyHandler` (api layer concern)

### Phase 3: Provider — FetchModels

Add a method to the OpenAI client that fetches and parses `/v1/models`:

```go
func (c *Client) FetchModels(ctx context.Context, account domain.UpstreamAccount, token []byte) ([]string, error)
```

Reuses the existing models bridge path (can use `ForwardBridgeRequestWithCapture` with `BridgeOpenAIModelsDirect` or adapt the existing OAuth models facade logic).

### Phase 4: Routing — Eligibility

- Add `AccountModelRepository` to `ProxyHandler` struct
- Implement `proxyAccountEligible` with batch model filtering
- `proxy.go` extracts model via `openai.ExtractModel(effectiveReqBodyBytes)`, calls `AccountsWithModel`, passes result to eligibility closure
- Same pattern for `websocket_proxy.go` (model is `""`, skip filtering)
- Same pattern for `proxy_models.go` (GET /v1/models union filtering)

### Phase 5: Admin API

- New handler struct or extend existing `Handler` in `internal/api/admin/handler.go`
- 4 new handler methods + envelope wrappers in `internal/api/adminapi/wrap.go`
- Route registration in `internal/api/adminapi/routes.go`

### Phase 6: OpenAPI Spec

- Add schemas + paths to `openapi/admin.yaml`
- `make openapi-gen` to regenerate Go + TS clients

### Phase 7: Frontend (separate track)

- Account detail page model management UI

## Data Flow (Routing)

```
Request: POST /v1/chat/completions {model: "deepseek-v4-flash-free"}
  │
  ├─ proxy.go:163  read body → reqBodyBytes
  ├─ proxy.go:195  model rename (if configured) → effectiveReqBodyBytes
  ├─ proxy.go:204  model = ExtractModel(effectiveReqBodyBytes)
  │
  ├─ proxy.go:     accountModelRepo.AccountsWithModel(ctx, "deepseek-v4-flash-free")
  │     └─ SELECT account_id FROM account_models WHERE model_id = 'deepseek-v4-flash-free'
  │     └─ returns [3, 7] (account IDs that support this model)
  │
  ├─ proxy.go:204  SelectEligible(ctx, sessionKey, proxyAccountEligible(route, accountIDSet))
  │     │
  │     ├─ store: ListActive() → all active accounts
  │     ├─ for each account:
  │     │   ├─ proxyAccountEligible: capability check (existing)
  │     │   ├─ proxyAccountEligible: model check (NEW — O(1) map lookup)
  │     │   │   └─ accountIDSet contains account.ID?
  │     │   └─ if either fails → filtered out
  │     ├─ if 0 remaining → return 400 model_not_supported
  │     └─ if ≥1 remaining → session hash / round-robin
  │
  └─ ForwardBridgeRequestWithCapture → upstream
```

## Error Codes

| Code | Symbol | HTTP | Meaning |
|------|--------|------|---------|
| 8001 | `model_not_supported` | 400 (data-plane native, not envelope) | No active account supports the requested model. Fires on data-plane path (`/v1/chat/completions`), so uses native MVP shape (`{"error":{"type":"invalid_request","code":"model_not_supported","message":"..."}}`). The code 8001 is used in operator logs and is registered for potential admin API use; it is NOT an envelope code on data-plane paths. |
| 8002 | `account_model_duplicate` | 200 | Attempted to add a model that already exists on the account. |
| 8003 | `account_model_refresh_failed` | 200 | Upstream models endpoint unreachable or returned error. |
| 8900 | `account_model_internal_error` | 500 | Unexpected model service failure. |

Reserved range: 8xxx (Feature 008 Account Model Routing).

## Edge Cases

### Backward Compatibility & Migration

**Deploy procedure**: Operator manually refreshes or configures models for each existing account after deploy. No automatic fill.

**Data migration**: No boot-time auto-fill. The `account_models` table starts empty for all existing accounts. Operators MUST add models post-deploy before routing works.

**During transition**: After deploy, all proxied requests return 400 `model_not_supported` until at least one account has models configured. Operator should:
1. Deploy the new binary + migration
2. For each active API-key account: `POST /api/admin/accounts/{id}/models/refresh`
3. For each active OAuth account: manually add models via `POST /api/admin/accounts/{id}/models/add`

### Account Disabled/Deleted with Models

If an account is disabled or deleted while it has `account_models` rows, the orphaned rows are harmless:
- They never match in routing (disabled/deleted accounts are filtered by `ListActive`)
- They consume negligible storage (indexed rows with no read overhead)
- `ON DELETE CASCADE` handles deletion automatically
- No cleanup goroutine needed

### Performance

**Batch lookup (no N+1)**: `AccountsWithModel(modelID)` returns all account IDs having the model in a single query. Routing uses 2 total queries regardless of account count.

| Case | Behavior |
|------|----------|
| Account created with no models | All requests rejected until at least one model added |
| Model renamed via `model_renames` config | Rename happens before eligibility check; eligibility sees the renamed model |
| Upstream /v1/models returns 503 | Refresh returns error 8003, existing models unchanged |
| Duplicate manual add | Returns 200 with code 8002 `account_model_duplicate` |
| OAuth refresh, no upstream models found | Deletes all `source=upstream` rows (leaves manual), account may end up with 0 models |
| Session key + model filter | Session hash runs against filtered list; if the hashed account doesn't have the model, it's excluded from the active set → hash to next account |
| Concurrent refresh | `ReplaceUpstreamModels` runs in a transaction: delete WHERE account_id=X AND source='upstream', then insert new rows |
| WebSocket (model="") | Model filtering skipped entirely; all eligible accounts available |

## Rollback Plan

1. Reverse the DB migration: `000008_account_models.down.sql` (drops `account_models` table; `ON DELETE CASCADE` ensures no orphaned rows remain)
2. Revert the eligibility function in `proxy.go` / `websocket_proxy.go` / `proxy_models.go` to ignore model
3. Remove admin API endpoints
4. **Data loss**: Dropping `account_models` loses all model associations but no request history or account data. Operators must re-configure models if they roll back and re-deploy.
