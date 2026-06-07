# Feature Spec: Account Model Routing

**ID**: 008-account-model-routing
**Created**: 2026-06-07
**Updated**: 2026-06-07
**Status**: Ready

## Overview

Platform operators need per-account model filtering so that requests for models a specific upstream account doesn't support (e.g. `deepseek-v4-flash-free` routed to a ChatGPT OAuth account) are rejected early with a clear error instead of silently forwarded to an incompatible backend. Each account (API-key and OAuth) can declare which models it supports via manual configuration or auto-discovery from the upstream `/v1/models` endpoint. At routing time, the router filters eligible accounts by the requested model and returns a business error if no account supports the model.

## User Scenarios

### US-1: Configure Per-Account Model Support (P0)

As a platform operator, I want to declare which models each upstream account supports, so that the router does not forward requests to accounts that cannot handle them.

**Acceptance Scenarios**:
1. Given an API-key account is active, when I add `gpt-4o` to its model list via `POST /api/admin/accounts/{id}/models/add`, then the model is stored with `source=manual`.
2. Given an OAuth account is active, when I manually add `deepseek-v4-flash-free` via the admin endpoint, then the model is stored with `source=manual`.
3. Given an account already has a model, when I attempt to add the same model again, then I receive a `account_model_duplicate` error (code 8002).
4. Given an account has manual models, when I remove one, then the model is deleted regardless of source.
5. Given an account does not have a model, when I remove it, then the operation succeeds as a no-op (idempotent).

**Edge Cases**:
- Adding a model to a non-existent account returns `account_not_found` (code 1001).
- Model IDs are case-sensitive exact strings with no validation beyond non-empty.

### US-2: Auto-Discover Models from Upstream (P0)

As a platform operator, I want to refresh an account's model list from its upstream `/v1/models` endpoint, so that I don't have to manually maintain the full list.

**Acceptance Scenarios**:
1. Given an API-key account with `base_url=https://api.openai.com/v1`, when I call `POST /api/admin/accounts/{id}/models/refresh`, then the router calls `GET {base_url}/models` and stores the returned model IDs as `source=upstream`.
2. Given an OAuth account, when I call refresh, then the router calls `GET {CodexBackendBaseURL}/codex/models` and stores the returned model IDs as `source=upstream`.
3. Given an account has both manual and upstream models, when I refresh, then `source=upstream` rows are replaced and `source=manual` rows are preserved.
4. Given the upstream `/v1/models` endpoint is unreachable, when I refresh, then I receive an `account_model_refresh_failed` error (code 8003) and existing models are unchanged.

**Edge Cases**:
- OAuth accounts that don't support `/codex/models` return an error.
- Concurrent refresh operations use a transaction to avoid partial state.

### US-3: Route Requests by Model (P0)

As an internal client developer, I want the router to only send requests to accounts that support the requested model, so that I don't get silent failures or provider-specific errors.

**Acceptance Scenarios**:
1. Given account A supports `gpt-4o` and account B supports `deepseek-v4-flash-free`, when I request `model=gpt-4o`, then the request is routed to account A.
2. Given no account supports the requested model, when I send a request, then I receive a `model_not_supported` error (code 8001) at HTTP 400.
3. Given an account has an empty model list, when a request arrives for any model, then that account is filtered out.
4. Given a WebSocket connection (model is `""`), when it arrives, then model filtering is skipped and all eligible accounts are available.

**Edge Cases**:
- Model rename (`model_renames` config) is applied before eligibility check; the eligibility check sees the renamed model.
- Session hash routing runs against the filtered account list; if the hashed account doesn't support the model, the hash maps to the next eligible account.

### US-4: View Models in GET /v1/models (P1)

As an internal client developer, when I call `GET /v1/models`, I want the response to only include models that at least one active account supports, so that I don't enumerate models that will immediately return 400.

**Acceptance Scenarios**:
1. Given account A supports `gpt-4o` and account B supports `deepseek-v4-flash-free`, when I call `GET /v1/models`, then the response includes both models.
2. Given account A supports `gpt-4o` but has no `dall-e-3` in its `account_models`, when I call `GET /v1/models`, then `dall-e-3` is excluded even if the upstream returned it.
3. Given no accounts have any models configured, when I call `GET /v1/models`, then the response returns an empty list.

**Edge Cases**:
- Models present in `account_models` but not returned by any upstream are excluded from the response.
- The intersection is computed from active, eligible accounts only.

### US-5: Audit Model Mutations (P1)

As a platform operator, I want every model add/remove/refresh operation to generate a structured log event, so that I can audit changes.

**Acceptance Scenarios**:
1. Given I add a model via the admin API, when the operation succeeds, then a WARN-level log event `account_model_added` is emitted with `account_id`, `model_id`, and `source`.
2. Given I refresh models, when the operation succeeds, then a WARN-level log event `account_models_refreshed` is emitted with `account_id`, `added_count`, and `kept_manual_count`.

**Edge Cases**:
- No authentication enforcement on admin endpoints (follows MVP pattern).
- Log events never include token material.

## Non-Functional Requirements

1. **Performance**: Routing decisions use at most 2 DB queries regardless of account count (batch model lookup + ListActive).
2. **Storage**: `account_models` rows are small (indexed integers + short strings). No performance degradation for existing routing when the table is empty.
3. **Backward compatibility**: After deploy, the router continues to work but all proxied requests return `model_not_supported` until at least one account has models configured.
4. **Zero-downtime deploy**: The migration is additive (new table only). No existing tables are modified.
5. **Rollback**: Dropping `account_models` loses model associations but no request history or account data.

## Scope Boundaries

**In scope**:
- `account_models` table + migration (3 dialects)
- Admin API: 4 new endpoints for model CRUD
- Routing: model filter in eligibility closure
- `GET /v1/models`: intersection with `account_models`
- Frontend: model management in account detail page
- OpenAPI spec updates

**Out of scope**:
- Bulk model import/export
- Per-model rate limits or quotas (future column on `account_models`)
- Auto-refresh on schedule (operator-triggered only)
- Model validation against upstream (just stores what the operator or refresh provides)
