# Error Code Registry

**Status**: Live (2026-04-27)
**Owner**: platform / all feature authors (each feature's author is responsible for registering new codes here before shipping).
**Applies to**: Every router-owned HTTP endpoint **except** `/v1/*` and selected `/backend-api/*` data-plane paths.

All router-owned responses use a single envelope. See `specs/001-codex-router-mvp/plan/tech-design.md` and `specs/002-setup-wizard-and-admin-portal-skeleton/contracts/` for the per-endpoint contracts.

```json
{
  "code": 0,          // integer, 0 = success, non-zero = error (see table below)
  "msg":  "ok",       // human-readable; "ok" on success
  "data": { … }       // endpoint-specific payload. MUST always be present and MUST NEVER be JSON null —
                      // on error (and on success with no payload) emit the zero value of the success
                      // type: `{}` for object-shaped endpoints, `[]` for list-shaped endpoints.
                      // This matches AGENTS.md §API Contract ("data is never null") so that
                      // TypeScript clients can rely on non-nullable envelope data.
}
```

## HTTP status policy

- **HTTP 200**: every expected response, including **business errors** (validation failures, already-committed state, permissions, etc.). The business error is in `body.code`.
- **HTTP 500**: system errors — panics, storage corruption, migration failures, the process being unable to answer. `body.code` still set for drill-down.
- **HTTP 5xx other than 500**: **not emitted by envelope-bearing handlers**. The exception is the native data plane — `/v1/*` and selected `/backend-api/*` paths — which is outside the envelope entirely (see below). Specifically, the setup gate returns **HTTP 503** + 001's native error shape on data-plane paths while `config.json` is absent, preserving the wire contract for data-plane clients. Inside envelope-bearing handlers, 502/503/504 are reserved for the reverse proxy layer if any and must not originate from router code.
- **HTTP 413**: removed in 2026-04-18 PM revision — body-too-large now returns **HTTP 200** with `code = 2009` (`request_body_too_large`) so the envelope policy stays consistent. The body-cap middleware still closes the connection for > 4× the cap (pathological abuse) with no body; that is the only router-owned HTTP response that is *not* a JSON envelope.
- **`/v1/*` and selected `/backend-api/*` paths are always excluded from the envelope.** Router-generated errors on those data-plane paths use 001's native MVP shape: `{"error": {"type", "code", "message"}}` with native HTTP status by class (`400 invalid_request`, `404 unsupported_endpoint`, `403 blocked_endpoint`, `502 upstream_connect_failed` / `upstream_response_invalid`, `503 no_available_account`, `504 upstream_timeout`). `X-Request-Id` is response-header-only and is never duplicated into the native error body. Provider responses on supported data-plane paths are never wrapped in `{code,msg,data}`. API-key accounts preserve the OpenAI Platform-compatible forwarding path and provider body shape only for the Feature 006 operation matrix, with `GET /v1/models` aggregated as a strict fail-fast union across active route-eligible API-key and OAuth accounts. OAuth accounts use ChatGPT Codex backend transport for explicit mapped paths: Responses, Responses compact, Feature 006 Chat Completions adapter, OAuth model-list facade contribution to `GET /v1/models`, and selected Codex-native `/backend-api/*`. OAuth `/v1/responses` forces upstream streaming because the ChatGPT Codex backend rejects `stream:false`, but the downstream response follows the client stream flag: `stream:true` returns SSE and `stream:false` or omitted `stream` returns the collected JSON response. Terminal upstream SSE events such as `response.failed` are converted to their terminal JSON payload for non-streaming clients; malformed or oversized upstream SSE is returned as `upstream_response_invalid`. When upstream response-body logging is enabled, the router stores the collected complete Responses JSON in `request_records.upstream_response_body` for router-forced-streaming OAuth calls.

`X-Request-Id` is set on every response header (success and error), including `/v1/*`. It is **not** duplicated into the envelope body.

## Code space layout

| Range | Feature | Owner |
|---|---|---|
| `0` | Success | — |
| `-1` | Unknown / unexpected (top-level recover) | platform |
| `1000–1999` | Feature 001 — Codex Router MVP | 001 author |
| `2000–2999` | Feature 002 — Setup wizard + admin portal skeleton | 002 author |
| `3000–3999` | Feature 003 — Multi-mode Codex Auth (OAuth browser + device, auth.json import/export, token refresh) | 003 author |
| `4000–4999` | Feature 004 — Account Playground | 004 author |
| `5000–5999` | Feature 005 — Observability | 005 author |
| `6000–6999` | Feature 006 — OpenAI API gateway | 006 author |
| `7000–7999` | Feature 007 — Data-plane Operation Bridge (reserved, no codes minted) | 007 author |
| `8000–8999` | Feature 008 — Account Model Routing | 008 author |

Each feature should prefer numbers toward the **low end** of its range and leave headroom at the top for additive codes in later revisions.

## Active codes

### Success

| Code | Symbol | Meaning |
|---|---|---|
| `0` | `ok` | Request succeeded. `msg = "ok"`, `data` carries the payload. |

### Generic / platform

| Code | Symbol | HTTP | Meaning |
|---|---|---|---|
| `-1` | `unknown_error` | 500 | Top-level panic recovered; `msg` is a sanitized string; `data = {}`. Operator should look at the logs. |

### Feature 001 — Codex Router MVP

> **Note**: `/v1/*` proxy endpoints and selected `/backend-api/*` data-plane paths are *excluded* from the envelope — they preserve the MVP router-error contract (`{error: {type, code, message}}` with `X-Request-Id` in the response header only and native HTTP 5xx on router errors), and provider responses never become Admin API envelopes. Feature 003 makes upstream transport account-specific: API-key rows preserve 002's Platform-compatible path except explicitly deferred/blocked paths; OAuth rows use the ChatGPT Codex backend for Responses traffic. Feature 006 adds OAuth `/v1/chat/completions` through a Codex Responses compatibility adapter, `GET /v1/models` as a strict fail-fast union of API-key Platform model lists and OAuth Codex model-list facade contributions, selected Codex-native `/backend-api/*` compatibility paths, and router-local usage under `GET /api/admin/usage`. `/v1/audio/transcriptions` is deferred from 006. The codes below apply only to **001's admin endpoints**, which were **relocated in 002** from `/admin/*` to `/api/admin/*` (`/api/admin/accounts`, `/api/admin/requests`, `/api/admin/sessions/resolve`, `/api/admin/health`) and wrapped in the new envelope at the same time. Path references in the rows below use the post-002 prefix.
>
> `/v1/*` and selected `/backend-api/*` router-generated errors retain their native shape; they do **not** appear in this table.

| Code | Symbol | HTTP | Meaning |
|---|---|---|---|
| `1000` | `ok` | 200 | **Legacy alias** of the generic success code `0`. New 002-era handlers and envelope wrappers around 001's admin endpoints MUST emit `code = 0, msg = "ok"`. `1000` is kept only to avoid breaking any 001 author that already shipped it; it is NOT to be used by the 002 envelope wrap (`T-303`) or by any handler written from 002 onwards. |
| `1001` | `account_not_found` | 200 | `GET /api/admin/accounts/{id}` or mutation against a missing/soft-deleted account. |
| `1002` | `account_name_conflict` | 200 | `POST /api/admin/accounts` with a name that already exists (unique constraint violation). |
| `1003` | `invalid_account_payload` | 200 | Create/update request body failed validation (missing field, unknown provider, malformed base_url, etc.). `data.field` carries the offending key. |
| `1004` | `account_already_in_state` | 200 | Enable/disable requested but account is already in that state. Idempotent no-op hint. |
| `1005` | `request_record_not_found` | 200 | `GET /api/admin/requests/{id}` target missing or already pruned by retention. |
| `1006` | `invalid_request_filter` | 200 | Request-log list/options/detail received an invalid query/path filter (bad time range, empty or oversized search, unknown outcome/response mode, invalid account id, invalid limit, or invalid cursor/id). Applies to `GET /api/admin/requests`, `GET /api/admin/requests/options`, and `GET /api/admin/requests/{id}`. |
| `1007` | `session_not_found` | 200 | `GET /api/admin/sessions/resolve?session_key=...` has no mapping (session unused or evicted). |
| `1008` | `invalid_pagination` | 200 | Reserved for future non-request-log pagination surfaces. Request-log pagination validation currently uses `1006 invalid_request_filter` so every list/options/detail validation failure has one operator code. |
| `1009` | `no_available_account` | 200 | Admin-surfaced variant of the `/v1/*` error — emitted by `/api/admin/health` when the account summary reports zero enabled accounts. |
| `1900` | `db_unavailable` | 500 | Admin read/write failed because the DB handle is down. System error. |
| `1901` | `internal_error` | 500 | Admin handler panicked or hit an unexpected state. Recovered by top-level middleware. |

### Feature 002 — Setup wizard + admin portal skeleton

| Code | Symbol | HTTP | Meaning |
|---|---|---|---|
| `2001` | `setup_already_done` | 200 | `config.json` is present on disk; setup wizard POSTs are no-ops. Client SPA should hard-navigate to `/admin/`. |
| `2002` | `invalid_driver` | 200 | DB driver must be one of `sqlite3`, `postgres`, `mysql`. |
| `2003` | `invalid_dsn` | 200 | DSN failed parse/probe. `data.hint` is an object with driver-specific remediation keys (e.g. `timeout_ms`, `sqlstate`, `driver_message`); unknown keys are ignored by clients. Contracts that emit this code (currently `contracts/setup-api.md`) MUST set `data.hint` on every non-parse failure and MAY set it on parse failures. |
| `2004` | `invalid_account_name` | 200 | Account name must be 1..64 characters (after trimming) and free of control characters. |
| `2005` | `invalid_api_key` | 200 | `api_key` must be 1..256 characters. |
| `2006` | `invalid_plugin_flag` | 200 | `plugins.<id>.enabled` must be a boolean and `<id>` must be known at this feature level. |
| `2007` | `invalid_retention` | 200 | `runtime.log_retention_days` must be an integer in `[1, 365]`. (Pre-Round-3 alias `retention_days` is the same key.) |
| `2008` | `malformed_body` | 200 | Request body is not valid JSON. |
| `2009` | `request_body_too_large` | 200 | Body exceeds the endpoint's tight cap. Single-layer endpoints (setup: 8 KiB, settings: 16 KiB, Playground run: 96 KiB) emit `data = {scope: "envelope", limit_bytes: <cap>}`. **Dual-layer endpoints** (`POST /api/admin/accounts/import-auth-json` per 003 `research.md` Decision 7) emit `data = {scope: "envelope", limit_bytes: 65536}` when the outer `http.MaxBytesReader` trips on the whole 64 KB multipart body (preamble-flood defence), or `data = {scope: "part", limit_bytes: 16384}` when the inner `io.LimitReader` trips on the 16 KB `auth_json` part (semantic budget). Clients MUST NOT assume a single `limit_bytes` value and SHOULD surface `scope` in operator-facing error messages to distinguish preamble-flood attacks (`envelope`) from oversized legitimate payloads (`part`). |
| `2010` | *(reserved — removed 2026-04-19 PM per D10; do NOT reuse)* | — | Previously `probe_in_progress` (server-wide one-in-flight lock on `/api/setup/probe-dsn`). Dropped in 002 because setup-pending routers deploy behind a trusted boundary and the 5-second probe deadline already bounds resource use. Re-introduce in 003 only if admin-auth multi-operator concurrency becomes an abuse vector. |
| `2011` | `setup_required` | 200 | `/api/admin/*` non-health endpoint hit while `config.json` is absent. **Never** emitted on data-plane prefixes — `/v1/*` and `/backend-api*` are outside the envelope during setup-pending and receive HTTP 503 + 001's native `type: "service_unavailable"` shape instead. |
| `2012` | `unknown_config_key` | 200 | `POST /api/admin/settings/update` carries a key that is not patchable in 002. |
| `2013` | `env_override_readonly` | 200 | Operator attempted to patch a field whose effective source is `env:*`. **Dormant in 002**: no 002 runtime key is env-overridable and `db.*` is not patchable via the settings endpoint at all, so this path is unreachable in production. The server-side check MUST still exist so 003+ can expose new env-overridable runtime keys (and so plugin flags added in 003+ can participate). Test coverage in T-301 uses a test-only extension of the env-overridable allow-list. |
| `2014` | `invalid_log_level` | 200 | `runtime.log_level` must be one of `debug`, `info`, `warn`, `error`. |
| `2015` | `invalid_account_provider` | 200 | `first_account.provider` is not in the 002 allow-list. Only `openai` is accepted at this feature level; additional providers ship in 003+. |
| `2016` | `invalid_base_url` | 200 | `first_account.base_url` failed URL validation (missing scheme, non-http(s), contains path/query/fragment, or exceeds 256 characters). |
| `2017` | `invalid_model_rename` | 200 | `runtime.model_renames` contains an invalid model mapping: non-array value, non-object entry, empty or overlong `from`/`to`, duplicate `from`, `from == to`, or more than 32 entries. |
| `2900` | `db_connect_failed` | 500 | Commit-time DB open failed after the operator's successful probe. |
| `2901` | `migrate_failed` | 500 | Boot-time or commit-time `migrate up` returned a non-`ErrNoChange` error. |
| `2902` | `commit_tx_failed` | 500 | DB COMMIT failed during `POST /api/setup/commit` **before** the config file was written. No on-disk state change; client may retry. |
| `2903` | `config_write_failed` | 500 | Filesystem error during tmp-file write, fsync, or rename *after* a successful DB COMMIT. Recoverable on next boot via brownfield auto-materialize. |

### Feature 003 — Multi-mode Codex Authentication

All `code != 0` entries below are **business errors at HTTP 200** per the envelope policy (except the explicitly-marked `3900`/`3901` system errors). The `/auth/callback` loopback listener served by `internal/oauth/flow.go` is **not** an envelope-bearing route (it is browser-facing HTML/plaintext only, lives outside `/api/*`); its shape is documented in `specs/003-multi-mode-codex-auth/contracts/oauth-flow-api.md` §`GET /auth/callback`. The export endpoint's **success** body is `application/json` + `Content-Disposition: attachment` carrying the verbatim `auth.json` bytes (documented exception below) — its **error** responses DO wear the envelope.

| Code | Symbol | HTTP | Meaning |
|---|---|---|---|
| `3001` | `oauth_flow_in_progress` | 200 | Another OAuth flow is already pending on this router; FR-008 forbids parallel flows. `data` carries `{method, flow_id, expires_at, created_at}` of the in-flight flow. Emitted by `POST /api/admin/oauth/{browser,device}/start`. |
| `3002` | `invalid_oauth_provider` | 200 | `provider` not in the 003 allow-list (`{"openai"}` only). `data.field = "provider"`. |
| `3003` | `oauth_state_mismatch` | 200 | CSRF `state` mismatch on `POST /api/admin/oauth/browser/manual-callback`. Flow stays `pending`; the other rail may still close it, or the operator may re-paste a corrected URL. Per-rail diagnostic: emitted via `oauth_rail_rejected` INFO log with `rail=manual_paste`. The loopback rail's state mismatch is NOT surfaced via this code — it returns a 400 plaintext page to the browser directly (see `contracts/oauth-flow-api.md` §`/auth/callback`). |
| `3004` | `no_flow_in_progress` | 200 | Manual-callback hit when no flow is pending; operator likely missed the timeout. |
| `3005` | `already_consumed` | 200 | `Flow.Consumed` CAS lost — the other rail already exchanged the code. Harmless race; UI's next `GET /flow` poll shows `status=success`. Emitted on `POST /api/admin/oauth/browser/manual-callback` only; the loopback rail's CAS-loss silently renders its normal 200 success page. |
| `3006` | `flow_expired` | 200 | Flow's `ExpiresAt` already passed before the callback arrived. Usually the expiry reaper logged `oauth_flow_expired` first; this is the fallback when a rail raced the reaper. |
| `3007` | `invalid_callback_url` | 200 | `POST /api/admin/oauth/browser/manual-callback` received a URL with no `code` AND no `error` param, OR whose scheme+host+path does not start with `http://localhost:<1455-1458>/auth/callback`. `data.reason` identifies which check failed (`missing_code_and_error`, `url_prefix_mismatch`). |
| `3008` | `flow_id_mismatch` | 200 | `POST /api/admin/oauth/cancel` received a `flow_id` that does not match the currently pending flow (stale UI). |
| `3009` | `oauth_invalid_grant` | 200 | OpenAI's `/oauth/token` endpoint returned `invalid_grant` during code exchange (PKCE `code_verifier` mismatch, code reuse, etc.). `data.provider_error` + `data.provider_message` carry the upstream fields verbatim. |
| `3010` | `invalid_auth_json_structure` | 200 | `POST /api/admin/accounts/import-auth-json`: **Content-Type is NOT `multipart/form-data`** OR the required `auth_json` part is missing OR the `auth_json` part body is not valid JSON OR is valid JSON but not an object. `data = {}`. (Wire shape per contract: `multipart/form-data` with one `auth_json` file part; aligned with the codex-lb reference implementation.) |
| `3011` | `invalid_auth_json` | 200 | `POST /api/admin/accounts/import-auth-json`: body parsed as an object but a required path is missing (`tokens.access_token`, `tokens.refresh_token`, `tokens.id_token`) OR `id_token` fails base64url-decode / JSON-parse of its claims segment. `data.missing_fields` lists offending paths (does NOT echo any token bytes). |
| `3013` | `oauth_mode_requires_flow_endpoint` | 200 | `POST /api/admin/accounts` was called with `auth_method ∈ {oauth_browser, oauth_device, oauth_import}`. OAuth onboarding is multi-step and must go through the dedicated flow endpoints (`/api/admin/oauth/{browser,device}/start`, `/api/admin/accounts/import-auth-json`). `data = {allowed_here: ["api_key"], got: <the rejected auth_method>}` — `got` echoes the input so the UI can render a precise redirect hint without re-reading the request body. |
| `3014` | `not_oauth_account` | 200 | `POST /api/admin/accounts/{id}/export-auth-json` was called against an `auth_method='api_key'` row. FR-014 explicitly forbids export for API-key rows. |
| `3015` | `device_auth_unavailable` | 200 | OpenAI's `/api/accounts/deviceauth/usercode` endpoint returned 404 or equivalent unavailability signal (matches `codex-lb` behaviour — the provider appears to feature-flag device auth). Operator can retry later or fall back to browser OAuth. `data.provider` echoes the provider string (currently enum `["openai"]` per OpenAPI `DeviceAuthUnavailableEnvelope`). No `data.hint` field — remediation guidance lives in the UI layer, not the wire envelope. |
| `3016` | `oauth_upstream_error` | 200 | OpenAI's `/oauth/token` or `deviceauth/token` endpoint returned a terminal error that is not `invalid_grant` (e.g. provider 400/500, unparseable response body). `data.provider_error` + `data.provider_message` + `data.http_status` for drill-down. |
| `3017` | `account_not_found_003` | 200 | **Reserved for parity with 001's `1001 account_not_found`** — the 003 `/export-auth-json` handler reuses `1001 account_not_found` (cross-feature pattern; 001 already owns the semantic) rather than minting a duplicate 3xxx code. This row is reserved to document the deliberate non-use; do NOT allocate. |
| `3900` | `oauth_internal_error` | 500 | Panic recovery inside `oauth.Coordinator` (single-flight panic, callback-server crash, etc.), OR an unclassified DB failure during flow completion. Operator should consult logs. |
| `3901` | `oauth_store_failed` | 500 | **Write-side failure**. Flow completed upstream (`/oauth/token` succeeded + tokens decoded) but the persist-to-`upstream_accounts` step failed (DB disk full, transaction deadlock, schema drift). The tokens are **lost** (not retried) and the flow transitions to `error`; operator must re-run OAuth. |
| `3902` | `oauth_export_read_failed` | 500 | **Read-side failure**. `POST /api/admin/accounts/{id}/export-auth-json` found the target row but the read of the token columns (`GetForExport`) failed at the storage layer (DB handle died mid-query, transient deadlock, schema drift). No bytes were written to the response; operator may retry. Distinct from `3901` (write-side); sharing that code would lose the read-vs-write observability signal. |

**Envelope-exemption, documented by contract**: `POST /api/admin/accounts/{id}/export-auth-json` returns the verbatim Codex CLI `auth.json` shape on **success** (byte-compatible with the Codex CLI's on-disk format — FR-014 operator interop). Error responses on that endpoint DO wear the envelope at HTTP 200 (business errors: `1001 account_not_found`, `3014 not_oauth_account`) or HTTP 500 (system errors: `3902 oauth_export_read_failed`). Clients discriminate by `Content-Disposition: attachment` — present on success, absent on error. See `specs/003-multi-mode-codex-auth/contracts/accounts-api.md` §`POST /api/admin/accounts/{id}/export-auth-json`.

### Feature 004 — Account Playground

All business errors below are returned at **HTTP 200** in the standard Admin API envelope. `4900` is a system error and is returned at **HTTP 500** only.

| Code | Symbol | HTTP | Meaning |
|---|---|---|---|
| `4001` | `invalid_playground_request` | 200 | `POST /api/admin/playground/run` failed request validation: empty/over-limit text, invalid model, invalid selection mode, missing account id, invalid session key, or invalid max-output cap. `data.field` identifies the field when available. |
| `4002` | `playground_no_active_account` | 200 | Automatic selection found no active eligible upstream account. No upstream request was made. |
| `4003` | `playground_account_unavailable` | 200 | Explicit account mode targeted a missing, deleted, disabled, or otherwise ineligible account. The router did not fall back to Automatic mode. |
| `4004` | `playground_upstream_error` | 200 | Provider returned a non-2xx response. `data` may include safe account id, upstream status, provider error symbol, and sanitized provider message. |
| `4005` | `playground_upstream_timeout` | 200 | Provider request exceeded the Playground wait limit. |
| `4006` | `playground_response_malformed` | 200 | Provider returned HTTP 2xx but the body was not safe parseable JSON for the Admin API result. |
| `4007` | `playground_response_too_large` | 200 | Provider response exceeded the bounded read cap before it could be safely rendered. |
| `4900` | `playground_internal_error` | 500 | Unexpected Playground handler/service failure. Operator should consult logs. |

### Feature 005 — Observability

All business errors below are returned at **HTTP 200** in the standard Admin API envelope. `5900` is a system error and is returned at **HTTP 500** only.

| Code | Symbol | HTTP | Meaning |
|---|---|---|---|
| `5001` | `dashboard_invalid_filter` | 200 | `GET /api/admin/dashboard` received an invalid range or account filter. |
| `5900` | `dashboard_internal_error` | 500 | Unexpected dashboard handler/service/storage failure. Operator should consult logs. |

### Feature 006 — OpenAI API gateway

`6900` is a system error and is returned at **HTTP 500** only.

| Code | Symbol | HTTP | Meaning |
|---|---|---|---|
| `6900` | `usage_internal_error` | 500 | Unexpected usage summary handler/service/storage failure for `GET /api/admin/usage`. Operator should consult logs. |

### Feature 008 — Account Model Routing

All business errors below are returned at **HTTP 200** in the standard Admin API envelope, except `8001` which fires on the data-plane path (`/v1/chat/completions`) and uses the native MVP shape (`{"error":{"type":"invalid_request","code":"model_not_supported","message":"..."}}`) at **HTTP 400**. `8900` is a system error and is returned at **HTTP 500** only.

| Code | Symbol | HTTP | Meaning |
|---|---|---|---|
| `8001` | `model_not_supported` | 400 (data-plane native) | No active account supports the requested model. Data-plane only — native MVP error shape, not admin envelope. String constant `ErrCodeModelNotSupported` in `internal/api/errors.go`. |
| `8002` | `account_model_duplicate` | 200 | Attempted to add a model that already exists on the account. |
| `8003` | `account_model_refresh_failed` | 200 | Upstream models endpoint unreachable or returned error during refresh. |
| `8900` | `account_model_internal_error` | 500 | Unexpected model service failure. |

## Adding a new code (checklist)

Every PR that introduces a new error path must:

1. Pick the lowest unused number in the feature's range.
2. Add a row to the table above, including the symbol (lowercase snake_case), HTTP status, and a one-line meaning.
3. Export a Go constant in `internal/api/errcode/codes.go`. The handler must always go through the constant — never a magic number literal at the call site.
4. Add a matching frontend constant in `frontend/src/lib/errcode.ts` so the UI can switch on it without string-matching `msg`.
5. Update any feature-level `contracts/*.md` response-error table to reference the symbol and code.

## Changelog

| Version | Date | Change |
|---|---|---|
| 1.0 | 2026-04-18 PM | Initial registry, created for the envelope rollout. 002 codes bootstrapped; 001 codes to be backfilled when 001's tech-design is updated. |
| 1.1 | 2026-04-18 PM | Backfilled 001 admin endpoint codes (`1000–1009` business, `1900–1901` system). Clarified that 001 `/v1/*` endpoints remain *outside* the envelope and keep their MVP error shape. |
| 1.2 | 2026-04-18 PM | Path convention update: admin and setup APIs moved under `/api/*` (affects every reference in this file — `/admin/setup/*` → `/api/setup/*`, `/admin/*` → `/api/admin/*`). `1000` demoted to legacy-alias status; `0` is the canonical success code. `2003` `data.hint` shape formalised as an object. Setup-pending policy for `/v1/*` spelled out: HTTP 503 + 001 native error shape, never envelope `code = 2011`. |
| 1.3 | 2026-04-19 | Round-3 settings schema review: added `2014 invalid_log_level`; clarified `2013 env_override_readonly` is dormant in 002 (no env-overridable runtime keys) but server-side enforcement is retained for 003+. Renamed `retention_days` → `log_retention_days` in `2007` description. |
| 1.4 | 2026-04-19 PM | Added `2015 invalid_account_provider` and `2016 invalid_base_url` so the setup-wizard validator no longer reaches into 001's `1003 invalid_account_payload` for fields that are exclusively wizard-scoped. Both new codes are HTTP 200 business errors (validation failures). |
| 1.5 | 2026-04-15 (003 Stage-5) | Added `3902 oauth_export_read_failed` (HTTP 500) to distinguish export-time read-side failures from the flow-completion `3901 oauth_store_failed` write-side failures. Updated `2009` to document the dual-layer body-cap policy for `POST /api/admin/accounts/import-auth-json` (outer **64 KB** envelope + inner 16 KB `auth_json` part, discriminated by `data.scope`). Aligned `3015` to omit `data.hint` per OpenAPI `DeviceAuthUnavailableEnvelope`. |
| 1.6 | 2026-04-23 | Reassigned `4000–4999` to Feature 004 Account Playground and registered `4001–4007` plus `4900`. Updated the code-addition checklist to point at the live Go/TS registries. |
| 1.7 | 2026-04-25 | Clarified `/v1/*` envelope exclusion after Feature 003: API-key rows remain OpenAI Platform-compatible, while OAuth rows intentionally use ChatGPT Codex backend transport for Responses traffic instead of byte-for-byte Platform forwarding. |
| 1.8 | 2026-04-25 | Registered Feature 005 Observability codes `5001 dashboard_invalid_filter` and `5900 dashboard_internal_error`. |
| 1.9 | 2026-04-26 | Clarified Feature 006 data-plane envelope exclusions for selected `/backend-api/*` paths, plus native `unsupported_endpoint` / `blocked_endpoint` router errors that are intentionally outside the Admin API code registry. |
| 1.10 | 2026-04-27 | Moved router-local usage observability to `GET /api/admin/usage` and registered `6900 usage_internal_error`; removed `/api/codex/usage` from data-plane envelope exclusions. |
| 1.11 | 2026-06-07 | Reserved `8000–8999` for Feature 008 — Account Model Routing. |
