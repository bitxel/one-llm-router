# Account Models API Contract

## POST /api/admin/accounts/{id}/models/add

**Request:**
```json
{
  "model_id": "gpt-4o"
}
```

**Success (200):**
```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "account_id": 1,
    "model_id": "gpt-4o",
    "source": "manual",
    "created_at": "2026-06-07T00:00:00Z",
    "updated_at": "2026-06-07T00:00:00Z"
  }
}
```

**Duplicate (200):**
```json
{
  "code": 8002,
  "msg": "account_model_duplicate",
  "data": {}
}
```

**Account not found (200):**
```json
{
  "code": 1001,
  "msg": "account_not_found",
  "data": {}
}
```

---

## POST /api/admin/accounts/{id}/models/remove

**Request:**
```json
{
  "model_id": "gpt-4o"
}
```

**Success (200):**
```json
{
  "code": 0,
  "msg": "ok",
  "data": {}
}
```

**Model not found (200):**
```json
{
  "code": 0,
  "msg": "ok",
  "data": {}
}
```
(Idempotent — removing a non-existent model is a no-op.)

---

## POST /api/admin/accounts/{id}/models/refresh

**Request:** No body.

**Success (200):**
```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "added": 15,
    "kept_manual": 3
  }
}
```

**Upstream error (200):**
```json
{
  "code": 8003,
  "msg": "account_model_refresh_failed",
  "data": {}
}
```

---

## GET /api/admin/accounts/{id}/models

**Request:** No body.

**Success (200):**
```json
{
  "code": 0,
  "msg": "ok",
  "data": {
    "models": [
      {
        "id": 1,
        "account_id": 1,
        "model_id": "gpt-4o",
        "source": "manual",
        "created_at": "2026-06-07T00:00:00Z",
        "updated_at": "2026-06-07T00:00:00Z"
      },
      {
        "id": 2,
        "account_id": 1,
        "model_id": "deepseek-v4-flash-free",
        "source": "upstream",
        "created_at": "2026-06-07T00:00:00Z",
        "updated_at": "2026-06-07T00:00:00Z"
      }
    ]
  }
}
```

**Account not found (200):**
```json
{
  "code": 1001,
  "msg": "account_not_found",
  "data": {}
}
```
