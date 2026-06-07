# Quickstart: Account Model Routing (008)

## Setup

1. Deploy the new binary + run migration:
   ```bash
   # SQLite
   go run github.com/golang-migrate/migrate/v4/cmd/migrate \
     -path internal/store/migrations/sqlite -database "sqlite3://router.db" up
   ```

2. For each API-key account, refresh models from upstream:
   ```bash
   curl -X POST http://localhost:8080/api/admin/accounts/1/models/refresh
   ```

3. For each OAuth account, manually add models:
   ```bash
   curl -X POST http://localhost:8080/api/admin/accounts/2/models/add \
     -H 'Content-Type: application/json' \
     -d '{"model_id": "gpt-4o"}'
   ```

4. Verify models are configured:
   ```bash
   curl http://localhost:8080/api/admin/accounts/1/models
   ```

5. Test routing:
   ```bash
   curl -X POST http://localhost:8080/v1/chat/completions \
     -H 'Content-Type: application/json' \
     -d '{"model": "gpt-4o", "messages": [{"role": "user", "content": "hello"}]}'
   ```

## Verify GET /v1/models intersection

```bash
curl http://localhost:8080/v1/models
```

Should only return models that at least one active account has in its `account_models` table.
