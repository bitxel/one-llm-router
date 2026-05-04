# Quickstart — Feature 002

**Feature**: 002-setup-wizard-and-admin-portal-skeleton
**Audience**: a developer sitting down to implement 002, or a reviewer who wants to run the finished feature end-to-end in 5 minutes.

This is not a tutorial for users. It is the shortest path from an empty checkout to a green dashboard, plus the smoke checks the implementer should pass before calling any task `done`.

---

## Prerequisites

- **Go 1.25** (`go version`; matches `go.mod` directive `go 1.25.0`; Go 1.25+ toolchain required because `modernc.org/sqlite@v1.48.2` depends on it)
- **Node 22 LTS** (`node --version`; pinned in `.node-version` or `.nvmrc`)
- **pnpm 9** (`pnpm --version`; `corepack enable` or `npm i -g pnpm@9`)
- A working checkout at `~/app/project/one-llm-router`
- One of: SQLite (bundled, default), local PostgreSQL 16, or local MySQL 8 — only needed if you want to exercise a non-sqlite path
- A modern browser (Chromium/Firefox/Safari)

The frontend builds to `frontend/dist/` and is embedded into the Go binary via `go:embed`. For a production-like smoke, run `pnpm -C frontend build` **before** `go run ./cmd/one-llm-router`. For dev iteration, run Vite (`pnpm -C frontend dev` on :5173) alongside Go (`:8080`) and Vite's proxy forwards API calls.

### First-time setup

```bash
cd ~/app/project/one-llm-router/frontend
pnpm install --frozen-lockfile
cd ..
```

---

## Path A — Greenfield (US-1 happy path)

**Goal**: prove that a fresh build with no config walks an operator through the wizard and ends on the dashboard.

```bash
cd ~/app/project/one-llm-router

# 0. Make sure no prior state leaks in
rm -f router.db config.json config.json.tmp.*
unset ROUTER_DB_DRIVER ROUTER_DB_URL ROUTER_BODY_LOGGING ROUTER_RETENTION_DAYS ROUTER_CONFIG_PATH

# 1. Build the frontend bundle (prod-mode embed target)
pnpm -C frontend build
# → produces frontend/dist/{index.html,assets/*.{js,css}}; Go go:embed picks these up on the next build

# 2. Build and start. Listen on the default port (8080).
go run ./cmd/one-llm-router
# expected log: "router started in setup mode; open http://localhost:8080/setup/ to continue"
```

In a second terminal:

```bash
# 2. Setup status should be "pending"  (all router-owned JSON responses use {code, msg, data})
curl -s http://localhost:8080/api/setup/status | jq
# → {"code":0,"msg":"ok","data":{"state":"pending","supported_drivers":["sqlite3","postgres","mysql"], ...}}

# 3. /v1/* must be blocked while in setup mode. /v1/* is PERMANENTLY excluded from
#    the envelope (see AGENTS.md §HTTP API Style); the setup gate emits 001's native
#    MVP error shape with HTTP 503.
curl -si http://localhost:8080/v1/chat/completions | head -1
# → HTTP/1.1 503 Service Unavailable
curl -s http://localhost:8080/v1/chat/completions | jq '.error.code, .error.message'
# → "setup_required"
#   "router is in setup mode; complete /setup/ first"

# 4. Probe a (valid) sqlite DSN
curl -s -X POST http://localhost:8080/api/setup/probe-dsn \
  -H 'content-type: application/json' \
  -d '{"db":{"driver":"sqlite3","url":"router.db"}}' | jq
# → {"code":0,"msg":"ok","data":{"ok":true,"latency_ms":<small>,"server_version":"sqlite 3.x"}}

# 5. Commit
curl -s -X POST http://localhost:8080/api/setup/commit \
  -H 'content-type: application/json' \
  -d '{
    "db":            {"driver":"sqlite3","url":"router.db"},
    "first_account": {"name":"shared-prod-01","provider":"openai","api_key":"sk-TEST","base_url":null},
    "plugins":       {"admin_auth":{"enabled":false},"client_keys":{"enabled":false}},
    "runtime":       {"log_client_request_body":false,"log_upstream_request_body":false,"log_upstream_response_body":false,"log_retention_days":30,"log_level":"info"}
  }' | jq
# → {"code":0,"msg":"ok","data":{"redirect":"/admin/"}}

# 6. config.json now exists with mode 0600
ls -l config.json
# → -rw------- ... 1 ... config.json
jq '.version, .db.driver, .plugins' config.json
# → 1
#   "sqlite3"
#   {"admin_auth":{"enabled":false},"client_keys":{"enabled":false}}

# 7. Portal shell lit up — settings endpoint reports the expected 4-block shape.
#    Note the split: the SPA page lives at /admin/settings (HTML), the JSON API
#    lives at /api/admin/settings. Browsers hitting /admin/settings get the SPA
#    and then fetch /api/admin/settings from inside it.
#    Setup-state lives on the always-reachable health endpoint, NOT on settings
#    (which is setup-gated).
curl -s http://localhost:8080/api/admin/health | jq '.code, .data.setup_state'
# → 0
#   "done"
curl -s http://localhost:8080/api/admin/settings | jq '{code, runtime:.data.runtime, db:.data.db, plugins:.data.plugins, system:.data.system}'
# → {
#     "code": 0,
#     "runtime": {"log_client_request_body":false,"log_upstream_request_body":false,"log_upstream_response_body":false,"log_retention_days":30,"log_level":"info"},
#     "db":      {"driver":"sqlite3","host":"local","database_name":"router.db"},
#     "plugins": [],
#     "system":  {"router_version":"v0.2.0","router_git_sha":"abcdef1","router_built_at":"2026-04-17T…Z"}
#   }

# 8. /v1/* now routes (will 401 from OpenAI because the key is fake; that's fine).
#    Note: /v1/* bodies are NOT wrapped in the envelope. In this 002 API-key
#    smoke path they are OpenAI Platform-compatible pass-through; Feature 003
#    adds the OAuth ChatGPT Codex backend transport for Responses traffic.
curl -si http://localhost:8080/v1/models | head -1
# → HTTP/1.1 200 OK   (or 401 from upstream — either proves routing works)
```

Browser walkthrough to confirm the visual side:
1. Open `http://localhost:8080/`. You should be redirected to `/setup/`.
2. Step through the wizard. The page should look and feel like `specs/002-.../mocks/v9-neoretro-grafana.html` — same layout, typography, accent, theming. The React SPA renders the same design using shadcn/ui + Tailwind v4; byte-for-byte identity with the HTML mock is not a requirement. Playwright screenshot + a local `impeccable detect --fast` run against the rendered SPA is the objective spot-check (automated CI gate de-scoped with T-502).
3. On confirm, you should land on `/admin/` showing the portal shell (top bar, left sidebar with `Dashboard`, `Accounts`, `Playground`, `Requests`, and `Settings` live, plus disabled `planned` entries for `Client Keys` and `Observability` — the nav labels are **hard-coded client-side** in `frontend/src/components/shared/Sidebar.tsx`, so they render identically regardless of what `GET /api/admin/settings` returns for `data.plugins[]`; in 002 that array is `[]`) and a "Setup complete" landing card that points operators at the live Accounts surface. The KPI dashboard visible in `v9-dashboard.html` is intentionally not shipped in 002 — it is the target design for 005.
4. Click `Settings` in the sidebar. The page renders five runtime controls (`log_client_request_body`, `log_upstream_request_body`, `log_upstream_response_body`, `log_retention_days`, `log_level`), a **Plugin intents** panel with two switches whose badge text comes from `data.plugin_intents[].status` (current value: `intent only` while no matching plugin binary is installed), a read-only DB info card, and a system footer. Flipping a plugin-intent switch writes to `config.json.plugins.<id>.enabled` via `POST /api/admin/settings/update`; a grey caption under each switch notes that the flag only records operator intent until the matching plugin is installed.

---

## Path B — Brownfield upgrade (US-2)

**Goal**: prove that an existing MVP deployment is not forced through the wizard.

```bash
cd ~/app/project/one-llm-router

# 0. Pretend we are running 001 already: env-configured, DB already has an account, no config file
rm -f config.json
export ROUTER_DB_DRIVER=sqlite3
export ROUTER_DB_URL=router.db
# (the router.db from Path A still has the account; if not, re-run Path A or
#  insert one manually before starting)

# 1. Start 002
go run ./cmd/one-llm-router

# 2. /v1/* should serve on the first request — no wizard
curl -si http://localhost:8080/v1/models | head -1
# → HTTP/1.1 200 OK  (or upstream error)

# 3. Setup status should be "done" without operator action
curl -s http://localhost:8080/api/setup/status | jq '.code, .data.state'
# → 0
#   "done"

# 4. `config.json` was auto-materialized from env + DB probe on boot
ls -l config.json
# → -rw------- 1 user group … config.json
jq '.db.driver, .db.url, .version' config.json
# → "sqlite3"
#   "router.db"
#   1
```

On brownfield upgrade boot, the router sees `config.json` missing but `ROUTER_DB_DRIVER` + `ROUTER_DB_URL` set **and** the DB's `upstream_accounts` table already populated, so it **synthesizes a fresh `config.json`** from env + defaults (plan.md T-2 / data-model.md §Brownfield auto-materialization). This delivers FR-002 — the 001-era operator's install shows as "complete" on first 002 boot with zero action — and restores the single source of truth (config.json presence ↔ setup done). The Settings page then shows the 5 runtime defaults (`log_client_request_body=false`, `log_upstream_request_body=false`, `log_upstream_response_body=false`, `log_retention_days=30`, `log_level="info"`) and the `db` block derived from the env-sourced DSN (for sqlite3: `host="local"`, `database_name=<basename>`). 002 does not render "pinned by env" badges anywhere in the UI — none of the 5 runtime keys is env-overridable (see research.md §Decision 10).

---

## Path C — Live settings edit (US-4)

Assumes Path A completed. The settings-update endpoint is `POST /api/admin/settings/update` (RPC-style); there is no `PATCH /api/admin/settings`.

```bash
# Toggle client/upstream request and upstream response body logging and tighten log level
curl -s -X POST http://localhost:8080/api/admin/settings/update \
  -H 'content-type: application/json' \
  -d '{"runtime":{"log_client_request_body":true,"log_upstream_request_body":true,"log_upstream_response_body":true,"log_level":"debug"}}' \
  | jq '{code, runtime:.data.runtime}'
# → {
#     "code": 0,
#     "runtime": {"log_client_request_body":true,"log_upstream_request_body":true,"log_upstream_response_body":true,"log_retention_days":30,"log_level":"debug"}
#   }

# Verify config.json WAS touched (atomic rewrite via tmp + rename, 0600)
stat -f '%m' config.json  # mtime strictly greater than pre-update mtime
jq '.runtime, .updated_at' config.json
# → {"log_client_request_body":true,"log_upstream_request_body":true,"log_upstream_response_body":true,"log_retention_days":30,"log_level":"debug"}
#   "2026-04-17T…"   (refreshed)

# Unknown keys are rejected with code=2012 unknown_config_key — wizard-only fields
# cannot be updated via this endpoint.
curl -s -X POST http://localhost:8080/api/admin/settings/update \
  -H 'content-type: application/json' \
  -d '{"db":{"driver":"postgres"}}' | jq '.code, .msg'
# → 2012
#   "config key `db.driver` is not patchable in 002"

# Out-of-range values are rejected with the field-specific code.
curl -s -X POST http://localhost:8080/api/admin/settings/update \
  -H 'content-type: application/json' \
  -d '{"runtime":{"log_retention_days":9999}}' | jq '.code, .msg'
# → 2007
#   "log_retention_days must be an integer in [1, 365]"

curl -s -X POST http://localhost:8080/api/admin/settings/update \
  -H 'content-type: application/json' \
  -d '{"runtime":{"log_level":"TRACE"}}' | jq '.code, .msg'
# → 2014
#   "log_level must be one of debug, info, warn, error"

# Note: the env_override_readonly path (code=2013) is unreachable in 002 because
# no 002 runtime key is env-overridable. Contract test TestUpdateEnvPinnedReturnsEnvOverrideReadonly
# exercises the machinery via a synthetic test-only fixture.

# D9 — plugin-flag patching. Pre-stage operator intent before 003 admin-auth ships.
# The Settings page renders a "Plugin intents" panel (D11) that wires the same
# two POSTs to Admin-auth / Client-API-keys switches; curl here matches the UI.
curl -s -X POST http://localhost:8080/api/admin/settings/update \
  -H 'content-type: application/json' \
  -d '{"plugins":{"admin_auth":{"enabled":true}}}' | jq '.code, .data.plugins'
# → 0
#   []     (data.plugins stays empty in 002 — no plugin binary registered yet;
#           the write lands in config.json and 003 reads it when admin_auth ships)
jq '.plugins' config.json
# → {"admin_auth":{"enabled":true},"client_keys":{"enabled":false}}

# Unknown plugin IDs are rejected because 002's PluginsConfig has no sub-struct
# for them yet.
curl -s -X POST http://localhost:8080/api/admin/settings/update \
  -H 'content-type: application/json' \
  -d '{"plugins":{"prometheus":{"enabled":true}}}' | jq '.code, .msg'
# → 2012
#   "config key `plugins.prometheus.enabled` is not patchable in 002"

# Non-boolean plugin-flag values are rejected with 2006 (same code setup commit
# uses for the same class of error).
curl -s -X POST http://localhost:8080/api/admin/settings/update \
  -H 'content-type: application/json' \
  -d '{"plugins":{"admin_auth":{"enabled":"yes"}}}' | jq '.code, .msg'
# → 2006
#   "plugins.admin_auth.enabled must be a boolean"
```

---

## Path D — Plugin registry sanity (contract-level)

```bash
# The Go tests verify the plugin contract. No HTTP needed.
go test ./internal/plugin/... -run 'TestRegistry_' -v
go test ./internal/app/...    -run 'TestPluginMatrix' -v
```

Expected test names (from contracts/plugin-interface.md):
- `TestRegistry_EmptyIsValid`
- `TestRegistry_DuplicateIDPanics`
- `TestRegistry_StableOrder`
- `TestPluginMatrix_EmptyAndSetupPending`
- (from 003+) `TestPlugin_Identity`, `TestPlugin_SkippedWhenDisabled`, `TestPlugin_InitRejectsBadConfig`

In 002 itself there are **no concrete plugins**, so the plugin-specific tests in the last bullet don't exist yet — 003 adds them alongside the `admin_auth` plugin.

---

## Smoke checks the implementer must pass before marking `done`

1. `go build ./...` — no errors, no warnings.
2. `go test ./...` — all green on the first try, including the new tests in `internal/app`, `internal/config`, `internal/plugin`, `internal/setup`, and `internal/api/setup`.
3. `golangci-lint run` — no new findings.
4. `gofmt -l .` — empty output.
5. `pnpm -C frontend build` — succeeds; `frontend/dist/index.html` exists and references hashed asset filenames.
6. `pnpm -C frontend lint && pnpm -C frontend typecheck` — Biome + tsc both green.
7. `pnpm -C frontend test` — Vitest + RTL + MSW suites all pass.
8. `pnpm -C frontend test:e2e` — Playwright wizard happy-path + portal-shell + wizard-defaults all pass (4/4). `impeccable detect --fast` against rendered screenshots remains a local spot-check per T-502's de-scope.
9. Paths A, B, C, D above all pass manually.
10. Open `http://localhost:8080/setup/` and `http://localhost:8080/admin/` in a browser — both render without console errors, both toggle dark/light correctly, both are visually aligned with their v9 mock at 1440×900 (align with the design direction; pixel-diff is not a gate).
11. Kill the process mid-commit (between the DB COMMIT and the `config.json` rename) and restart with `ROUTER_DB_DRIVER` / `ROUTER_DB_URL` still set — the router must come back up **with the setup gate open**, because brownfield auto-materialize re-synthesizes `config.json` from env + the already-committed `upstream_accounts` row. Operator did not get forced through the wizard. This is R-1 in `plan.md` and the reason for the DB-first/file-last commit order.

---

## Rollback

A botched 002 upgrade on a running system rolls back as:

```bash
# stop 002
# point binary back at 001
# ROUTER_DB_DRIVER and ROUTER_DB_URL are still set correctly
# config.json is ignored by 001 (001 doesn't read it)
```

002 adds **no new DB tables or columns** — the schema is the 001 schema, unchanged. 001 continues to work against the same DB as-is.

---

## Known rough edges (tracked in plan.md)

- **R-1**: Commit is not a single filesystem transaction. The DB-committed/file-not-renamed window (plan.md R-1) is handled structurally by the **DB-first, file-last** ordering — a crash in that window leaves the DB consistent (one account, migrations applied) and `config.json` absent, which next boot's brownfield auto-materializer synthesizes from env with zero operator action. No "half-configured" state is user-visible.
- **R-2**: No admin auth. Deploying 002 behind anything other than a reverse proxy on a trusted network is out-of-spec. `README.md` says so.
