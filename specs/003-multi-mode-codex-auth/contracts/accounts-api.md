# Contract: Admin Accounts API — 003 delta

**Feature**: 003-multi-mode-codex-auth
**Base path**: `/api/admin/accounts/*`
**Relationship to 002**: delta against `specs/002-setup-wizard-and-admin-portal-skeleton/contracts/admin-api.md`. Every 002 endpoint keeps its v1 shape; 003 adds response-shape fields and three new POST endpoints. Wrappers in `internal/api/adminapi/wrap.go` translate 001 handler output into the envelope (002 pattern); the three new 003 endpoints live in `internal/api/adminapi/import_auth_json.go` (US-6 import, per `tasks.md T-065`), `internal/api/exportapi/export_auth_json.go` (US-7 export, per `tasks.md T-080` — separate package because the success branch is a non-envelope `application/json` file download and we do NOT want adminapi's envelope wrappers shadowing that response), and `internal/api/adminapi/reauth.go` (US-5 reauth, per `tasks.md T-070`). All three write directly through `api.WriteOK` / `api.WriteBizErr` / `api.WriteSysErr` in `internal/api/envelope.go` for their error branches; `exportapi` writes the success body itself.

**Machine-readable source of truth (partial)**: [`openapi/admin.yaml`](../../../openapi/admin.yaml) (OpenAPI 3.0.3) is the canonical spec ONLY for the **003-new** admin surface: `/api/admin/oauth/*`, `POST /api/admin/accounts/import-auth-json`, `POST /api/admin/accounts/{id}/export-auth-json`, and `POST /api/admin/accounts/{id}/reauth`. The **002-inherited** routes — `GET /api/admin/accounts` and `POST /api/admin/accounts` below — stay documented in this Markdown file (and in `specs/002-.../contracts/admin-api.md`) until the admin-auth feature (004+) lifts them into OpenAPI; see `openapi/admin.yaml` §`info.description` "Deferred to a later feature". Per `AGENTS.md §API Contract` rule #1 the YAML remains authoritative for the 003-new shapes, this Markdown is authoritative for the 002-inherited shapes PLUS the flow prose that YAML cannot express (audit semantics, drop-current-tokens behavior, import/export exemptions). Any disagreement within each authority zone is a bug; both files MUST be updated in the same commit when a 003-new shape changes.

**Response envelope — non-negotiable**. Every `/api/admin/accounts/*` endpoint returns `Content-Type: application/json; charset=utf-8` and **always** uses the 002 envelope policy defined in `docs/error-codes.md` §HTTP envelope policy and implemented by `internal/api/envelope.go`:

- **HTTP 200** for success AND business errors. The outcome lives in `body.code`: `code: 0` is success, non-zero is a registered 1xxx/2xxx/3xxx business error.
- **HTTP 500** (system error) for unrecoverable failures: `oauth_internal_error` (3900), `oauth_store_failed` (3901), or the 1xxx/2xxx system codes from earlier features.
- **HTTP 4xx is forbidden** on `/api/admin/accounts/*` — the `envelope_parity_test` CI check fails any 4xx leak.

**One documented exemption** lives in this file: the **success** body of `POST /api/admin/accounts/{id}/export-auth-json` is the verbatim Codex-CLI `auth.json` (FR-014 operator interop with the Codex CLI's on-disk format). Its **error** responses DO wear the envelope. Clients discriminate success vs error by `Content-Disposition` being present. This exemption is documented in `docs/error-codes.md` §Envelope exemptions.

---

## GET `/api/admin/accounts` — response shape extended

**Relative to 002**: response rows gain 6 new fields (only populated for OAuth rows), and one existing response field (`api_key`) becomes forbidden even as a redacted value.

### Response (Success)

**HTTP 200**:

```json
{
  "code": 0,
  "msg":  "ok",
  "data": {
    "accounts": [
      {
        "id":           42,
        "name":         "prod-plus",
        "provider":     "openai",
        "auth_method":  "oauth_browser",
        "status":       "active",
        "base_url":     "https://api.openai.com",
        "created_at":   "2026-04-15T09:14:00Z",
        "updated_at":   "2026-04-15T10:02:17Z",

        "email":              "alice@example.com",
        "plan_type":          "chatgpt-plus",
        "plan_type_label":    "ChatGPT Plus",
        "chatgpt_account_id": "org_7f2b9a3e",
        "last_refresh":       "2026-04-15T10:02:17Z",
        "access_expires_at":  "2026-04-15T11:02:17Z"
      },
      {
        "id":           7,
        "name":         "staging-apikey",
        "provider":     "openai",
        "auth_method":  "api_key",
        "status":       "active",
        "base_url":     null,
        "created_at":   "2026-03-02T08:00:00Z",
        "updated_at":   "2026-03-02T08:00:00Z"
      }
    ]
  }
}
```

Notes:
- **API-key rows do NOT carry `email` / `plan_type` / `plan_type_label` / `chatgpt_account_id` / `last_refresh` / `access_expires_at`** — those keys are omitted, not `null` (keeps the admin table clean; the UI treats key-absence as "N/A" per FR-011a).
- **`access_expires_at` is OAuth-only and nullable** — present on every OAuth row as an absolute UTC RFC 3339 timestamp computed by the server (`last_refresh + provider-supplied expires_in` for US-1/US-3/US-4, or `last_refresh + tokens.OAuth.exp_in_days` with the 28-day codex-lb fallback on import per US-5). When the provider omits `expires_in` entirely, the field is emitted as JSON `null` rather than omitted so the UI can distinguish "unknown lifetime" from "not applicable (API key)".
- **No token material is ever in this response** — `access_token`, `refresh_token`, `id_token`, and `api_key` are never keys of an account object returned from this route (FR-003, FR-011).
- `plan_type_label` is **server-computed** so every UI client agrees on the label. Mapping (case-sensitive; unmapped → raw `plan_type` string): `chatgpt-plus → "ChatGPT Plus"`, `chatgpt-team → "ChatGPT Team"`, `chatgpt-enterprise → "ChatGPT Enterprise"`. Unknown → the raw value (FR-011a explicit fallback).

### Response (Errors)

Same as 002 — this route is read-only and inherits the 002 envelope shape unchanged.

---

## POST `/api/admin/accounts` — request shape extended

**Relative to 002**: request body gains `auth_method`. When `auth_method='api_key'` the behavior preserves the 002 create-account semantics while returning the explicit `auth_method` discriminator. For the three OAuth modes, this endpoint creates the row as a side-effect of an OAuth flow and therefore is NOT the operator-facing entry point — the UI routes operators through `/oauth/browser/start` / `/oauth/device/start` / `/accounts/import-auth-json` instead. This endpoint is kept callable only for `auth_method='api_key'` so 002's wizard + admin table both keep working unchanged.

### Request (API-key mode only)

```json
{
  "name":        "prod-apikey",
  "provider":    "openai",
  "api_key":     "sk-…",
  "base_url":    "https://api.openai.com",
  "auth_method": "api_key"
}
```

(If `auth_method` is omitted the server defaults it to `"api_key"` — preserves 002's request shape.)

### Response (Errors)

| HTTP | `code` | `msg` | When | `data` shape |
|---|---|---|---|---|
| 200 | 3013 | `oauth_mode_requires_flow_endpoint` | Operator passed `oauth_browser` / `oauth_device` / `oauth_import` on this endpoint — server refuses; OAuth onboarding is multi-step and must go through `/api/admin/oauth/browser/start`, `/api/admin/oauth/device/start`, or `/api/admin/accounts/import-auth-json`. | `{ "allowed_here": ["api_key"], "got": "oauth_browser" }` |

All other errors identical to 002 (the envelope translation happens inside `adminapi/wrap.go` — see `contracts/admin-api.md` in 002's spec).

---

## POST `/api/admin/accounts/{id}/update` — api_key-row edit

Edit an existing **api_key** upstream account. OAuth rows are rejected (`ErrInvalidAccountShape` → `1008 invalid_account_payload`): their tokens are flow-minted and their metadata is observed upstream fact, neither of which the operator hand-edits — re-auth/re-import is the OAuth edit path.

**Auth**: admin-auth plugin (same as every other `/api/admin/*` mutation).
**Semantics**: optional-field patch — a field **omitted** (or `null`) keeps the current value; a field **present** is set. This is a POST (project RPC verb convention), not PATCH.

### Request

```json
{
  "name":         "renamed-account",
  "api_key":      "sk-…",
  "base_url":     "https://api.openai.com",
  "capabilities": ["op.openai.responses"]
}
```

| Field | Type | Semantics |
|---|---|---|
| `name` | string | optional; if present must be 1..64 characters after trimming (charset unrestricted; control characters rejected — see `domain.ValidAccountName`). Stored trimmed. |
| `api_key` | string | optional; `""` (or absent) keeps the existing key; a non-empty value must be 1..256 characters and replaces the stored key. Never echoed back. |
| `base_url` | string \| null | optional; `""` clears the stored override to NULL; a non-empty value must be an absolute http(s) URL ≤256 chars without query/fragment. |
| `capabilities` | string[] | optional; absent/`null` keeps the current set; an array (incl. `[]`) replaces the whole set. Values restricted to known capability prefixes. |

An entirely-empty request body (`{}`) is a no-op: the server returns the current row without touching `updated_at`.

### Response (Success)

HTTP 200 with the updated account row in `data` — same shape as `GET /api/admin/accounts/{id}` (`id`, `name`, `provider`, `base_url`, `status`, `created_at`, `updated_at`, `capabilities`; OAuth-only metadata absent for api_key rows).

### Response (Errors)

| HTTP | `code` | `msg` | When | `data` shape |
|---|---|---|---|---|
| 200 | 1003 | `invalid_account_payload` | Field validation failed (name/key/base_url/capabilities) or the target row is an OAuth account. | `{ "field": "name", "detail": "<server message>" }` |
| 200 | 1001 | `account_not_found` | No row matches `{id}`. | `{}` |
| 200 | 1004 | `account_already_in_state` | The row is deleted. | `{}` |

---

## POST `/api/admin/accounts/import-auth-json` (NEW in 003)

Import a local Codex CLI `~/.codex/auth.json` as a new OAuth upstream account (US-6).

**Auth**: admin-auth plugin.
**Idempotent**: No — each import creates a row. Duplicate `chatgpt_account_id` is allowed (US-6 edge case 2).
**Body cap**: dual-layer, per `research.md` Decision 7.

- **Outer envelope cap**: 64 KB on the entire `multipart/form-data` request body (via `http.MaxBytesReader`). Sized so legitimate multipart clients NEVER trip it; exists solely as a preamble-flood OOM defence for `mime/multipart`. Overflow ⇒ `code: 2009 request_body_too_large` + `data = {scope: "envelope", limit_bytes: 65536}`.
- **Inner part cap**: 16 KB on the `auth_json` part content (via `io.LimitReader`). This is the semantic payload budget operators see; typical Codex CLI `auth.json` is 5–8 KB (three JWTs + metadata). Overflow ⇒ `code: 2009 request_body_too_large` + `data = {scope: "part", limit_bytes: 16384}`.

Both layers reuse 002's registered `2009` code to avoid duplication; callers discriminate by `data.scope`.

### Request

**Wire shape**: `multipart/form-data` with ONE required part `auth_json` whose body is the raw Codex CLI `auth.json` bytes. This shape is aligned with the codex-lb reference implementation — no `name` or `provider` fields in the request.

- `name` is **derived server-side** from the id_token's `email` claim (fallback `chatgpt_account_id` when the claim is missing).
- `provider` is always `"openai"` in 003 (the only supported provider).
- Any additional form parts are silently ignored.

Example request (multipart body, rendered as HTTP wire):

```
POST /api/admin/accounts/import-auth-json HTTP/1.1
Content-Type: multipart/form-data; boundary=----boundary

------boundary
Content-Disposition: form-data; name="auth_json"; filename="auth.json"
Content-Type: application/json

{ "OPENAI_API_KEY": null, "tokens": { "id_token": "eyJ…", "access_token": "eyJ…", "refresh_token": "rt_…", "account_id": "org_7f2b9a3e" }, "last_refresh": "2026-04-15T10:02:17Z" }
------boundary--
```

Expected `auth_json` part body shape (matches the Codex CLI and our own export):

```json
{
  "OPENAI_API_KEY": null,
  "tokens": {
    "id_token":       "eyJ…",
    "access_token":   "eyJ…",
    "refresh_token":  "rt_…",
    "account_id":     "org_7f2b9a3e"
  },
  "last_refresh": "2026-04-15T10:02:17Z"
}
```

Unknown top-level keys are **ignored**, not rejected (US-6 edge case 4).

### Response (Success)

**HTTP 200**:

```json
{
  "code": 0,
  "msg":  "ok",
  "data": {
    "account": { /* AccountListItem per data-model.md — auth_method=oauth_import */ }
  }
}
```

Extracted fields (populated from the imported payload):
- `access_token` / `refresh_token` / `id_token` ← `tokens.{...}` (raw string → UTF-8 bytes → BLOB/BYTEA/`VARBINARY(8192)` column, per `data-model.md` row shape)
- `chatgpt_account_id` ← decoded from `id_token` payload, probing in order (first hit wins): `claims["https://api.openai.com/auth"]["chatgpt_account_id"]` → `claims["auth"]["chatgpt_account_id"]` → `tokens.account_id` (the id_token is the authoritative claim; `tokens.account_id` is only used when the id_token doesn't expose the claim)
- `email` ← `id_token` `email` claim (RFC 7519 standard)
- `plan_type` ← decoded from `id_token` payload, probing in order: `claims["https://api.openai.com/auth"]["plan_type"]` → `claims["auth"]["plan_type"]`; `null` if both missing
- `last_refresh` ← `last_refresh` from the payload, parsed as RFC3339; if missing or unparseable, falls back to `time.Now()` (log WARN `auth_json_import_last_refresh_fallback` once per import so operators notice drift)
- `access_expires_at` ← derived per `data-model.md` §`access_expires_at` derivation fallback: first try `id_token` `exp` claim (RFC 7519 integer seconds-since-epoch; convert via `time.Unix(exp,0).UTC()`); if the claim is missing or not an integer, fall through to `time.Now().Add(5*time.Minute)` and emit `auth_json_import_last_refresh_fallback` WARN with `reason=id_token_exp_absent`. Required non-NULL for `oauth_import` rows (see `data-model.md` row-level invariants).

### Response (Errors)

| HTTP | `code` | `msg` | When | `data` shape |
|---|---|---|---|---|
| 200 | 3010 | `invalid_auth_json_structure` | `Content-Type` is NOT `multipart/form-data` OR the required `auth_json` part is missing OR the part body is not valid JSON OR is valid JSON but not an object. | `{}` |
| 200 | 2009 | `request_body_too_large` | Outer: multipart envelope > 64 KB (preamble-flood defence — legitimate clients never hit this). | `{ "scope": "envelope", "limit_bytes": 65536 }` |
| 200 | 2009 | `request_body_too_large` | Inner: `auth_json` part body > 16 KB (semantic payload over budget). | `{ "scope": "part", "limit_bytes": 16384 }` |
| 200 | 3011 | `invalid_auth_json` | The `auth_json` part body parsed as an object but a required path is missing (`tokens.access_token`, `tokens.refresh_token`, `tokens.id_token`) OR `id_token` fails base64url-decode / JSON-parse of its claims segment. `data.missing_fields` is a per-field list of dotted paths — shape compatible with 002's wizard validator. `data.missing_fields` MUST NOT echo any token bytes. | `{ "missing_fields": ["tokens.access_token", …] }` |

Logs: `auth_json_import_rejected` INFO event on any business-error envelope; the token payload MUST NOT appear in any log line (contract with log-scrub test). Successful imports emit a normal `account_created` INFO event (002 pattern) — NOT an OAuth-specific event, because no OAuth flow ran.

---

## POST `/api/admin/accounts/{id}/export-auth-json` (NEW in 003)

Dump the stored `auth.json` for one OAuth account (FR-014).

**Auth**: admin-auth plugin (exactly like every other `/api/admin/*` mutation — when the plugin is disabled on single-operator boxes, the operator inherits the 002 trust boundary).
**Idempotent**: Yes (reading the same row twice returns the same bytes), but every call audits.
**Rate limit**: NONE beyond the generic per-operator admin-API rate envelope (decision recorded in `spec.md` Clarifications session 2026-04-15).

### Request

Empty body; the `id` in the URL path is the only parameter.

### Response (Success) — **documented envelope exemption**

**HTTP 200**. Headers:
- `Content-Type: application/json; charset=utf-8`
- `Content-Disposition: attachment; filename="auth.json"` — **discriminator**. Clients check this header: present → body is a raw Codex CLI `auth.json`; absent → body is the envelope (error path).
- `Cache-Control: no-store, private`
- `X-Content-Type-Options: nosniff`

**Body** (byte-compatible with Codex CLI's on-disk format — Decision 8 in `research.md` explains why):

```json
{
  "OPENAI_API_KEY": null,
  "tokens": {
    "id_token":       "eyJhbGciOi…",
    "access_token":   "eyJhbGciOi…",
    "refresh_token":  "rt_…",
    "account_id":     "org_7f2b9a3e"
  },
  "last_refresh": "2026-04-15T10:02:17Z"
}
```

The SPA wires this response as `<a download>` on a blob URL so the token JSON lands on disk as a file and **never** touches React state / console (FR-014).

**Why this exemption exists**: if we wrapped the `auth.json` in `{"code":0,"msg":"ok","data":{...}}` the operator would have to unwrap it by hand before feeding it to the Codex CLI — the whole point of the endpoint is drop-in compatibility with `~/.codex/auth.json`. The exemption is a contract with the Codex CLI's on-disk format, not a convenience; the discriminator header makes it safe for programmatic clients.

### Response (Errors) — envelope as usual

| HTTP | `code` | `msg` | When | `data` shape |
|---|---|---|---|---|
| 200 | 1001 | `account_not_found` | `id` does not exist. **Reuses 001's registered code** rather than minting a duplicate 3xxx (cross-feature semantic already owned by 001; `docs/error-codes.md` §Feature 003 documents this under reserved row 3017). | `{}` |
| 200 | 3014 | `not_oauth_account` | Row exists but `auth_method='api_key'` — FR-014 explicitly forbids export for API-key rows. UI MUST hide the button for those rows; this is the server-side backstop. | `{ "auth_method": "api_key" }` |

Logs: **every** successful export emits `oauth_auth_json_exported` WARN (fields in `contracts/oauth-flow-api.md`). Every failed export emits the matching INFO (`oauth_auth_json_export_rejected`) with the business-error `code`; no token bytes in either.

---

## POST `/api/admin/accounts/{id}/reauth` (NEW in 003)

Rotate credentials in place without losing the row (US-5, FR-010).

**Auth**: admin-auth plugin.
**Idempotent**: No (starts a new OAuth flow or accepts a new API key).

### Request

Two shapes, keyed on the row's existing `auth_method`:

- For an API-key row:
  ```json
  { "api_key": "sk-new-…" }
  ```
- For an OAuth row: send an empty JSON object body `{}`. A zero-byte body is transport-invalid and returns `2008 malformed_body`. The operator picks `browser` or `device` via a query param `?method=browser|device`.

### Response (Success)

**HTTP 200**. Two payload shapes depending on the row's `auth_method`:

- **API-key rotation**:

  ```json
  {
    "code": 0,
    "msg":  "ok",
    "data": {
      "account": { /* updated AccountListItem per data-model.md */ }
    }
  }
  ```

- **OAuth rotation** — same `data` fields as `POST /api/admin/oauth/browser/start` or `/api/admin/oauth/device/start` **plus one additional field `target_account_id`**:

  ```json
  {
    "code": 0,
    "msg":  "ok",
    "data": {
      "flow_id":          "fl_a1b2c3",
      "authorize_url":    "https://auth.openai.com/oauth/authorize?…",
      "callback_url":     "http://localhost:1455/auth/callback",
      "listener_bound":   true,
      "expires_at":       "2026-04-15T10:19:00Z",
      "method":           "browser",
      "target_account_id": 42
    }
  }
  ```

  (For `?method=device`: `data` swaps `authorize_url`/`callback_url`/`listener_bound` for `user_code`/`verification_url`/`interval_seconds`, identical to `/oauth/device/start`'s `data` shape, plus the same `target_account_id` echo.)

  `target_account_id` MUST equal the `{id}` from the URL path and MUST be echoed by `GET /api/admin/oauth/flow` (inside `data.target_account_id`) for the lifetime of this flow (`data-model.md` §`OAuthFlow.TargetAccountID`). Its presence is how the admin UI knows to render the "Re-authenticating account #42…" banner instead of the "Adding new account…" banner, and how it navigates back to `/admin/accounts/42` on success instead of `/admin/accounts`. On success, the `store` call is a **credential-only UPDATE** against this `id` (FR-010) — `name`/`provider`/`base_url`/`stats` are NOT touched, and no new row is inserted.

### Response (Errors)

| HTTP | `code` | `msg` | When | `data` shape |
|---|---|---|---|---|
| 200 | 1001 | `account_not_found` | `id` does not exist. Reuses 001's registered code (see `docs/error-codes.md` §Feature 003 reserved row 3017). | `{}` |
| 200 | 3012 | `auth_method_mismatch` | API-key request body was sent against an OAuth row (or vice versa). | `{ "row_auth_method": "oauth_browser" }` — tells the UI which shape to send instead. |
| 200 | 3001 | `oauth_flow_in_progress` | Another OAuth flow is running. | Same `data` shape as `/oauth/browser/start` §3001. |
| 200 | 2005 | `invalid_api_key` | Same as 002 (reuses 002's registered code; `auth_method='api_key'` path only). | 002's shape. |

Rotation never overwrites `name`, `provider`, `base_url`, `routing config`, or `stats`; only credential columns move (FR-010).

**US-5 AC-1 clarification (drop current tokens)**. "Drop current tokens" is interpreted as: once an OAuth-row reauth flow reaches `Status=success` on the coordinator, the `store.PersistFlow` transaction (a) writes the **new** `access_token`/`refresh_token`/`id_token` bytes to the existing row by `id=target_account_id`, and (b) performs NO explicit DELETE on the old bytes — the UPDATE replaces them in-place. No sidecar "old token" is retained anywhere (no audit log entry carries token bytes; no soft-delete column). This satisfies AC-1 "drop the previously stored tokens" literally: after commit, the row observably holds only the new credentials. On flow `Status=error` / `Status=cancelled`, the row is **untouched** — the original tokens keep serving traffic until the operator retries or explicitly disables the row. Detailed call sequence: `plan.md` §US-5 reauth sequence.
