# Data Model: Account Playground

**Feature**: 004-account-playground
**Spec**: `specs/004-account-playground/spec.md`
**Created**: 2026-04-23
**Status**: Ready

No persistent schema changes are required. The feature is operational and additive: it creates one transient Playground Run per submit and records completed/failed submitted runs through the existing request-record model.

---

## Entity: Playground Run

Transient request/response object owned by the Admin Portal run operation.

| Attribute | Type | Constraints | Description |
|-----------|------|-------------|-------------|
| `selection_mode` | enum | `auto` or `account`; required | Operator's routing choice |
| `account_id` | integer | Required only when `selection_mode=account`; must reference an active eligible account | Explicit account target |
| `session_key` | string | Optional, 1..128 characters when present | Sticky automatic-selection value for repeated runs |
| `model` | string | Required, 1..128 characters; UI default `gpt-5.4-mini` | Provider model identifier for the text probe |
| `text` | string | Required, 1..16000 Unicode characters after trimming | Single-turn prompt text |
| `max_output_tokens` | integer | Optional, 1..4096; default `1024` | Upper bound for generated output |
| `include_raw_response` | boolean | Optional, default false | Whether the response may include bounded safe raw JSON |
| `selected_account` | object | Present when an account is resolved | Non-secret account metadata used for display/debugging |
| `outcome` | enum | success, validation_failure, no_available_account, account_unavailable, upstream_error, upstream_timeout, router_error, cancelled, no_extractable_text | Terminal run outcome |
| `latency_ms` | integer | Non-negative | Router-observed run duration |
| `usage` | object | Optional | Token usage extracted from provider response when available |
| `output_text` | string | Optional | Parsed text when extractable; omitted/empty when unavailable |
| `raw_response` | object | Optional, bounded, sanitized | Safe raw provider JSON when requested, within size limits, and sanitized |

### Relationships

- Playground Run → Upstream Account: zero or one account before selection; exactly one account after a successful selection or account-specific failure after resolution.
- Playground Run → Request Record: one submitted run should create at most one request record. Client-side validation failures that create no run do not create a request record.

### State Transitions

```
idle -> submit_valid -> pending -> success
idle -> submit_invalid -> validation_failure
pending -> selected_account_missing -> account_unavailable
pending -> no_active_account -> no_available_account
pending -> upstream_non_2xx -> upstream_error
pending -> upstream_timeout -> upstream_timeout
pending -> upstream_body_unparseable -> router_error
pending -> provider_success_no_text -> no_extractable_text
pending -> client_cancel -> cancelled
pending -> system_failure -> router_error
```

---

## Entity: Selection Mode

| Attribute | Type | Constraints | Description |
|-----------|------|-------------|-------------|
| `mode` | enum | `auto` or `account` | Discriminates how the account is selected |
| `session_key` | string | Optional in `auto` | Allows sticky auto selection using the router's normal policy |
| `account_id` | integer | Required in `account` | Explicit active account id |

### Invariants

- `auto` mode ignores `account_id`.
- `account` mode rejects missing or non-positive `account_id`.
- `account` mode must use the selected account or fail; it never falls back to `auto`.

---

## Entity: Safe Account Summary

Non-secret account projection shown in the picker/result.

| Attribute | Type | Constraints | Description |
|-----------|------|-------------|-------------|
| `id` | integer | Required | Upstream account id |
| `name` | string | Required | Operator-facing account name |
| `provider` | string | Required | Provider key |
| `auth_method` | enum | Required | API-key or OAuth auth method |
| `status` | enum | Required | Account status |
| `base_url` | string | Optional | Non-secret upstream base URL |
| `email` | string | Optional | OAuth metadata |
| `plan_type` | string | Optional | OAuth metadata |
| `plan_type_label` | string | Optional | Human-readable plan label |
| `last_refresh` | timestamp | Optional | OAuth metadata |
| `access_expires_at` | timestamp | Optional | OAuth metadata |

### Invariants

- No API key, access token, refresh token, ID token, or authorization header can appear in this projection.
- Account mode only exposes active eligible accounts as selectable. Disabled/deleted accounts may appear only as non-runnable context if the UI deliberately chooses to show them; P0 should filter to active.

---

## Entity: Request Record (existing)

Existing `request_records` rows are reused for submitted Playground runs.

| Existing Attribute | Planned Use |
|--------------------|-------------|
| `request_id` | Correlates UI, logs, and request history |
| `upstream_account_id` | Selected account id when known |
| `session_key` | Playground session key when provided |
| `method` | `POST` |
| `path` | `/api/admin/playground/run`, distinguishing Playground from `/v1/*` client traffic |
| `status_code` | Upstream status when reached; router/admin status for pre-upstream failures |
| `latency_ms` | End-to-end run duration |
| `outcome` | Existing outcome values where possible; new playground-specific outcomes stay in error code/metadata unless a later migration adds an origin/outcome enum |
| `error_code` | Registered playground or upstream error symbol when present |
| `model` | Requested model |
| `model_params` | Safe run parameters such as `max_output_tokens`, `selection_mode`, and raw-response omission reason |
| `router_metadata.upstream_endpoint` | Actual downstream path sent from the router to the LLM server when an upstream request was built, e.g. `/codex/responses`; empty when no upstream request was built |
| `response_mode` | `json` for P0 |
| `token_usage` | Provider usage when extractable |
| `client_request_body` | Playground API request body only if runtime client request-body logging is enabled |
| `upstream_request_body` | Provider request body only if runtime upstream request-body logging is enabled |
| `upstream_response_body` | Provider/admin response body only if runtime upstream response-body logging is enabled |

## Migration Strategy

- **Forward**: No database migration.
- **Rollback**: No database rollback required.
- **Data compatibility**: Existing request history and account rows remain compatible. The new feature writes additional rows to the existing request history table only when a submitted run is created.

## Open Planning Constraints

- If implementation discovers the existing request-record outcome enum is too narrow for query UX, do not widen the schema in this feature unless tests prove path-based filtering is insufficient. The current plan intentionally avoids a migration.
