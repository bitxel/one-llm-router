# Setup Wizard API Contract

**Feature**: 002-setup-wizard-and-admin-portal-skeleton
**Path prefix**: `/api/setup/`  (JSON APIs only — the SPA at `/setup/*` owns the HTML/assets surface)
**Auth**: None (the router is unauthenticated in setup mode; spec FR-014)
**Availability**: mixed — see per-endpoint section. `GET /api/setup/status` is **always** available (the SPA calls it on every load to decide whether to show the wizard or the portal). `POST /api/setup/probe-dsn` and `POST /api/setup/commit` are **only** available when `config.json` is absent from disk (i.e. gate engaged); after a successful commit they return envelope `code = 2001 setup_already_done`. The commit handler pre-checks `os.Stat(configPath)` before any DB work; concurrent commit losers also receive `code = 2001` via the `O_EXCL`/rename race described in `plan.md § Risk R-6`.

> **Path convention (2026-04-18 PM)**: this contract moved every JSON endpoint from `/admin/setup/*` to `/api/setup/*`. Rationale: the SPA owns `/setup/*` (wizard) and `/admin/*` (portal) exclusively for HTML deep-linking; all router-owned JSON endpoints live under the `/api/*` prefix to avoid path collisions (e.g. SPA `/admin/settings` page vs. API `/admin/settings` JSON). See `ROADMAP.md` Architectural Decision #9.

All router-owned JSON responses use the shared envelope (`docs/error-codes.md`):

```json
{ "code": <int>, "msg": "<string>", "data": <payload|{}|null> }
```

- HTTP `200` for all expected responses, including business errors (`code != 0`).
- HTTP `500` only when the server crashes mid-response or hits a system failure (2900-range codes).
- `X-Request-Id` on every response header; never duplicated in the body.
- `Content-Type: application/json; charset=utf-8`.

---

## `GET /api/setup/status`

**Description**: Return whether the wizard should be shown. Called by the frontend on every portal page load to decide redirect behavior.
**Idempotent**: yes
**Rate limit**: none
**Always available**: This endpoint responds in both `state = "pending"` (config.json absent) and `state = "done"` (config.json present). It is the single source of truth the SPA uses to decide between wizard and portal — hiding it after commit would strand the SPA on its first load post-setup. The handler does not touch the DB; it only stats `ROUTER_CONFIG_PATH`.

### Request
No body. No query parameters.

### Response · setup not yet complete

HTTP 200, envelope:

```json
{
  "code": 0,
  "msg":  "ok",
  "data": {
    "state": "pending",
    "supported_drivers": ["sqlite3", "postgres", "mysql"],
    "defaults": {
      "db": { "driver": "sqlite3", "url": "router.db" },
      "upstream_provider": "openai"
    }
  }
}
```

The list of current and future nav entries is hard-coded in the SPA bundle as TS constants in `frontend/src/components/shared/Sidebar.tsx` (Round-3 review 2026-04-19 — see `contracts/admin-api.md §GET /api/admin/settings` and the D7 decision in `plan.md §Decisions 2026-04-19`), so the wizard does not need to fetch it from the server. In 002 the server-side `data.plugins[]` returned by `GET /api/admin/settings` is an **empty array** — no concrete plugin ships in 002 — and it is consumed only by the Settings page (never by the sidebar). The current shell keeps `Dashboard`, `Accounts`, `Playground`, `Requests`, and `Settings` live, with `Client Keys` and `Observability` shown as disabled `planned` entries.

### Response · setup already complete

HTTP 200, envelope:

```json
{ "code": 0, "msg": "ok", "data": { "state": "done" } }
```

The frontend redirects to `/admin/` when it sees `data.state: "done"`.

### Response errors

None expected on the happy path. The handler only stats `ROUTER_CONFIG_PATH` (via `setup.ProbeState`) and never touches the DB, so the failure modes are narrow:

| HTTP | `code` | `msg` | When |
|------|--------|-------|------|
| 500  | `2900` | `setup_probe_failed` | `setup.ProbeState` returned a non-`ErrNotExist` filesystem error (e.g. permission denied on the config path, EIO). The handler emits the envelope with `data = null`; the SPA surfaces it via the generic error banner. |
| 500  | `-1`   | `unknown_error` | catch-all for any other system failure during a status lookup. |

---

## `POST /api/setup/probe-dsn`

**Description**: Attempt to open a database connection with the given driver+DSN, run `SELECT 1`, close the connection. Returns success/failure + latency. Does NOT persist anything.
**Idempotent**: yes (no side effects)
**Timeout**: server-side 5s wall clock; probe aborted at 5s regardless of driver behavior (FR-004)

### Request
```json
{
  "db": {
    "driver": "postgres",
    "url": "postgres://user:pass@localhost:5432/router?sslmode=disable"
  }
}
```

| Field | Type | Required | Validation |
|---|---|---|---|
| `db.driver` | string | yes | one of `sqlite3`, `postgres`, `mysql` |
| `db.url` | string | yes | 1..4096 chars |

### Response · reachable

HTTP 200, envelope:

```json
{
  "code": 0,
  "msg":  "ok",
  "data": {
    "ok": true,
    "latency_ms": 142,
    "server_version": "PostgreSQL 16.2"
  }
}
```

### Response · unreachable

Network failure is modeled as a **business error** under the envelope policy (the handler answered the request; the upstream DB did not). HTTP 200, `code = 2003`:

```json
{
  "code": 2003,
  "msg":  "timed out connecting to database after 5s",
  "data": {
    "ok": false,
    "latency_ms": 5002,
    "hint": { "timeout_ms": 5000 }
  }
}
```

Rationale: using `code = 0 + data.ok = false` would force clients to do two checks per response (`code === 0 && data.ok === true`). Under the envelope rules, an operational DSN failure is an expected response, so it earns its own top-level code. The client checks `code === 0` and always flows through the single envelope branch. The `data.hint` field is the driver-specific remediation slot registered in `docs/error-codes.md`; its shape is an object with driver-specific keys (e.g. `timeout_ms`, `sqlstate`, `driver_message`) — clients that don't recognise a key ignore it.

### Response errors

| HTTP | `code` | Symbol | Message template | When |
|---|---|---|---|---|
| 200 | `2002` | `invalid_driver` | "driver must be one of: sqlite3, postgres, mysql" | `db.driver` not in allow-list |
| 200 | `2003` | `invalid_dsn` | "DSN is empty or exceeds 4096 characters" | length violation |
| 200 | `2003` | `invalid_dsn` (same code, different msg) | "timed out connecting to database after 5s" | network/driver failure during the probe |
| 200 | `2008` | `malformed_body` | "request body is not valid JSON" | JSON parse failed |
| 200 | `2001` | `setup_already_done` | "setup is already complete; this endpoint is no longer available" | `config.json` is present on disk (gate open) |
| 200 | `2009` | `request_body_too_large` | "request body exceeds 8 KiB" | body-cap middleware |

> **Note — `2010 probe_in_progress` removed 2026-04-19 PM (D10).** Earlier drafts required a server-wide one-in-flight lock on this endpoint. The feature was dropped because 002 routers deploy behind a trusted boundary (no admin auth yet; see `spec.md §FR-014`) and the 5-second hard probe deadline already bounds resource exposure. The code slot `2010` is now reserved — see `docs/error-codes.md` for the reservation note. If admin-auth + multi-operator in 003 makes concurrent probing an abuse vector, a replacement code is expected to land in 003's 3xxx range, not by reusing `2010`.

---

## `POST /api/setup/commit`

**Description**: The wizard's final step. Performs the setup transaction in **DB-first, file-last** order: pre-check `os.Stat(config.json)` → open DB + migrate + BEGIN tx + INSERT first account `ON CONFLICT DO NOTHING` + COMMIT tx → atomically write `config.json` (tmp + O_EXCL + fsync + rename). The rename is the single observable "setup complete" moment. Success transitions the router out of setup mode. See `plan.md §Data Flow US-1` and `plan.md §Risk R-1` for the exact step ordering and crash-recovery matrix (including the brownfield auto-materialize that recovers from crashes between COMMIT and rename).
**Idempotent**: no (a second successful call after a first one returns `code = 2001`)
**Timeout**: server-side 30s wall clock

### Request

```json
{
  "db": {
    "driver": "sqlite3",
    "url": "router.db"
  },
  "first_account": {
    "name": "shared-prod-01",
    "provider": "openai",
    "api_key": "sk-…",
    "base_url": null
  },
  "plugins": {
    "admin_auth":  { "enabled": false },
    "client_keys": { "enabled": false }
  },
  "runtime": {
    "log_client_request_body":   false,
    "log_upstream_request_body":  false,
    "log_upstream_response_body":  false,
    "log_retention_days": 30,
    "log_level":          "info"
  }
}
```

The `runtime` block is **optional** in the request body — the 002 wizard UI does not expose runtime knobs (spec FR-003 scopes the wizard to driver/DSN/account/plugins/commit), so the SPA MAY omit `runtime` entirely. When omitted, the server fills defaults (`log_client_request_body=false`, `log_upstream_request_body=false`, `log_upstream_response_body=false`, `log_retention_days=30`, `log_level="info"`). When present, every sub-field below is validated; partial `runtime` objects are rejected with `code = 2008 malformed_body` (the commit endpoint is not a partial-patch API — `POST /api/admin/settings/update` is).

The `first_account` block is also **optional** (since v2.4 — 2026-04-15). Operators may omit it entirely to defer upstream-account seeding to the admin portal. When omitted, the commit still runs migrations and writes `config.json`; `upstream_accounts` stays empty; `/api/admin/health` reports `degraded` until at least one active account is registered via `POST /api/admin/accounts`. A **partially** filled `first_account` (present but missing required scalars) is NOT treated as skip — the validator returns the per-field envelope error (`2004 invalid_account_name` / `2015 invalid_account_provider` / `2005 invalid_api_key` / `2016 invalid_base_url`).

The `db.driver` pre-condition ("MUST match a previously successful `probe-dsn` result") is relaxed for `sqlite3`: file-based DSNs are validated at commit time by the `migrate up` invocation, so the UI is permitted to skip `probe-dsn` entirely for that driver. Postgres and MySQL UIs still run probe-dsn first for UX (surface DSN errors on the Database step rather than at Commit). Regardless of driver, the commit handler **always** runs a server-side probe (`ProbeDSN`) as defense-in-depth — a malicious or buggy client that skips the UI probe is caught here and surfaces `2003 invalid_dsn` before migration starts.

| Field | Type | Required | Validation |
|---|---|---|---|
| `db.driver` | string | yes | one of `sqlite3`, `postgres`, `mysql`; MUST match a previously successful `probe-dsn` result (non-file drivers only — `sqlite3` exempts the probe prereq) |
| `db.url` | string | yes | ≤4096 chars |
| `first_account` | object | **no** (v2.4) | when omitted, upstream-account seeding is deferred; partial blocks are rejected per individual field rules |
| `first_account.name` | string | yes (when `first_account` is present) | 1..64 characters after trimming; free of control characters (charset otherwise unrestricted) |
| `first_account.provider` | string | yes (when `first_account` is present) | `"openai"` in 002 |
| `first_account.api_key` | string | yes (when `first_account` is present) | 1..256 chars; never echoed back |
| `first_account.base_url` | string \| null | no | if non-null, must be HTTPS URL ≤256 chars |
| `plugins.admin_auth` | object | yes | shape `{ "enabled": bool }` in 002. Stored verbatim at `config.json.plugins.admin_auth` for a future admin-auth feature to read; 002 does not activate the plugin even when `enabled=true`. The feature that ships admin auth extends the object with its own fields additively. |
| `plugins.admin_auth.enabled` | boolean | yes | operator intent for admin auth |
| `plugins.client_keys` | object | yes | shape `{ "enabled": bool }` in 002; 004 extends additively |
| `plugins.client_keys.enabled` | boolean | yes | operator intent for per-client API keys |
| `runtime` | object | no | if present, ALL five sub-fields below are required; if omitted, server fills defaults |
| `runtime.log_client_request_body` | boolean | yes (when `runtime` is present) | — |
| `runtime.log_upstream_request_body` | boolean | yes (when `runtime` is present) | — |
| `runtime.log_upstream_response_body` | boolean | yes (when `runtime` is present) | — |
| `runtime.log_retention_days` | integer | yes (when `runtime` is present) | 1..365 |
| `runtime.log_level` | string | yes (when `runtime` is present) | one of `debug`, `info`, `warn`, `error` |

### Response · commit succeeded

HTTP 200, envelope:

```json
{
  "code": 0,
  "msg":  "ok",
  "data": {
    "redirect": "/admin/",
    "config_version": 1,
    "account_id": 1
  }
}
```

The `redirect` field is the only value the SPA binds to today; `config_version` and `account_id` are additive envelope-safe fields emitted by the handler for operator diagnostics (e.g. log correlation when cross-referencing the `request_records` table). Envelope clients that ignore unknown keys continue to work byte-for-byte.

Since v2.4 the `account_id` field is **omitted** from the response when the operator skipped upstream-account seeding (empty `first_account`). SPA clients MUST tolerate its absence and fall back to `/api/admin/accounts` to enumerate accounts post-commit. The `setup_committed` INFO log carries a boolean `account_seeded` so operators can confirm the state from logs.

Side effects observable after `code = 0`:

1. `config.json` exists at the configured path (`$ROUTER_CONFIG_PATH` or `./config.json`) with mode `0600`. It contains the full operator-editable record: `db.{driver,url}`, `runtime.{log_client_request_body,log_upstream_request_body,log_upstream_response_body,log_retention_days,log_level}`, `plugins.{admin_auth,client_keys}`, `version=1`, `created_at`, `updated_at`. **File presence is the sole setup-completion marker.**
2. `upstream_accounts` has exactly one row matching the posted `first_account` (API key stored plaintext per ROADMAP decision). **When `first_account` was omitted** (v2.4+), the table stays empty; `/api/admin/health` returns `degraded` and the admin dashboard surfaces the "no healthy accounts" banner until the operator adds an account.
3. `GET /v1/*` now serves.
4. `POST /api/setup/probe-dsn` and `POST /api/setup/commit` now return envelope `code = 2001`. `GET /api/setup/status` continues to serve (always available).
5. The in-process `*Config` is published via `atomic.Value`, so subsequent `GET /api/admin/settings` immediately reflects the posted values.
6. The server log emits exactly one `setup_committed` INFO event with the correlation id, account name, driver, and plugin flags. The DSN is logged at DEBUG only. No log line ever contains `api_key`.
7. Any stale `config.json.tmp.*` sibling files created during a previous crashed attempt are unlinked.

### Response errors

| HTTP | `code` | Symbol | Message template | When |
|---|---|---|---|---|
| 200 | `2002` | `invalid_driver` | as probe | same validation as probe-dsn |
| 200 | `2003` | `invalid_dsn` | as probe | same |
| 200 | `2004` | `invalid_account_name` | "account name must be 1-64 characters (after trimming) and free of control characters" | empty, over-length, or control-character name |
| 200 | `2005` | `invalid_api_key` | "api_key must be 1..256 characters" | length violation |
| 200 | `2006` | `invalid_plugin_flag` | "plugins.<id>.enabled must be a boolean" | non-boolean or missing `enabled` under any known plugin key |
| 200 | `2007` | `invalid_retention` | "log_retention_days must be an integer in [1, 365]" | out of range |
| 200 | `2014` | `invalid_log_level` | "log_level must be one of debug, info, warn, error" | `runtime.log_level` outside the 4-value enum |
| 200 | `2008` | `malformed_body` | "request body is not valid JSON" | parse failed |
| 200 | `2001` | `setup_already_done` | "setup is already complete" | `config.json` is present on disk — either reached by a pre-check at the start of the handler, by a concurrent request that won the rename race (spec US-1 Edge-2 / plan.md R-6), or by a previous successful call. Merges the former `409 concurrent_commit` because the client distinction was not useful: in both cases the wizard should redirect to `/admin/`. |
| 200 | `2009` | `request_body_too_large` | "request body exceeds 8 KiB" | body-cap middleware |
| 500 | `2900` | `db_connect_failed` | "could not open the database for commit" | driver rejected the DSN at commit time |
| 500 | `2901` | `migrate_failed` | "database migrations could not be applied" | migration error |
| 500 | `2902` | `commit_tx_failed` | "database transaction could not commit" | DB COMMIT failed before the config file was written; no on-disk `config.json`; operator can retry |
| 500 | `2903` | `config_write_failed` | "could not write config.json atomically after successful DB commit" | filesystem error during tmp-file write, fsync, or rename *after* a successful DB COMMIT. See R-1 in plan.md — next boot's brownfield auto-materializer synthesizes `config.json` from env and gate opens without operator action. If env DB vars were not set at start, the operator must restart with them or manually create `config.json`. |

---

## `GET /setup/` and `GET /setup/*`

**Description**: Static frontend assets for the wizard — the React SPA (TanStack Router `/setup/*` routes) built by Vite. Served by `internal/api/web.go` from the embedded `frontend/dist/` directory. **Non-JSON responses do NOT wear the envelope** — the envelope policy applies to `Content-Type: application/json` only. SPA owns this prefix exclusively; there are no JSON endpoints under `/setup/*` (they all live under `/api/setup/*`).
**Content-Type**: `text/html; charset=utf-8` for `.html`, `text/css` / `application/javascript` / `font/woff2` for assets.
**Cache-Control**: `no-cache` for `index.html`, `public, max-age=31536000, immutable` for Vite-hashed assets under `frontend/dist/assets/*-<contenthash>.{js,css}`.

No JSON contract. The rendered page is the canonical React implementation of `specs/002-.../mocks/v9-neoretro-grafana.html` using shadcn/ui + Tailwind v4 — design direction, not pixel-diff.

---

## Rate limiting and body caps (cross-cutting)

- All `POST` endpoints get a **tight** 8 KiB body cap (none of these payloads should exceed ~2 KB in practice). The 32 MiB cap from 001 applies only to `/v1/*` proxied traffic. `POST /api/admin/settings/update` has its own `16 KiB` cap (see `contracts/admin-api.md §Cross-cutting concerns`). The setup endpoints' body-cap middleware returns HTTP 200 + envelope `code = 2009 request_body_too_large` on overflow (bodies > 4× the cap are closed at the connection level with no response body — the only router-owned non-envelope response).
- No per-IP rate limit in 002 (spec out-of-scope). No server-wide probe lock either — the earlier `2010 probe_in_progress` machinery was dropped 2026-04-19 PM per D10. `probe-dsn`'s only abuse control is the 5-second per-probe hard deadline (`context.WithTimeout` in T-026). 002 deployments are expected to sit behind a trusted network boundary; concurrent-probe abuse becomes a real concern only once 003 lands admin auth with multiple operators, at which point a replacement code is expected in 003's 3xxx range.

---

## Integration with 001's `/admin/health`

001's health endpoint is relocated to `/api/admin/health` as part of the 2026-04-18 PM path convention; see `contracts/admin-api.md §Health endpoint`. While the setup gate is engaged (i.e. `config.json` is absent), `/api/admin/health` is still served (the setup gate lets it through unconditionally). 002 adds additive fields to `data` so the SPA can display setup state and the running build identity without issuing setup-gated calls:

```json
{
  "code": 0,
  "msg":  "ok",
  "data": {
    "status": "healthy",
    "checked_at": "2026-04-17T09:14:00Z",
    "checks": { "database": { "ok": true, "latency_ms": 3 } },
    "setup_state": "pending",
    "system": {
      "router_version": "v0.2.0",
      "router_git_sha": "abcdef1",
      "router_built_at": "2026-04-17T09:00:00Z"
    }
  }
}
```

`data.setup_state` is derived from `os.Stat(configPath)` — `"pending"` when absent, `"done"` when present. `data.system` is sourced from `internal/app/buildinfo`, matching `GET /api/admin/settings` once setup is done. All other `data.*` fields are unchanged from 001. This is backward compatible — pre-002 clients that don't know about `setup_state` or `system` ignore them.

## Changelog

| Version | Date | Change |
|---|---|---|
| 1.0 | 2026-04-17 | Initial contract. |
| 1.1 | 2026-04-17 | `GET /admin/setup/status` is now always available (not 410 post-commit). `setup_already_done` is now `410 Gone` (semantically correct; was mistakenly `409`). Body-overflow is now `413 request_body_too_large` (was `503 body_too_large`). Header casing standardised to `X-Request-Id`. Added integration note for 001's `/admin/health`. Body cap tightened from inherited 32 MB to 8 KB for all setup POST endpoints. |
| 1.2 | 2026-04-18 | Aligned with single-file config model: `bootstrap.json` references replaced by `config.json`. Dropped the `409 concurrent_commit` error code; the concurrent-commit loser now also receives `410 setup_already_done` (see rationale in Response errors). Renamed `500 bootstrap_write_failed` → `500 config_write_failed`. Side-effects list expanded to document the in-process `atomic.Value` publish, stale tmp sweep, and the api_key no-log rule. |
| 1.3 | 2026-04-18 | Config path `plugin_intents.*` renamed to `plugins.*`; value domain `"off" \| "enforcing"` simplified to `"off" \| "on"`. Error code `invalid_plugin_intent` renamed to `invalid_plugin_flag` accordingly. |
| 1.4 | 2026-04-18 | `plugins.<id>` body shape changed from flat string `"on"\|"off"` to nested object `{ "enabled": bool }` to match the new on-disk schema (forward-compat for 003/004 plugin-specific keys). `invalid_plugin_flag` message updated. |
| 1.5 | 2026-04-18 PM | `system_config` DB table removed from 002 entirely. Setup-completion marker is now `config.json` presence only. Commit flow inverted to DB-first/file-last so crashes between COMMIT and rename auto-recover via brownfield auto-materialize (plan.md R-1). `setup_already_done` trigger changed from "DB row" to "file present". `commit_tx_failed` / `config_write_failed` semantics swapped to reflect the new step order. `/admin/health`'s `setup_state` field is now derived from file-stat, not a DB query. |
| 2.0 | 2026-04-18 PM | **Envelope rollout.** All responses wrapped in `{code: int, msg: string, data: any}`. HTTP 200 for all expected responses; HTTP 500 only for system crashes (2900-range codes). Error codes are integers registered in `docs/error-codes.md`. Specific swaps: `410 setup_already_done` → HTTP 200 + `code = 2001`; `413 request_body_too_large` → HTTP 200 + `code = 2009`; probe-dsn unreachable response lifted from `ok: false` sentinel to top-level `code = 2003` so clients only switch on `code`. ~~New code `2010 probe_in_progress` added for concurrent probe rejection.~~ *(Removed in 2.3 per D10 — 002 no longer implements the lock.)* |
| 2.1 | 2026-04-18 PM | **Path split.** All JSON endpoints moved from `/admin/setup/*` to `/api/setup/*` so that the SPA can own `/setup/*` and `/admin/*` exclusively for HTML deep-linking (fixes the SPA-vs-API path collision on `/admin/settings`). `data.details` on the unreachable probe response renamed to `data.hint` to match the single-slot shape registered in `docs/error-codes.md` for `code = 2003 invalid_dsn`. Integration-with-001 section updated to reference `/api/admin/health`. |
| 2.2 | 2026-04-19    | **Round-3 settings schema cascade.** `POST /api/setup/commit` request body's `runtime` block updated to the 5-key flat schema (`log_client_request_body`, `log_upstream_request_body`, `log_upstream_response_body`, `log_retention_days`, `log_level`) and made optional (server fills defaults when omitted, matching the fact that the 002 wizard UI does not expose runtime knobs). Added `2014 invalid_log_level` to the error table. Side-effects list's `config.json` shape updated accordingly. Removed stale reference to `internal/app/shipped_features.go` in `GET /api/setup/status` (that file was dropped by D7; the sidebar is hard-coded client-side). |
| 2.5 | 2026-04-25    | Runtime body-capture keys are now `log_client_request_body`, `log_upstream_request_body`, and `log_upstream_response_body`, matching the split request-record columns. |
| 2.3 | 2026-04-19 PM | **D10 — `2010 probe_in_progress` dropped.** Removed the server-wide one-in-flight lock from `POST /api/setup/probe-dsn` and the matching error row from the probe error table. Rationale: 002 routers deploy behind a trusted boundary and the 5-second probe deadline already bounds resource exposure (YAGNI). The `2010` slot is reserved in `docs/error-codes.md` (do not reuse); a replacement code in 003's 3xxx range will be introduced only if admin-auth multi-operator makes concurrent probing an abuse vector. Rate-limiting section updated accordingly. |
| 2.4 | 2026-04-15 | **`first_account` optional + SQLite probe relaxation.** `POST /api/setup/commit` accepts an omitted `first_account` block: commit still runs migrations and writes `config.json`, but `upstream_accounts` stays empty and `/api/admin/health` reports `degraded` until the operator seeds an account via `POST /api/admin/accounts`. Partial blocks continue to be rejected per-field (`2004` / `2005` / `2015` / `2016`). Response `data.account_id` is omitted when seeding was skipped; `setup_committed` log carries `account_seeded=false`. Separately, the "driver MUST match a previously successful `probe-dsn`" pre-condition is relaxed for `sqlite3` (file-based DSN; connectivity is checked when migrations run). The UI therefore skips `POST /api/setup/probe-dsn` entirely for SQLite. Postgres/MySQL unchanged. |
