# Roadmap: one-llm-router

**Created**: 2026-04-16
**Status**: Living document — updated at every release gate.
**Purpose**: Project-level delivery roadmap. The **Post-MVP feature plan** section below (002–006) is the committed delivery order, decided on 2026-04-16 after internal exploration + Codex cross-review. The **Post-MVP hardening** section (R-01..R-08) is the frozen output of the industrial-grade review on 001-codex-router-mvp; each entry states the risk, the trigger that promotes it to "do-now", and the recommended direction so future work does not rediscover context.

---

## Post-MVP feature plan — Committed 2026-04-16

Delivery order (each item is its own feature spec; run `sdd-specify` → `sdd-plan` → … independently):

| # | Spec | Theme | Priority | Notes |
|---|---|---|---|---|
| 002 | `specs/002-setup-wizard-and-admin-portal-skeleton/` | First-install wizard **+** admin portal shell (plugin registry, go:embed frontend layout, nav, auth-ready shell) | **NEXT** | Setup flow UX + the shared frontend scaffold that 003 fills in |
| 003 | `specs/003-admin-auth-and-portal-views/` | Admin auth plugin (static bearer / password) **+** main admin portal pages (accounts, requests, settings) | High | Blocks multi-operator + external deployment |
| 004 | `specs/004-client-api-keys-and-routing-policy/` | Client API keys (plaintext), per-key account allow-list, quota enforcement, routing policy integration (`AccountSelector.Select(SelectInput)`) | High | Blocks multi-tenant |
| 005 | `specs/005-observability/` | `ProxyHooks` abstraction + Prometheus exporter **+** per-account health check / breaker + `/api/admin/health` enrichment (merged metrics + health) | Medium | Single cohesive observability delivery |

### Architectural decisions (apply across 002–006)

Confirmed on 2026-04-16, revised 2026-04-17 (plugin model — see the architectural note below):

1. **Plugins are feature-flag-gated capability implementations, not generic HTTP middleware.** `internal/plugin/` package: `Plugin` interface with a single method, `ID() string`. Each plugin declares the extension points it implements by satisfying one or more **capability interfaces** (`AdminAuth`, `ClientKeyAuth`, `ProxyHook`, …). Enabled/disabled is **not** a plugin-level method — the app reads `config.Plugins.Enabled(id)` from a typed `PluginsConfig` struct (populated from `config.json.plugins.<id>.{enabled,...}`) before instantiating each factory; disabled plugins have their factories invoked zero times. The app discovers capabilities via type-assertion at boot and invokes them at pre-defined extension points. Self-registration via `init()` into a package-level registry; duplicate IDs panic. Compile-time typesafe only; no Go `.so` runtime plugins, no reflection.
2. **Capability interfaces are the extension-point vocabulary** — one named interface per extension point. Adding a new extension point is "define one interface + add one type-assertion arm in `BuildApp`". Plugins never declare URL paths, orders, scopes, or wrappers — the app decides where each capability is invoked. Initial capabilities: `AdminAuth.Authenticate(r)` (003), `ClientKeyAuth.Authenticate(r)` (004), `ProxyHook.OnProxyComplete(ctx, ev)` (005). Generic infrastructure middleware (logger, recoverer, request-id, body-cap, setup-gate) stays hard-coded in `internal/api/middleware.go` + `internal/setup/gate.go` — not plugins.
3. **`ProxyHook` is a plugin capability, not a standalone interface.** `ProxyHook.OnProxyComplete(ctx, ProxyCompleteEvent)` is defined in `internal/plugin/`. Exporters (Prometheus now, OTel later) implement the `ProxyHook` capability on a plugin; `proxy.go` calls `app.proxyHooks` once per request and never changes again.
4. **Single-file config, no DB state marker** — one operator-editable `config.json` on disk holds the full record (DB driver+DSN, runtime toggles, plugin intents) **and serves as the setup-completion marker (presence ↔ done)**. Precedence is `env > config.json > defaults`. The file is loaded once at boot and published through `atomic.Value` for hot-reloadable fields; `POST /api/admin/settings/update` rewrites it atomically (tmp+fsync+rename, `0600`). Startup log prints the final value + source per field ("file" / "env:ROUTER_*" / "default") to eliminate ghost configurations. Supersedes two earlier drafts: (a) a two-layer design (`bootstrap.json` + `system_config` runtime rows) which doubled the write surface; (b) a single-file + DB `system_config.setup.state` marker design which kept two sources of truth for one bit. See `specs/002-.../research.md §Decision 2` (revised 2026-04-18 PM).
5. **Setup-complete marker is `config.json` presence** — 002 adds **no new DB tables**; the 001 schema is unchanged. FR-007 (deleting the config file on a populated install MUST NOT re-engage setup) is satisfied at the application layer by the **brownfield auto-materializer** (architectural decision #8 below), not by a DB row. This collapses the R-1 crash recovery matrix to a single source of truth. 003–006 may introduce their own DB state-marker tables if/when they need persistent state that cannot live in `config.json` — 002 no longer provides a generic one up front.
6. **Client API key data model** — `client_api_keys` (API key plaintext stored directly — hashing deferred; `key_prefix`, `status`, `allow_all_accounts`, quota fields, soft-delete) + `client_api_key_account_bindings` (many-to-many, `allow_all_accounts=true` skips the binding check). `request_records` gains `client_api_key_id BIGINT NULL`. `AccountSelector.Select` signature changes to `Select(ctx, SelectInput{SessionKey, ClientKeyID})`.
7. **Thin `main()`** — `BuildApp(ctx, cfg, deps) (*App, error)` takes all dependencies by interface. Enables table-driven `TestPluginMatrix` that exercises every enabled-plugin combination without booting an HTTP server.
8. **Brownfield auto-materialize + DB-first commit** (2026-04-18 PM) — boot sequence: `config.Load` → if file is absent, probe `(ROUTER_DB_DRIVER+ROUTER_DB_URL set) AND (upstream_accounts count > 0)` → if both true, synthesize `config.json` from env + defaults via atomic tmp+rename and re-load. `POST /api/setup/commit` runs **DB-first, file-last**: `os.Stat` pre-check (return envelope `code = 2001 setup_already_done` if file exists) → `migrate up` → `BEGIN; INSERT account ON CONFLICT DO NOTHING; COMMIT` → write `config.json.tmp.<pid>` → `rename`. A crash between COMMIT and rename is recovered by the brownfield auto-materializer on next boot with zero operator action. Boot-time `migrate up` is always run; `golang-migrate` dirty state logs `slog.Error` but does NOT block boot (R-9). A new `one-llm-router migrate {up|down|force|version|status}` CLI subcommand is provided for operator recovery.
9. **Unified JSON response envelope + RPC-style admin mutations + SPA/API path split + flattened settings shape** (2026-04-18 PM, revised 2026-04-19 with decisions D1–D3, Round-3-revised 2026-04-19 PM with decisions D4–D8) — every router-owned JSON endpoint (`/api/admin/*` + `/api/setup/*`, including 001's relocated `/api/admin/health`) returns `{code: int, msg: string, data: any}`; `code = 0` is success, non-zero is error. **HTTP 200** covers successes and business errors; **HTTP 500** is reserved for system errors (panic, DB down). Integer codes are registered in `docs/error-codes.md` and partitioned by feature (1xxx = 001, 2xxx = 002, …). `/v1/*` (upstream AI API) is explicitly and permanently **excluded** — during setup-pending it returns 001's native MVP error shape (`{"error":{"code":"setup_required",...}}`) with **HTTP 503**, never the envelope. `X-Request-Id` is a response header only, never duplicated into `data`. Admin **mutation** endpoints adopt an RPC-style path convention: `POST /resource/verb` — in 002 the settings mutation is `POST /api/admin/settings/update`. **SPA/API path split**: JSON APIs live under `/api/`; the SPA exclusively owns the `/admin/*` and `/setup/*` HTML trees. Navigating to `/admin/settings` returns SPA HTML; the page fetches data from `/api/admin/settings`. **Settings shape (Round-3, D4–D8)**: `GET /api/admin/settings` returns `data = {runtime, db, plugins, system}` where `runtime` is a FLAT primitive object `{log_client_request_body, log_upstream_request_body, log_upstream_response_body, log_retention_days, log_level}` (no `{value,source}` wrapper — no 002 runtime key is env-overridable); `db` is `{driver, host, database_name}` (host is `"<host>:<port>"` for pg/mysql, `"local"` for sqlite3; `db.url` is NEVER returned); `plugins[]` is `{id, label, enabled}` emitted **only for plugins actually in `plugin.Registry()` AND wired in `PluginsConfig`** — 002 returns `[]` because no concrete plugin ships, and the SPA sidebar's coming-soon entries are hard-coded client-side (making the earlier D3 projection-skip obsolete); `system` is `{router_version (git tag, e.g. "v0.2.0"), router_git_sha (short7), router_built_at (ISO 8601 UTC)}` — `setup_state` lives only on `/api/admin/health`; `config_version` is dropped (no 002 use case). `POST /api/admin/settings/update` returns the same settings `data` shape. Patchable keys (post-D9, 2026-04-19 PM): the 5 runtime keys + `plugins.admin_auth.enabled` + `plugins.client_keys.enabled`; `db.*` is not patchable in 002; unknown plugin IDs (e.g. `plugins.prometheus.*` before 005) return `2012 unknown_config_key`. Error codes added: `2014 invalid_log_level`; `2013 env_override_readonly` is dormant in 002 but server-side enforcement is retained for 003+. See `specs/002-.../research.md §Decision 8`, `§Decision 9`, and `§Decision 10`, and the D1–D8 log in `specs/002-.../plan.md §Decisions 2026-04-19`. **Backward-compatibility cost**: 001's `/admin/health` relocates to `/api/admin/health` AND its body is wrapped (`{status:ok}` → `{code:0,msg:ok,data:{status:healthy,...}}`); HTTP status stays `200` so external liveness probes that only check the status code are unaffected after the URL migration. Operators scraping the body must migrate from `.status` to `.data.status` AND update their probe URL.

> **Note on the 2026-04-17 revision of #1–#3**: an earlier draft described plugins as generic HTTP middleware with `Bindings(cfg, deps) ([]Binding, error)` and a `Binding{PluginID, Order, Scope, Wrap}` two-dimensional binding. That model coupled plugins to URL routing and required every plugin author to reason about ordering — inconsistent with how Caddy, go-micro, and the broader Go ecosystem model extension points. The capability-interface model above is strictly simpler and matches the real mental model: "plugins are features you can toggle; each feature plugs into a named extension point". The practical migration is straightforward — `Bindings()` becomes capability-interface satisfaction; `Scope/Order` disappear; `SetupGate` stops being a plugin (it is always-on infrastructure).

### Feature 002 scope — setup wizard + admin portal skeleton (do-now)

**Backend deliverables**:

- **First-run detection**: no `config.json` at `$ROUTER_CONFIG_PATH` (default `./config.json`) after the brownfield auto-materialize probe has had its chance (architectural decision #8) → boot enters **setup-only mode**; only `/api/setup/*`, `/api/admin/health`, the SPA shells at `/setup/*` and `/admin/*` (which render the wizard when setup is pending), and static assets under `/assets/` are served. Other `/api/admin/*` JSON paths return the envelope `{code:2011 setup_required, msg, data:{}}` at HTTP 200 (per architectural decision #9). `/v1/*` returns 001's native error shape with HTTP 503 (per D1). Legacy admin HTML paths `/admin/*` redirect `302 → /setup/` only for non-SPA legacy probes — the SPA itself renders the wizard inline.
- **Plugin registry foundation**: `internal/plugin/` package with the `Plugin` root interface, the three capability interfaces (`AdminAuth`, `ClientKeyAuth`, `ProxyHook`), and the `Register`/`Registry` functions. **002 does not ship any concrete plugin** — only the interfaces + registry so 003/004/005 can slot in. The setup gate is explicitly *not* a plugin — it lives in `internal/setup/gate.go` as always-on infrastructure and is wired directly into `BuildApp`.
- **Config layer**: single `Config` struct (`db`, `runtime`, a typed `PluginsConfig` sub-struct with one struct per known plugin — each holds `Enabled bool` + future plugin-specific fields added additively in 003/004, `version`, timestamps). Loader with `env > config.json > defaults` precedence; per-field source recorded for FR-008 / FR-012. Atomic writer (tmp + fsync + rename, `0600`). In-process `atomic.Value` publisher for hot-reload. `PluginsConfig.Enabled(id)` is the single authoritative read for the enabled/disabled decision consumed by `BuildApp`.
- **Brownfield auto-materializer** (`internal/setup/brownfield.go`) handles MVP upgrade paths: on boot, if `config.json` is absent but env has DB creds and `upstream_accounts` has rows, the router synthesizes a fresh `config.json` from env + defaults before the gate decision — no DB state table, zero operator steps.
- **Migrator wrapper** (`internal/store/migrator.go`) — thin `golang-migrate` facade for `migrate up` on boot, `Version()`/`Dirty()` reads for the startup log, and the `one-llm-router migrate {up|down|force|version|status}` CLI subcommand (`cmd/one-llm-router/migrate_subcmd.go`) for operator recovery.
- **`BuildApp(ctx, cfg, deps)`** refactor of `cmd/one-llm-router/main.go` so tests can assemble any enabled-plugin combination via a `TestPluginMatrix` table.

**Frontend deliverables** (React 19 + Vite 6 + TypeScript 5 + shadcn/ui + Tailwind v4 SPA, built by `pnpm -C frontend build` into `frontend/dist/*` and embedded via `go:embed`):

- **Shared shell**: top nav, left sidebar (Accounts / Requests / Settings shown disabled until 003 lights them up — navigation is hard-coded in the SPA, not fetched), toast/error UX, fetch wrapper with `X-Request-Id` propagation, i18n scaffold (hard-code EN first, keep wiring in place).
- **Setup wizard flow** (steps 1–5):
  1. Welcome + DB driver picker (`sqlite` default / `postgres` / `mysql`)
  2. DSN form with live connectivity probe
  3. First upstream account seed (`name`, `provider=openai`, `api_key`, optional `base_url`)
  4. Plugin-flag preview (record operator's choice for admin auth / client API keys — persisted to `config.json.plugins.<id>.enabled: bool`; no plugin is activated in 002)
  5. Commit: **DB-first, file-last** — `os.Stat(config.json)` pre-check (return envelope `code = 2001 setup_already_done` at HTTP 200 if present) → `migrate up` → `BEGIN TX; INSERT upstream_accounts ON CONFLICT DO NOTHING; COMMIT TX` → write `config.json.tmp.<pid>` (`0600`, fsync) → `rename` → publish `*Config` via `atomic.Value`, then the SPA hard-navigates to `/admin/` (portal shell). See `specs/002-.../plan.md §Risk R-1` for the rollback matrix (downgraded to P=low × I=low under the new ordering).
- **Admin portal placeholder page** (`/admin/`): renders the shell with a "Setup complete. Feature pages arrive in 003." callout and a link to the roadmap. The KPI dashboard visible in `mocks/v9-dashboard.html` is intentionally **not** wired in 002 — it is the design target for 005 observability. The mock is kept as a design artifact.

**Post-setup safety**: `config.json` present → wizard never renders again and `POST /api/setup/commit` returns envelope `code = 2001 setup_already_done` (HTTP 200 per architectural decision #9). If `config.json` is deleted by hand but the DB still has `upstream_accounts` rows and env still supplies DB creds, the brownfield auto-materializer re-synthesizes the file on the next boot — so FR-007 ("deleting the file MUST NOT re-enable setup on a populated install") holds without any DB side-table.

**Backward compatibility**: existing MVP deployments upgrade straight through — `ROUTER_DB_DRIVER` / `ROUTER_DB_URL` env vars keep supplying effective DB credentials, and on first 002 boot the brownfield auto-materializer synthesizes `config.json` from env + defaults because `upstream_accounts` already has rows. Zero operator steps, no forced re-entry into the wizard. Operators who prefer to see the materialized file explicitly can also run `one-llm-router migrate status` to confirm the schema is current before promoting the binary.

### Feature 003 scope — admin auth plugin + portal views

- Plugin: `internal/plugin/adminauth/` implementing the `plugin.AdminAuth` capability, `static_bearer` mode (first) and `password` mode stub (deferred to future iteration).
- Admin-user model (`admin_users` table: id, username, password_hash, status, timestamps).
- Portal pages (native JS using the 002 shell): login page, accounts list + CRUD dialogs, requests list + filters + detail drawer, settings page (plugin toggles, retention, body-logging switch).
- Open policy (unchanged from previous consensus):
  - Admin auth default: **off** when `config.json.plugins.admin_auth.enabled == false` (or missing) and no `admin_users` row exists → behaves like MVP.
  - If `config.json.plugins.admin_auth.enabled == true` but no `admin_users` row exists: server refuses `/admin/*` except `/admin/setup/*`, logs `code=admin_auth_not_initialized` with recovery instructions, never prints initial-admin tokens to stdout.

### Feature 004 scope — client API keys + routing policy

- `client_api_keys` + `client_api_key_account_bindings` tables (see architectural decision #6).
- `request_records.client_api_key_id` column + index.
- `AccountSelector.Select(ctx, SelectInput{SessionKey, ClientKeyID})` signature change; `ProxyHandler` extracts the client key from `Authorization: Bearer sk-router-...`.
- Routing policies: allow-list enforcement, quota enforcement (per-minute RPM, daily request count, daily/monthly token budgets) via middleware — rate-limit denial returns router error JSON envelope with `error.code=client_key_rate_limited`.
- Portal pages: client keys list + CRUD + bindings + quota editor.
- Open policy (unchanged):
  - Plaintext API key is **always visible** to admin via `GET /api/admin/client_keys/{id}` (explicit product decision — hashing can be retrofitted later when security requires).
  - `allow_all_accounts=true` is the default for newly-issued keys; operators must explicitly lock down.

### Feature 005 scope — observability (metrics + health merged)

- `plugin.ProxyHook` capability (defined in `internal/plugin/`); `proxy.go` invokes every enabled plugin that satisfies `ProxyHook` after each request. `FanoutProxyHook` (a plumbing adapter in `internal/app/`, not a plugin) multiplexes when multiple plugins register.
- Prometheus exporter plugin (`internal/plugin/prometheus/`, toggleable, implements `ProxyHook`): counters for `router_requests_total{outcome,status}`, histograms for `router_request_duration_seconds`, gauges for `router_accounts_active`, `router_recorder_drops_total`.
- Per-account auto health check: background worker that monitors `(upstream_5xx_ratio, timeout_ratio)` over a sliding window; auto-`disabled` on threshold breach with cool-down probe before re-enabling. Manual `disabled` never auto-recovers.
- `/admin/health` enriched with per-account breaker state + probe history + last check timestamp.
- Portal page: Observability dashboard (total success rate, top failing accounts, breaker status table).

---

---

## Post-MVP hardening (from 001-codex-router-mvp review)

## Legend

| Field | Meaning |
|---|---|
| **Risk** | What can go wrong in production if the item stays un-addressed |
| **Trigger** | Objective condition that promotes this item to "must do now" |
| **Direction** | Minimum viable design sketch, not a contract |
| **Owner** | Role expected to drive the work, not the implementer |

---

## R-01 — Admin API AuthN / AuthZ

- **Risk**: `/admin/accounts` is unauthenticated. Anyone who reaches the listener can create / disable / delete upstream accounts and read `request_records` (which may contain prompt & response bodies when body logging is on).
- **Trigger**: Router deployed outside the operator's private network segment, OR more than one human operator, OR integration with a shared bastion.
- **Direction**:
  - Start with **static bearer token** (`ROUTER_ADMIN_TOKEN`) middleware on `/admin/*`, token stored in secret manager.
  - Upgrade path: OIDC (SSO) + role claims. Keep middleware pluggable.
  - Always deny by default if token config is empty **and** listener is not loopback.
- **Owner**: Security Engineer + Platform.

## R-02 — Admin Audit Log

- **Risk**: Account creation / disable / deletion leaves no trail. Cannot answer "who disabled account X at 03:00?".
- **Trigger**: Any of R-01 conditions, OR first incident review.
- **Direction**:
  - New table `admin_audit` (actor, action, target_type, target_id, before_json, after_json, request_id, created_at).
  - Write from admin handlers (synchronous, same TX as the mutation when possible; at-least-once otherwise).
  - 1-year retention independent of `ROUTER_LOG_RETENTION_DAYS`.
- **Owner**: Platform.

## R-03 — Rate Limit / Circuit Breaker / Retry Backoff

- **Risk**: A runaway client or upstream outage can saturate the router, exhaust the DB connection pool, or get the shared OpenAI key rate-limit-banned. Today we depend on the client behaving.
- **Trigger**: ≥ 2 concurrent client integrations, OR first observed upstream 429 storm, OR first DB connection exhaustion incident.
- **Direction**:
  - **Rate limit**: token bucket per `api_key_id` at the ingress (`/v1/*`), configured via DB per key.
  - **Circuit breaker**: per-account, open on consecutive 5xx > N in window W, half-open probe after cooldown.
  - **Retry backoff**: only for idempotent 5xx (not 4xx, not streamed SSE mid-body), exponential + jitter, capped.
  - Use `golang.org/x/time/rate` + an internal state machine; do **not** pull a heavy framework.
- **Owner**: Backend Engineer.

## R-04 — Multi-Account Failover Pool

- **Risk**: If the selected upstream account is quota-exhausted or rate-limited, the request fails. No automatic fallback to a sibling account.
- **Trigger**: Operator registers a second active account of the same provider for the same api_key.
- **Direction**:
  - On non-retryable client error → surface as-is.
  - On 429 / 5xx / timeout from a non-session-sticky request → route to next healthy account in the pool (excluding the failed one), cap attempts at N (default 2).
  - Session-sticky requests never failover silently — fail the request with a distinct `router_error` subtype so the client can restart the session.
  - Reuse the circuit-breaker state from R-03 to mark accounts unhealthy.
- **Owner**: Backend Engineer.

## R-05 — RequestRecorder Drop Counter + DLQ

- **Risk**: When both the async channel is full AND the synchronous fallback insert fails (DB down), the record is dropped with a structured log line only. No metric, no replay.
- **Trigger**: First observability sprint, OR first audit that requires "100% request log coverage".
- **Direction**:
  - Add `prometheus.CounterVec` (`recorder_drops_total{reason=...}`).
  - Persist the dropped record to a local append-only file (`/var/lib/router/dlq/YYYY-MM-DD.jsonl`) when DB write fails; a background replayer consumes the DLQ and inserts when DB recovers.
  - Cap DLQ total size; oldest-drop policy if the cap is hit, surfaced as a separate metric.
- **Owner**: Backend Engineer.

## R-06 — golang-migrate Startup Integration

- **Risk**: Production currently relies on `xorm.Sync()` for schema creation. `Sync()` does not handle destructive changes, does not version the schema, and cannot roll back. Migration files under `internal/store/migrations/{postgres,mysql,sqlite}/` exist but are only exercised by hand.
- **Trigger**: Any schema change after GA, OR production deployment, whichever comes first.
- **Direction**:
  - On startup (before serving traffic): run `golang-migrate` against the embedded migrations for the configured driver.
  - `ROUTER_MIGRATE_ON_START` flag (default `true`), `false` only for readers / sidecars.
  - Fail fast if migration errors. Emit a structured log line with the resulting version.
  - Keep `Sync()` only behind a dev-only flag (`ROUTER_DB_SYNC_DEV`).
- **Owner**: Backend Engineer.

## R-07 — `request_records.request_id` Index

- **Risk**: Operator lookup by `request_id` (the most useful correlation key) currently does a full scan of `request_records`. At 30 days × expected traffic this becomes O(millions) per lookup.
- **Trigger**: First operator report "can't find my request by id" that takes >1s, OR `request_records` row count > 1M.
- **Direction**:
  - Add a single-column index `idx_request_records_request_id (request_id)` across the three dialects.
  - Ship as a new migration (`000002_*.sql`), not a schema amendment to `000001`.
- **Owner**: Backend Engineer.

## R-08 — Chaos / Upstream Disconnect E2E Tests

- **Risk**: We have unit + integration coverage for happy path and parse-level faults, but no test exercises "upstream sends half a body and RSTs", "upstream stalls mid-SSE past our 30s write timeout", or "DB goes away mid-recorder-insert".
- **Trigger**: First production incident in any of these shapes, OR pre-GA hardening sprint.
- **Direction**:
  - Use `httptest.Server` with a `net.Conn` hijack that writes N bytes then closes.
  - Use `toxiproxy` or a lightweight in-process proxy to inject latency / reset.
  - Assert observable outcomes: client gets a clean `502`-equivalent, record is written with `outcome=upstream_error`, recorder does not block shutdown.
- **Owner**: QA Lead + Backend Engineer.

---

## Review cadence

Revisit this roadmap at every release-gate and when any **Trigger** fires. Promote an item out of this doc by creating a new feature spec under `specs/NNN-…/` and linking back here.
