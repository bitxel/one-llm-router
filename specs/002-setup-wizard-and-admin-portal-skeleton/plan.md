# Implementation Plan: Setup Wizard and Admin Portal Skeleton

**Feature**: 002-setup-wizard-and-admin-portal-skeleton
**Spec**: `specs/002-setup-wizard-and-admin-portal-skeleton/spec.md`
**Created**: 2026-04-17
**Revised**: 2026-04-18 — collapsed two-layer config (`bootstrap.json` + `system_config` runtime rows) into a **single `config.json` file** with brownfield auto-materialization, to align with spec Clarifications Session 2026-04-16. The `system_config` DB table is not introduced in 002 at all; `config.json` presence on disk is the sole setup-completion marker, with FR-002 preserved via boot-time synthesis from env + `upstream_accounts` probe.
**Status**: Draft

## Summary

Bring a freshly deployed `one-llm-router` from zero to serving `/v1/*` via a browser-based five-step wizard, then ship the same shell as the foundation every future admin page renders inside. Technically: (a) introduce a **single** operator-editable `config.json` (DB driver+DSN + runtime toggles + plugin intents) with precedence `env > config.json > defaults`, loaded once at boot and published via `atomic.Value` for hot-reloadable fields, (b) **use file presence alone as the setup-completion marker** — `config.json` exists on disk ⇒ gate open, absent ⇒ wizard, with a boot-time brownfield auto-materializer that synthesizes the file from env + DB probe when `upstream_accounts` already has rows (preserves FR-002 "zero-manual-steps" MVP upgrade), (c) run `migrate up` at every boot with an `slog.Error` warning but non-fatal continuation on `Dirty()` state, (d) **stand up the canonical frontend stack** — React 19 + Vite 6 + TypeScript 5 strict + shadcn/ui + Tailwind CSS v4 + TanStack Router/Query + React Hook Form + Zod, built via `pnpm build` → `frontend/dist/*` and embedded into the Go binary at build time via `go:embed` — visually aligned with the v9 design system (`mocks/v9-neoretro-grafana.html`) for wizard + portal shell, and (e) refactor `cmd/one-llm-router/main.go` into a thin `BuildApp(ctx, cfg, deps) (*App, error)` so the new setup gate, migration driver, and future plugins compose cleanly. 002 also lands a `one-llm-router migrate {up|down|force|version|status}` CLI subcommand wrapping `golang-migrate` for operator recovery. The admin portal in 002 is **shell + "Setup complete" landing page only**; the KPI dashboard visible in `mocks/v9-dashboard.html` is the **design target for 005 observability** and does not ship now. 003/004/005 portal pages drop into the 002 shell as new TanStack Router routes.

## Technical Context

| Item | Value | Source |
|---|---|---|
| Language | Go 1.25 | `AGENTS.md`, `go.mod` (`go 1.25.0`) — floor set by `modernc.org/sqlite@v1.48.2` which declares `go 1.25.0` in its own `go.mod` |
| HTTP | `net/http` + `http.ServeMux` pattern routing | existing pattern `internal/api/adminapi/routes.go` |
| Database | SQLite (default) / PostgreSQL / MySQL — dialect registry already in place | `internal/store/dialect*.go` |
| ORM | `xorm.io/xorm v1.3.11` | existing dep |
| Migrations | `golang-migrate v4.19.1` via `go run` (no direct import) | existing pattern |
| Frontend framework | **React 19 + Vite 6**, TypeScript 5 strict | `frontend/AGENTS.md`, `docs/standards/toolchain.md` |
| Frontend UI | shadcn/ui (Radix primitives) + Tailwind CSS v4 | `frontend/AGENTS.md` §Tech Stack |
| Frontend routing | TanStack Router v1 (file-based, type-safe search params) | `frontend/AGENTS.md` §Routing |
| Frontend server-state | TanStack Query v5 | `frontend/AGENTS.md` §Data Fetching |
| Frontend forms | React Hook Form + Zod | `frontend/AGENTS.md` §Forms |
| Frontend lint/format | Biome v2 | `frontend/AGENTS.md` |
| Frontend tests | Vitest + React Testing Library + MSW; Playwright for E2E | `frontend/AGENTS.md` §Testing |
| Node / pnpm | Node 22 LTS, pnpm 9 | `docs/standards/toolchain.md` |
| Embedding | `go:embed frontend/dist/*` in `internal/api/web.go`; production build = `pnpm build` → Go binary | `docs/standards/toolchain.md` §Deployment |
| Dev workflow | Two servers: `pnpm dev` (Vite :5173) + `go run ./cmd/one-llm-router` (Go :8080); Vite proxies `/api/*` (all router JSON APIs) and `/v1/*` (upstream proxy) → Go; Vite itself serves `/admin/*` and `/setup/*` SPA routes from HMR | `docs/standards/toolchain.md` §Deployment |
| Logging | `slog` (structured JSON) | existing pattern |
| Tests | `go test`, `stretchr/testify v1.11.1`, table-driven | `AGENTS.md` |
| Config file | Single `config.json`; default path `./config.json`; override `ROUTER_CONFIG_PATH`; atomic tmp+rename; `0600` perms | `data-model.md § config.json` |
| Build metadata | `router_version`, `router_git_sha`, `router_built_at` injected via `-ldflags "-X github.com/user/one-llm-router/internal/app/buildinfo.Version=..."` in the `Makefile` | Addresses the "where does `router_version` come from?" gap from the first plan review |
| New Go dependencies | **none** | Go side reuses stdlib + existing deps only |
| New npm dependencies | ~18 top-level packages from `frontend/AGENTS.md` §Tech Stack (React/Vite/TS/shadcn/Tailwind/TanStack×3/RHF/Zod/i18next/Biome/Vitest/Playwright/MSW) | All already named + version-pinned in `docs/standards/toolchain.md` §Pinned Toolchain Versions |

> **Why stand up the frontend stack in 002 now (overriding a prior "defer" intent)**: (i) every 003/004/005 portal page is CRUD + forms + tables — the vanilla-ESM path would force either (a) a React migration in 003 on top of a throwaway shell, or (b) hand-rolling Radix-equivalents for accessible dialogs/forms/combos in vanilla; both are strictly worse than landing the stack once in 002 and renting the components. (ii) `frontend/AGENTS.md` and `docs/standards/toolchain.md` already define the canonical stack — 002 is executing against an existing decision, not introducing a new one. (iii) v9 mocks translate cleanly to shadcn components + Tailwind theme tokens (the design-token layer maps 1:1). The +1 engineer-week cost for 002 amortises across 003/004/005 where each new portal page becomes "add a route + compose shadcn components + wire a TanStack Query hook" rather than "extend a vanilla ESM shell".

## Constitution Check

### 第一性原理 Gate
- [x] Driven by spec FR-001..FR-014 + US-1..US-5; no feature introduced "because other control planes do it"
- [x] Goals clear: zero-to-`/v1/*` in under 2 min (SC-1), zero-downtime upgrade (SC-2), atomic setup (SC-3), hot-reload settings (SC-4)
- [x] Every module in the plan traces to a specific FR (see Traceability below)
- [x] Shortest viable path: one `config.json` file as sole setup marker (no DB side-table) + `frontend/` Vite build + `go:embed frontend/dist`. Alternative "vanilla ESM now, migrate to React in 003" rejected (see Trade-off T-1 revised) because 003/004 portal pages require accessible forms/tables/dialogs the vanilla path would force us to hand-roll. Alternative "split bootstrap.json + runtime DB rows" rejected because it forces every hot-reload into two datastores; see Trade-off T-5. Alternative "`setup.state` row in a `system_config` DB table" rejected — it doubled the write surface with no operator-visible benefit once brownfield auto-materialize was introduced; see Trade-off T-2 revised.

### Simplicity Gate (Constitution Principle 2)
- [x] Five new Go packages only: `internal/app/` (BuildApp), `internal/config/` (single `Config` struct + loader + atomic writer + `atomic.Value` broadcaster), `internal/setup/` (setup state + gate + wizard handlers), `internal/plugin/` (plugin registry seam), `internal/api/setup/` (wizard HTTP handlers). Frontend is a **separate Vite workspace** at `frontend/`; its build output (`frontend/dist/`) is embedded into the Go binary via `go:embed` — no Go→JS bridging, no SSR, no runtime coupling beyond static file serving.
- [x] No future-proofing: the plugin registry in 002 is the minimum needed so 003/004/005 can slot in without rework. **002 ships zero concrete plugins** — only the `Plugin` interface, the three capability interfaces (`AdminAuth`, `ClientKeyAuth`, `ProxyHook`), and the `Register`/`Registry` functions. The setup gate is **not** a plugin; it is always-on infrastructure in `internal/setup/gate.go` wired directly into `BuildApp`.
- [x] No "might need later" abstractions — the `BuildApp(ctx, cfg, deps) (*App, error)` refactor is justified by needing a testable plugin-combination matrix without it (FR-011, SC-5).

### Anti-Abstraction Gate (Constitution Principle 2)
- [x] Uses `net/http.ServeMux` pattern routing and `http.Handler` middleware chaining directly. No router library wrapper.
- [x] `Config` is a plain Go struct, loaded once, published through `atomic.Value` — one representation, no DTO split between "bootstrap" and "runtime".
- [x] No new DB table in 002. Setup-completion marker is the on-disk presence of `config.json`; FR-007 "resist accidental `rm`" is preserved via the brownfield auto-materialize path on next boot. 003 may introduce a DB table (e.g. `user_credentials`) when admin_auth lands; 002 does not pre-allocate one.
- [x] The setup gate is implemented as one `http.Handler` middleware; no separate "SetupPolicy" interface layer.

### Integration-First Gate (Constitution Principle 7)
- [x] External contracts defined ahead of code — see `contracts/setup-api.md`, `contracts/admin-api.md`, `contracts/plugin-interface.md`.
- [x] Contract tests precede implementation: each wizard and admin endpoint gets a `*_contract_test.go` asserting request/response shape against `contracts/*.md` before the handler is written.
- [x] Real environment testing: SQLite in-memory for unit/integration; the Commit flow is tested end-to-end via an HTTP round-trip against a real `httptest.Server`, not via mocks.

### Test-First Gate (Constitution Principle 3)
- [x] Test strategy defined (see "Test Hints" below and `quickstart.md`).
- [x] Every Acceptance Scenario in the spec maps to at least one test: 13 Given/When/Then scenarios → 13 integration test cases, plus a fault-injection suite for atomicity (SC-3).

**Gate verdict**: all five PASS with no exceptions needed. No entries in Complexity Tracking.

## Architecture

### Module Boundaries

| Module | Responsibility | Change Type |
|---|---|---|
| `cmd/one-llm-router/main.go` | Parse flags, call `app.BuildApp`, serve | **Modified** (thinned to ~30 LOC; body moves to `app.BuildApp`) |
| `internal/app/` | `BuildApp(ctx, cfg, deps) (*App, error)` — wires everything, returns `http.Handler`; testable plugin matrix | **New** |
| `internal/config/` | Single `Config` struct (db + runtime + a typed `PluginsConfig` sub-struct with one struct per known plugin, each carrying `Enabled bool` plus future plugin-specific fields), loader with precedence `env > config.json > defaults`, atomic `config.json` writer, source-of-value reporting, `atomic.Value` broadcaster for hot-reloadable fields. Exposes `Plugins.Enabled(id string) bool` which the boot sequence uses to skip disabled plugin factories. | **New** |
| `internal/setup/` | Setup state reader, **hardcoded setup gate middleware** (`gate.go`), wizard step validators, Commit transaction, DSN probe. The gate is *not* a plugin — it is always-on infrastructure that `BuildApp` wires directly. | **New** |
| `internal/plugin/` | `Plugin` root interface + three capability interfaces (`AdminAuth`, `ClientKeyAuth`, `ProxyHook`) + `Register`/`Registry`. **No concrete plugin ships in 002**; 003/004/005 add them. | **New** |
| ~~`internal/app/shipped_features.go`~~ | ~~Static roadmap slice rendered as "coming soon" entries in the portal sidebar.~~ **Removed in Round-3 review 2026-04-19** — the sidebar is now hard-coded client-side in `frontend/src/components/shared/Sidebar.tsx`; the Go side no longer needs a roadmap slice because `GET /api/admin/settings` returns only *actually-registered* plugins (in 002, an empty array). | **Dropped** |
| `internal/app/buildinfo/` | `Version`, `GitSHA`, `BuiltAt` populated via `-ldflags "-X ..."` | **New** |
| `internal/store/migrator.go` | Thin wrapper over `golang-migrate/v4` — exposes `Up(ctx)`, `Down(ctx, n)`, `Force(ctx, version)`, `Version(ctx) (v uint, dirty bool, err error)`, `Status(ctx)`. Used both by `BuildApp` startup and by the `one-llm-router migrate` CLI subcommand. | **New** |
| `internal/store/migrator_test.go` | Integration test that exercises the migrator against SQLite; Postgres/MySQL runs behind build tag. | **New** |
| `cmd/one-llm-router/migrate_subcmd.go` | `one-llm-router migrate {up|down|force|version|status}` subcommand routing. Parses `argv[1]` before HTTP boot. | **New** |
| `internal/api/setup/` | `/api/setup/*` HTTP handlers (status, probe-dsn, commit). Commit flow is DB-first, file-last (see Data Flow US-1 + R-1). | **New** |
| `internal/api/adminapi/settings.go` | `GET /api/admin/settings` + `POST /api/admin/settings/update` — hot-reload the 5 operator-editable runtime fields (`log_client_request_body`, `log_upstream_request_body`, `log_upstream_response_body`, `log_retention_days`, `log_level`); surfaces `data.runtime` (flat; no `{value,source}` wrapper because 002 runtime is not env-overridable), `data.db` (driver + host `"host:port"`/sqlite3`"local"` + database_name/sqlite3-basename), `data.plugins[]` (rows of `{id,label,enabled}` — empty array in 002 because no concrete plugin ships; each of 003/004/005 adds its own row as part of landing), and `data.system` (3 build fields: `router_version`, `router_git_sha`, `router_built_at`). `update` is RPC-style POST, not PATCH (project convention adopted 2026-04-18 PM for all `/api/admin/*` mutations). | **New** (added to existing `internal/api/adminapi` package — the `admin` identifier is reserved for the shared Go ASTs under `internal/api/`, so the handler package name is `adminapi` to avoid collisions) |
| `internal/api/envelope.go` | `{code:int, msg:string, data:any}` response envelope helpers (`WriteOK`, `WriteBizErr`, `WriteSysErr`) + integer error-code constants matching `docs/error-codes.md` 2000–2999 range. All router-owned handlers (setup + admin + 001's migrated `/api/admin/health`) route responses through this package. | **New** |
| `frontend/` (repo root) | New Vite + React + TS workspace per `frontend/AGENTS.md`. Contains `package.json`, `vite.config.ts`, `tsconfig.json`, `biome.json`, `tailwind.config.ts`, `playwright.config.ts`, `index.html` entry, `src/` tree (routes, components/ui = shadcn, hooks, lib, styles). Wizard + portal shell routes live in `src/routes/`. `pnpm build` emits `frontend/dist/` which `go:embed` picks up. | **New** |
| `internal/api/web.go` | `go:embed frontend/dist/* → embed.FS`, serves Vite-produced assets with Vite's content-hash filenames; `Content-Type` inferred; `Cache-Control: public, max-age=31536000, immutable` for hashed assets (`assets/*.{js,css}`), `no-cache` for `index.html`. SPA fallback serves `index.html` for any unmatched `/setup/*` or `/admin/*` path so TanStack Router client-side routing works. | **New** |

**Total new Go packages: 5** — `app`, `config`, `setup`, `plugin`, `api/setup`. `internal/app/buildinfo` is a sub-package of `app`, not a separate top-level module. `internal/store/migrator.go` is added to the existing `internal/store` package; it is not a new top-level package. Within Simplicity Gate ≤ 5 for the feature scope; see Complexity Tracking (empty).

### Data Flow — P0 User Stories

**US-1 First-install walkthrough**
```
Browser GET /                         →  SetupGate middleware (config.json absent)
                                         ↓ 302 /setup/
Browser GET /setup/                    →  web.FS serves setup.html + assets
Codex client POST /v1/chat/completions →  SetupGate (still pending): NO envelope applied;
                                         001 native shape ( {"error":{"type":"router_error",
                                         "code":"setup_required","message":"…",
                                         "request_id":"…"}} ) + HTTP 503. (AGENTS.md §HTTP API
                                         Style: /v1/* is permanently excluded from the envelope.)
Browser POST /api/setup/probe-dsn    →  setup.ProbeDSN (opens Engine, runs SELECT 1, closes)
                                         ↓ 200 envelope { code:0, msg:"ok", data:{latency_ms,…} }
Browser POST /api/setup/commit       →  setup.Commit (DB-first, file-last):
                                           0. os.Stat(config.json) — if present → 200 envelope
                                              { code:2001, msg:"setup_already_done", data:{} }
                                           1. open DB via given driver+DSN, run migrations (idempotent)
                                           2. BEGIN DB tx (IMMEDIATE on SQLite; SERIALIZABLE on PG/MySQL)
                                           3. IF req.first_account is non-empty:
                                                 INSERT first upstream account ON CONFLICT DO NOTHING
                                              ELSE:
                                                 skip the INSERT — tx is a pure schema-migration no-op
                                           4. COMMIT DB tx                             -- DB state is now persistent
                                           5. prepare config.json.tmp.<pid> via O_EXCL|O_CREATE|O_WRONLY, 0600;
                                              write full Config (db + runtime defaults + plugin intents); fsync; close
                                           6. os.Rename(tmp → config.json)             -- SINGLE publish moment
                                         ↓ 200 envelope { code:0, msg:"ok", data:{redirect:"/admin/", account_id?} }
                                           (account_id omitted when first_account was empty; dashboard then
                                            shows the "no healthy accounts" banner sourced from /api/admin/health,
                                            which reports degraded until an account is seeded.)
Browser GET /admin/                    →  SetupGate passes (setup-done) → web.FS serves index.html (SPA route)
```

**US-2 Upgrade without re-setup (brownfield auto-materialize — FR-002)**
```
Process boot  →  config.Load:
                   read config.json at ROUTER_CONFIG_PATH (default "./config.json")
                     file present?   apply file values → overlay env → normal boot
                     file absent?    return ErrNoConfig, continue to brownfield probe
              →  brownfield probe (BuildApp, only when ErrNoConfig):
                   require ROUTER_DB_DRIVER + ROUTER_DB_URL set; else gate stays closed → wizard
                   open DB with env values; run migrate up (dirty → slog.Error, continue)
                   SELECT COUNT(*) FROM upstream_accounts
                     if count > 0  →  synthesize config.json from env + defaults
                                      (writer.WriteAtomic, 0600); log
                                      "brownfield upgrade: materialized config.json"
                                   →  reload config → gate OPENS
                     if count == 0 →  gate CLOSES (fresh env-configured DB) → wizard
              →  steady-state: /v1/* live, SPA page /admin/settings (served from
                   frontend/dist/index.html) issues GET /api/admin/settings and shows:
                     data.runtime = flat { log_client_request_body, log_upstream_request_body, log_upstream_response_body,
                                           log_retention_days, log_level } — synthesized defaults,
                                     all 4 editable via POST /api/admin/settings/update
                     data.db      = { driver:"postgres", host:"db.internal:5432",
                                      database_name:"router_prod" } — display-only in 002
                     data.plugins = []  — no concrete plugin ships in 002
                     data.system  = { router_version, router_git_sha, router_built_at }
```

**US-3 Portal shell foundation**
```
Browser GET /admin/                   →  SetupGate passes → web.FS serves index.html (SPA route)
Browser GET /api/setup/status         →  setup.StatusHandler
                                         ↓ 200 { state:"done" }  (SPA now knows to stay on /admin/)
Browser GET /api/admin/settings       →  admin.SettingsHandler:
                                           returns data.runtime (flat: 4 keys),
                                                   data.db (driver, host, database_name),
                                                   data.plugins[] (only REGISTERED plugins,
                                                     each { id, label, enabled }; empty [] in 002),
                                                   data.plugin_intents[] (persisted operator
                                                     intent for admin_auth/client_keys),
                                                   data.system (3 build fields: version, sha, built_at)
                                         ↓ 200 envelope { code:0, msg:"ok",
                                                          data:{ runtime, db, plugins, system } }
SPA renders shell with hard-coded nav entries (`Dashboard`, `Accounts`, `Playground`,
   `Requests`, `Settings` live; `Client Keys`/`Observability` tagged `planned`); live on/off state of any shipped plugin
   comes from data.plugins[].
```

No `/api/admin/bootstrap` endpoint exists. The SPA's navigation structure is **hard-coded** in `frontend/src/components/shared/Sidebar.tsx` — nav entries, labels, and `planned` badges are all client-side constants. This means the sidebar renders identically regardless of what the server returns in `data.plugins[]`. The server-provided `data.plugins[]` is consumed only by the Settings page (to show live on/off status for plugins that have actually shipped). The separate `data.plugin_intents[]` array is the required Settings-page source for persisted `admin_auth` and `client_keys` intent switches. In 002 and 003 the server returns `data.plugins = []` because no concrete plugin binary ships yet; later admin-auth/client-key/observability features add registered plugin rows when their binaries exist. See `contracts/admin-api.md §GET /api/admin/settings` for the row schema and rationale for the Round-3 review 2026-04-19 simplification that removed `shipped_in_feature` and `source` fields.

The canonical mapping of nav entry → enabling plugin capability lives in `data-model.md § Page ↔ plugin dependency map`. The current shell ships `Dashboard`, `Accounts`, `Playground`, `Requests`, and `Settings` as enabled entries, with `Client Keys` and `Observability` rendered as disabled and tagged `planned`.

### Trade-off Analysis

| # | Decision | Approach A | Approach B | **Chosen** | Rationale |
|---|---|---|---|---|---|
| T-1 | Frontend build toolchain | Vanilla ESM + `go:embed` static HTML/CSS (no Vite) | **React 19 + Vite 6 + shadcn/ui + Tailwind v4 (built via pnpm, embedded via `go:embed frontend/dist/*`)** | **B** | 003/004/005 portal pages are CRUD + forms + dialogs + tables — **vanilla ESM would force either a 003 migration on top of a throwaway shell or hand-rolled accessible primitives**. `frontend/AGENTS.md` + `docs/standards/toolchain.md` already define this exact stack (previously "deferred beyond MVP"); 002 activates it. v9 mocks map 1:1 to shadcn components + Tailwind theme tokens. +1 engineer-week cost amortises across 003/004/005. Revised 2026-04-18 from an earlier draft that chose vanilla ESM. |
| T-2 | Setup state location | **Plain marker file on disk (`config.json` itself) + brownfield auto-materialize guard** | DB row `system_config('setup.state','done')` | **A** | `config.json` presence is the setup marker; FR-007 "deleting the file MUST NOT re-enable setup on a populated install" is satisfied by the brownfield auto-materializer, which synthesizes a fresh `config.json` at boot when `upstream_accounts > 0`. This is a cleaner reversal of the previous B choice — **every layer that cares about setup state reads from one place (the filesystem)** and the brownfield guard provides the DB-side invariant without a dedicated state column or table. Revised 2026-04-18 PM from earlier B. |
| T-3 | Config file format | YAML | TOML | **JSON** | JSON is already used by the Go stdlib (`encoding/json`), the only format all three datacenters tool round-trip losslessly, and matches the shell's `fetch()` ergonomics. YAML adds a dep and surface for formatting drift. |
| T-4 | Atomic write primitive | flock + overwrite | **tmp + rename** | **B** | POSIX `rename(2)` is atomic on same filesystem on Linux and macOS (the supported hosts) and does not depend on advisory locks the admin's platform may not honor. Same pattern already used by `store` for schema migrations. |
| T-5 | Settings hot-reload mechanism | File watcher (`fsnotify`) | **File is the authoritative source; in-memory broadcast via `atomic.Value`** | **B** | `POST /api/admin/settings/update` writes a new `config.json` atomically (tmp+rename) and publishes the new `*Config` through `atomic.Value`. Readers always `Load()`; no retained pointers, no shared mutation. File is readable by ops out-of-band (e.g. `jq config.json`) which is a nice property for debugging. Avoids a new dep (`fsnotify`) and the edit-vs-read race an external file watcher would introduce. **Why not DB as the runtime source?** It would force a round-trip on every hot-reload read-back, doubles the number of writes in Commit, and contradicts the spec clarification "single file configuration … the portal writes back to the same file for hot-reloadable fields". |
| T-6 | Plugin registry shape | Generic `Plugin.Wrap(surface, next)` URL-bound middleware | **Feature-flag + capability interfaces** (`Plugin.ID()` only + capability interfaces like `AdminAuth`; app owns enabled/disabled via `config.Plugins.Enabled(id)`) | **B** | Matches how Caddy, go-micro, and the broader Go ecosystem model extension points. Plugins express *what they do* (capability), not *where they mount* (URL). URL mounting is the app's concern. Enabled/disabled is a *platform* concern, not a *plugin* concern — the app reads one typed struct instead of asking each plugin to re-derive the bit. See `contracts/plugin-interface.md` §Mental model and `research.md` Decision 4. |
| T-7 | Setup gate enforcement | Check in every handler · or: ship as a plugin | **Hardcoded root-chain middleware in `internal/setup/gate.go`** | **B** | Fail-closed default means forgetting the check in one handler cannot leak routes. Making it a plugin would let operators disable it via feature flag — which is exactly the opposite of what the gate exists for. One middleware + allow-list: SPA static `/setup/*`, setup APIs `/api/setup/*`, health `/api/admin/health`. Everything else on `/api/admin/*` receives envelope `code=2011`; `/admin/*` HTML paths get `302 → /setup/`; `/v1/*` receives 001's native MVP error shape + HTTP 503 (the envelope policy excludes `/v1/*`). |
| T-8 | Wizard state persistence between steps | Server session | **Stateless — each step posts the full partial config; server only persists on Commit** | **B** | Spec edge case: "operator closes the tab halfway, comes back, fields unpopulated; no partial config persisted" (US-1). Stateless also eliminates session-store dependency. |
| T-9 | `POST /api/admin/settings/update` concurrency | Optimistic concurrency via `config.json.updated_at` etag + dedicated conflict code | **Last-writer-wins** | **B** | 002 has no admin auth, the blast radius is two keys, and the UI can layer a "your edit may have been overwritten" toast later if it becomes real pain. Optimistic concurrency adds a failure mode the operator cannot easily interpret without context, and would require a client-visible etag scheme that has no other use yet. Revisit when a future admin-auth feature introduces multi-operator sessions. |
| T-10 | Response envelope for router-owned endpoints | REST-idiomatic (HTTP status codes + `{error:{code,message}}` on error) | **Unified `{code:int, msg:string, data:any}` wrap for every JSON response on every non-`/v1/*` path, `code = 0` = success** | **B** | Decided 2026-04-18 PM. Rationale: (1) clients stop having to handle two response shapes (one for success, one for error) + three code vocabularies (HTTP status + business code + message text); (2) integer codes give operators a single numeric space to alert on, with a registry at `docs/error-codes.md`; (3) `/v1/*` is explicitly excluded from the Admin API envelope — during setup-pending the gate emits 001's native MVP error shape (`{"error":{"type":"service_unavailable","code":"setup_required","message":…}}`) with **HTTP 503** and request id header-only, never envelope `code = 2011`; setup-done provider responses are never wrapped. Feature 003 amends setup-done upstream transport: API-key rows preserve 002 Platform-compatible forwarding, while OAuth rows use the ChatGPT Codex backend for Responses traffic. HTTP 200 covers expected responses including business errors; HTTP 500 is reserved for system crashes. The one exception is non-JSON responses (static assets, redirects, `405 Method Not Allowed`, body-cap connection resets for bodies > 4×) — those stay raw. **Cost**: 001's `/admin/health` is backward-incompatible — (i) path relocated to `/api/admin/health`, (ii) body wrapped in envelope. Operators who scrape HTTP status for liveness must switch to checking `data.status` at the new path. Mitigated by announcing both changes in the 002 release notes and landing them alongside the 002 boot-time migrator. |
| T-11 | Settings mutation verb | REST `PATCH /admin/settings` (partial update) | **RPC-style `POST /api/admin/settings/update`** (sub-path verb) | **B** | Decided 2026-04-18 PM alongside T-10. Rationale: (1) consistent verb shape for all `/api/admin/*` mutations now and future (`POST /api/admin/accounts/create`, `.../disable`, `.../rotate`), readable by anyone who has written a gRPC or Taobao/Alibaba-style JSON-RPC service; (2) avoids PATCH vs PUT debates whenever a new field group is added; (3) the body semantics stay partial-update so the migration is a pure verb-rename on the Go + TS sides. **Cost**: non-idiomatic for REST-expecting reviewers — we document the convention in AGENTS.md. |

### Project Structure

```
one-llm-router/
├── cmd/one-llm-router/
│   └── main.go                              # MODIFIED: thin entrypoint delegating to app.BuildApp
├── internal/
│   ├── app/
│   │   ├── app.go                           # NEW: BuildApp(ctx,cfg,deps)(*App,error)
│   │   ├── app_test.go                      # NEW: TestPluginMatrix — table-driven with zero, one-of-each capability
│   │   ├── buildinfo/
│   │   │   ├── buildinfo.go                 # NEW: Version, GitSHA, BuiltAt (populated via -ldflags)
│   │   │   └── buildinfo_test.go            # NEW: smoke — defaults exist, format shape valid
│   │   └── doc.go                           # NEW
│   ├── config/
│   │   ├── config.go                        # NEW: Config, DBConfig, RuntimeConfig, PluginsConfig (+ per-plugin sub-structs AdminAuthPluginConfig/ClientKeysPluginConfig), version + timestamps; PluginsConfig.Enabled(id string) bool
│   │   ├── config_test.go                   # NEW: struct round-trip, default-fill, version-reject, Enabled(id) switch coverage
│   │   ├── loader.go                        # NEW: Load() — file → env overlay → defaults; source-of-value per field (env overrides in 002 restricted to {db.driver, db.url} only; runtime.* and plugins.* are file/default only — see D4 in plan.md Decisions 2026-04-19). 003+ may extend the env-overridable set.
│   │   ├── loader_test.go                   # NEW: env-wins, file-missing, bad-version, 0600-warn, plugin-flags-not-env-overridable
│   │   ├── writer.go                        # NEW: WriteAtomic (tmp.<pid> + fsync + rename, 0600), stale-tmp cleanup
│   │   ├── writer_test.go                   # NEW: crash-before-rename, bad-parent-dir, re-entry idempotence
│   │   ├── live.go                          # NEW: atomic.Value broadcaster — Publisher.Store(*Config) + Reader.Load() *Config; request-time readers never retain a *Config across requests
│   │   └── doc.go                           # NEW
│   ├── setup/
│   │   ├── state.go                         # NEW: StateReader (os.Stat ROUTER_CONFIG_PATH; presence == done)
│   │   ├── state_test.go                    # NEW: present/absent/unreadable-perm
│   │   ├── gate.go                          # NEW: SetupGate middleware (NOT a plugin; hardcoded infra). Reads StateReader once per request via sync.Once-memoized Load hint; allow-list /setup/* (SPA static), /api/setup/*, /api/admin/health. /v1/* NEVER wears the envelope — gate emits 001's native {error:{...}} + HTTP 503 on /v1/* in setup mode.
│   │   ├── gate_test.go                     # NEW: allow-list rules, envelope code=2011 paths on /api/admin/*, 302 redirect on HTML, native-MVP 503 error on /v1/*
│   │   ├── brownfield.go                    # NEW: BootstrapIfBrownfield(ctx, db, cfgPath, env) — the FR-002 auto-materializer; synthesizes config.json when accounts>0 and file missing
│   │   ├── brownfield_test.go               # NEW: env-set+accounts → materialize; env-set+empty → skip; env-unset → skip; parent-dir-readonly → error out
│   │   ├── probe.go                         # NEW: ProbeDSN (temp engine, SELECT 1, close, 5s hard timeout)
│   │   ├── probe_test.go                    # NEW
│   │   ├── commit.go                        # NEW: Commit (DB-first, file-last; see R-1). Pre-check os.Stat on config.json → envelope code=2001 setup_already_done if present.
│   │   ├── commit_test.go                   # NEW: fault-injection suite for SC-3 + R-1 (every crash point verified against auto-recovery rules)
│   │   ├── validator.go                     # NEW: per-step input validation (Go; the SPA has an independent Zod schema covering the same rules — they are kept in sync by the contract, not by code-sharing across languages)
│   │   └── doc.go                           # NEW
│   ├── plugin/
│   │   ├── plugin.go                        # NEW: Plugin interface (ID() only; enabled check lives on config.PluginsConfig) + optional Initializer + Deps
│   │   ├── capability.go                    # NEW: AdminAuth, ClientKeyAuth, ProxyHook + ProxyCompleteEvent
│   │   ├── registry.go                      # NEW: Register, Registry — duplicate-ID panic, lexical-ID order
│   │   ├── registry_test.go                 # NEW: empty-registry, duplicate-panic, stable-order
│   │   └── doc.go                           # NEW
│   ├── store/
│   │   ├── migrator.go                      # NEW: thin wrapper over golang-migrate/v4 (Up/Down/Force/Version/Status); used by BuildApp and by `one-llm-router migrate` CLI
│   │   └── migrator_test.go                 # NEW: SQLite in-process; PG/MySQL guarded by build tag
│   └── api/
│       ├── setup/
│       │   ├── handler.go                   # NEW: /api/setup/{status,probe-dsn,commit} handlers (routed under /api/setup/*; the SPA at /setup/* is the HTML surface)
│       │   ├── handler_contract_test.go     # NEW: request/response contract tests against setup-api.md
│       │   ├── routes.go                    # NEW: RegisterRoutes(mux, h)
│       │   └── doc.go                       # NEW
│       ├── admin/
│       │   ├── settings.go                  # NEW: GET /api/admin/settings + POST /api/admin/settings/update — returns data.runtime (flat, 4 keys), data.db (driver+host+database_name; no source), data.plugins[] (registered plugins only; [] in 002), data.system (3 build fields)
│       │   ├── settings_test.go             # NEW
│       │   ├── settings_contract_test.go    # NEW: contract test against admin-api.md §v4.0 schema — asserts flat runtime, plugins=[] when no concrete plugin shipped, system has exactly {router_version, router_git_sha, router_built_at}, update allow-list is the 4 log_* keys
│       │   └── routes.go                    # MODIFIED: register new routes
│       ├── envelope.go                      # NEW: WriteOK(w, data) / WriteBizErr(w, code, msg, data) / WriteSysErr(w, code, msg) + integer code constants (wraps docs/error-codes.md entries as Go consts)
│       ├── envelope_test.go                 # NEW: shape-stability tests — success=200+code=0, biz-error=200+code≠0, sys-error=500+code≠0, non-JSON paths (redirects, static assets) bypass the wrapper
│       └── web.go                           # NEW: go:embed frontend/dist/*, Vite-hashed asset serving, SPA fallback for /admin/* and /setup/*
├── frontend/                                # NEW: React + Vite + TS + shadcn + Tailwind workspace (conventions in frontend/AGENTS.md)
│   ├── package.json                         #       pnpm; pins React 19 / Vite 6 / TS 5 / shadcn / Tailwind v4 / TanStack Router|Query / RHF / Zod / Biome / Vitest / MSW / Playwright
│   ├── pnpm-lock.yaml                       #       committed
│   ├── vite.config.ts                       #       dev proxy: /api/* + /v1/* → http://localhost:8080 (SPA routes /admin/* and /setup/* served by Vite directly)
│   ├── tsconfig.json                        #       strict: true; no any; path alias @/ → ./src/
│   ├── biome.json                           #       per frontend/AGENTS.md
│   ├── tailwind.config.ts                   #       theme tokens derived from v9 design system (colors, radii, font families: IBM Plex Sans + Mono)
│   ├── playwright.config.ts                 #       targets the wizard happy-path E2E in 002
│   ├── index.html                           #       Vite entry — single SPA root
│   ├── public/                              #       favicon, robots.txt, any static passthroughs
│   ├── src/
│   │   ├── main.tsx                         #       QueryClient + Router + Theme provider mounts
│   │   ├── routes/
│   │   │   ├── __root.tsx                   #       Portal shell layout (top nav + sidebar + toast region)
│   │   │   ├── setup/
│   │   │   │   ├── route.tsx                #       /setup wizard root
│   │   │   │   ├── step-welcome.tsx         #       step 1
│   │   │   │   ├── step-dsn.tsx             #       step 2 (uses RHF + Zod)
│   │   │   │   ├── step-account.tsx         #       step 3
│   │   │   │   ├── step-plugins.tsx         #       step 4
│   │   │   │   └── step-commit.tsx          #       step 5 — two-phase commit call-site
│   │   │   ├── admin/
│   │   │   │   ├── index.tsx                #       /admin — "Setup complete" landing card
│   │   │   │   ├── settings.tsx             #       SPA route `/admin/settings`; calls GET /api/admin/settings + POST /api/admin/settings/update with RHF + Zod
│   │   │   │   ├── accounts.tsx             #       003+ accounts surface (list / onboarding / detail)
│   │   │   │   ├── playground.tsx           #       live Account Playground route
│   │   │   │   └── requests.tsx             #       live request/response history route
│   │   │   └── not-found.tsx                #       catch-all
│   │   ├── components/
│   │   │   ├── ui/                          #       shadcn components added via `pnpm dlx shadcn@latest add [name]`: button, input, form, card, select, alert, dialog, sonner, badge, separator, skeleton
│   │   │   └── shared/
│   │   │       ├── AppShell.tsx             #       top nav + sidebar + content slot
│   │   │       ├── Sidebar.tsx              #       nav entries are HARD-CODED client-side: Dashboard/Accounts/Playground/Requests/Settings are live; Client Keys/Observability render disabled with `planned` badges. data.plugins[] from GET /api/admin/settings is read ONLY by the Settings page to render live on/off state of already-shipped plugins
│   │   │       ├── ThemeToggle.tsx          #       localStorage + prefers-color-scheme
│   │   │       ├── ErrorBanner.tsx          #       correlation-id surfacing (FR-010)
│   │   │       └── WizardStepper.tsx        #       progress indicator shared across wizard steps
│   │   ├── hooks/
│   │   │   ├── use-settings.ts              #       GET /api/admin/settings + POST /api/admin/settings/update via TanStack Query (envelope-aware)
│   │   │   ├── use-setup-status.ts          #       GET /api/setup/status
│   │   │   ├── use-probe-dsn.ts             #       POST /api/setup/probe-dsn
│   │   │   └── use-commit.ts                #       POST /api/setup/commit
│   │   ├── lib/
│   │   │   ├── api-client.ts                #       fetch wrapper — adds X-Request-Id; unwraps {code,msg,data} envelope; surfaces code + msg as typed RouterApiError for UI; reads X-Request-Id response header for correlation; typed via generated client stub (003 will regenerate via @hey-api/openapi-ts)
│   │   │   ├── errcode.ts                   #       mirror of docs/error-codes.md — named integer constants (Err002SetupAlreadyDone = 2001, etc.); guards call-sites from magic numbers
│   │   │   └── cn.ts                        #       shadcn utility — Tailwind class merger
│   │   ├── styles/
│   │   │   └── globals.css                  #       Tailwind @import + CSS custom properties for v9 dark/light theme tokens
│   │   └── locales/                         #       per frontend/AGENTS.md — en + zh-CN; 002 ships en fully populated, zh-CN scaffolded
│   └── tests/
│       ├── e2e/
│       │   └── wizard-happy-path.spec.ts    #       Playwright — wizard 5 steps + landing redirect
│       └── setup.ts                         #       Vitest global setup (MSW)
├── specs/002-.../
│   ├── spec.md                              # existing
│   ├── plan.md                              # this file
│   ├── research.md                          # NEW
│   ├── data-model.md                        # NEW
│   ├── quickstart.md                        # NEW
│   ├── contracts/
│   │   ├── setup-api.md                     # NEW
│   │   ├── admin-api.md                     # NEW
│   │   └── plugin-interface.md              # NEW
│   └── checklists/spec-quality.md           # existing
```

**Changed lines estimate**: Backend ~1900 NEW + ~200 MODIFIED (`main.go` thinning + `api/admin/routes.go` additions). Frontend ~2400 NEW TSX/TS (8 routes × ~150 LOC + ~5 shared components × ~80 LOC + 4 hooks × ~40 LOC + lib + styles + test scaffolding). Total ~4300 LOC for 002.

## Traceability (FR / NFR → plan component)

| Spec item | Plan component |
|---|---|
| FR-001 first-run detection | `internal/config.Load` (detects missing `config.json`) + `internal/setup.Gate` |
| FR-002 auto-mark-complete on MVP upgrade | `internal/setup/brownfield.go` — boot-time probe materializes `config.json` when `upstream_accounts > 0` and file missing |
| FR-003 wizard covers the 5 steps | `internal/api/setup/handler.go` endpoints + `internal/setup/validator.go` |
| FR-004 DSN validation before advancing | `internal/setup.ProbeDSN` + `POST /api/setup/probe-dsn` |
| FR-005 atomic Commit | `internal/setup.Commit` two-phase path (see R-1) |
| FR-006 refuse `/v1/*` until done | `internal/setup.Gate` (hardcoded infra, not a plugin) — emits 001's native `{error:{...}}` + HTTP 503 on `/v1/*` while `config.json` is absent (the envelope policy permanently excludes `/v1/*`; see AGENTS.md §HTTP API Style) |
| FR-007 deleting `config.json` MUST NOT re-enable setup on a populated install | `internal/setup/brownfield.go` re-creates the file on next boot (T-2 revised). Deliberate friction: to truly reset, operator must also truncate `upstream_accounts`. |
| FR-008 env precedence (server-enforced; UI visibility deferred) | `internal/config/loader.go` tracks per-field source in-memory (used by the server to decide whether to emit `2013` on writes). 002 does not emit the source over HTTP because no 002 runtime key is env-overridable. |
| FR-009 admin shell with nav stubs | `frontend/src/components/shared/AppShell.tsx` + `Sidebar.tsx`. Seven nav entries hard-coded client-side: `Dashboard`, `Accounts`, `Playground`, `Requests`, and `Settings` live; `Client Keys` and `Observability` rendered as disabled stubs with `planned` badges. `data.plugins[]` from `GET /api/admin/settings` (returned as `[]` in 002) is read only by `routes/admin/settings.tsx` to render live/on-off state of any already-shipped plugin. No Go-side `shipped_features.go` is needed. |
| FR-010 correlation id surfacing | existing `RequestIDMiddleware` + `X-Request-Id` header (both request & response; never duplicated into `data`) |
| FR-011 hot-reload on selected fields | `internal/config/writer.go` (atomic rewrite) + `internal/config/live.go` (`atomic.Value` broadcast). Allow-list: `runtime.log_client_request_body`, `runtime.log_upstream_request_body`, `runtime.log_upstream_response_body`, `runtime.log_retention_days`, `runtime.log_level`. |
| FR-012 reject edits to env-pinned fields | `POST /api/admin/settings/update` consults the per-field source map; returns envelope `code = 2013 env_override_readonly` (HTTP 200). Unreachable in 002 (no 002 runtime key is env-overridable); kept wired for 003+. |
| FR-013 single port | existing `http.Server`; no change |
| FR-014 MVP-parity default | default `runtime.*` values in `internal/config/config.go`; plugin flags only recorded in `config.json.plugins.*`, not activated |
| NFR Performance wizard <500ms | in-process handlers; `ProbeDSN` 5s timeout |
| NFR Reliability atomic Commit | `commit_test.go` fault-injection matrix |
| NFR Security 0600 perms | `internal/config/writer.go` sets `os.FileMode(0o600)` |
| NFR Usability 2-min happy path | verified by `quickstart.md` scenario A |

## Risk Assessment

| # | Risk | P | I | Mitigation | Verification |
|---|---|---|---|---|---|
| R-1 | Commit partial success (DB account inserted, `config.json` rename fails — or vice-versa) leaves router in half-configured state | L | M | **DB-first, file-last ordering makes this structurally recoverable.** Commit flow: (a) pre-check `os.Stat(config.json)` → HTTP 200 + envelope `code = 2001 setup_already_done` if present; (b) BEGIN DB tx; (c) INSERT first account `ON CONFLICT DO NOTHING`; (d) COMMIT DB tx — DB is now durable; (e) open `config.json.tmp.<pid>` with `O_EXCL|O_CREATE`, write JSON, fsync, close; (f) `os.Rename(tmp, config.json)` — the single publish moment. If any step fails **before (d)**, the DB tx rolls back and no state changes. If a crash occurs **between (d) and (f)**, next boot sees: no `config.json` + `upstream_accounts` populated → brownfield auto-materializer runs (see FR-002 path) → synthesizes `config.json` from env → gate opens. **No half-configured state is observable to `/v1/*` clients.** Stale `.tmp.<pid>` files from any crash are swept on next boot by `internal/config/writer.SweepStale(dir)`. The wizard's rerun is idempotent: step-1 "DB already populated" branch offers the operator a "use existing" shortcut; `ON CONFLICT DO NOTHING` guarantees no duplicate account. | `commit_test.go` fault-injection table: inject an error at each of {tx-begin, insert-account, commit-tx, tmp-write, fsync, rename}; assert recovery state always = "either fully done OR cleanly redo-able" and `/v1/*` is never 500-ing with "no account" after a successful COMMIT. |
| R-2 | `config.json` creation with `0600` isn't honored on certain NFS / Windows hosts | L | M | Out of scope (`AGENTS.md` supports Linux + macOS for MVP); document the limitation in `specs/002-.../quickstart.md` and surface a startup warning if `os.Stat().Mode().Perm() != 0o600` | startup integration test on Linux CI with `umask` variations |
| R-3 | Env-var override silently ignored by operator who edits Settings → operator thinks the change took effect | L | M | `POST /api/admin/settings/update` returns envelope `code = 2013 env_override_readonly` (HTTP 200) for any key whose `source` is `env:*`. Note: **no 002 runtime key is env-overridable**, so this path is dormant in 002. 003+ re-introduces at least one env-overridable runtime key (candidate: `log_level`); at that point the server path activates and the portal adds a "pinned by env" inline hint (not currently in 002 UI scope). | `settings_contract_test.go::TestUpdateEnvPinnedReturnsEnvOverrideReadonly` (unit test uses a synthetic env-pinned fixture field to exercise the path without relying on 002 runtime keys) |
| R-4 | `frontend/dist/` generated artefact drifts from `frontend/src/` at commit time — operator builds and forgets to rebuild after a src edit | L | M | `Makefile` `build` target runs `pnpm -C frontend install --frozen-lockfile && pnpm -C frontend build` *before* `go build`, so `frontend/dist/*` is always fresh in CI and release artefacts. Developers running `go run ./cmd/one-llm-router` locally without a dist see an explicit 500 page from `internal/api/web.go` with a "run `cd frontend && pnpm build` first" hint. `.gitignore` keeps `frontend/dist/` out of VCS. | CI job `make build` green + manual smoke: delete `frontend/dist`, `go run` → see the hint |
| R-5 | Plugin registry abstraction gets over-built for "future flexibility" and slows 002 | M | M | **002 ships zero concrete plugins.** The registry has exactly the surface needed for the future `admin_auth` feature to plug in: `Plugin` root interface, three capability interfaces, `Register`/`Registry`. No dynamic loading, no `.so`, no reflection. If `internal/plugin/` exceeds 300 LOC (excluding tests & doc) before a concrete plugin lands, redesign. | `internal/plugin/` LOC budget in PR review + `TestRegistry_EmptyIsValid` passes |
| R-6 | Two concurrent Commit requests both enter the transaction | L | M | Pre-check `os.Stat(config.json)` at handler entry → envelope `code = 2001 setup_already_done`. DB uses `BEGIN IMMEDIATE` (SQLite) / `SELECT ... FOR UPDATE` on `upstream_accounts` (Postgres/MySQL); loser's `INSERT ... ON CONFLICT DO NOTHING` is a no-op. The loser then tries `O_EXCL` on tmp creation; if winner already wrote tmp → loser's open fails and handler returns the same envelope. If by race both writers reach rename simultaneously, POSIX rename atomicity means one wins and the second observes the file → the next request handler returns the envelope. Net effect: exactly one `upstream_account` row, exactly one `config.json`, losing client gets `code = 2001`. | `commit_test.go::TestConcurrentCommit` — spawns 5 goroutines, asserts 1×(code=0) + 4×(code=2001) all at HTTP 200, and exactly one account row. |
| R-7 | Hot-reload race: reader sees `Config.Runtime.BodyLogging=true` while writer is mid-PATCH, leaving a half-updated struct | L | M | `*Config` stored in `atomic.Value`; PATCH writes a **new** instance, not a mutation of the existing one. Publish order: (1) atomic `config.json` rewrite via tmp+rename, (2) `atomic.Value.Store(newCfg)`, (3) return 200. Readers always `Load()` and never retain pointers across request boundaries. Last-writer-wins (no 409); see T-9. | race detector on `settings_test.go` with `-race` |
| R-8 | shadcn/Tailwind rendering diverges from mocks over time, invalidating the "v9 is the design spec" contract | L | M | `frontend/tailwind.config.ts` encodes v9 theme tokens (color palette, radii, typography, spacing scale) as the single source; shadcn components consume those tokens via CSS variables. `mocks/v9-*.html` stays in `specs/002-.../mocks/` as the design spec the Tailwind theme is derived from. A locally-runnable `impeccable detect --fast` script against a Playwright-rendered screenshot of the built SPA stays on disk for spot-checks; the automated CI gate was de-scoped with T-502 and is tracked as follow-up work. We do **not** require pixel parity; mocks are the north star, shadcn+Tailwind render is the ship. | Manual design review + locally-runnable `impeccable detect` in `specs/002-.../mocks/` |
| R-9 | Boot-time `migrate up` leaves the DB in a **dirty** state (migration killed mid-run) and we decided to boot through the warning anyway | L | H | On `m.Version()` returning `dirty=true`, log `slog.Error("database migrations are dirty; manual recovery required", "version", v)` and continue booting. **We deliberately do not block boot** because a dirty state on an already-populated production DB should not take the router offline — operators need `/api/admin/health` to respond to see the warning. The `one-llm-router migrate force <version>` CLI subcommand is the documented recovery path. Risk: if the dirty migration was a partial DDL on a column `/v1/*` handlers depend on, the router may error at query time. Accepted because (i) the window is narrow — migrations in 001/002 are all small additive DDL, (ii) health endpoint surfaces the condition, (iii) the alternative of refusing boot hurts availability more than it helps correctness for the expected failure modes. Revisit if 003+ introduces destructive DDL. | `migrator_test.go::TestBootWithDirtyDB` asserts log line AND HTTP server still starts. |

**Summary**: 1 high-impact risk (R-9 dirty-migration boot-through — accepted with warning, documented recovery). R-1 downgraded from H to M after the commit-order inversion removed the half-configured window structurally. R-6 downgraded from H to M because `ON CONFLICT DO NOTHING` + `O_EXCL` makes concurrent-commit idempotent, not dangerous. All risks have verification hooks.

## Security Considerations

- **Authentication**: None in 002 (spec FR-014 MVP-parity). The wizard itself is unauthenticated; this is acceptable because a fresh install has no credentials yet, and the setup gate closes `/v1/*` until Commit succeeds. A plugin intent for admin-auth may be recorded via the wizard's plugin preview step; it takes effect only when 003 ships.
- **Authorization**: `/api/setup/*` is reachable only when `config.json` is absent (gate engaged) — with the single exception of `GET /api/setup/status`, which is always reachable. `/api/admin/*` is reachable only when `config.json` is present (gate open), with the single exception of `GET /api/admin/health`, which is always reachable so the SPA shell can render a banner during setup. The SPA surfaces at `/admin/*` and `/setup/*` (HTML) follow the same gate: HTML `/admin/*` during setup-pending redirects to `/setup/`; HTML `/setup/*` post-setup is served (the SPA itself then sees `state=done` and redirects to `/admin/`). `/v1/*` always produces 001's native error shape under the gate — never envelope. All rules enforced by `SetupGate` middleware and unit-tested.
- **Input validation**:
  - DB driver enum: one of `sqlite3 | postgres | mysql` — rejected with HTTP 200 + envelope `code=2002 invalid_driver` otherwise.
  - DSN string: length <= 4096; no shell metacharacters stripped but driver connect is the real validator (FR-004).
  - Upstream API key: length <= 256, non-empty; not echoed back in any response body.
  - JSON body size: existing `MaxRequestBodyMB` middleware already caps at 32 MB.
- **Sensitive data handling**:
  - Upstream API key flows through HTTPS (deployment concern) and is persisted by 001's `upstream_accounts.api_key` column in plaintext (ROADMAP accepted). Never logged, never serialized back to the portal.
  - `config.json` contains DB driver + DSN + runtime toggles + plugin intents. DSN may contain a DB password — so the file MUST be `0600` and its path MUST NOT appear in logs at INFO+ (DEBUG-level only). DSN values are never logged or echoed to the portal; `GET /api/admin/settings` returns only `db.driver` (not `db.url`).
  - Correlation id is non-sensitive; always safe to surface.
- **Three-tier boundary check (AGENTS.md)**:
  - `internal/domain/` untouched — no new domain entities, only config & infra.
  - `internal/core/` receives no new code in 002 — business rules unchanged.
  - `internal/api/` receives the new HTTP handlers.
  - `internal/setup/` and `internal/config/` sit alongside `internal/core/` at the orchestration layer, not below domain. Boundary preserved.
- **Body size caps**: `POST /api/setup/probe-dsn` and `POST /api/setup/commit` cap request bodies at 8 KiB (tighter than the default 32 MiB global cap because both endpoints only accept small JSON). `POST /api/admin/settings/update` caps at 16 KiB. Over-limit requests receive HTTP 200 + envelope `code = 2009 request_body_too_large`. Pathological abuse (body > 4× the cap) is terminated by connection reset with no response body — the only router-owned non-envelope response. See `contracts/setup-api.md` and `contracts/admin-api.md`.

## Test Hints (for sdd-test)

**Must-test scenarios** (mapped from Acceptance Criteria):

| Test ID | Maps to | Notes |
|---|---|---|
| T-US1-1 | US-1 AC-1 | GET `/` with no `config.json` → 302 `/setup/` |
| T-US1-2 | US-1 AC-2 | Full 5-step happy path → `/admin/` reachable + `/v1/*` serves |
| T-US1-3 | US-1 AC-3 | Unreachable DSN → probe returns HTTP 200 envelope `{code:2003,msg:"invalid_dsn",data:{latency_ms,hint}}` within 5s; Commit rejected with `code=2003` |
| T-US2-1 | US-2 AC-1 | env ROUTER_DB_* set + DB has accounts + no config.json → brownfield auto-materialize on boot → skip wizard; `/v1/*` on first request; `config.json` materialized with mode `0600` |
| T-US2-2 | US-2 AC-2 | env ROUTER_DB_* set + new (empty) DB → brownfield probe returns count=0 → wizard runs; after wizard completes, steady-state restart skips wizard |
| T-US3-1 | US-3 AC-1 | `/admin/` post-setup shows shell with disabled nav entries + "Setup complete" content |
| T-US3-2 | US-3 AC-2 | Client fetch fails → portal shows toast with correlation id |
| T-US4-1 | US-4 AC-1 | POST /api/admin/settings/update `{runtime:{log_client_request_body:true, log_upstream_request_body:true, log_upstream_response_body:true}}` → subsequent proxy request logs client/upstream request and upstream response bodies; also verify `log_retention_days` (range) and `log_level` (enum) paths |
| T-US4-2 | US-4 AC-2 | POST /api/admin/settings/update on a synthetic env-pinned fixture field → HTTP 200 envelope `{code:2013,msg:"env_override_readonly"}` + unchanged value. Uses a test-only registered key (002 ships none env-overridable); exercises the rejection machinery that 003+ relies on. |
| T-US5-1 | US-5 AC-1 | Fresh install: defaults pre-filled correctly |
| T-US5-2 | US-5 AC-2 | Happy-path wall-clock <10s on SQLite local |
| T-EDGE-abandon | US-1 Edge-1 | Close tab mid-wizard → next visit step 1 with empty fields |
| T-EDGE-race-commit | US-1 Edge-2 | Two concurrent Commit: exactly one succeeds (HTTP 200 + `code=0`); loser sees HTTP 200 + `code=2001 setup_already_done` |
| T-EDGE-bad-key | US-1 Edge-3 | Wizard completes even with bad upstream key; banner appears on `/admin/` |
| T-EDGE-concurrent-patch | US-4 Edge-1 | Concurrent POST /api/admin/settings/update: last-writer-wins (no conflict code); readers see a consistent post-state |
| T-EDGE-fail-mid-write | US-4 Edge-2 | In-memory update tx abort → no visible state change |
| T-FAULT-commit-matrix | SC-3 / R-1 | Inject error at each of {tx-begin, insert-account, commit-tx, tmp-write, fsync, rename} → for post-COMMIT crashes, brownfield auto-materialize fills in `config.json` on next boot; for pre-COMMIT crashes, the wizard is re-runnable and the DB tx rolled back cleanly; no orphan `.tmp.*` file after next boot (SweepStale). |
| T-FAULT-dirty-migrate | R-9 | Simulate a dirty migration (`force` the dirty bit), restart router → `slog.Error` line emitted + HTTP server still starts + `/api/admin/health` returns 200. |

**Tricky edge cases**:
- `config.json` exists but points to a DB with an outdated schema → boot-time `migrate up` brings schema forward; all 001/002 migrations are additive so no data loss.
- Operator uses a path with spaces or unicode in the SQLite DSN.
- Postgres host refuses TCP but responds ICMP — `ProbeDSN` should still time out within 5s (FR-004).
- `config.json.version` > `SupportedVersion` → router refuses to start with a clear error message (never silently downgrade).
- `config.json` exists but is `0644` instead of `0600` → startup logs a warning but continues (the file's content is still correct; widening perms is the operator's problem, not the router's).
- `config.json` absent AND `ROUTER_DB_URL` points to a DB with `upstream_accounts=0` → brownfield probe returns count=0 → gate stays closed → wizard runs as greenfield. No surprise auto-open.
- `config.json` absent AND brownfield probe would materialize, but the config-path's parent directory is read-only → boot fails with a clear error ("cannot materialize config.json for brownfield upgrade: permission denied on <dir>; fix perms or run wizard manually").

**Performance test parameters**:
- `ProbeDSN` wall clock under 500ms when the DB is reachable, under 5s when unreachable.
- Commit wall clock under 2s for SQLite local, under 8s for Postgres over localhost — well under the 10s P95 NFR.
- `pnpm build` wall clock under 30s cold, under 5s warm (tracked in CI).
- Initial SPA load (`frontend/dist/index.html` → interactive) under 1.5s over localhost.

**Regression areas**:
- Existing 001 integration tests must continue to pass unmodified after the `BuildApp` refactor.
- The `RequestIDMiddleware` wiring order must not change (spec FR-010 depends on request ids being propagated).
- 001's admin endpoints remain functionally identical post-refactor, with two wire-level changes called out in 001's tech-design changelog: (a) paths relocated to `/api/admin/accounts`, `/api/admin/requests`, `/api/admin/sessions/resolve`, `/api/admin/health`; (b) bodies wrapped in the envelope. The existing 001 behaviour (query results, validation, error shapes) is preserved byte-for-byte inside `data`.

**Frontend tests (Vitest + RTL + MSW for unit/integration; Playwright for E2E)**:
- `frontend/src/routes/setup/*.test.tsx` — per-step RHF+Zod validation, MSW-mocked `probe-dsn` 200/400/timeout.
- `frontend/src/hooks/use-commit.test.ts` — TanStack Query mutation invalidates `['setup','status']` on success.
- `frontend/src/components/shared/Sidebar.test.tsx` — asserts the hard-coded nav entries render regardless of `data.plugins[]` content (the sidebar is not server-driven); snapshot covers the `planned` badges on future entries and the live links for current surfaces.
- `frontend/tests/e2e/wizard-happy-path.spec.ts` — Playwright 5-step happy path against a live `go run ./cmd/one-llm-router` + `pnpm build` served binary.

## Complexity Tracking

> One documented deviation, accepted.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|---|---|---|
| `AGENTS.md` §API Contract says "OpenAPI 3.0.x is the single source of truth for admin APIs **from 003 onwards**" — 002 writes the three 002 admin/setup contracts in Markdown (`contracts/*.md`) instead of standing up an OpenAPI spec. | `spec.md` §Out of Scope explicitly defers OpenAPI generation to 003. Standing up OpenAPI now would require either (a) scaffolding the full `oapi-codegen` + `@hey-api/openapi-ts` toolchain two features early, adding ~1 engineer-week and ~500 LOC of generated stubs for a 5-endpoint surface, or (b) polluting `internal/generated/adminapi` with half-generated code that 003 has to re-derive. The Markdown contracts are specified in enough detail that the 003 OpenAPI generation is a straight translation. | Full OpenAPI adoption now → too early; wiring the codegen into CI for three endpoints does not pay back. Skipping contract docs → loses the contract-first guarantee the Integration-First Gate requires. |

## Dependency Versions (Verified 2026-04-17)

| Package | Version | Notes |
|---|---|---|
| `xorm.io/xorm` | `v1.3.11` | existing — no bump for 002 |
| `modernc.org/sqlite` | `v1.48.2` | existing — no bump |
| `github.com/lib/pq` | `v1.12.3` | existing — no bump |
| `github.com/go-sql-driver/mysql` | `v1.9.3` | existing (indirect) — no bump |
| `github.com/stretchr/testify` | `v1.11.1` | existing — no bump |
| `github.com/golang-migrate/migrate/v4` | `v4.19.1` | existing (indirect) — no bump |

**No new Go dependencies are introduced by 002.** Go side reuses stdlib + existing pinned packages.

**New npm dependencies (per `frontend/AGENTS.md` + `docs/standards/toolchain.md`)**, all pinned in `frontend/package.json`:

| Package | Version | Rationale |
|---------|---------|-----------|
| `react` + `react-dom` | `^19.0.0` | UI framework |
| `vite` | `^6.0.0` | build + dev server |
| `typescript` | `^5.5.0` | strict-mode TS |
| `@vitejs/plugin-react` | `^4.3.0` | Vite React plugin |
| `tailwindcss` | `^4.0.0` | styling |
| `@tanstack/react-router` | `^1.60.0` | routing |
| `@tanstack/react-query` | `^5.60.0` | server state |
| `react-hook-form` | `^7.53.0` | forms |
| `@hookform/resolvers` + `zod` | `^3.9.0` / `^3.23.0` | form validation |
| `react-i18next` + `i18next` | `^15.1.0` / `^23.16.0` | i18n (en + zh-CN scaffold) |
| shadcn/ui components | copy-pasted into `src/components/ui/` via `pnpm dlx shadcn@latest add …` — no package dependency | Radix primitives transitively pinned |
| `class-variance-authority` + `clsx` + `tailwind-merge` | shadcn utilities | required by shadcn components |
| `@biomejs/biome` | `^2.0.0` | lint + format |
| `vitest` + `@testing-library/react` + `msw` | `^2.1.0` / `^16.1.0` / `^2.6.0` | unit/integration tests |
| `@playwright/test` | `^1.49.0` | E2E |

Fonts: self-hosted IBM Plex subset under `frontend/public/fonts/` — licensed under SIL OFL 1.1, committed as .woff2 files (~60KB total). No CDN dependency at runtime.

## Build metadata

Version, git SHA, and build timestamp are injected at link time so the portal's settings page and startup log can report them without embedding build tooling in the Go source. The `Makefile` target (or equivalent build script) wires:

```
-ldflags "-X github.com/user/one-llm-router/internal/app/buildinfo.Version=$(VERSION) \
          -X github.com/user/one-llm-router/internal/app/buildinfo.GitSHA=$(shell git rev-parse --short HEAD) \
          -X github.com/user/one-llm-router/internal/app/buildinfo.BuiltAt=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)"
```

Module path matches `go.mod` (`module github.com/user/one-llm-router`). Using any other prefix would cause `go build` to succeed but produce defaults — silent failure. The `Makefile` target runs `go build -ldflags="..."` only (no `go install`) so the symbol substitution is applied on every build.

Defaults (for `go run` during development): `Version="dev"`, `GitSHA="unknown"`, `BuiltAt="unknown"`. Surfaced in `GET /api/admin/settings` under `data.system.{router_version,router_git_sha,router_built_at}`.

## Decisions 2026-04-19

Audit of 002's planning artifacts surfaced three ambiguities that required an operator decision. Each is recorded here so the spec/plan/tasks set reads as one coherent design.

| Id | Issue | Options considered | Decision | Impact |
|---|---|---|---|---|
| **D1** | During setup-pending, does `/v1/*` wear the unified envelope (`code = 2011`) or return 001's native MVP error shape? Earlier drafts of `admin-api.md`, `quickstart.md`, and `tasks.md` implied the envelope; `AGENTS.md §HTTP API Style` explicitly excludes `/v1/*` from the envelope. | (a) envelope `code=2011` HTTP 200; (b) **001 native error + HTTP 503** | **(b) v1_native** | Setup gate writes `{"error":{"type":"service_unavailable","code":"setup_required","message":…}}` with HTTP 503 and request id header-only when a `/v1/*` request arrives pre-setup. Clients built against 001's MVP error contract keep working unchanged; the envelope is opt-in per surface, not forced on upstream pass-throughs. Implemented in T-024; asserted in `gate_test.go` and `quickstart.md`. |
| **D2** | SPA route `/admin/settings` (HTML page) and admin JSON API `GET /admin/settings` collide on the same URL — ambiguous which the gate/router should serve. | (a) **move APIs under `/api/` prefix**; (b) rename SPA route; (c) content-negotiate (Accept header) | **(a) api_under_api_prefix** | All router-owned JSON APIs move to `/api/admin/*` and `/api/setup/*`. The SPA owns `/admin/*` and `/setup/*` HTML exclusively. A browser navigating to `/admin/settings` gets SPA HTML, which fetches data from `/api/admin/settings`. 001's four admin endpoints (`health`, `accounts`, `requests`, `sessions/resolve`) relocate to `/api/admin/*` as part of T-303 (see 001 tech-design changelog). |
| **D3** *(superseded by Round-3 review 2026-04-19)* | `GET /api/admin/settings` projection iterates over a roadmap plugin table. For IDs not yet wired into `config.PluginsConfig` (e.g. `prometheus` — ships in 005), calling `cfg.Plugins.Enabled(id)` logs `slog.Warn("unknown plugin id …")` even though the answer is "obviously disabled". | (a) skip `Enabled(id)` for roadmap-only IDs; (b) accept the warning; (c) change `Enabled()` to return `(bool, bool)` with an "exists" flag | **Obsolete** | The Round-3 per-field review (2026-04-19) removed the roadmap table entirely: `data.plugins[]` now lists **only actually-registered plugins** (the sidebar's "coming soon" entries are hard-coded client-side). 002 therefore returns `data.plugins = []` and the projection never walks an unshipped ID, so the warn path cannot fire. No `roadmap.go` ships. D3 is preserved in this table for audit-trail continuity only. |

The decisions are applied uniformly across `plan.md`, `spec.md`, `research.md`, `data-model.md`, `contracts/admin-api.md`, `contracts/setup-api.md`, `contracts/plugin-interface.md`, `docs/error-codes.md`, `quickstart.md`, `tasks.md`, `AGENTS.md`, `ROADMAP.md`, and `specs/sdd/state.json`. No task in `tasks.md` predates these decisions.

### Round-3 review 2026-04-19 — Settings schema walkthrough

After D1–D3 were locked in, a per-field walkthrough of `GET /api/admin/settings` and `POST /api/admin/settings/update` surfaced four additional simplifications. They are recorded here and applied to `contracts/admin-api.md §v4.0`, `spec.md`, `research.md`, `data-model.md`, `tasks.md`, `quickstart.md`, `docs/error-codes.md`, `ROADMAP.md` Decision #9, and `specs/sdd/state.json`.

| Id | Issue | Decision |
|---|---|---|
| **D4** | `data.runtime` previously wrapped each key in `{value, source}` so the UI could render `source = "env:*"` fields as read-only (FR-008 visibility). None of the five 002 runtime keys is in practice env-overridable. | **Flatten** `data.runtime` to raw primitives: `{log_client_request_body: bool, log_upstream_request_body: bool, log_upstream_response_body: bool, log_retention_days: int, log_level: "debug"\|"info"\|"warn"\|"error"}`. Per-field `source` is not surfaced. FR-008 is retained as a server-side behaviour (refuse writes via 2013) but its UI-visibility clause is dropped in 002. When 003+ lands an env-overridable runtime key, reconsider the shape. |
| **D5** | `body_logging` was one boolean covering client request, upstream request, and upstream response body logging. Operators want the three capture points independently. `retention_days` was ambiguous scope (records? logs? both?). | Split `body_logging` into three independent booleans `log_client_request_body`, `log_upstream_request_body`, and `log_upstream_response_body`; rename `retention_days` to `log_retention_days`. Add a new `log_level` runtime key (enum `debug\|info\|warn\|error`, default `"info"`). The update endpoint's runtime allow-list becomes those 5 keys (plugin-flag patching is decided separately in D9). New error code `2014 invalid_log_level` for enum-violation. |
| **D6** | `data.db` previously exposed only `{driver, source}`. The portal UI wants a non-secret identity string ("Postgres @ db.internal:5432 / router_prod"). Earlier drafts never defined a place for the host or database name; operators would squint at the driver line alone. | Add `host` (formatted `"host:port"`; `"local"` for sqlite3) and `database_name` (basename of file for sqlite3). Drop the `source` field (kept parity with `runtime` simplification). `db.url` remains forbidden (secret). DSN parse failures set both extra fields to `""`. |
| **D7** | `data.plugins[]` previously included `shipped_in_feature` and `source`, was the source of truth for the sidebar's "coming soon" entries, and required a roadmap table to enumerate unshipped rows (this was the source of the D3 warn-path friction). | Simplify each row to `{id, label, enabled}`. In 002 and 003, `data.plugins = []` because no concrete plugin binary ships yet; later admin-auth/client-key/observability features add rows only when their plugin binaries exist. The SPA sidebar is hard-coded client-side (live entries + `planned` badges are TS constants). This supersedes D3 (the roadmap table is removed) and removes `internal/app/shipped_features.go` + `internal/api/adminapi/roadmap.go` from 002's Go footprint. |
| **D8** | `data.system` previously had `{router_version, router_git_sha, router_built_at, setup_state, config_version}`. `setup_state` is redundant (this endpoint is setup-gated) and `config_version` has no user in 002 (only one schema version exists). `router_version` format was ambiguous (`"0.2.0-002"` vs. a git tag). | Keep only the three build fields. `router_version` is a **git-tag string** (`git describe --tags`, e.g. `"v0.2.0"` or `"v0.2.0-rc1"`). `router_git_sha` stays 7-char short. `router_built_at` stays ISO 8601 UTC. Clients that care about setup state hit `GET /api/admin/health` (always reachable). |

### Round-4 review 2026-04-19 PM — follow-up audit

After cascading D4–D8 a second audit sweep found three cross-doc contradictions and two plan-ambiguity items. Two needed user input (D9, D10 below); the other cross-doc fixes are recorded in the per-file changelogs.

| Id | Issue | Decision |
|---|---|---|
| **D9** | `ROADMAP.md` Architectural Decision #9 and `tasks.md` T-301 stated the update endpoint's allow-list is `the 5 runtime keys + plugins.<id>.enabled`. `contracts/admin-api.md` and D5 above stated only the 5 runtime keys are patchable and `plugins.*` is wizard-only. Real conflict — without a decision the implementer would have to guess. | **Allow plugin-flag patching in 002.** `POST /api/admin/settings/update` accepts `plugins.admin_auth.enabled` and `plugins.client_keys.enabled` (the two plugin IDs that have a `PluginsConfig` sub-struct in 002; future admin-auth/client-key features add their plugin-specific config fields additively, and 005 adds `PrometheusPluginConfig`). Unknown plugin IDs (e.g. `plugins.prometheus.*` in 002) return `2012 unknown_config_key`. Non-boolean values return `2006 invalid_plugin_flag` (the same code setup-api.md already uses). Rationale: operators must be able to flip plugin intent without re-running the wizard (the only alternative is `rm config.json` → re-wizard, which risks data loss on the DB side). In 002 the flip is a no-op on behaviour — it only records intent into `config.json.plugins.<id>.enabled`, consumed when the matching plugin binary ships. Covered in `contracts/admin-api.md §v4.1 changelog`. |
| **D10** | `contracts/setup-api.md` promised a server-wide one-in-flight lock on `/api/setup/probe-dsn` returning `code = 2010 probe_in_progress`. T-026 (ProbeDSN core) and T-102 (probe handler) did not mention implementing the lock — contract-to-task gap that would ship a dormant error code and a broken contract assertion. | **Drop the lock + drop `2010 probe_in_progress` from 002.** YAGNI: setup-pending routers are not exposed to the public internet (FR-014 notes "no admin auth in 002 — deploy behind a trusted boundary"), and the 5-second hard deadline already caps resource exposure per probe. Removing the code simplifies `error-codes.md`, `setup-api.md`, `specs/sdd/state.json`, and the T-102 test matrix. If 003 admin-auth + multi-operator makes concurrent probing an abuse vector, re-introduce the code in 003's 3xxx range. |
| **D11** | D9 unlocked plugin-flag patching at the API layer but T-311 still said "the plugins panel is suppressed when `data.plugins[]` is empty" — meaning in 002 there is NO UI surface for the operator to flip `plugins.admin_auth.enabled` / `plugins.client_keys.enabled` without writing a raw `curl`. The D9 rationale ("operators must not re-run the wizard to change plugin intent") requires a UI affordance, not just an API hook. | **Always render a "Plugin intents" panel on `/admin/settings` in 002** with two switches (Admin authentication → `plugins.admin_auth.enabled`; Client API keys → `plugins.client_keys.enabled`). Both switches are editable in 002 even though neither plugin is registered yet; each shows a grey caption `"plugin not yet installed in this build — flipping the switch only records operator intent"` whenever no matching row exists in `data.plugins[]`. When the matching plugin ships, the existing data-plugins row drives the caption off and the same switch flips real on/off behaviour — zero code change to `routes/admin/settings.tsx`. T-311 description + 2 extra test cases (happy_path for empty-array + happy_path for forward-compat non-empty) added. Prometheus / Observability / other plugin IDs are NOT shown in this panel — Observability lands in 005 with its own panel edit. |

## Open Questions for Review

These are intentional deferrals, not blocking:

1. **Admin-auth shipping boundary**: 002 records the operator's admin-auth intent but does not enforce it. If 003 slips, does 002 ship with a visible "admin portal is unauthenticated" banner on every page? → **Answer**: deferred to 003. Originally planned as a shell-wide yellow banner driven off `data.plugins[]`, but in 002 the admin portal is strictly local/single-operator (`FR-014` MVP-parity), the wizard itself is unauthenticated, and adding a banner would imply a security posture the feature does not deliver. 003 owns the admin-auth plugin and, alongside it, the visible "unauthenticated" banner in the same shell file (`frontend/src/components/shared/AppShell.tsx`).
2. **`config.json` location**: hardcoded next to the DB file, or via `ROUTER_CONFIG_PATH` env var? → **Answer**: env var with default `./config.json` relative to working directory. Matches 001's DB path convention.
3. **IBM Plex self-hosting**: ship subset or full family? → **Answer**: subset (Latin + tabular numerals) via `fonttools subset`. Total ≈60 KB. Vendored into `frontend/public/fonts/`; documented in `frontend/AGENTS.md` + `quickstart.md`.
4. **Static asset hashing**: resolved by Vite natively — `pnpm build` emits `frontend/dist/assets/index-<contenthash>.{js,css}` with matching references in `frontend/dist/index.html`. 002 does *not* implement a custom hashing step. `internal/api/web.go` serves `frontend/dist/assets/*` with `Cache-Control: public, max-age=31536000, immutable`, and `frontend/dist/index.html` with `Cache-Control: no-cache`.

---

**Next step**: run `sdd-tasks` to decompose this plan into an ordered, executable task list with L1/L2/L3 annotations and a dependency graph.
