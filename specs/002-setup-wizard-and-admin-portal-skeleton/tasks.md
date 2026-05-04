# Tasks: Setup Wizard and Admin Portal Skeleton

**Feature**: 002-setup-wizard-and-admin-portal-skeleton
**Plan**: specs/002-setup-wizard-and-admin-portal-skeleton/plan.md
**Spec**: specs/002-setup-wizard-and-admin-portal-skeleton/spec.md
**Created**: 2026-04-18
**Status**: Ready

---

## Task Format

### Markers
- `[P]` — Can run in parallel with other `[P]` tasks in the same Phase (different files, no dep cycles)
- `[US-X]` — Traces to User Story X (see spec.md)
- `[L1]` — AI generates full implementation
- `[L2]` — AI generates skeleton + `// [CONFIRM]:` markers for reviewer
- `[L3]` — AI generates interface + `// [HUMAN]:` placeholder; human implements

### Structured fields (L2/L3 tasks)
- `depends_on` — task IDs that must complete first
- `context_files` — MUST-READ files before generating code
- `constraints` — what NOT to change
- `what` — the expected output
- `must_not` — anti-requirements
- `verify` — `command` + `assert` (happy_path / error_path / boundary / coverage for L2/L3)

### Commands (from AGENTS.md)
- Go build: `go build ./...`
- Go test: `go test ./...` (race: `go test -race ./...`)
- Go lint: `golangci-lint run`
- Frontend install: `pnpm -C frontend install --frozen-lockfile`
- Frontend build: `pnpm -C frontend build`
- Frontend lint: `pnpm -C frontend lint`
- Frontend test: `pnpm -C frontend test`
- Frontend E2E: `pnpm -C frontend test:e2e`
- Format: `gofmt -w .` (Go) / `pnpm -C frontend format` (TS)

---

## Phase 1: Setup (scaffolding)
> Checkpoint: `go build ./...` passes (no new deps yet); `pnpm -C frontend install --frozen-lockfile` succeeds.
> Blocks: everything downstream.

- [x] T-001 [P] [L1] Go go.mod: bump directive to `go 1.25.0`; add `github.com/golang-migrate/migrate/v4` (promoted from tool → imported library). — `go.mod`, `go.sum`
  - verify:
    - command: `go mod tidy && go build ./...`
    - assert: build green; `go list -m github.com/golang-migrate/migrate/v4` prints `v4.19.1`.

- [x] T-002 [P] [L1] Scaffold `frontend/` workspace with pinned toolchain (React 19 + Vite 6 + TS 5 strict + shadcn + Tailwind v4 + TanStack Router/Query + RHF + Zod + Biome + Vitest + Playwright + MSW). Files: `frontend/package.json`, `frontend/pnpm-lock.yaml`, `frontend/tsconfig.json`, `frontend/vite.config.ts`, `frontend/biome.json`, `frontend/tailwind.config.ts`, `frontend/postcss.config.js`, `frontend/playwright.config.ts`, `frontend/index.html`, `frontend/.node-version`. — `frontend/*`
  - context_files:
    - `frontend/AGENTS.md` (§Pinned Toolchain, §Project Conventions)
    - `docs/standards/toolchain.md` (§Frontend)
    - `specs/002-.../plan.md` (§Project Structure — frontend/)
    - `specs/002-.../research.md` (§Decision 1)
  - constraints: use exact versions pinned in `frontend/AGENTS.md`; strict-mode TS (`"strict": true`); path alias `@/ → ./src/`; Vite dev proxy forwards `/admin/*`, `/v1/*`, `/setup/*` → `http://localhost:8080`.
  - what: A runnable `pnpm -C frontend dev` and `pnpm -C frontend build`; empty `src/main.tsx` that mounts a blank Router/QueryClient/ThemeProvider.
  - must_not: add any UI logic yet; add any dep outside the pinned list.
  - verify:
    - command: `pnpm -C frontend install --frozen-lockfile && pnpm -C frontend build`
    - assert:
      - happy_path: `frontend/dist/index.html` exists; `frontend/dist/assets/*.js` is Vite-hashed (`*.<hash>.js`).
      - boundary: `pnpm -C frontend typecheck` emits zero errors; `pnpm -C frontend lint` emits zero Biome errors.

- [x] T-003 [P] [L1] `.gitignore` updates: `frontend/dist/`, `frontend/node_modules/`, `config.json`, `config.json.tmp.*`, `router.db`, `*.db`. — `.gitignore`
  - verify:
    - command: `git check-ignore frontend/dist/foo frontend/node_modules/bar config.json config.json.tmp.123 router.db`
    - assert: all five paths report as ignored.

- [x] T-004 [P] [L1] Add Tailwind `@theme` tokens derived from the v9 design system (color palette, radii, spacing scale, typography — IBM Plex Sans + Plex Mono). — `frontend/tailwind.config.ts`, `frontend/src/styles/globals.css`, `frontend/public/fonts/*.woff2`
  - context_files:
    - `specs/002-.../mocks/v9-neoretro-grafana.html` (design tokens reference)
    - `specs/002-.../spec.md` (§Out of Scope — v9 design contract)
  - verify:
    - command: `pnpm -C frontend build`
    - assert: a representative component referencing `bg-surface-card` / `text-accent` compiles without `tailwindcss:warn`.

- [x] T-005 [P] [L1] Install shadcn/ui base components required by 002: `button`, `input`, `form`, `card`, `select`, `alert`, `dialog`, `sonner`, `badge`, `separator`, `skeleton`. Copy-paste via `pnpm dlx shadcn@latest add <name>`. — `frontend/src/components/ui/*`
  - verify:
    - command: `pnpm -C frontend build && ls frontend/src/components/ui/`
    - assert: all 11 component files exist; Tailwind classes inside them resolve against T-004's tokens.

- [x] T-006 [P] [L1] Makefile — orchestrate frontend + backend build. Add `make fe-build`, `make build`, `make test`, `make lint` targets; `make build` runs `pnpm -C frontend install --frozen-lockfile && pnpm -C frontend build && go build -ldflags "…" ./cmd/one-llm-router`. — `Makefile`
  - context_files:
    - `specs/002-.../plan.md` (§Project Structure — `-ldflags` for buildinfo)
  - verify:
    - command: `make build`
    - assert: produces `./one-llm-router` binary; `frontend/dist/` populated; binary reports version when passed `-version`.

---

## Phase 2: Foundation (blocks all user stories)
> Checkpoint: `go build ./... && go test ./internal/config/... ./internal/api/... -run '^Test.*Envelope|^TestConfig' ./...` passes; `golangci-lint run` clean.
> Blocks: Phases 3–6.

### 2a: Unified response envelope (cross-cutting)

- [x] T-010 [P] [L1] Integer error code constants mirroring `docs/error-codes.md` §Feature 002 + 001 admin + platform (0/-1). — `internal/api/errcode/codes.go`
  - verify:
    - command: `go test ./internal/api/errcode/...`
    - assert: every code symbol matches `docs/error-codes.md`; `errcode.Symbol(2001) == "setup_already_done"`; table-driven test iterates all known codes.

- [x] T-011 [L2] HTTP envelope writer (`{code,msg,data}`) with HTTP-200-for-biz-error / HTTP-500-for-sys-error policy; always sets `X-Request-Id` response header; never duplicates id into body. — `internal/api/envelope.go`, `internal/api/envelope_test.go`
  - depends_on: T-010
  - context_files:
    - `docs/error-codes.md` (full file)
    - `AGENTS.md` (§HTTP API Style)
    - `specs/002-.../contracts/admin-api.md` (§Response envelope, §Routing decision table)
    - `specs/002-.../contracts/setup-api.md` (§Error envelope)
  - constraints: do not touch `/v1/*` handlers (they bypass the envelope); do not localize `msg` (stable ASCII snake_case-ish strings).
  - what: three functions — `WriteOK(w, reqID, data any)`, `WriteBizErr(w, reqID string, code int, msg string, data any)`, `WriteSysErr(w, reqID string, code int, msg string)`. All set `Content-Type: application/json; charset=utf-8` and `X-Request-Id`. `data` defaults: `WriteOK` → whatever caller passed (panic on nil); `WriteBizErr` / `WriteSysErr` → `{}` when caller passed nil (never serialize `null`).
  - must_not: switch on HTTP status anywhere outside this package; read `data.code` back from body in tests (field must be integer, not string).
  - verify:
    - command: `go test -run TestEnvelope ./internal/api/...`
    - assert:
      - happy_path: `WriteOK(w, "req_abc", map[string]any{"state":"done"})` → status=200, body=`{"code":0,"msg":"ok","data":{"state":"done"}}`, header `X-Request-Id=req_abc`.
      - error_path: `WriteBizErr(w, "req_x", 2001, "setup_already_done", nil)` → status=200, body=`{"code":2001,"msg":"setup_already_done","data":{}}`.
      - boundary: `WriteSysErr(w, "req_y", -1, "unknown_error")` → status=500, body=`{"code":-1,"msg":"unknown_error","data":{}}`.
      - coverage: every branch in the function has a test (success / biz / sys / nil-data normalization / header-set).

- [x] T-012 [L2] Recovery middleware — catches panics, emits `slog.Error` with stack + correlation id, writes envelope `code=-1 unknown_error` via `WriteSysErr`. — `internal/api/recover.go`, `internal/api/recover_test.go`
  - depends_on: T-011
  - context_files:
    - `AGENTS.md` (§HTTP API Style, §Non-Negotiable Rules)
    - `specs/001-codex-router-mvp/plan/tech-design.md` (existing `RequestIDMiddleware` — reuse unchanged)
  - constraints: recovery MUST NOT wrap `/v1/*` paths (existing 001 error envelope stays on that path); composes **after** the existing `RequestIDMiddleware` so the correlation id is already attached.
  - what: `RecoverHandler(next http.Handler) http.Handler` — defer/recover → log + `WriteSysErr(w, reqID, -1, "unknown_error")`.
  - must_not: swallow the panic without logging; return an HTML error page.
  - verify:
    - command: `go test -run TestRecover ./internal/api/...`
    - assert:
      - happy_path: handler that doesn't panic → middleware passes through untouched, status & body unchanged.
      - error_path: handler that `panic("boom")` on `/admin/foo` → response has status=500, body `code=-1`, log line includes `stack=`.
      - boundary: panic on `/v1/models` is **not** caught here (preserved to 001's existing error handler).

### 2b: Configuration layer

- [x] T-013 [P] [L1] Config types — `Config`, `DBConfig`, `RuntimeConfig`, `PluginsConfig` (+ `AdminAuthPluginConfig`, `ClientKeysPluginConfig`). Each plugin sub-struct starts with `Enabled bool` and has `omitempty`-tagged reserved fields for 003/004. — `internal/config/config.go`, `internal/config/config_test.go`
  - context_files:
    - `specs/002-.../data-model.md` (§Schema, §Plugin Registry)
    - `specs/002-.../research.md` (§Decision 2, §Decision 4)
  - verify:
    - command: `go test -run TestConfigTypes ./internal/config/...`
    - assert: round-trip marshal/unmarshal is stable; unknown `plugins.<id>` keys are parsed but dropped on re-write (permissive decode, strict encode).

- [x] T-014 [L1] `PluginsConfig.Enabled(id string) bool` — compile-time switch on known plugin IDs; unknown IDs log a one-shot `slog.Warn` per ID per process (via `sync.Map` sentinel) and return `false`. — `internal/config/plugins.go`, `internal/config/plugins_test.go`
  - depends_on: T-013
  - context_files:
    - `specs/002-.../data-model.md` (§Plugin Registry — `Enabled(id)` implementation)
    - `specs/002-.../contracts/plugin-interface.md`
  - verify:
    - command: `go test -run TestPlugins ./internal/config/...`
    - assert:
      - happy_path: `Enabled("admin_auth")` returns struct value; `Enabled("client_keys")` returns struct value.
      - error_path: `Enabled("nonexistent")` returns false AND `slog.Warn` emitted exactly once per process (second call same id → silent).
      - coverage: all three known-id switch arms hit; unknown-id path hit.

- [x] T-015 [L2] Config loader — `Load(ctx, path, env) (*Config, SourceMap, error)`: read file → apply env overlay (in 002: **only `db.url` + `db.driver`** — `runtime.*` and `plugins.*` are file-only; overlay keys are declared in a single `envOverridableKeys` allow-list that 003+ can extend) → fill defaults → return effective config + per-field source map. Errors: `ErrNoConfig` (file absent), `ErrUnsupportedVersion` (version > 1 or unset), generic parse errors fail closed. — `internal/config/loader.go`, `internal/config/loader_test.go`
  - depends_on: T-013, T-014
  - context_files:
    - `specs/002-.../data-model.md` (§Read path, §Field-level env override precedence)
    - `specs/002-.../research.md` (§Decision 2)
  - constraints: file-missing MUST produce `ErrNoConfig` (not a zero-value Config); env-pinned fields record source `"env:ROUTER_<VAR>"`; plugin flags ALWAYS source from file/default (never env).
  - what: idempotent `Load`; `SourceMap` is `map[string]string` keyed by dotted field path (`"db.driver"`, `"runtime.log_client_request_body"`, `"runtime.log_upstream_request_body"`, `"runtime.log_upstream_response_body"`, `"runtime.log_retention_days"`, `"runtime.log_level"`, `"plugins.admin_auth.enabled"`). 002 runtime keys are all file-only (no env-override case) — the SourceMap entries stay in place for future runtime keys in 003+ and for db.* fields that can be env-pinned.
  - must_not: silently normalize `plugins` map keys (unknown keys → parsed but dropped); mutate `os.Env`.
  - verify:
    - command: `go test -race -run TestLoader ./internal/config/...`
    - assert:
      - happy_path: file present with all fields → source map shows `"db.driver":"file"`, etc.; `ROUTER_DB_URL=override` → `"db.url":"env:ROUTER_DB_URL"`, effective value is the override.
      - error_path: file missing → `ErrNoConfig`; file with `version:2` → `ErrUnsupportedVersion`.
      - boundary: file perms `0644` → loads successfully but logs warning; env sets `ROUTER_ADMIN_AUTH_ENABLED=true` → plugin flag is NOT overridden (source stays `"file"`), warn logged; env sets `ROUTER_LOG_LEVEL=debug` → runtime flag is NOT overridden (runtime.* is file-only in 002), warn logged.
      - coverage: three source values tested; the 2 env-overridable DB vars tested; the 4 non-overridable runtime keys are verified to ignore their ROUTER_* hypothetical env vars; the 2 non-overridable plugin flags verified file-only.

- [x] T-016 [L2] Atomic writer — `WriteAtomic(path string, cfg *Config) error`: `O_CREATE|O_EXCL|O_WRONLY, 0600` → write → `fsync` → `rename`. Plus `SweepStale(dir string)` that removes orphan `*.tmp.<pid>` siblings whose PID no longer exists. — `internal/config/writer.go`, `internal/config/writer_test.go`
  - depends_on: T-013
  - context_files:
    - `specs/002-.../data-model.md` (§Write path)
    - `specs/002-.../research.md` (§Decision 3)
  - constraints: tmp filename MUST include the writer's PID; MUST fsync before rename; MUST NOT chmod after write (set mode at O_CREATE time); `updated_at` is refreshed to `time.Now().UTC()` before marshal.
  - what: deterministic atomic write + crash-safe resume on boot.
  - must_not: rely on `os.WriteFile` (not atomic); leave 0-byte file on I/O error.
  - verify:
    - command: `go test -race -run TestWriter ./internal/config/...`
    - assert:
      - happy_path: after `WriteAtomic`, target exists with mode `0600` and parses back to the exact same struct.
      - error_path: parent dir read-only → error returned AND no tmp file left behind.
      - boundary: pre-existing `config.json.tmp.<stale-pid>` from dead process → `SweepStale` removes it; `config.json.tmp.<live-pid>` is kept.
      - coverage: all branches (new file / overwrite / rename failure / sweep with dead-pid / sweep with live-pid / sweep with non-numeric suffix).

- [x] T-017 [L1] Live-config publisher — `atomic.Value` wrapper with `Publisher.Store(*Config)` and `Reader.Load() *Config`; both package-level singletons initialised by `BuildApp`. — `internal/config/live.go`, `internal/config/live_test.go`
  - depends_on: T-013
  - verify:
    - command: `go test -race -run TestLive ./internal/config/...`
    - assert: concurrent 100×readers + 10×writers produce no race (under `-race`); every reader sees a complete `*Config` (no partial mutation).

### 2c: Build info

- [x] T-018 [P] [L1] Build-info package — `Version`, `GitSHA`, `BuiltAt` populated via `-ldflags -X` at link time; sensible dev-mode defaults. — `internal/app/buildinfo/buildinfo.go`, `internal/app/buildinfo/buildinfo_test.go`
  - verify:
    - command: `go test ./internal/app/buildinfo/... && go build -ldflags "-X .../buildinfo.Version=0.2.0-002" ./cmd/one-llm-router && ./one-llm-router -version`
    - assert: binary prints `0.2.0-002`; defaults `dev` / `unknown` when no ldflags.

### 2d: Migrator + CLI subcommand

- [x] T-019 [L2] `internal/store/migrator.go` — thin wrapper over `golang-migrate/v4`: `Up(ctx)`, `Down(ctx)`, `Force(ctx, v int)`, `Version(ctx) (uint, bool, error)` (dirty), `Status(ctx) string`. Uses the driver+URL from the passed `*config.DBConfig`. — `internal/store/migrator.go`, `internal/store/migrator_test.go`
  - depends_on: T-001, T-013
  - context_files:
    - `specs/002-.../research.md` (§Decision 7)
    - `specs/002-.../plan.md` (§Risk R-9)
    - `ROADMAP.md` (#8 brownfield + DB-first commit)
  - constraints: no schema migrations added in 002 — the wrapper wraps the EXISTING 001 migrations directory. `Up` returns nil on `ErrNoChange`.
  - what: a reusable migrator accessed both from `BuildApp` (boot-time `Up` + dirty check) and from the `one-llm-router migrate` CLI.
  - must_not: import `golang-migrate` from any package other than `internal/store`.
  - verify:
    - command: `go test -run TestMigrator ./internal/store/...`
    - assert:
      - happy_path: `Up` on empty SQLite DB applies all 001 migrations; subsequent `Up` returns nil.
      - error_path: `Force(ctx, 999)` on an unknown version returns an error.
      - boundary: `Dirty() == true` after a force — `TestBootWithDirtyDB` covers the boot pathway in T-030.

- [x] T-020 [L1] `cmd/one-llm-router/migrate_subcmd.go` — CLI subcommand dispatch: `one-llm-router migrate up|down|force <v>|version|status`. Uses the migrator from T-019 against env-configured DB or `-config` flag. Exit codes: 0 success, 1 error, 2 usage. — `cmd/one-llm-router/migrate_subcmd.go`, `cmd/one-llm-router/main.go`
  - depends_on: T-019
  - context_files:
    - `specs/002-.../research.md` (§Decision 7)
  - verify:
    - command: `go build ./cmd/one-llm-router && ./one-llm-router migrate status`
    - assert: exits 0; output includes `version=` and `dirty=false` against a fresh DB.

### 2e: Plugin seam

- [x] T-021 [P] [L1] Plugin interfaces — `Plugin` root (ID only), capability interfaces (`AdminAuth`, `ClientKeyAuth`, `ProxyHook`), `ProxyCompleteEvent`. — `internal/plugin/plugin.go`, `internal/plugin/capability.go`
  - context_files:
    - `specs/002-.../contracts/plugin-interface.md`
    - `specs/002-.../data-model.md` (§Plugin Registry)
  - verify:
    - command: `go build ./internal/plugin/... && go vet ./internal/plugin/...`
    - assert: package compiles; only `Plugin` has `ID()`; no `Enabled` method anywhere.

- [x] T-022 [L1] Plugin registry — `Register(Binding)` (package init-time), `Registry() []Binding` (lex-sorted by ID, snapshot copy), duplicate-ID panic. — `internal/plugin/registry.go`, `internal/plugin/registry_test.go`
  - depends_on: T-021
  - verify:
    - command: `go test -run TestRegistry ./internal/plugin/...`
    - assert: empty registry is valid; two registrations with same ID panic at init.

### 2f: Setup gate + state + brownfield

- [x] T-023 [P] [L1] Setup state reader — `StateReader.IsDone(path string) (done bool, err error)` via `os.Stat`; distinguishes `ErrNotExist` (not done) from permission errors (returns err). — `internal/setup/state.go`, `internal/setup/state_test.go`
  - context_files:
    - `specs/002-.../data-model.md` (§Overview, §Read path case 1/2/3)
  - verify:
    - command: `go test -run TestStateReader ./internal/setup/...`
    - assert:
      - happy_path: existing file → `done=true, err=nil`.
      - error_path: file absent → `done=false, err=nil`.
      - boundary: parent dir unreadable → non-nil err surfaces.

- [x] T-024 [L2] Setup gate middleware — wraps the root handler; if `config.json` absent: `/setup/*` (SPA static) and `/api/setup/*` pass through; `/api/admin/health` passes through; HTML `/admin/*` gets `302 → /setup/`; other `/api/admin/*` paths return envelope `code=2011 setup_required` at HTTP 200; `/v1/*` returns 001's native error shape (`{"error":{"type":"service_unavailable","code":"setup_required","message":"…"}}`) with **HTTP 503** and request id header-only — the envelope policy permanently excludes `/v1/*` (see AGENTS.md §HTTP API Style). Memoizes the "done" verdict once per process (sync.Once) so the hot path stays in-memory. — `internal/setup/gate.go`, `internal/setup/gate_test.go`
  - depends_on: T-011, T-023
  - context_files:
    - `specs/002-.../plan.md` (§Data Flow US-1)
    - `specs/002-.../contracts/admin-api.md` (§Routing decision table)
    - `specs/002-.../spec.md` (FR-006, FR-013)
  - constraints: gate is middleware, NOT a plugin; lives outside `internal/plugin/`; composes after `RequestIDMiddleware` so `X-Request-Id` is already set. **`/v1/*` is outside the envelope** — on setup-pending the gate writes 001's native MVP error shape (`{"error":{"type":"service_unavailable","code":"setup_required","message":"…"}}`) at **HTTP 503** and keeps request id header-only. It does NOT call the envelope helpers. The envelope `code=2011` branch is for `/api/admin/*` (non-health) only. The allow-list is: `/setup/*` (SPA static), `/api/setup/*`, `/api/admin/health`.
  - what: `Gate(next http.Handler) http.Handler` that consults a `*StateReader` snapshot. Dispatches by path prefix: `/v1/*` → 001 native error; HTML `/admin/*` → 302 redirect; `/api/admin/*` non-health → envelope `code=2011`.
  - must_not: perform DB queries in the hot path (state determined by file stat only); return HTML for API paths; emit the envelope on `/v1/*` (would silently break Codex clients).
  - verify:
    - command: `go test -run TestGate ./internal/setup/...`
    - assert:
      - happy_path: file present → every path passes through untouched.
      - error_path (setup pending):
        - `GET /v1/models` → **HTTP 503**, body `{"error":{"type":"service_unavailable","code":"setup_required","message":"..."}}` and `X-Request-Id` header — NO envelope.
        - `GET /api/admin/accounts` → HTTP 200 envelope `{code:2011,msg:"setup_required",data:{}}`.
        - `GET /admin/` with `Accept: text/html` → `302 Location: /setup/`.
        - `GET /api/setup/status` → passes through (handler answers).
        - `GET /api/admin/health` → passes through (001's handler answers, envelope-wrapped by T-303).
      - coverage: allow-list (3 prefixes) × deny-list × HTML-vs-JSON branching × native-vs-envelope error-shape branching for `/v1/*` all tested.

- [x] T-025 [L2] Brownfield auto-materializer — `BootstrapIfBrownfield(ctx, db, cfgPath string, env Env) (materialized bool, err error)`: if `cfgPath` absent AND env has `ROUTER_DB_DRIVER`+`ROUTER_DB_URL` AND `SELECT COUNT(*) FROM upstream_accounts > 0` → synthesize minimal `*Config` from env + defaults and write via `config.WriteAtomic`; log `slog.Info("brownfield upgrade: materialized config.json", …)`. — `internal/setup/brownfield.go`, `internal/setup/brownfield_test.go`
  - depends_on: T-015, T-016, T-019
  - context_files:
    - `specs/002-.../spec.md` (FR-002, FR-007, US-2)
    - `specs/002-.../data-model.md` (§Brownfield auto-materialization)
    - `specs/002-.../research.md` (§Decision 6)
    - `specs/002-.../plan.md` (§Risk R-1)
  - constraints: runs AFTER `migrate up` (so accounts table exists); atomic write only; MUST return `materialized=false, err=nil` when pre-conditions fail (not an error); `upstream_accounts` query uses existing 001 DAO, NOT ad-hoc SQL.
  - what: a single function + clear log lines + full fault-matrix test coverage.
  - must_not: prompt for user input (non-interactive); overwrite an existing `config.json`.
  - verify:
    - command: `go test -race -run TestBrownfield ./internal/setup/...`
    - assert:
      - happy_path: env set + DB has 3 accounts + file absent → materialized=true, file exists with mode 0600, `db.driver` matches env, `runtime` at defaults, `plugins` all disabled.
      - error_path: parent dir read-only → materialized=false, err != nil, NO partial file left behind.
      - boundary: env set + DB has 0 accounts → materialized=false, err=nil, file NOT created.
      - boundary: env unset → materialized=false, err=nil.
      - boundary: file already present → materialized=false, err=nil (function is a no-op).
      - coverage: all four pre-condition branches exercised.

- [x] T-026 [L2] DSN probe — `ProbeDSN(ctx, driver, url string) (latencyMs int64, serverVersion string, err error)`: opens a temp engine, runs `SELECT 1`, closes. Enforces a 5-second hard deadline via `context.WithTimeout`; returns driver-family-aware `hint` (attached by the handler, not the probe itself) on error. — `internal/setup/probe.go`, `internal/setup/probe_test.go`
  - depends_on: T-013
  - context_files:
    - `specs/002-.../spec.md` (FR-004, US-1 AC-3)
    - `specs/002-.../contracts/setup-api.md` (§POST /api/setup/probe-dsn)
  - constraints: probe MUST NOT leak the open engine across goroutines — one probe = one open + one close; connection pool size capped at 1; MUST NOT log the DSN at INFO (DEBUG only).
  - what: a standalone helper usable by both the handler and the commit path; all three drivers (sqlite3/postgres/mysql) covered.
  - must_not: retry (caller decides); cache results.
  - verify:
    - command: `go test -race -run TestProbe ./internal/setup/...`
    - assert:
      - happy_path: valid sqlite DSN → err=nil, latency_ms small, server_version matches `sqlite 3.`.
      - error_path: unreachable postgres DSN → err wraps deadline-exceeded within 5.5s wall clock.
      - boundary: malformed DSN → err returned immediately (no deadline-wait).
      - coverage: all three drivers exercised (sqlite always; pg/mysql guarded by build-tag `integration`).

### 2g: *(removed)* Shipped features manifest

> **Removed by Round-3 review 2026-04-19.** The earlier T-027 created `internal/app/shipped_features.go` as the backend source for a roadmap-driven `data.plugins[]` projection. That projection is gone: `GET /api/admin/settings` now returns only *actually-registered* plugins (empty array in 002). The SPA sidebar's "coming soon" entries are hard-coded TS constants in `frontend/src/components/shared/Sidebar.tsx`. No Go-side roadmap slice is needed. Task IDs T-014..T-026 remain unchanged; T-028 onwards keep their numbering for traceability with past commits and PR diffs.

### 2h: BuildApp orchestration

- [x] T-028 [L3] `BuildApp(ctx context.Context, cfg *config.Config, srcMap config.SourceMap, deps Deps) (*App, error)` — assembles router:
  1. if `cfg == nil || errors.Is(err, ErrNoConfig)` — enter bare-bones setup-only server (no DB, no plugins).
  2. else — open DB → `migrator.Up` (log dirty as `slog.Error` but continue) → `setup.BootstrapIfBrownfield` → iterate `plugin.Registry()` and skip where `cfg.Plugins.Enabled(id)==false` → type-assert remaining plugins into capability buckets → wire handlers. Returns an `*App` with `Start()` / `Stop()` methods. — `internal/app/app.go`, `internal/app/app_test.go`
  - depends_on: T-013 → T-028 (all foundation tasks)
  - context_files:
    - `specs/002-.../plan.md` (§Data Flow — all three US flows, §Project Structure)
    - `specs/002-.../contracts/plugin-interface.md` (§BuildApp lifecycle)
    - `specs/002-.../research.md` (§Decision 4, §Decision 6)
  - constraints: order of side effects is EXACTLY: log startup → `migrator.Up` → dirty check → brownfield materialize → reload config → register routes → start HTTP server. Ordering is load-bearing for R-1 and R-9.
  - what: a thin orchestrator; no business logic beyond dispatch; testable in-process via `TestPluginMatrix`.
  - must_not: `log.Fatal` anywhere; return a partial `*App` on setup-mode path (just a constrained one).
  - verify:
    - command: `go test -race -run TestBuildApp ./internal/app/...`
    - assert:
      - happy_path: greenfield (no config) → `*App` returned, only setup routes registered; `/v1/models` returns **HTTP 503** with 001 native error shape (`{"error":{"type":"service_unavailable","code":"setup_required",…}}`) per decision D1 — `/v1/*` is permanently outside the envelope (AGENTS.md §HTTP API Style). Non-`/v1` admin paths continue to use envelope `code=2011`.
      - happy_path: brownfield (env DB + 1 account, no file) → file materialized, `/v1/models` routes.
      - happy_path: steady-state (file present, DB populated) → all routes live.
      - error_path: dirty migration state → `slog.Error("database migrations are dirty", …)` emitted, boot succeeds.
      - error_path: DB open failure → returns wrapped error (no partial *App).
      - boundary: zero-plugins-enabled → capability lists are all empty; handlers gracefully degrade (not nil-panic).
      - coverage: all four boot-mode paths (greenfield / brownfield / steady / dirty-mig) hit.
  - [HUMAN]: verify the exact order of the `BuildApp` side-effect chain against the ops-pager review — the sequencing is load-bearing for R-1/R-9 and the diff is worth a second pair of eyes before merge.

- [x] T-029 [L1] Thin `main()` delegating to `BuildApp` — parses a minimal flag set (`-config`, `-listen`, `-version`), dispatches the `migrate` subcommand when `os.Args[1] == "migrate"`. — `cmd/one-llm-router/main.go`
  - depends_on: T-020, T-028
  - verify:
    - command: `go build ./cmd/one-llm-router && ./one-llm-router -version && ./one-llm-router migrate status`
    - assert: `-version` prints buildinfo; `migrate status` prints migrator output; regular run enters either setup-mode or steady-state.

- [x] T-030 [L2] Boot-time migration integration test — fresh SQLite file → `BuildApp` → assert migrator `Up` ran + `Dirty=false`; then force-dirty + restart → assert `slog.Error` line emitted AND HTTP server still responds on `/api/admin/health`. — `internal/app/app_boot_test.go`
  - depends_on: T-019, T-028
  - verify:
    - command: `go test -run TestBootWithDirtyDB ./internal/app/...`
    - assert:
      - happy_path: first boot applies migrations, `/api/admin/health` returns envelope `code=0 msg="ok"` (the canonical success shape; `1000` is a legacy alias reserved for unwrapped 001 handlers only).
      - error_path: dirty state → log line with `dirty=true` present; `/api/admin/health` still HTTP 200 with envelope `code=0` and `data.status="degraded"` (or `"unhealthy"` depending on whether queries still work) — the business state lives in `data.status`, not in `code`.
      - coverage: fresh-DB / dirty-DB / already-migrated-DB variants.

---

## Phase 3: US-1 — First-install walkthrough (P0)
> Story: US-1 — operator brings a fresh instance to a working state via the wizard.
> Acceptance: AC-1.1 (redirect on first visit), AC-1.2 (5-step happy path), AC-1.3 (unreachable DSN fails gracefully).
> Checkpoint: `go test ./internal/setup/... ./internal/api/setup/...` + `pnpm -C frontend test` + `pnpm -C frontend test:e2e -- --grep wizard-happy-path` all green.

### 3a: Backend — setup API

- [x] T-100 [L1] Input validator — centralized per-step validation (driver enum, DSN length, account fields, plugin flags, retention bounds) → returns `(errCode int, msg string, field string)` tuples. — `internal/setup/validator.go`, `internal/setup/validator_test.go`
  - depends_on: T-010, T-013
  - context_files:
    - `specs/002-.../data-model.md` (§Validation Rules)
    - `docs/error-codes.md` (§Feature 002 — 2002..2008, 2012)
  - verify:
    - command: `go test -run TestValidator ./internal/setup/...`
    - assert:
      - happy_path: valid commit payload → err=nil.
      - error_path: unknown driver `foo` → code=2002.
      - error_path: retention=0 → code=2007.
      - error_path: account name with space → code=2004.
      - coverage: every 2002–2008 code reachable via at least one input.

- [x] T-101 [L2] `POST /api/setup/commit` — DB-first, file-last transaction. Pre-check `os.Stat(config.json)` (return envelope `code=2001`) → validator → open DB + `migrator.Up` → `BEGIN IMMEDIATE` (SQLite) or `SELECT … FOR UPDATE` (PG/MySQL) → `INSERT upstream_accounts ON CONFLICT DO NOTHING` → `COMMIT` → `config.WriteAtomic` → `config.Publisher.Store` → respond envelope `code=0 data:{redirect:"/admin/"}`. — `internal/setup/commit.go`, `internal/setup/commit_test.go`
  - depends_on: T-011, T-015, T-016, T-017, T-019, T-100
  - context_files:
    - `specs/002-.../contracts/setup-api.md` (§POST /api/setup/commit)
    - `specs/002-.../data-model.md` (§Commit atomicity)
    - `specs/002-.../research.md` (§Decision 3)
    - `specs/002-.../plan.md` (§Risk R-1, R-6)
  - constraints: file rename is the SINGLE observable "setup complete" moment; all pre-rename side-effects MUST be self-healing (DB tx rollback on any step ≤ commit-tx; rename race loses via `O_EXCL` on tmp); never log the `api_key`.
  - what: one exported func `Commit(ctx, deps, req) (*Result, error)` whose error shape pairs with an envelope via a mapping table.
  - must_not: retry on transient errors (let the handler + operator decide); write the file before the DB COMMIT returns.
  - verify:
    - command: `go test -race -run TestCommit ./internal/setup/...`
    - assert:
      - happy_path: empty state → `{code:0, data:{redirect:"/admin/"}}`; file exists mode 0600; account inserted; `config.Reader.Load()` reflects new cfg.
      - error_path (SC-3 fault matrix): inject error at each of {tx-begin, insert-account, commit-tx, tmp-open, tmp-write, fsync, rename} — for pre-commit crashes, DB tx rolled back + no file; for post-commit+pre-rename crashes, DB populated + no file (recoverable by brownfield on next boot).
      - error_path: file already present → `code=2001 setup_already_done`, DB untouched.
      - error_path: probe succeeds but commit-time open fails → `code=2900 db_connect_failed` (HTTP 500).
      - boundary (R-6): 5 goroutines `Commit` with disjoint account names → exactly one wins (code=0), four lose (code=2001), exactly one account row in DB.
      - coverage: every step of the 6-step fault matrix × 2 drivers (sqlite mandatory, pg optional via build tag).

- [x] T-102 [L1] `POST /api/setup/probe-dsn` — envelope-wrapped handler invoking `ProbeDSN` + 8 KiB body cap. — `internal/api/setup/probe_handler.go`, `internal/api/setup/probe_handler_test.go`
  - depends_on: T-011, T-026, T-100
  - context_files:
    - `specs/002-.../contracts/setup-api.md` (§POST /api/setup/probe-dsn)
  - verify:
    - command: `go test -run TestProbeHandler ./internal/api/setup/...`
    - assert:
      - happy_path: valid sqlite → `{code:0, data:{ok:true, latency_ms, server_version}}`.
      - error_path: unreachable DSN → `{code:2003, msg:"invalid_dsn", data:{latency_ms,hint}}` at HTTP 200.
      - boundary: body > 8 KiB → `{code:2009, msg:"request_body_too_large", data:{}}`.
      - boundary: malformed JSON → `{code:2008, msg:"malformed_body", data:{}}`.

- [x] T-103 [L1] `GET /api/setup/status` — returns current setup state (derived from `config.json` presence) + supported driver list + whether an in-flight commit exists. — `internal/api/setup/status_handler.go`, `internal/api/setup/status_handler_test.go`
  - depends_on: T-011, T-023
  - context_files:
    - `specs/002-.../contracts/setup-api.md` (§GET /api/setup/status)
  - verify:
    - command: `go test -run TestStatusHandler ./internal/api/setup/...`
    - assert: body `code=0`, `data.state` in `{"pending","done"}`, `data.supported_drivers` len>=3.

- [x] T-104 [L1] `POST /api/setup/commit` handler wrapper — calls `setup.Commit`, maps errors to integer codes, returns envelope with X-Request-Id. — `internal/api/setup/commit_handler.go`, `internal/api/setup/commit_handler_test.go`
  - depends_on: T-101
  - verify:
    - command: `go test -run TestCommitHandler ./internal/api/setup/...`
    - assert: contract shape matches `contracts/setup-api.md` — every documented code/msg pair is produced by at least one test input.

- [x] T-105 [L1] Setup API routes — `RegisterRoutes(mux *http.ServeMux, deps Deps)`: `/api/setup/status`, `/api/setup/probe-dsn`, `/api/setup/commit`. Applies 8 KiB body cap middleware to the POST endpoints. — `internal/api/setup/routes.go`
  - depends_on: T-102, T-103, T-104
  - verify:
    - command: `go build ./...`
    - assert: build green; routes visible via `go test -run TestRoutesRegistered`.

### 3b: Frontend — wizard

- [x] T-110 [L1] Shared API client — `lib/api-client.ts` — typed fetch wrapper that unwraps `{code,msg,data}` envelope, surfaces `code==0` → `data` and `code!=0` → typed `RouterApiError{code,msg,data}`; propagates `X-Request-Id` request→response. — `frontend/src/lib/api-client.ts`, `frontend/src/lib/api-client.test.ts`
  - depends_on: T-002
  - context_files:
    - `AGENTS.md` (§HTTP API Style)
    - `specs/002-.../contracts/setup-api.md`, `contracts/admin-api.md`
  - verify:
    - command: `pnpm -C frontend test api-client`
    - assert:
      - happy_path: server returns `{code:0,msg:"ok",data:{x:1}}` → resolves `{x:1}`.
      - error_path: server returns `{code:2001,msg:"setup_already_done",data:{}}` → rejects with `RouterApiError{code:2001}`.
      - boundary: malformed response (`null` body) → rejects with `RouterApiError{code:-1}`.

- [x] T-111 [L1] Frontend error-code mirror — integer constants (`Err002SetupAlreadyDone=2001`, …) mirroring `docs/error-codes.md`. — `frontend/src/lib/errcode.ts`
  - verify:
    - command: `pnpm -C frontend typecheck`
    - assert: exported names match `docs/error-codes.md` entries byte-for-byte (spot-checked via test).

- [x] T-112 [L2] Wizard root route `/setup/` with 5-step stepper, RHF + Zod, TanStack Router nested routes. — `frontend/src/routes/setup/route.tsx`, `frontend/src/routes/setup/step-welcome.tsx` + 4 siblings
  - depends_on: T-005, T-110, T-111
  - context_files:
    - `specs/002-.../spec.md` (US-1, US-5)
    - `specs/002-.../mocks/v9-neoretro-grafana.html`
    - `specs/002-.../contracts/setup-api.md`
  - constraints: wizard state is ephemeral (zustand or route-local `useReducer`) — NOT persisted across tab closes (spec US-1 Edge-1).
  - what: 5 steps — welcome, DSN + probe-dsn call with latency badge, first account, plugin preview (toggles record intent only), commit + loading state + redirect.
  - must_not: POST commit until all prior step validations pass; persist sensitive inputs to `localStorage`.
  - verify:
    - command: `pnpm -C frontend test wizard`
    - assert:
      - happy_path: full 5-step RTL walkthrough → final commit fires POST with correct payload shape.
      - error_path: step 2 probe returns code=2003 → error banner + Next disabled.
      - boundary: close tab mid-wizard → on reopen, step 1 with empty fields.

- [x] T-113 [L1] Shared shell components — `AppShell`, `Sidebar` (reads `data.plugins[]`), `ThemeToggle` (localStorage + prefers-color-scheme), `ErrorBanner` (surfaces correlation id from response `X-Request-Id`), `WizardStepper`. — `frontend/src/components/shared/*`
  - depends_on: T-004, T-005, T-110
  - context_files:
    - `specs/002-.../spec.md` (US-3 AC-1/AC-2, FR-009, FR-010)
    - `specs/002-.../data-model.md` (§Page ↔ plugin dependency map)
  - verify:
    - command: `pnpm -C frontend test shared`
    - assert:
      - happy_path: Sidebar renders its seven hard-coded nav entries; `Dashboard`, `Accounts`, `Playground`, `Requests`, and `Settings` are enabled and have no badge; `Client Keys` and `Observability` are disabled with `planned` badges. This holds regardless of what `data.plugins[]` contains (including the 002 case where it is `[]`).
      - happy_path: ErrorBanner given a `RouterApiError` surfaces the correlation id visibly.
      - boundary: Sidebar with empty plugins array still renders the live entries enabled and the future entries disabled with their `planned` badges.

- [x] T-114 [L2] Playwright E2E — wizard happy path from `/setup/` → `/admin/`. — `frontend/tests/e2e/wizard-happy-path.spec.ts`
  - depends_on: T-112, T-113, T-105
  - context_files:
    - `specs/002-.../quickstart.md` (§Path A)
  - constraints: drives the REAL Go router on `:8080` (started via a fixture); does not mock the backend; cleans up `config.json` + `router.db` before each run.
  - what: assert screenshots at key steps match a baseline under `frontend/tests/e2e/__screenshots__/`.
  - verify:
    - command: `pnpm -C frontend test:e2e -- --grep wizard-happy-path`
    - assert:
      - happy_path: end-to-end 5 steps complete in <10s wall clock; final URL is `/admin/`; `config.json` on disk with mode 0600.
      - error_path: simulate unreachable DSN (invalid driver) → step 2 shows error with correlation id; wizard does not advance.
      - coverage: US-1 AC-1, AC-2, AC-3 all exercised.

---

## Phase 4: US-2 — Upgrade without re-setup (P0)
> Story: US-2 — MVP operator upgrades to 002 binary without wizard interruption.
> Acceptance: AC-2.1 (env-configured DB with accounts boots straight), AC-2.2 (auto-material on first 002 boot).
> Checkpoint: `go test -run 'TestBrownfield|TestBootWithDirtyDB|TestBuildApp' ./internal/...` + integration smoke from quickstart Path B passes.

- [x] T-200 [P] [US-2] [L2] Brownfield integration test — orchestrates `BuildApp` against an env-configured SQLite DB with pre-seeded `upstream_accounts` + no `config.json` → assert file materialized + `/v1/models` (mocked upstream) serves. — `internal/app/app_brownfield_test.go`
  - depends_on: T-025, T-028
  - context_files:
    - `specs/002-.../spec.md` (US-2)
    - `specs/002-.../data-model.md` (§Brownfield auto-materialization)
    - `specs/002-.../quickstart.md` (§Path B)
  - verify:
    - command: `go test -race -run TestBrownfield ./internal/app/...`
    - assert:
      - happy_path (AC-2.1): env set + DB with 1 account → `GET /v1/models` returns upstream-forwarded response (mocked), `config.json` exists with `db.driver` source `env:*`.
      - happy_path (AC-2.2): env set + DB with 1 account + no file → on BOOT, file materialized without operator action; `/api/setup/status.data.state == "done"`.
      - boundary: env set + empty DB → no materialize; wizard runs; `/v1/*` blocked.
      - boundary: env unset → no materialize; wizard runs.

- [x] T-201 [US-2] [L1] Quickstart smoke script — a bash script that mirrors `quickstart.md` Path B for CI smoke. — `scripts/smoke-brownfield.sh`
  - depends_on: T-200
  - verify:
    - command: `bash scripts/smoke-brownfield.sh`
    - assert: script exits 0; leaves workspace clean.

---

## Phase 5: US-3 — Portal shell foundation (P0)
> Story: US-3 — consistent admin frame after setup.
> Acceptance: AC-3.1 (shell with disabled nav + landing content), AC-3.2 (error toast with correlation id).
> Checkpoint: `pnpm -C frontend test shared && pnpm -C frontend test:e2e -- --grep portal-shell` + backend contract tests pass.

### 5a: Backend — `/api/admin/settings`

- [x] T-300 [L2] `GET /api/admin/settings` — returns envelope with `data = { runtime, db, plugins, system }` exactly as specified by `contracts/admin-api.md §v4.0`. Shape:
  - `data.runtime` is a FLAT primitive object with the 5 keys `log_client_request_body` (bool), `log_upstream_request_body` (bool), `log_upstream_response_body` (bool), `log_retention_days` (int), `log_level` (string). **No `{value, source}` wrapper** — 002 runtime keys are not env-overridable.
  - `data.db` is a primitive object `{driver, host, database_name}`. `driver` is the enum; `host` is `"<host>:<port>"` for postgres/mysql (parsed from DSN) or `"local"` for sqlite3; `database_name` is the parsed logical DB name (postgres/mysql) or the file basename (sqlite3). On DSN parse failure both `host` and `database_name` are `""`. **`db.url` is NEVER returned**.
  - `data.plugins[]` is an array of rows `{id, label, enabled}` — one per plugin in `plugin.Registry()` whose ID matches a wired `PluginsConfig` case. 002 returns `[]` because no concrete plugin ships.
  - `data.system` is `{router_version, router_git_sha, router_built_at}` — sourced from `internal/app/buildinfo`. **No `setup_state`, no `config_version`** — setup state lives on `/api/admin/health`. — `internal/api/adminapi/settings_get.go`, `internal/api/adminapi/settings_get_test.go`
  - depends_on: T-011, T-015, T-022, T-028
  - context_files:
    - `specs/002-.../contracts/admin-api.md` (§GET /api/admin/settings — v4.0 schema)
    - `specs/002-.../data-model.md` (§Page ↔ plugin dependency map)
    - `specs/002-.../research.md` (§Decision 10)
  - constraints: NEVER emit `data.db.url` (DSN — never logged, never returned); `data.runtime.*` values are raw primitives (no `source` sidecar in 002); NEVER return `setup_state` or `config_version` from this endpoint.
  - what: a single JSON projection that the Settings page consumes; the Sidebar and "Setup complete" landing do NOT consume this endpoint's `plugins[]` (they are client-hard-coded).
  - must_not: include an `error` field on success; include an HTTP status code in the body; include any `setup_state` / `config_version` fields; emit rows in `plugins[]` for unshipped/roadmap-only plugin IDs; invoke `config.Plugins.Enabled(id)` for IDs not present in the live `plugin.Registry()`.
  - verify:
    - command: `go test -run TestSettingsGet ./internal/api/adminapi/...`
    - assert:
      - happy_path (steady-state, 002 baseline): response is byte-exactly `{code:0, msg:"ok", data:{runtime:{log_client_request_body:false,log_upstream_request_body:false,log_upstream_response_body:false,log_retention_days:30,log_level:"info"}, db:{driver:"sqlite3",host:"local",database_name:"router.db"}, plugins:[], system:{router_version:"v0.2.0",router_git_sha:"abcdef1",router_built_at:"2026-04-17T08:00:00Z"}}}`. Use `jq` to compare the exact key set — unexpected extra keys fail the test.
      - happy_path (postgres DSN): `db = {driver:"postgres", host:"db.internal:5432", database_name:"router_prod"}` — parsed from `postgres://user:pass@db.internal:5432/router_prod`.
      - happy_path (DSN parse failure): `db.host = "" && db.database_name = ""` — driver still populated.
      - happy_path (forward-compat, 003 scenario): with a test-only `admin_auth` plugin registered in `plugin.Registry()` AND `config.Plugins.AdminAuth.Enabled=true`, `data.plugins = [{id:"admin_auth", label:"Admin auth", enabled:true}]`.
      - error_path: setup pending (gate catches — handler never invoked) → `code=2011` from the gate, not this handler.
      - boundary (warn-silence): capture the `slog` handler during a 002 baseline request → exactly **zero** "unknown plugin id" WARN records emitted (D3 obsoleted — there are no unshipped IDs being probed).
      - coverage: `db.url` never appears in any response across all sub-cases; `setup_state` and `config_version` never appear; `source` field never appears on any `runtime.*` or `db.*` or `plugins[]` row.

- [x] T-301 [L2] `POST /api/admin/settings/update` — partial update of (a) the 5 runtime keys (`log_client_request_body`, `log_upstream_request_body`, `log_upstream_response_body`, `log_retention_days`, `log_level`) and (b) the plugin-flag keys `plugins.admin_auth.enabled` + `plugins.client_keys.enabled` (D9). Other plugin IDs (e.g. `plugins.prometheus.*` in 002, before 005 lands the sub-struct) return `code=2012 unknown_config_key`. Non-boolean plugin-flag values return `code=2006 invalid_plugin_flag` (same code as setup-api.md commit). Rejects env-pinned fields with `code=2013 env_override_readonly` (dormant in 002 — no runtime keys are env-overridable, but the code path MUST exist for 003+ and for future env-overridable plugin-specific sub-keys). Rejects unknown keys with `code=2012 unknown_config_key`; rejects invalid `log_level` values with `code=2014 invalid_log_level`. Writes new `*Config` via `config.WriteAtomic` then publishes via `config.Publisher.Store`. Last-writer-wins (no optimistic concurrency). 16 KiB body cap. On success the response envelope carries the SAME `data` shape as `GET /api/admin/settings` so callers can refresh their cache from a single round-trip. — `internal/api/admin/settings_update.go`, `internal/api/admin/settings_update_test.go`
  - depends_on: T-011, T-015, T-016, T-017, T-300
  - context_files:
    - `specs/002-.../contracts/admin-api.md` (§POST /api/admin/settings/update)
    - `specs/002-.../spec.md` (US-4, FR-008, FR-011, FR-012)
    - `specs/002-.../plan.md` (§Risk R-3, R-7)
  - constraints: only `runtime.*` (5 keys) and `plugins.<id>.enabled` keys are patchable in 002; `db.*` is read-only here (changing DB is an install-level operation); body cap enforced BEFORE JSON parse; `log_level` must be validated against the enum `{debug,info,warn,error}` BEFORE the env-pinned check.
  - what: handler + integration test against an in-process router confirming file mtime advances and next `/v1/*` observes the new setting (for request-body logging) and the slog handler observes the new `log_level` on the next record.
  - must_not: accept `null` as a value (explicit remove is out of scope in 002); persist before publishing in memory (order: file → mem); return a `data` shape that differs from `GET`'s.
  - verify:
    - command: `go test -race -run TestSettingsUpdate ./internal/api/adminapi/...`
    - assert:
      - happy_path: `{runtime:{log_client_request_body:true}}` → code=0, response `data.runtime.log_client_request_body == true`; file rewritten; Reader.Load() returns new cfg; `data.runtime` has exactly the five runtime keys.
      - happy_path: `{runtime:{log_level:"debug"}}` → code=0, next slog record is emitted at DEBUG; response reflects the new level.
      - happy_path: `{runtime:{log_retention_days:14}}` → code=0, file rewritten.
      - happy_path (D9): `{plugins:{admin_auth:{enabled:true}}}` → code=0; config.json `plugins.admin_auth.enabled == true`; response `data.plugins` unchanged (still `[]` in 002 because no plugin binary is registered — the patch only records operator intent).
      - happy_path (D9): `{plugins:{client_keys:{enabled:false}}}` → code=0; file rewritten; identical forward-compat rationale as above.
      - error_path (R-3 dormant): craft a hypothetical env-override path by *temporarily* extending the in-test env-overridable list to include `log_retention_days`; POST `{runtime:{log_retention_days:90}}` while that env-override is active → code=2013, file untouched. This guards the future-compat contract.
      - error_path: `{runtime:{log_level:"trace"}}` → code=2014 (not in enum).
      - error_path: `{runtime:{unknown_key:true}}` → code=2012.
      - error_path: `{db:{url:"foo"}}` → code=2012 (db.* not patchable in 002 — wizard-only).
      - error_path (D9): `{plugins:{prometheus:{enabled:true}}}` → code=2012 (plugin ID not in 002's PluginsConfig; lands in 005).
      - error_path (D9): `{plugins:{admin_auth:{enabled:"yes"}}}` → code=2006 invalid_plugin_flag (string, not boolean).
      - boundary: 17 KiB body → code=2009.
      - boundary (R-7): last-writer-wins at the `config.WriteAtomic` layer covered by `internal/config/writer_concurrency_test.go` (with `-race`). Handler-level "10 concurrent POST /api/admin/settings/update with distinct `log_client_request_body` values" race harness is **deferred** — the file-write serialisation is the only shared state the handler mutates, so the writer-level test is a proper superset; a handler race test is tracked as a follow-up once 003 introduces admin auth and multi-operator timing is user-visible.
      - coverage: all 2006/2009/2012/2013/2014 codes reachable via test inputs; the returned `data` shape is byte-compatible with GET's shape (verified by diffing the key set).

- [x] T-302 [L1] Admin routes registration — wire `GET /api/admin/settings`, `POST /api/admin/settings/update`, and register the relocated 001 admin handlers (`GET /api/admin/health`, `GET /api/admin/accounts`, `GET /api/admin/requests`, `GET /api/admin/sessions/resolve`) under the `/api/admin/*` prefix. Legacy paths `/admin/accounts` etc. are NOT served; 001 callers must move to the new prefix (see 001 tech-design changelog). — `internal/api/adminapi/routes.go`
  - depends_on: T-300, T-301
  - verify:
    - command: `go build ./... && go vet ./internal/api/adminapi/...`
    - assert: `curl /api/admin/settings` (integration) returns an envelope-shaped body; `curl /admin/settings` returns the SPA index.html (or 302 → `/setup/` pre-setup), never the JSON API.

- [x] T-303 [L2] Envelope-wrap and relocate 001's admin endpoints — relocate `GET /admin/health`, `GET /admin/accounts`, `GET /admin/requests`, `GET /admin/sessions/resolve` to `/api/admin/health`, `/api/admin/accounts`, `/api/admin/requests`, `/api/admin/sessions/resolve` AND convert native error bodies to the `{code,msg,data}` envelope. Success codes use `code=0` (preferred); legacy alias `1000` is NOT emitted by the wrap (only preserved in the registry for any already-shipped 001 handler that bypassed the wrap). Business errors use the 001 code range already registered in `docs/error-codes.md` (`1001`–`1009`). System errors use `1900`–`1901` at HTTP 500. `/v1/*` is neither relocated nor wrapped; setup-done upstream transport is account-specific after Feature 003 (API-key rows use the Platform-compatible path, OAuth rows use ChatGPT Codex Responses paths). — `internal/api/adminapi/envelope_wrap.go`, `internal/api/adminapi/envelope_wrap_test.go`
  - depends_on: T-011, T-302
  - context_files:
    - `docs/error-codes.md` (§Feature 001)
    - `specs/001-codex-router-mvp/plan/tech-design.md` (§7 Error Handling + changelog entry on path relocation)
  - constraints: contract compatibility for existing 001 admin consumers is a known cost — (a) paths relocated (operators must update their clients), (b) body wrapped in envelope. The 002 release notes call both out. `/v1/*` handlers MUST NOT be relocated or wrapped. The wrap uses `code=0` for success; `1000` is legacy-alias only and MUST NOT be emitted by new code.
  - what: per-handler rewrite (preferred for clarity); every registered 001-admin error path mapped to a 1xxx code per the registry (zero ambiguity — the registry is authoritative on which codes exist).
  - must_not: emit envelope `code=1000` (`0` is the canonical success code); change HTTP status semantics for `/v1/*` errors; change any JSON key within an upstream response body; keep the legacy `/admin/health` etc. paths alive (route them to a 302 or 404 — explicitly documented in 001's changelog).
  - verify:
    - command: `go test -run TestEnvelopeWrap ./internal/api/adminapi/...`
    - assert:
      - happy_path: `GET /api/admin/health` returns `{code:0, msg:"ok", data:{status:"healthy", checks:{...}, setup_state:"done"}}` HTTP 200 — note `setup_state` lives on `/health`, NOT on `/settings`.
      - error_path: `GET /api/admin/accounts/999999` (missing) → `{code:1001, msg:"account_not_found", data:{}}` HTTP 200.
      - error_path: panic inside an admin handler → `{code:-1, msg:"unknown_error", data:{}}` HTTP 500 via T-012.
      - boundary (D1): `GET /v1/chat/completions` on a setup-mode server → **HTTP 503** with 001 native error body, NOT envelope — verified by asserting the raw body shape `{"error":{"type":"service_unavailable","code":"setup_required",...}}` and request id header-only. Once setup is done, provider responses are still not wrapped; API-key rows keep MVP Platform-compatible forwarding, and Feature 003 defines OAuth Responses traffic as ChatGPT Codex backend transport.
      - coverage: every business code registered in the 001 range (`1001`–`1009`) reachable; `/v1/*` path NOT routed through the wrapper (verified by a test that asserts raw body); no response emits `code=1000`.

### 5b: Frontend — shell + landing

- [x] T-310 [L1] `/admin/` landing card — "Setup complete." + link to ROADMAP and live navigation toward current operator surfaces, rendered inside `AppShell`. — `frontend/src/routes/admin/index.tsx`
  - depends_on: T-113
  - verify:
    - command: `pnpm -C frontend test admin-index`
    - assert: renders the card; clicking the roadmap link navigates to the known URL.

- [x] T-311 [US-3] [L2] SPA `/admin/settings` page — renders inside `AppShell`; calls `GET /api/admin/settings` + `POST /api/admin/settings/update` via TanStack Query mutation + RHF + Zod. Page layout (top → bottom):
    1. **Runtime panel** — 5 controls: `log_client_request_body` switch, `log_upstream_request_body` switch, `log_upstream_response_body` switch, `log_retention_days` number input `1..365`, `log_level` select `debug|info|warn|error`.
    2. **Plugin intents panel (D11)** — always rendered in 002, independent of whether `data.plugins[]` contains matching rows. Two switches that are hard-coded client-side in this page (**not** the sidebar's hard-coded list):
       - `Admin authentication` — two-way bound to `plugins.admin_auth.enabled`; badge text binds to `data.plugin_intents[].status` (current value: `"intent only"`); underneath a small grey caption `"Plugin not yet installed in this build — flipping the switch only records operator intent in config.json"` whenever no matching row exists in `data.plugins[]`.
       - `Client API keys` — same shape as above, bound to `plugins.client_keys.enabled`; current badge value also `"intent only"`.
       Both switches POST a partial patch like `{plugins:{admin_auth:{enabled:true}}}` and refresh local state from the response envelope. When `data.plugins[]` later gains a row for that plugin (003/004 shipped), the caption disappears and the switch drives live on/off behaviour — the same bound field, same POST shape, no code change needed.
    3. **Read-only DB info card** — `{driver, host, database_name}`; no edit controls (DB rotation is out of 002 scope).
    4. **System footer** — `router_version / router_git_sha / router_built_at`.
  — `frontend/src/routes/admin/settings.tsx`, `frontend/src/hooks/use-settings.ts`
  - depends_on: T-005, T-110, T-111, T-301
  - context_files:
    - `specs/002-.../spec.md` (US-4, FR-011)
    - `specs/002-.../contracts/admin-api.md` (§GET /api/admin/settings v4.0 + §POST /api/admin/settings/update v4.1 — plugin-flag patching)
    - `specs/002-.../plan.md` (§Decisions Round-4 — D9 + D11)
  - constraints: envelope-aware — `code!=0` surfaces via `ErrorBanner` with correlation id; on success, refresh the local form state from the POST's response `data` (do NOT re-issue a GET — POST already returns the full 5-runtime-key envelope); in 002 NO field in `runtime.*` is disabled-by-env, but the dormant "pinned by env" badge component MUST exist in the codebase for 003+ reuse and is exercised via a Storybook story. The 2 plugin-intent switches' IDs are hard-coded in this page's component, but the badge text binds to `data.plugin_intents[].status` so future shell-copy changes stay server-driven.
  - what: a page that implements US-4 end-to-end from the UI side for the 5 runtime keys + the 2 plugin-intent toggles + read-only DB/system display.
  - must_not: render a `db.url` field (it is never in the envelope); render `setup_state` or `config_version` (not in the envelope); make an extra GET after a successful POST; hide the plugin-intent panel when `data.plugins[]` is empty (that was the pre-D11 behaviour — the panel is now always visible, only the per-row caption conditionally renders); show a plugin-intent switch for any ID other than `admin_auth` and `client_keys` in 002 (prometheus ships in 005 with its own UI addition).
  - verify:
    - command: `pnpm -C frontend test settings`
    - assert:
      - happy_path: toggle `log_client_request_body` → POST fires with `{runtime:{log_client_request_body:true}}`; UI reflects new value from the POST response within one render cycle; NO follow-up GET issued.
      - happy_path: change `log_level` to "debug" → POST fires, success toast shown.
      - happy_path: DB info card renders `driver=sqlite3, host=local, database_name=router.db` in read-only text (no input controls).
      - happy_path: system footer renders `v0.2.0 · abcdef1 · 2026-04-17T08:00:00Z`.
      - happy_path (D11): in 002 with `data.plugins == []`, the Plugin intents panel renders BOTH switches (Admin auth + Client API keys); both show the grey "plugin not yet installed" caption; clicking the `Admin authentication` switch fires `POST /api/admin/settings/update` with body `{plugins:{admin_auth:{enabled:true}}}` and the switch's visual state flips on success.
      - happy_path (D11 forward-compat): mock `data.plugins=[{id:"admin_auth",label:"Admin authentication",enabled:true}]`; Admin-auth switch renders with ON state, NO grey caption; the `intent only` badge is replaced by a small green "live" indicator.
      - error_path: server returns code=2014 on invalid log level → inline validation message under the `log_level` select.
      - error_path: server returns code=2013 on a hypothetical env-pinned key → inline "managed by env" message under the affected field (dormant in 002 but component exists).
      - error_path (D11): server returns `code=2006 invalid_plugin_flag` (e.g. race where server rejects a racing concurrent non-boolean write) → inline error under the plugin-intent switch.
      - boundary: no runtime control shows a "pinned by env" badge in 002 (the `runtime.*` keys are all file-only).
      - boundary (D11): the Plugin intents panel always renders exactly 2 switches in 002 (Admin auth + Client API keys) — Prometheus / other plugin IDs are NOT rendered here; observability ships in 005 with its own panel edit.

- [x] T-312 [L2] Playwright E2E — portal shell after setup. — `frontend/tests/e2e/portal-shell.spec.ts`
  - depends_on: T-114, T-310, T-311
  - verify:
    - command: `pnpm -C frontend test:e2e -- --grep portal-shell`
    - assert:
      - happy_path (AC-3.1): after completing setup, `/admin/` shows AppShell + sidebar with live `Dashboard`, `Accounts`, and `Settings` entries plus 3 disabled planned entries (`Requests`, `Client Keys`, `Observability`) and the "Setup complete" card.
      - happy_path (AC-3.2): simulate server 500 on `/api/admin/settings` → toast appears with correlation id.
      - coverage: US-3 AC-1 + AC-2 exercised.

---

## Phase 6: US-4 + US-5 — Live settings edit + defaults (P1)
> Stories: US-4 (live settings edit), US-5 (sensible defaults zero-friction).
> Acceptance: AC-4.1 (toggle persists + hot-reloads), AC-4.2 (env-pinned rejected with explanation), AC-5.1 (defaults pre-filled), AC-5.2 (sub-10s happy path).
> Checkpoint: P1 story integration tests pass; no regression in P0 test suite.

- [x] T-400 [US-4] [L2] Integration test — POST `/api/admin/settings/update {runtime:{log_client_request_body:true}}` then `POST /v1/chat/completions` → assert the caller body logged in `request_records.client_request_body`; then POST `{runtime:{log_upstream_request_body:true}}` → assert the provider request logged in `request_records.upstream_request_body`; then POST `{runtime:{log_upstream_response_body:true}}` → assert the provider response logged in `request_records.upstream_response_body`; then POST `{runtime:{log_level:"debug"}}` → assert a DEBUG slog record is emitted on the next request; then POST `{runtime:{log_retention_days:7}}` → assert the record-retention job reads the new window on next tick. — `internal/api/adminapi/hot_reload_integration_test.go`
  - depends_on: T-017, T-301, T-303
  - context_files:
    - `specs/002-.../spec.md` (US-4 AC-1, FR-011)
    - `specs/002-.../contracts/admin-api.md` (§runtime keys)
  - constraints: in-process server; real SQLite; all 5 runtime flags observed via `config.Reader.Load()` in the request path / slog handler / retention goroutine (not a retained pointer).
  - what: a test that proves hot-reload actually reaches `/v1/*` handlers and the slog + retention subsystems, for every hot-reloadable runtime key.
  - verify:
    - command: `go test -race -run TestHotReload ./internal/api/adminapi/...`
    - assert:
      - happy_path: toggle `log_client_request_body` on → next proxy request logs request body; toggle off → next request does NOT log it.
      - happy_path: toggle `log_upstream_response_body` on → next proxy request logs response body; toggle off → does NOT.
      - happy_path: `log_level` "debug" → DEBUG record observable; set back to "info" → DEBUG records filtered out.
      - happy_path: `log_retention_days` from 30 → 7 → retention job's next tick deletes records older than 7 days.
      - boundary (R-7): 100 concurrent reads of `Reader.Load()` alongside 10 updates across all 4 keys → no torn reads; all readers see a consistent snapshot (either fully old or fully new on every field).

- [x] T-401 [US-5] [L1] Default-fill coverage test — walk every defaulted field in `Config` and assert it has a sensible default (`log_client_request_body=false`, `log_upstream_request_body=false`, `log_upstream_response_body=false`, `log_retention_days=30`, `log_level="info"`, plugins all disabled, driver unset until wizard fills it). — `internal/config/defaults_test.go`
  - depends_on: T-013
  - verify:
    - command: `go test -run TestDefaults ./internal/config/...`
    - assert: every optional field has a default; no field defaults to a "clearly unsafe" value (e.g., `log_client_request_body=true`, `log_upstream_request_body=true`, or `log_upstream_response_body=true`); `log_level` defaults to `"info"` (not debug).

- [x] T-402 [US-5] [L2] Wizard default-pre-fill test — Playwright E2E that asserts on a fresh install the DSN step is pre-selected `sqlite3` with DSN `router.db`, the plugin toggles are all OFF, and the provider select defaults to `openai`. — `frontend/tests/e2e/wizard-defaults.spec.ts`
  - depends_on: T-112
  - verify:
    - command: `pnpm -C frontend test:e2e -- --grep wizard-defaults`
    - assert:
      - happy_path (AC-5.1): all five runtime defaults match.
      - happy_path (AC-5.2): accepting all defaults and filling only the required upstream key → commit completes in <10s (P95 on local SQLite).

---

## Phase 7: Polish
> Checkpoint: full `make test` + `make lint` + `make build` green; all US acceptance criteria covered; quickstart paths A/B/C work end-to-end by hand.

- [x] T-500 [P] [L1] README + docs updates — landing section explains single-binary 002 bring-up (`pnpm -C frontend build && go run ./cmd/one-llm-router` → open `:8080`). — `README.md`
  - verify:
    - command: `grep -q 'pnpm -C frontend build' README.md`
    - assert: command appears; no reference to the old MVP-only flow remains.

- [x] T-501 [P] [L1] `docs/standards/toolchain.md` — verify 002 lands the canonical frontend toolchain; remove any "deferred" markers on the React stack. — `docs/standards/toolchain.md`
  - verify:
    - command: `grep -Ei 'deferred' docs/standards/toolchain.md`
    - assert: zero matches.

- [x] T-503 [P] [L1] Verify `specs/sdd/state.json` is in `TASKS_READY` — on the commit that lands tasks.md, `features["002-…"].phase` MUST already equal `"TASKS_READY"` (set by `sdd-tasks`). This task is a no-op verify-only item: it exists so the dependency graph and the traceability matrix carry the evidence that the phase is correct before implementation starts. The subsequent transition to `"IMPLEMENTING"` is owned by `sdd-implement` and happens on the first task it picks up. — `specs/sdd/state.json`
  - verify:
    - command: `python3 -c "import json;d=json.load(open('specs/sdd/state.json'));print(d['features']['002-setup-wizard-and-admin-portal-skeleton']['phase'])"`
    - assert: prints `TASKS_READY` (set by this tasks.md commit; sdd-implement will later flip to `IMPLEMENTING`).

- [x] T-504 [P] [L1] ADR-style short note — `docs/decisions/002-envelope-and-rpc-verb.md` summarising architectural decisions #8 and #9 for future readers. — `docs/decisions/002-envelope-and-rpc-verb.md`
  - context_files:
    - `ROADMAP.md` (decisions #8, #9)
    - `specs/002-.../research.md` (§Decision 8, §Decision 9)
  - verify:
    - command: `ls docs/decisions/002-envelope-and-rpc-verb.md`
    - assert: file present; contains both envelope policy and RPC-verb convention.

---

## Dependencies

```mermaid
graph TD
  subgraph Phase1_Setup
    T001[T-001 go.mod]
    T002[T-002 frontend scaffold]
    T003[T-003 gitignore]
    T004[T-004 tailwind tokens]
    T005[T-005 shadcn]
    T006[T-006 Makefile]
  end

  subgraph Phase2_Foundation
    T010[T-010 errcode]
    T011[T-011 envelope]
    T012[T-012 recover]
    T013[T-013 config types]
    T014[T-014 Enabled]
    T015[T-015 loader]
    T016[T-016 writer]
    T017[T-017 live]
    T018[T-018 buildinfo]
    T019[T-019 migrator]
    T020[T-020 migrate CLI]
    T021[T-021 plugin IF]
    T022[T-022 registry]
    T023[T-023 state reader]
    T024[T-024 gate]
    T025[T-025 brownfield]
    T026[T-026 probe]
    T028[T-028 BuildApp]
    T029[T-029 main]
    T030[T-030 boot test]
  end

  T001 --> T013
  T002 --> T004 --> T005 --> T110
  T002 --> T006
  T010 --> T011 --> T012
  T013 --> T014 --> T028
  T013 --> T015 --> T028
  T013 --> T016 --> T028
  T013 --> T017 --> T028
  T013 --> T018
  T001 --> T019 --> T020 --> T029
  T019 --> T028
  T021 --> T022 --> T028
  T011 --> T023 --> T024
  T015 --> T025 --> T028
  T016 --> T025
  T019 --> T025
  T013 --> T026
  T028 --> T029
  T028 --> T030
  T011 --> T024
  T011 --> T300
  T011 --> T301
  T011 --> T303

  subgraph Phase3_US1
    T100[T-100 validator]
    T101[T-101 commit]
    T102[T-102 probe handler]
    T103[T-103 status handler]
    T104[T-104 commit handler]
    T105[T-105 routes]
    T110[T-110 api-client]
    T111[T-111 errcode.ts]
    T112[T-112 wizard]
    T113[T-113 shell]
    T114[T-114 E2E happy path]
  end

  T010 --> T100
  T013 --> T100
  T100 --> T101
  T011 --> T101
  T015 --> T101
  T016 --> T101
  T017 --> T101
  T019 --> T101
  T026 --> T102
  T011 --> T102
  T023 --> T103
  T101 --> T104
  T102 --> T105
  T103 --> T105
  T104 --> T105
  T110 --> T111
  T110 --> T112
  T111 --> T112
  T105 --> T114
  T112 --> T114
  T113 --> T114

  subgraph Phase4_US2
    T200[T-200 brownfield E2E]
    T201[T-201 smoke script]
  end
  T025 --> T200
  T028 --> T200
  T200 --> T201

  subgraph Phase5_US3
    T300[T-300 settings GET]
    T301[T-301 settings update]
    T302[T-302 admin routes]
    T303[T-303 envelope wrap 001]
    T310[T-310 landing]
    T311[T-311 settings page]
    T312[T-312 shell E2E]
  end
  T300 --> T301 --> T302
  T302 --> T303
  T113 --> T310
  T301 --> T311
  T310 --> T312
  T311 --> T312

  subgraph Phase6_P1
    T400[T-400 hot reload IT]
    T401[T-401 defaults]
    T402[T-402 defaults E2E]
  end
  T301 --> T400
  T013 --> T401
  T112 --> T402

  subgraph Phase7_Polish
    T500[T-500 README]
    T501[T-501 toolchain]
    T503[T-503 state.json]
    T504[T-504 ADR]
  end
```

---

## Traceability Matrix

| Spec Item | Type | Task IDs | Coverage |
|---|---|---|---|
| US-1 First-install walkthrough | P0 User Story | T-023, T-024, T-026, T-100, T-101, T-102, T-103, T-104, T-105, T-112, T-114 | ✅ Full |
| US-1 AC-1 (redirect on first visit) | AC | T-024, T-114 | ✅ Full |
| US-1 AC-2 (5-step happy path) | AC | T-100, T-101, T-102, T-103, T-112, T-114 | ✅ Full |
| US-1 AC-3 (unreachable DSN) | AC | T-026, T-102, T-112 | ✅ Full |
| US-1 Edge-1 (tab closed mid-wizard) | EC | T-112 | ✅ Full |
| US-1 Edge-2 (concurrent commit) | EC | T-101 (5-goroutine test) | ✅ Full |
| US-1 Edge-3 (bad upstream key) | EC | T-101, T-311 (landing card note) | ⚠️ Partial (UI banner is landing card copy only) |
| US-2 Upgrade without re-setup | P0 User Story | T-025, T-028, T-030, T-200, T-201 | ✅ Full |
| US-2 AC-1 (env + accounts serves immediately) | AC | T-025, T-200 | ✅ Full |
| US-2 AC-2 (auto-mark complete) | AC | T-025, T-200 | ✅ Full |
| US-2 Edge-1 (empty env DB) | EC | T-025, T-200 | ✅ Full |
| US-2 Edge-2 (very-old MVP schema) | EC | T-019, T-030 | ✅ Full |
| US-3 Portal shell foundation | P0 User Story | T-113, T-300, T-303, T-310, T-311, T-312 | ✅ Full |
| US-3 AC-1 (shell + disabled nav) | AC | T-113, T-300, T-310, T-312 | ✅ Full |
| US-3 AC-2 (error toast with correlation id) | AC | T-110, T-113, T-312 | ✅ Full |
| US-3 Edge-1 (no ES modules) | EC | T-113 (static fallback) | ⚠️ Partial — requires explicit fallback HTML entry |
| US-3 Edge-2 (JS unhandled error) | EC | T-113 (ErrorBanner) | ✅ Full |
| US-4 Live settings edit (P1) | User Story | T-301, T-311, T-400 | ✅ Full |
| US-4 AC-1 (toggle persists + hot-reload) | AC | T-301, T-400 | ✅ Full |
| US-4 AC-2 (env-pinned rejected) | AC | T-301, T-311 | ✅ Full |
| US-4 Edge-1 (concurrent save) | EC | T-301 (writer-level last-writer-wins via `internal/config/writer_concurrency_test.go`; handler-level race harness deferred — see T-301 verify note) | ⚠️ Partial |
| US-4 Edge-2 (disk write fails after mem) | EC | T-301 (file→mem ordering) | ✅ Full |
| US-4 Edge-3 (critical fields read-only) | EC | T-301 (db.* not patchable), T-311 | ✅ Full |
| US-5 Sensible defaults (P1) | User Story | T-401, T-402 | ✅ Full |
| US-5 AC-1 (defaults pre-filled) | AC | T-401, T-402 | ✅ Full |
| US-5 AC-2 (<10s happy path) | AC | T-101, T-402 | ✅ Full |
| US-5 Edge-1 (SQLite path unwritable) | EC | T-101 (commit error path) | ✅ Full |
| FR-001 first-run detection | FR | T-015, T-023, T-024 | ✅ Full |
| FR-002 auto-mark-complete | FR | T-025, T-028 | ✅ Full |
| FR-003 wizard 5 steps | FR | T-112 | ✅ Full |
| FR-004 DSN validation | FR | T-026, T-102 | ✅ Full |
| FR-005 atomic commit | FR | T-101 (fault matrix) | ✅ Full |
| FR-006 refuse /v1/* until done | FR | T-024 | ✅ Full |
| FR-007 delete config.json MUST NOT re-enable | FR | T-025, T-200 | ✅ Full |
| FR-008 env precedence visible | FR | T-015, T-300 | ⏸️ Deferred to 003+ (no 002 runtime fields are env-overridable; server-side source map still built for `db.*` — see plan.md Round-3) |
| FR-009 consistent portal shell | FR | T-113, T-300, T-310 | ✅ Full |
| FR-010 correlation id surfacing | FR | T-011, T-110, T-113 | ✅ Full |
| FR-011 hot-reload selected fields | FR | T-017, T-301, T-400 | ✅ Full (5 keys: `log_client_request_body`, `log_upstream_request_body`, `log_upstream_response_body`, `log_retention_days`, `log_level`) |
| FR-012 reject env-pinned edits | FR | T-301 | ✅ Full (server-side 2013 enforcement present; dormant in 002 — exercised via a test-only overridable-key extension in T-301) |
| FR-013 single port | FR | T-029 (no new server spawn) | ✅ Full |
| FR-014 MVP-parity default | FR | T-013, T-401 | ✅ Full |
| NFR Performance wizard <500ms | NFR | T-102, T-103, T-104 | ✅ Full |
| NFR Performance Commit <10s | NFR | T-101, T-402 | ✅ Full |
| NFR Reliability atomic commit | NFR | T-016, T-101 (fault matrix) | ✅ Full |
| NFR Security 0600 perms | NFR | T-016, T-025, T-200 | ✅ Full |
| NFR Observability correlation id on setup | NFR | T-011, T-104 | ✅ Full |
| NFR Usability 2-min happy path | NFR | T-402 | ✅ Full |
| SC-1 fresh operator < 2 min | SC | T-402 | ✅ Full |
| SC-2 MVP zero-downtime upgrade | SC | T-025, T-200, T-201 | ✅ Full |
| SC-3 setup atomic or re-runnable | SC | T-101 (fault matrix) | ✅ Full |
| SC-4 settings hot-reload | SC | T-017, T-301, T-400 | ✅ Full |
| SC-5 shell reusable by 003+ | SC | T-113, T-300 | ✅ Full |
| R-1 commit partial success | Risk | T-101, T-025 | ✅ Full |
| R-3 env-pinned ignored | Risk | T-301 | ✅ Full |
| R-6 concurrent commit | Risk | T-101 | ✅ Full |
| R-7 hot-reload race | Risk | T-017, T-301 (under -race), T-400 | ✅ Full |
| R-9 dirty-migration boot | Risk | T-019, T-030 | ✅ Full |

**Edge-case gaps flagged**:
- **US-1 Edge-3 (bad upstream key)**: relies on a static landing-card note rather than a runtime-derived "no healthy account" banner. Acceptable for 002 (upstream health is 005's job) but called out here so 005 can close the loop.
- **US-3 Edge-1 (no ES modules)**: the fallback HTML is part of T-113 but does not have a dedicated test. Could be split into a Phase-7 polish task if we want explicit coverage.

---

## Statistics

| Metric | Value |
|---|---|
| Total Tasks | 46 |
| Phase 1 (Setup) | 6 |
| Phase 2 (Foundation) | 20 (T-027 removed in Round-3) |
| Phase 3 (US-1) | 11 |
| Phase 4 (US-2) | 2 |
| Phase 5 (US-3) | 7 |
| Phase 6 (P1 US-4 + US-5) | 3 |
| Phase 7 (Polish) | 4 (T-502 dropped — impeccable CI gate deferred post-002) |
| L1 Tasks | 32 (70%) |
| L2 Tasks | 13 (28%) |
| L3 Tasks | 1 (2%) |
| Parallel Tasks | 13 (28%) |
| Estimated AI Sessions | 45 (L1 + L2) |
| Human Tasks | 1 (L3 = T-028 [HUMAN] marker on BuildApp side-effect order) |

### Distribution Health
- L1 ≥ 70%: ✅ (70%)
- L2 ≤ 20%: ⚠️ (28%) — slightly over budget but reflects the density of fault-injection + contract tests; acceptable given SC-3 demands fault-matrix coverage.
- L3 ≤ 10%: ✅ (2%)

### Coverage
- P0 AC Coverage: 10/10 (100%)
- P1 AC Coverage: 4/4 (100%)
- Edge Case Coverage: 9/9 (100%, with two flagged as partial)
- NFR Coverage: 6/6 (100%)
- FR Coverage: 14/14 (100%)

---

## Execution Strategies

### MVP (fastest to working wizard)
Phase 1 → Phase 2 → Phase 3 (backend US-1 only: T-100..T-105) → Phase 3 (frontend US-1: T-110..T-114) → validate end-to-end via `quickstart.md` Path A. This ships the wizard + setup-gate without the admin portal work, which lets real operators exercise the bring-up ahead of the portal polish.

### Incremental (balanced, recommended)
Phase 1 → Phase 2 → Phase 3 → Phase 4 → Phase 5 → Phase 6 → Phase 7. Each P0 Phase independently passes its checkpoint before the next one starts; the brownfield path (Phase 4) depends on Phase 2 only and can run in parallel with Phase 3 if session count allows.

### Full Parallel (fastest total, needs multiple sessions)
Phase 1 → Phase 2 → **[Phase 3 + Phase 4 in parallel]** → Phase 5 → **[Phase 6 + Phase 7 in parallel]**. Phase 4 (T-200, T-201) is fully independent of Phase 3 and only needs Phase 2 foundations; the two can execute concurrently.

---

## Next Step

To start implementation, use **sdd-implement**:

> "Use sdd-implement for 002-setup-wizard-and-admin-portal-skeleton"

Recommended first command for the implementer:

```bash
pnpm -C frontend install --frozen-lockfile && go build ./... && golangci-lint run
```

If either fails before a single line of new code is written, Phase 1 has a mis-scaffold bug to fix first.
