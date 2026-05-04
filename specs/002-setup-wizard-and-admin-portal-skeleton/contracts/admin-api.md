# Admin Portal API Contract

**Feature**: 002-setup-wizard-and-admin-portal-skeleton
**Path prefix**: `/api/admin/`  (JSON APIs only — the SPA at `/admin/*` owns the HTML/assets surface)
**Auth**: None in 002 (spec FR-014). 003 will introduce the `admin_auth` plugin — when enabled, it gates every endpoint below except `GET /api/setup/*` (always-public) and `/admin/*` / `/setup/*` SPA static assets.
**Availability**: Only when `config.json` is present on disk (gate open). Before that, all `/api/admin/*` endpoints **except `/api/admin/health`** respond with HTTP 200 + envelope `code = 2011 setup_required` and `data = {}`. `/api/admin/health` is always reachable so the SPA shell can display a banner during setup.

> **Path convention (2026-04-18 PM)**: this contract moved every JSON endpoint from `/admin/*` to `/api/admin/*`. Rationale: the SPA owns `/admin/*` exclusively for HTML deep-linking (e.g. a page at SPA path `/admin/settings` used to collide with the JSON endpoint at the same path and fail on browser refresh). All router-owned JSON endpoints live under `/api/*`; all router-owned HTML/assets live under non-`/api/*` prefixes (`/setup/*`, `/admin/*`). This applies to 001's admin endpoints too — see `specs/001-codex-router-mvp/plan/tech-design.md` changelog for the 001 migration entry. See `ROADMAP.md` Architectural Decision #9.

All router-owned JSON endpoints (every path except `/v1/*` upstream-proxy traffic) use the **single success-envelope** defined in `docs/error-codes.md`:

```json
{ "code": <int>, "msg": "<string>", "data": <payload|{}|null> }
```

- HTTP `200` covers every expected response, including business errors (non-zero `code`).
- HTTP `500` is used only for system crashes (`code = -1` or a 2900-range system code). Body is still an envelope.
- Every response carries `X-Request-Id`.
- `Content-Type: application/json; charset=utf-8`.

002 ships the **skeleton** of the admin API — enough for the portal shell to render on first load and for the operator to see setup succeeded. Feature-level endpoints (account CRUD, logs search, client keys, policies, per-request detail, observability dashboards) are explicitly deferred to 003+. The KPI dashboard visible in `mocks/v9-dashboard.html` is a design target for 005, **not** a 002 deliverable.

---

## `GET /api/admin/settings`

**Description**: Returns everything the Settings page needs to render: the effective runtime config (flat primitives), non-secret DB identity, the set of actually-registered plugins with their operator-enabled state, and build metadata. One request per Settings-page load. The sidebar does **not** consume this endpoint — nav entries are hard-coded client-side (see `data.plugins[]` note below).

**Idempotent**: yes
**Cache**: `Cache-Control: no-store`

### Request
No body, no query parameters.

### Response (HTTP 200, `code = 0`)

```json
{
  "code": 0,
  "msg":  "ok",
  "data": {
    "runtime": {
      "log_client_request_body":   false,
      "log_upstream_request_body":  false,
      "log_upstream_response_body": false,
      "log_retention_days": 30,
      "log_level":          "info"
    },
    "db": {
      "driver":        "postgres",
      "host":          "db.internal:5432",
      "database_name": "router_prod"
    },
    "plugins": [],
    "plugin_intents": [
      {"id": "admin_auth",  "label": "Admin authentication", "enabled": false, "status": "intent only"},
      {"id": "client_keys", "label": "Client API keys",       "enabled": false, "status": "intent only"}
    ],
    "system": {
      "router_version":  "v0.2.0",
      "router_git_sha":  "abcdef1",
      "router_built_at": "2026-04-17T08:00:00Z"
    }
  }
}
```

Notes on 002-specific semantics:

- **`runtime`** — flat object of 002's five runtime config keys. All types are JSON primitives (no `{value, source}` wrapper — Round-1 review 2026-04-19 simplified the shape after the operator UX decision that env-pinning semantics are server-enforced, not user-visible in the response). None of these keys is env-overridable; changing them always means editing `config.json` or calling `POST /api/admin/settings/update`. Defaults: `log_client_request_body=false`, `log_upstream_request_body=false`, `log_upstream_response_body=false`, `log_retention_days=30`, `log_level="info"`. Enum for `log_level`: `"debug" | "info" | "warn" | "error"`. Range for `log_retention_days`: `[1, 365]`. Secrets never appear here.
- **`db`** — non-secret identity of the database connection, for UI display ("Connected: Postgres @ db.internal:5432 / router_prod"). **`db.url` is never returned** — it may contain a DB password.
  - `driver`: enum `"sqlite3" | "postgres" | "mysql"`.
  - `host`: for `postgres` / `mysql`, parsed from the DSN as `"<host>:<port>"` (port always included, even when the driver default is used). For `sqlite3`, always `"local"` (sqlite has no host concept, the file is co-located with the router process). If DSN parsing fails, `""`.
  - `database_name`: for `postgres` / `mysql`, parsed from the DSN (the logical database). For `sqlite3`, the **basename** of the file path (e.g. `"router.db"`), not the full path — the full path is an operator-environment secret and is never surfaced over HTTP. If DSN parsing fails, `""`.
  - No `source` field. Driver change requires re-running the wizard (002 does not expose a DSN-rotate endpoint; tracked for 003+).
- **`plugin_intents`** — ordered array of operator-persisted plugin-enable choices read straight out of `config.json.plugins.<id>.enabled`. Rows correspond 1:1 to the known plugin sub-structs in `config.PluginsConfig` (002 ships two: `admin_auth`, `client_keys`; 005 adds `prometheus`). Each row is `{id, label, enabled, status}` — `status` is a short operator-facing phrase describing the current shell semantics while no matching plugin binary is installed (current value: `"intent only"`). **This is the canonical source the Settings page's "Plugin intents" panel (D11) binds to.** The split with `plugins[]` is deliberate: `plugins[]` answers "is this plugin live in this build?" (currently empty in 002), `plugin_intents[]` answers "what did the operator choose while the plugin is still absent?" Writes via `POST /api/admin/settings/update { plugins: {...} }` flip the `enabled` bit on the corresponding row.
- **`plugins`** — array of **registered** plugins with the operator's enabled/disabled decision. Each row has exactly three fields:
  - `id`: stable plugin identifier matching `internal/plugin/<id>/` and `config.json.plugins.<id>` (e.g. `"admin_auth"`).
  - `label`: operator-facing display name for the settings pane (e.g. `"Admin auth"`).
  - `enabled`: `true` iff the plugin is registered in `plugin.Registry()` in this build **and** `config.PluginsConfig.Enabled(id)` returned `true` at boot.
  - In 002, `data.plugins = []` (empty array) because 002 ships no concrete plugins. 003 adds `admin_auth`, 004 adds `client_keys`, 005 adds `prometheus` — each feature ships its own row in this array as part of landing the plugin.
  - **The SPA's sidebar is NOT driven by this list.** The sidebar hard-codes its nav entries client-side — `Dashboard`, `Accounts`, `Playground`, `Requests`, and `Settings` are live in the current build; `Client Keys` and `Observability` render as disabled stubs with a generic `planned` badge — so the shell renders identically regardless of which plugins are registered. `plugins[]` here is consumed only by the Settings page (to render live on/off status of already-shipped plugins) and by future admin tooling that needs to discriminate "plugin is in this build" from "plugin is operator-enabled". Earlier drafts of the contract had the sidebar dependent on this array, including a `shipped_in_feature` field and a `source` field — both were removed in the Round-3 review 2026-04-19 when the sidebar moved fully to a client-hard-coded list.
- **`system.router_version`** — a git tag string such as `"v0.2.0"` (or `"v0.2.0-rc1"` on a release candidate build). Injected at build time via `-ldflags "-X github.com/user/one-llm-router/internal/app/buildinfo.Version=$(git describe --tags)"`. Default when run via `go run` (no ldflags injection): `"dev"`.
- **`system.router_git_sha`** — 7-character short commit hash (`git rev-parse --short=7 HEAD`). Default: `"unknown"`.
- **`system.router_built_at`** — ISO 8601 UTC timestamp string, e.g. `"2026-04-17T08:00:00Z"`. Default: `"unknown"`.
- The previously-present fields `system.setup_state` and `system.config_version` were removed in the Round-4 review 2026-04-19. `setup_state` is redundant here (this endpoint is gated — if it responds with `code = 0`, setup is by definition done; the live source of truth is `GET /api/admin/health` which is always reachable). `config_version` was a fail-fast hook for a schema-migration concern that does not exist in 002 (only one version exists). Both can be added back when a concrete use case arises.
- There is deliberately **no** `system.uptime_seconds` or `config.json` path in this payload. Uptime is an observability concern deferred to 005. The on-disk path is an operator-environment secret and is logged only at DEBUG level, never surfaced over HTTP.

### Response errors

| HTTP | `code` | Symbol | When |
|---|---|---|---|
| 200 | `2011` | `setup_required` | `config.json` is absent (gate engaged); setup not done. `data = {}`. |

---

## `POST /api/admin/settings/update`

**Description**: Update runtime settings and plugin-enabled flags. 002 accepts two classes of operator-editable keys:

1. The five `runtime.*` keys — `runtime.log_client_request_body`, `runtime.log_upstream_request_body`, `runtime.log_upstream_response_body`, `runtime.log_retention_days`, `runtime.log_level`.
2. The plugin-enable booleans — `plugins.admin_auth.enabled`, `plugins.client_keys.enabled` (the two plugins whose `PluginsConfig` sub-struct exists in 002; see `contracts/plugin-interface.md §Adding a new plugin`). Future admin-auth/client-key features land the actual plugin binaries and add plugin-specific config keys additively; 005 extends the allow-list to include `plugins.prometheus.enabled`. Until the plugin binary is registered, flipping `enabled=true` is a no-op on behaviour — it only records operator intent in `config.json.plugins.<id>.enabled`, consumed by the matching feature when it ships (this is the forward-compat hook that lets operators pre-stage their choice right after 002's install).

Everything outside those two classes returns `code = 2012 unknown_config_key`. Semantically an RPC "update" — takes a partial patch, returns the fresh settings snapshot. Verb is `POST`, not `PATCH`, per the 2026-04-18 PM API convention review (the project uses RPC-style sub-paths for all write operations on `/api/admin/*`; REST verbs are reserved for `/v1/*` provider-compatible data-plane operations).

**Idempotent**: yes (same body → same post-state).

**Concurrency policy**: **last-writer-wins** — if two admins call this endpoint concurrently, the second write silently overwrites the first. This is acceptable for 002 because there is no admin-auth yet, the blast radius is two keys, and the UI can add a "your change may have been overwritten" toast later if it becomes a real pain point. Optimistic concurrency is explicitly **not** implemented in 002 (see `plan.md §Trade-off T-9`).

**Side effects**: a successful call atomically rewrites `config.json` (tmp + fsync + rename, `0600`) with `updated_at` refreshed, then publishes a new `*Config` in-process via `atomic.Value`. No DB rows are modified.

### Request

```json
{
  "runtime": {
    "log_client_request_body": true,
    "log_upstream_request_body": true,
    "log_upstream_response_body": true,
    "log_retention_days": 60,
    "log_level": "debug"
  },
  "plugins": {
    "admin_auth":  { "enabled": true  },
    "client_keys": { "enabled": false }
  }
}
```

Only keys present in the request body are updated (partial patch). Missing keys are untouched. Request body cap: `16 KiB`. The 002 allow-list is:

- `runtime.log_client_request_body`, `runtime.log_upstream_request_body`, `runtime.log_upstream_response_body`, `runtime.log_retention_days`, `runtime.log_level`
- `plugins.admin_auth.enabled`, `plugins.client_keys.enabled`

Keys outside the allow-list return `code = 2012 unknown_config_key`:

- `db.*` — not patchable in 002 (changing DB is an install-level operation; tracked for 003+ as a separate "DSN rotate" endpoint).
- `plugins.<unknown_id>.enabled` — only plugin IDs whose `PluginsConfig` sub-struct exists in this build are patchable. Patching `plugins.prometheus.enabled` in 002 therefore returns `2012` because the 002 `PluginsConfig` struct does not have a `Prometheus` field yet (lands in 005).
- any other root-level key (`system.*`, top-level unknowns) — `2012`.

### Response (HTTP 200, `code = 0`)

```json
{
  "code": 0,
  "msg":  "ok",
  "data": { /* same shape as GET /api/admin/settings .data, reflecting post-update values */ }
}
```

### Response errors

| HTTP | `code` | Symbol | Message | When |
|---|---|---|---|---|
| 200 | `2012` | `unknown_config_key` | "config key `foo` is not patchable in 002" | key not in the allow-list above — covers `db.*`, unknown plugin IDs (`plugins.prometheus.*` etc.), `system.*`, and any other top-level unknown |
| 200 | `2008` | `malformed_body` | "`log_client_request_body` must be a boolean" | JSON type mismatch on a boolean field (`log_client_request_body`, `log_upstream_request_body`, `log_upstream_response_body`) |
| 200 | `2007` | `invalid_retention` | "log_retention_days must be an integer in [1, 365]" | type or range violation on `log_retention_days` |
| 200 | `2014` | `invalid_log_level` | "log_level must be one of debug, info, warn, error" | `log_level` outside the 4-value enum |
| 200 | `2006` | `invalid_plugin_flag` | "plugins.`<id>`.enabled must be a boolean" | `plugins.<id>.enabled` is present but not a JSON boolean (same code as setup-api.md) |
| 200 | `2009` | `request_body_too_large` | "request body exceeds 16 KiB" | body-cap middleware rejected |
| 200 | `2011` | `setup_required` | as above | setup not done |
| 500 | `2903` | `config_write_failed` | "failed to persist config.json; no changes applied" | atomic file write failed (disk full, permission error); previous `*Config` remains published |

> **2013 `env_override_readonly` is NOT listed here.** 002 runtime keys are not env-overridable (see `GET /api/admin/settings` notes on `runtime`), so 2013 cannot be triggered by this endpoint. The code stays reserved in `docs/error-codes.md` for future runtime keys that do allow env override (landed in 003+).

---

## `GET /admin/` and `GET /admin/*` (SPA assets — NOT an API path)

**Description**: Static frontend assets for the portal shell — the React SPA built by Vite, embedded via `go:embed frontend/dist/*` and served by `internal/api/web.go`. **These are non-JSON responses** and therefore do **not** wear the envelope — an envelope around `text/html` makes no sense. The envelope policy applies to JSON endpoints only. The SPA **exclusively** owns `/admin/*`; all admin JSON APIs live under `/api/admin/*` — there are no JSON endpoints on `/admin/*`.

**Content-Type**: per extension (`text/html`, `text/css`, `application/javascript`, `image/svg+xml`, `font/woff2`, …). MIME types are derived from `mime.TypeByExtension`.

**Behavior**: `GET /admin/` serves `frontend/dist/index.html`. Unknown `GET` paths under `/admin/` that do *not* look like an asset path (no file extension, or extension is `.html`) fall back to `frontend/dist/index.html` so TanStack Router can handle deep links (e.g. `/admin/settings`, `/admin/accounts/42` once 003 lights up). Requests for missing assets (e.g. `/admin/assets/missing-<hash>.css`) return `404 Not Found` with an empty body (the SPA fallback does NOT swallow asset 404s; otherwise a typo'd `<link>` reference would silently return HTML).

This fallback applies to `GET` only. Other methods against unregistered SPA paths return `405 Method Not Allowed` with an empty body.

**Setup-mode redirect**: while `config.json` is absent from disk, `GET /admin/` (and any deep link under `/admin/` that is not an asset) returns `302 Found` with `Location: /setup/` instead of serving the shell. This keeps the operator on a single URL surface during setup.

**Caching**: `index.html` is served with `Cache-Control: no-cache`. Vite-hashed static assets (`frontend/dist/assets/*-<contenthash>.{js,css}`) are served with `Cache-Control: public, max-age=31536000, immutable`. Hashing is emitted natively by `pnpm build`; 002 does not run a separate hashing step. See `plan.md §Open Questions #4`.

---

## Health endpoint — `GET /api/admin/health`

002 does **not** add a new health endpoint. It relocates 001's existing `/admin/health` handler to `/api/admin/health` as part of the path convention (see `specs/001-codex-router-mvp/plan/tech-design.md` for 001's migration entry) and wraps its response in the shared envelope. The health payload also exposes the same build metadata as `GET /api/admin/settings` so setup-pending and steady-state shells can display the running router version without hardcoded frontend strings.

```json
{
  "code": 0,
  "msg":  "ok",
  "data": {
    "status": "healthy",               // or "degraded" / "unhealthy"
    "checked_at": "2026-04-17T09:14:00Z",
    "checks": {
      "database": { "ok": true, "latency_ms": 3 }
    },
    "setup_state": "pending",           // additive field 002 introduces
    "system": {
      "router_version": "v0.2.0",
      "router_git_sha": "abcdef1",
      "router_built_at": "2026-04-17T09:00:00Z"
    }
  }
}
```

- **HTTP status**: always `200`. The three health states (`healthy | degraded | unhealthy`) live in `data.status`, not in the HTTP status line or in `code`. Rationale: under the envelope policy, `code != 0` means "the handler refused this request for a *business* reason"; an unhealthy downstream dependency is not such a refusal — the handler answered normally and the diagnosis is in the payload. `code` stays `0` for all three states. Operators who previously relied on a 200/5xx split for their liveness probe should switch to checking `data.status == "unhealthy"` via an HTTP parser; there is no code path in 002 that produces a non-`0` `code` on `/api/admin/health` except a genuine system crash (`code = -1` via the top-level recover middleware, HTTP 500).
- `data.setup_state` is derived from `os.Stat(configPath)` — `"pending"` when absent, `"done"` when present. Additive only; pre-002 clients ignore it.
- `data.system` is sourced from `internal/app/buildinfo` and matches the `data.system` block on `GET /api/admin/settings`. It is present even during setup-pending because settings is setup-gated but the shell still needs the real build identity.
- Always reachable (both pre- and post-setup); the setup gate allow-lists this path so the SPA shell can surface a "setup required" banner from a single fetch.

---

## Routing decision table

| Path prefix | Setup pending (`config.json` absent) | Setup done (`config.json` present) |
|---|---|---|
| `/setup/*` (SPA static)              | serve SPA (HTML, no envelope)      | serve SPA — the SPA itself calls `/api/setup/status`, sees `done`, redirects to `/admin/` |
| `/admin/*` (SPA static / deep links) | `302 → /setup/` (no envelope, Location header) | serve `frontend/dist/index.html` fallback (no envelope) |
| `POST /api/setup/probe-dsn`          | serve API            | HTTP 200 + envelope `code = 2001 setup_already_done` |
| `POST /api/setup/commit`             | serve API            | HTTP 200 + envelope `code = 2001 setup_already_done` |
| `GET  /api/setup/status`             | serve API            | serve API (always available — see setup-api.md) |
| `GET  /api/admin/health`             | envelope always; adds `data.setup_state` while pending (001 behaviour wrapped) | envelope always |
| `GET  /api/admin/settings`           | HTTP 200 + envelope `code = 2011 setup_required` | serve API |
| `POST /api/admin/settings/update`    | HTTP 200 + envelope `code = 2011 setup_required` | serve API |
| `GET  /api/admin/*` (other 001 APIs) | HTTP 200 + envelope `code = 2011 setup_required` | serve API |
| `/v1/*`                              | **001 native error shape** (no envelope) + **HTTP 503** — body `{"error":{"type":"service_unavailable","code":"setup_required","message":"..."}}`; `X-Request-Id` is header-only. The envelope policy explicitly excludes `/v1/*`; see AGENTS.md §HTTP API Style. | proxy to provider (001 behavior; upstream body is NOT wrapped — the envelope policy explicitly excludes `/v1/*`) |

---

## Cross-cutting concerns (all endpoints)

- **Body cap**: per endpoint — `POST /api/admin/settings/update` is `16 KiB`; `/api/setup/probe-dsn` and `/api/setup/commit` are `8 KiB` (see `contracts/setup-api.md`); all other `/api/admin/*` JSON endpoints default to `16 KiB`. Exceeding returns envelope `code = 2009 request_body_too_large` at HTTP 200. The body-cap middleware still closes the connection for bodies > 4× the cap (pathological abuse) with no body; that is the only non-envelope router-owned response.
- **Rate limiting**: Not implemented in 002 for `/api/admin/*`. Land with the admin-auth plugin in 003.
- **Error envelope**: every JSON response on every router-owned path uses the `{code, msg, data}` envelope. `code = 0` is the sole success indicator; the 200-vs-500 split is preserved for operational tooling but is NOT the business-success signal. The full code registry lives in `docs/error-codes.md`; this contract references symbolic codes by name.
- **Request ID**: Every request reads or creates `X-Request-Id`; same name + casing as 001. Response always echoes the value back in the same header. The value is **not** duplicated into the body (envelope stays compact).

## Changelog

| Version | Date | Change |
|---|---|---|
| 1.0 | 2026-04-17 | Initial contract (`/admin/overview` + `/admin/config` + new `/admin/health`). |
| 2.0 | 2026-04-17 | Removed `/admin/overview` (KPI dashboard deferred to 005). Renamed `/admin/config` → `/admin/settings` and merged in `shipped_features` + `plugins` + `system.router_*` build metadata. Dropped the redundant new `/admin/health` (reuse 001's). Switched `PATCH` to last-writer-wins (no 409). Added 413 body cap. Standardised header casing to `X-Request-Id`. |
| 2.1 | 2026-04-18 | Aligned with single-file config model: replaced `bootstrap` block with `db.driver` (non-secret identity only; `db.url` never returned). Dropped fabricated `max_request_body_mb` runtime key and `system.uptime_seconds`. Added `system.config_version`. Added `500 config_write_failed`, `400 invalid_type`. Added explicit `302 → /setup/` entry to the routing table. Tightened SPA asset fallback rules to avoid swallowing asset 404s. Reconciled per-endpoint body caps with `plan.md §Security`. |
| 2.2 | 2026-04-18 | Config path `plugin_intents.*` renamed to `plugins.*`; `PATCH` allow-list note updated accordingly. The existing `plugins[]` field in the `GET` response (a registry snapshot of `{id,enabled}`) is unchanged — it is a separate concept from the config-file `plugins` map. |
| 2.3 | 2026-04-18 | `config.json.plugins.<id>` shape changed from flat string `"on"\|"off"` to nested object `{ "enabled": bool }` (forward-compat for plugin-specific config keys in 003/004). Added a new `plugin_flags` block to the `GET` response exposing each plugin's operator-intent flag and its `source` (always `"file"` or `"default"` in 002 — plugin flags are not env-overridable). Split the two concepts explicitly in the notes: `plugins[]` = registry state, `plugin_flags{}` = operator intent from `config.json`. |
| 3.0 | 2026-04-18 PM | **Envelope rollout.** All JSON responses now use `{code: int, msg: string, data: any}`; HTTP 200 for business, HTTP 500 for system crash; `code = 0` ⇔ success. Error codes are integers registered in `docs/error-codes.md`. **Merged `shipped_features` + `plugins[]` + `plugin_flags{}`** into a single `data.plugins[]` array with per-row `{id, label, shipped_in_feature, enabled, source}` to eliminate the three-way client-side join. **Renamed `PATCH /admin/settings` → `POST /admin/settings/update`** per the RPC-style convention adopted for all `/admin/*` mutations. `request_body_too_large` moved from HTTP 413 to HTTP 200 + `code = 2009` to keep the envelope policy consistent. |
| 3.1 | 2026-04-18 PM | **Path split.** All JSON endpoints moved from `/admin/*` to `/api/admin/*` so the SPA can own `/admin/*` exclusively for HTML deep-linking (resolves SPA-vs-API path collision, e.g. SPA page at `/admin/settings` vs. JSON `GET /admin/settings`). `/v1/*` envelope policy reaffirmed: `/v1/*` **never** wears the envelope — during setup-pending, the setup gate returns 001's native MVP error shape (`{"error":{...}}`) with **HTTP 503**, not envelope `code = 2011`. Health endpoint `data.status` clarified as the source of truth for liveness — no `code = 1001 unhealthy` is emitted (that code is owned by 001 `account_not_found`); `code` stays `0` for `healthy / degraded / unhealthy`. Projection of `data.plugins[]` now skips calling `config.Plugins.Enabled(id)` for rows whose `id` is not wired in `PluginsConfig` (assigns `source = "unshipped"` directly) — eliminates the `slog.Warn("unknown plugin id …")` path for roadmap-only IDs. |
| 4.1 | 2026-04-19 PM | **Plugin-flag patching (D9) + Settings panel (D11).** `POST /api/admin/settings/update` allow-list extended to include `plugins.admin_auth.enabled` and `plugins.client_keys.enabled`. Rationale: operators need a post-setup way to flip plugin intents without re-running the wizard (the alternative was delete-config-and-re-install, which invites data-loss mistakes). In 002 the flip only writes to `config.json` (no plugin binary is registered yet), so it is a forward-compat hook consumed by the matching feature when that plugin ships. Unknown plugin IDs (e.g. `plugins.prometheus.*`) still return `2012 unknown_config_key` because 002's `PluginsConfig` has no corresponding sub-struct. Added `2006 invalid_plugin_flag` to the update-endpoint error table (same symbol 002 already used on `/api/setup/commit`). **D11 UI companion**: the 002 Settings page always renders a "Plugin intents" panel with the two switches (see `tasks.md §T-311`) — UI surface of the API hook, independent of `data.plugins[]` being empty in 002. |
| 4.0 | 2026-04-19    | **Settings schema simplification** after per-field walkthrough with the operator. (a) `data.runtime` is now a **flat** object — no `{value, source}` wrapper — because all 002 runtime keys are **not** env-overridable, so there is no source to disambiguate. Keys renamed and expanded: `body_logging` → **`log_client_request_body`** + **`log_upstream_request_body`** + **`log_upstream_response_body`**; `retention_days` → **`log_retention_days`**; **new** `log_level` (enum `debug|info|warn|error`, default `"info"`). (b) `data.db` dropped `source` and added `host` (as `"host:port"`; `"local"` for sqlite3) and `database_name` (basename for sqlite3); `db.url` remains forbidden. (c) `data.plugins[]` row simplified to `{id, label, enabled}` — dropped `shipped_in_feature` and `source`; 002 returns **empty array** (no concrete plugins ship yet); sidebar hard-codes its nav entries client-side (no longer derived from this array); **D3 decision** (projection skipping `Enabled(id)` for roadmap-only rows) is now **obsolete** — there are no roadmap-only rows to project. (d) `data.system` dropped `setup_state` (redundant in a gated endpoint; use `/api/admin/health`) and `config_version` (no schema-migration use case in 002); `router_version` format clarified as a git-tag string (e.g. `"v0.2.0"`). (e) `POST /api/admin/settings/update` allow-list expanded to the 5 runtime-key set; new error code `2014 invalid_log_level`; `2013 env_override_readonly` stays in the registry but is unreachable via this endpoint in 002. Consequence: `spec.md` FR-008 (source visibility) and FR-012 (env-pinned rejection) are tightened — server still refuses env-pinned writes via 2013, but the UI no longer paints env-pinned fields as read-only because no runtime key is env-pinned in 002. |
| 4.2 | 2026-04-25    | Request/response body capture split after `request_records` schema update: `runtime.log_client_request_body` controls the body received from the caller, `runtime.log_upstream_request_body` controls the body sent to the selected upstream provider, and `runtime.log_upstream_response_body` controls the upstream/provider response body. |
