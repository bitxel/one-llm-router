# Data Model: Account Model Routing (008)

## New Table: `account_models`

| Column | Type | Constraints | Notes |
|--------|------|-------------|-------|
| `id` | INTEGER | PK AUTOINCREMENT | Row identifier |
| `account_id` | INTEGER | NOT NULL, FK → `upstream_accounts(id)` ON DELETE CASCADE | Owning account |
| `model_id` | TEXT | NOT NULL | Exact model ID string (e.g. `gpt-4o`) |
| `source` | TEXT | NOT NULL DEFAULT `'manual'`, CHECK(`source` IN (`'manual'`, `'upstream'`)) | Manual or auto-discovered |
| `metadata` | TEXT NULL | — | JSON snapshot of the upstream model object (added by migration `000011_account_models_metadata`); `NULL` for manual rows without upstream metadata |
| `created_at` | TIMESTAMP | DEFAULT CURRENT_TIMESTAMP | Row creation time |
| `updated_at` | TIMESTAMP | DEFAULT CURRENT_TIMESTAMP | Last modification time |

**Indexes:**
- `idx_account_models_account_id` on `(account_id)` — fast lookup for per-account model list
- `idx_account_models_model_id` on `(model_id)` — fast lookup for routing: "which accounts have this model?"

**Unique constraint:** `UNIQUE(account_id, model_id)` — prevents duplicate model entries per account.

## Domain Struct

```go
type AccountModel struct {
    ID        int64     `xorm:"pk autoincr 'id'" json:"id"`
    AccountID int64     `xorm:"not null 'account_id'" json:"account_id"`
    ModelID   string    `xorm:"not null 'model_id'" json:"model_id"`
    Source    string    `xorm:"not null default('manual') 'source'" json:"source"`
    Metadata  *string   `xorm:"'metadata'" json:"metadata,omitempty"`
    CreatedAt time.Time `xorm:"created not null 'created_at'" json:"created_at"`
    UpdatedAt time.Time `xorm:"updated not null 'updated_at'" json:"updated_at"`
}
```

## Core Interface

```go
type AccountModelRepository interface {
    HasModel(ctx context.Context, accountID int64, modelID string) (bool, error)
    ListByAccount(ctx context.Context, accountID int64) ([]domain.AccountModel, error)
    ListByAccounts(ctx context.Context, accountIDs []int64) ([]domain.AccountModel, error)
    Insert(ctx context.Context, accountID int64, modelID string, source string) error
    Delete(ctx context.Context, accountID int64, modelID string) error
    ReplaceUpstreamModels(ctx context.Context, accountID int64, models []domain.AccountModelDraft) (added int, keptManual int, err error)
    AccountsWithModel(ctx context.Context, modelID string) ([]int64, error)
}
```

## Key Queries

### Routing: "Which accounts support model X?"

```sql
SELECT account_id FROM account_models WHERE model_id = ?
```

Returns account IDs. Used by `AccountsWithModel()`. Indexed on `model_id`.

### GET /v1/models: "What models do active accounts declare?"

```sql
SELECT account_id, model_id, source, metadata
FROM account_models
WHERE account_id IN (?, ?, ...)
ORDER BY model_id ASC, account_id ASC
```

Returns full cache rows (including upstream object `metadata` JSON snapshots). Used by `ListByAccounts()` for the cache-backed `GET /v1/models` list. Indexed on `account_id`.

### Refresh: Replace upstream models atomically

```sql
BEGIN;
DELETE FROM account_models WHERE account_id = ? AND source = 'upstream';
INSERT INTO account_models (account_id, model_id, source, metadata) VALUES (?, ?, 'upstream', ?), ...;
COMMIT;
```

Manual (`source='manual'`) rows are preserved and keep their existing `metadata` (typically NULL).

## Invariants

1. `account_models` rows are only meaningful for active accounts. Orphaned rows (account disabled/deleted) are harmless — `ON DELETE CASCADE` handles deletion, and routing only considers active accounts.
2. `source='manual'` rows survive refresh; `source='upstream'` rows are replaced.
3. Empty model list = reject all requests for that account (no catch-all behavior).
4. Model IDs are case-sensitive exact strings with no validation beyond non-empty.
