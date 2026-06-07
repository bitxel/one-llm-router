# Tasks: Account Model Routing (008)

## Phase 1: DB + Domain + Store

### Task 1.1: Create `account_models` migration (SQLite)
- Create `internal/store/migrations/sqlite/000008_account_models.up.sql`
- Create `internal/store/migrations/sqlite/000008_account_models.down.sql`
- Verify migration runs cleanly on SQLite

### Task 1.2: Create `account_models` migration (PostgreSQL)
- Create `internal/store/migrations/postgres/000008_account_models.up.sql`
- Create `internal/store/migrations/postgres/000008_account_models.down.sql`

### Task 1.3: Create `account_models` migration (MySQL)
- Create `internal/store/migrations/mysql/000008_account_models.up.sql`
- Create `internal/store/migrations/mysql/000008_account_models.down.sql`

### Task 1.4: Create domain struct
- Create `internal/domain/account_model.go`
- Define `AccountModel` struct with xorm tags
- Define `ErrModelNotFound` sentinel

### Task 1.5: Create `AccountModelRepository` interface
- Add `AccountModelRepository` interface to `internal/core/interfaces.go`
- Methods: `HasModel`, `ListByAccount`, `Insert`, `Delete`, `ReplaceUpstreamModels`, `AccountsWithModel`, `DistinctModelsForAccounts`

### Task 1.6: Implement `AccountModelRepo` store
- Create `internal/store/account_models.go`
- Implement all methods from `AccountModelRepository` interface
- `ReplaceUpstreamModels` uses a transaction (delete upstream rows, insert new ones)

### Task 1.7: Write tests for `AccountModelRepo`
- Test all CRUD operations
- Test `ReplaceUpstreamModels` preserves manual rows
- Test `AccountsWithModel` returns correct account IDs
- Test `DistinctModelsForAccounts` returns correct model IDs
- Use SQLite in-memory for fast tests

## Phase 2: Core — Eligibility

### Task 2.1: Update `proxyAccountEligible` for model filtering
- Modify `proxyAccountEligible` in `internal/api/proxy.go` to accept `eligibleAccountIDs map[int64]struct{}`
- Add model check: if `eligibleAccountIDs != nil`, check map membership
- Update call site in `proxy.go` to pre-fetch model IDs via `AccountsWithModel`

### Task 2.2: Update `websocket_proxy.go` for model filtering
- Apply same pattern: model is `""` for WebSocket, skip filtering (pass `nil` for `eligibleAccountIDs`)

### Task 2.3: Update `proxy_models.go` for GET /v1/models intersection
- After building model union from upstream, call `DistinctModelsForAccounts` to get declared models
- Filter union to only include models in the declared set

### Task 2.4: Write routing eligibility tests
- Test that accounts without the requested model are filtered out
- Test that accounts with the model are eligible
- Test that WebSocket (model="") skips filtering
- Test that empty model list rejects all requests

## Phase 3: Admin API

### Task 3.1: Create admin model handlers
- Create `internal/api/adminapi/account_models.go` (or extend existing handler)
- Implement 4 handler methods: `addModel`, `removeModel`, `refreshModels`, `listModels`
- Each wraps response in envelope `{code, msg, data}`

### Task 3.2: Register routes
- Add routes to `internal/api/adminapi/routes.go`
- POST `/api/admin/accounts/{id}/models/add`
- POST `/api/admin/accounts/{id}/models/remove`
- POST `/api/admin/accounts/{id}/models/refresh`
- GET `/api/admin/accounts/{id}/models`

### Task 3.3: Create error codes
- Add `8001-8003` and `8900` to `internal/api/errcode/codes.go`
- Add frontend constants to `frontend/src/lib/errcode.ts`

### Task 3.4: Write admin API tests
- Test add model success and duplicate error
- Test remove model success and non-existent model
- Test refresh models success and upstream failure
- Test list models returns all rows
- Test account not found returns code 1001

### Task 3.5: Add audit logging
- Emit structured WARN log events for each mutation
- Events: `account_model_added`, `account_model_removed`, `account_models_refreshed`

## Phase 4: OpenAPI Spec

### Task 4.1: Update OpenAPI spec
- Add schemas and paths to `openapi/admin.yaml`
- Run `make openapi-gen` to regenerate Go + TS clients

## Phase 5: Frontend

### Task 5.1: Add model management UI to account detail page
- Table: model_id | source | actions (remove)
- "Add Model" button → inline input → POST add
- "Refresh from upstream" button → confirm dialog → POST refresh
- Show loading/error states

## Phase 6: Integration Tests

### Task 6.1: End-to-end routing test
- Create two accounts with different models
- Verify requests route to the correct account
- Verify requests for unsupported models return 8001

### Task 6.2: GET /v1/models intersection test
- Verify only declared models are returned
- Verify empty model list returns empty union
