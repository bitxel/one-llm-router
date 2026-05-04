# one-llm-router

Spec-Driven Development workspace for a Codex-first LLM routing platform that can expand to additional client protocols over time.

## Key Documents
- `AGENTS.md`: project constraints, stack defaults, commands, and workflow rules
- `sdd/constitution.md`: non-negotiable engineering principles
- `specs/001-codex-router-mvp/spec.md`: first feature specification for the launch scope
- `specs/002-setup-wizard-and-admin-portal-skeleton/spec.md`: operator onboarding and admin portal shell
- `specs/006-openai-api-gateway/spec.md`: operation-level data-plane gateway scope and compatibility matrix
- `docs/platform-direction.md`: product direction and phased roadmap
- `docs/error-codes.md`: admin envelope code registry plus native data-plane error notes
- `docs/decisions/002-envelope-and-rpc-verb.md`: ADR for the JSON envelope and RPC-verb admin routes
- `docs/decisions/002-setup-gate-and-handler-swap.md`: ADR for the atomic handler swap that promotes the router from setup-pending to steady-state without a restart

## Current Focus
- Land the Feature 006 OpenAI API gateway expansion without weakening the admin/data-plane split
- Prove API-key OpenAI Platform compatibility plus explicit ChatGPT OAuth Codex mappings
- Keep deferred API families, including `/v1/audio/transcriptions`, visibly unsupported until they have their own spec and tests

## Getting Started — 002 Single-Binary Bring-Up

The 002 release delivers a single Go binary that serves the data plane (`/v1/*`, selected `/backend-api/*`, selected `/api/codex/usage` paths) and a React SPA (`/admin/*`, `/setup/*`) on the same port.

```bash
# 1. Build the frontend SPA (Vite → frontend/dist/*)
pnpm -C frontend install --frozen-lockfile
pnpm -C frontend build

# 2. Run the router — opens http://localhost:8080 and redirects
#    first-install operators to the setup wizard.
go run ./cmd/one-llm-router

# …or in one shot via the top-level Makefile:
make build   # produces ./one-llm-router (frontend baked in)
./one-llm-router
```

On first launch the wizard walks the operator through:
1. Database connection (sqlite3 / postgres / mysql)
2. First upstream account (OpenAI-compatible API key)
3. Optional plugin intents (admin auth + client API keys — both ship in later features)
4. Review & commit (writes `config.json` atomically; starts serving the data-plane routes)

### Brownfield Path (001 → 002 in place)

If a 001 MVP operator already has a populated database and their DB driver/URL in env, the 002 binary auto-materializes `config.json` on first boot — no wizard needed. Smoke-testable via:

```bash
bash scripts/smoke-brownfield.sh
```

### Developer Commands

The `Makefile` at the repo root aggregates the common flows. `make help` lists every target.

```bash
# Backend
make go-build            # go build -ldflags with stamped buildinfo
make go-test             # go test ./...
make go-test-race        # go test -race ./...
make go-lint             # golangci-lint (falls back to go vet)

# Frontend
pnpm -C frontend dev        # Vite dev server with proxy to :8080
make fe-test                # Vitest unit suite
make fe-typecheck           # tsc --noEmit
make fe-lint                # biome check

# Everything at once
make build                  # frontend install + fe build + go build
make test                   # Go + frontend unit tests
make lint                   # Go + frontend lint

# End-to-end
make e2e                    # Playwright specs with local/mock upstreams
make data-plane-compat      # targeted 006 data-plane compatibility matrix

# Opt-in live upstream compatibility smoke (never runs in default CI)
LIVE_UPSTREAM_SMOKE=1 \
LIVE_OPENAI_API_KEY=sk-... \
LIVE_OPENAI_BASE_URL=https://api.openai.com \
LIVE_OPENAI_MODEL=gpt-4o-mini \
make live-upstream-compat

# Optional live Chat Completions model override
LIVE_OPENAI_CHAT_MODEL=gpt-4o-mini make live-upstream-compat

# Smoke: 001 → 002 brownfield upgrade
make smoke-brownfield
```

Live compat uses an API-key account only and includes both Playwright live checks and an OpenAI Python SDK smoke through the router. `LIVE_OPENAI_BASE_URL` is the provider origin, without `/v1`. Chat Completions JSON and streaming checks use `LIVE_OPENAI_MODEL` by default; set `LIVE_OPENAI_CHAT_MODEL` only when the chat endpoint needs a different model. Optional cases: set `LIVE_OPENAI_IMAGE_URL` for image input and `LIVE_OPENAI_ENABLE_WEB_SEARCH=1` for web search. The SDK smoke pins `openai==2.32.0` by default; override with `OPENAI_PYTHON_VERSION` only when deliberately testing an SDK upgrade. Use a low-budget test key; do not run live jobs on PR CI or with OAuth/auth.json secrets.

## Repo Layout

```
cmd/one-llm-router/            # service entrypoint (HTTP listener + migrate CLI)
internal/
  api/                 # HTTP handlers: envelope helpers, middleware, admin + setup + proxy
  app/                 # BuildApp composition root; brownfield boot orchestration
  config/              # config.json schema, loader, writer, live-reload slot
  core/                # account / request / session business logic
  domain/              # entities and invariants
  plugin/              # plugin interfaces + registry (002 ships empty)
  provider/            # upstream provider adapters (OpenAI)
  setup/               # wizard validator + commit tx + gate middleware + probe
  store/               # DB access layer (xorm) + golang-migrate wrapper
frontend/              # React 19 + Vite 6 SPA (wizard + admin shell)
docs/                  # cross-feature reference material (error codes, ADRs)
scripts/               # operator + CI helpers
sdd/                   # SDD state machine, constitution, templates
specs/                 # per-feature spec/plan/tasks/contracts
```

## SDD Workflow

This project uses Spec-Driven Development. The specification is the source of truth.

- **Constitution**: `sdd/constitution.md`
- **Feature Specs**: `specs/[NNN-feature-name]/`
- **State**: `sdd/state.json`
- **Flow**: sdd-specify → sdd-plan → sdd-tasks → sdd-implement → sdd-test → sdd-verify → sdd-review → sdd-deliver
