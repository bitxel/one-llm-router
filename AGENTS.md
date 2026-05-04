# one-llm-router

## Project Overview

Control and routing plane for AI coding clients. The first release serves Codex clients through one managed endpoint and preserves a clean path to add Claude Code and other protocols later.

**Personas**: Platform operator, internal client developer, protocol adapter maintainer

## Tech Stack

**Backend**: Go 1.25 · `net/http` (built-in routing) · SQLite (default) / PostgreSQL 16 / MySQL 8 · `xorm` (ORM) · `golang-migrate` · `slog` · `golangci-lint` · pluggable `internal/store` Dialect registry for adding more relational DBs

**Frontend**: React 19 · Vite 6 · TypeScript 5 strict · shadcn/ui · Tailwind CSS v4 · TanStack Router/Query/Table · React Hook Form + Zod · Biome · Vitest · Playwright · pnpm 10.30.3 · Node 22.22.0 — stood up in feature 002; see `frontend/AGENTS.md`

**API Contract**: OpenAPI 3.0.x (source of truth) · `oapi-codegen` v2 (Go server) · `@hey-api/openapi-ts` (TS client, pin exact version)

**Deployment**: dev = independent Vite + Go servers · prod = `go:embed` single binary

## Commands

```bash
# Backend
go build ./...                       # build
go test ./...                        # test
golangci-lint run                    # lint
go run ./cmd/one-llm-router                  # dev server
gofmt -w .                           # format
go generate ./internal/generated/... # regenerate from OpenAPI

# Database migrations (SQLite)
go run github.com/golang-migrate/migrate/v4/cmd/migrate \
  -path internal/store/migrations/sqlite -database "sqlite3://router.db" up

# Database migrations (PostgreSQL)
go run -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate \
  -path internal/store/migrations/postgres -database "$DATABASE_URL" up

# Database migrations (MySQL)
go run -tags 'mysql' github.com/golang-migrate/migrate/v4/cmd/migrate \
  -path internal/store/migrations/mysql -database "mysql://$DSN" up

# Frontend (when frontend/ exists)
cd frontend && pnpm install && pnpm dev      # dev
cd frontend && pnpm build                    # build
cd frontend && pnpm biome check --write .    # lint + format
cd frontend && pnpm tsc --noEmit             # type check
cd frontend && pnpm vitest run               # unit test
cd frontend && pnpm playwright test          # e2e test
cd frontend && pnpm openapi-ts               # regenerate TS client

# Phase 003 quality gates
bash scripts/envelope-parity.sh              # per-operation /api/admin envelope gate
bash scripts/log-scrub.sh test-output/       # grep Go + Playwright logs for token leaks
bash scripts/coverage-floor.sh               # 80% floor for oauth/oauthapi/adminapi/exportapi

# Phase 006 data-plane gates
make data-plane-compat                       # local mock-upstream 006 compatibility matrix
make live-upstream-compat                    # opt-in only; requires LIVE_UPSTREAM_SMOKE=1 and live API-key env
```

### Development Runtime Rule

Code changes do not affect already-running processes automatically. During development, after changing backend or frontend code, rebuild and restart the affected process before validating in the browser or API client:

- Frontend changes: run `make fe-build` first. For production-style local testing, then run `make go-build` so `frontend/dist` is staged into `internal/spa/dist` and embedded into `./one-llm-router`; restart the running router process before validating.
- Backend changes: run `make go-build`, then stop and restart the running router process from the rebuilt `./one-llm-router` binary.
- Full frontend + backend changes: run `make build` or, when dependencies are already installed, run `make fe-build && make go-build`.
- OpenAPI changes: regenerate Go and TS clients, then rebuild in order with `make fe-build && make go-build`, and restart the running process.

## Project Structure

```
one-llm-router/
├── openapi/                # OpenAPI specs (source of truth)
├── cmd/one-llm-router/             # Go service entrypoint
├── internal/
│   ├── domain/             # Pure data entities, enums, invariants
│   ├── core/               # Business logic, interfaces, orchestration
│   ├── api/                # HTTP handlers (validation only, no business logic)
│   │   ├── admin/          # /api/admin/* JSON handler implementations
│   │   ├── oauthapi/       # /api/admin/oauth/* admin OAuth handlers
│   │   └── setup/          # /api/setup/* JSON handler implementations (002+)
│   ├── oauth/              # OAuth browser/device/import/export/refresh orchestration
│   ├── provider/           # Upstream provider adapters
│   │   └── openai/         # OpenAI API client, SSE parser, usage extraction
│   ├── store/              # Persistence (XORM models, migrations/)
│   ├── api/exportapi/      # raw auth.json attachment success path + enveloped errors
│   └── generated/          # oapi-codegen output (never hand-edit)
├── frontend/               # React admin panel (later phase)
├── docs/                   # Product direction, architecture, standards
├── specs/                  # Feature specifications + SDD metadata
│   └── sdd/                # Constitution, state
├── scripts/                # Build, codegen, deploy scripts
└── .github/                # CI/CD workflows
```

## Code Style

```go
// Explicit error wrapping and small orchestration methods.
func (s *SessionService) Attach(ctx context.Context, req AttachRequest) (SessionRef, error) {
	ref, err := s.selector.Select(ctx, req.Protocol, req.SessionID)
	if err != nil {
		return SessionRef{}, fmt.Errorf("select upstream session: %w", err)
	}
	return s.store.Save(ctx, ref)
}
```

- `slog` for all logging; `slog.With()` for per-request context enrichment
- `fmt.Errorf("context: %w", err)` for error wrapping; sentinel errors for "not found"
- Keep direct dependencies minimal (3–5, not 30)
- Table-driven tests preferred

## Non-Negotiable Rules

### API Contract
1. OpenAPI spec in `openapi/` is the single source of truth for admin APIs **from 003 onwards**. 002 predates `openapi/` and uses Markdown contracts in `specs/002-.../contracts/*.md`; 003 converts them to `openapi/admin.yaml` as part of landing admin auth. Treat the Markdown contracts as authoritative until 003 regenerates them.
2. Every API change updates spec + regenerates Go server + TS client in the **same PR** (once OpenAPI is live — 003+).
3. Generated code is committed; CI verifies freshness via `git diff --exit-code` after regeneration.
4. Data plane (Codex proxy, `/v1/*`, and selected `/backend-api/*` paths) is NOT forced into OpenAPI if streaming makes it awkward. Its account-specific transport contract is documented in feature specs: API-key accounts use the OpenAI Platform-compatible `/v1/*` path, while OAuth ChatGPT accounts use explicit ChatGPT Codex backend mappings for supported paths. Router-local usage observability is an Admin API concern.

### HTTP API Style (router-owned endpoints)
Applies to **every** router-owned endpoint except `/v1/*` and selected `/backend-api/*` data-plane paths (provider-compatible AI data plane — excluded from the Admin API envelope; upstream transport is account-specific).

1. **Unified JSON envelope**: responses are wrapped as `{ "code": int, "msg": string, "data": any }`. `code = 0` is success; non-zero is error. `msg` is a short, stable, operator-grep-able string; never localised. `data` is the endpoint-specific payload on success and the payload's default zero value (`{}` for object, `[]` for array) on error — never `null`, so frontend types stay stable.
2. **HTTP status policy**:
   - **200 OK** — successes and business errors (invalid input, not-found, gone, conflict, rate-limited). The envelope's `code` discriminates.
   - **500 Internal Server Error** — *system* errors only (panic recovery, DB down, out-of-memory). Operator monitoring alerts on HTTP 5xx.
   - Non-envelope responses — `302` redirects, `204 No Content`, static assets, `405 Method Not Allowed`, connection-reset on oversized bodies — stay raw.
3. **Error code registry**: every non-zero `code` is listed in `docs/error-codes.md` with its symbol (e.g. `2001 setup_already_done`), HTTP status, and human-readable meaning. Codes are partitioned by feature range (1xxx = 001, 2xxx = 002, 3xxx = 003, …). Reserved: `0 = success`, `-1 = unknown`.
4. **Correlation id**: `X-Request-Id` is set as a **response header only**. Never duplicate it into `data` or `msg`.
5. **Mutation verb convention (RPC-style)**: admin mutation endpoints use `POST /resource/verb` (e.g. `POST /api/admin/settings/update`, `POST /api/admin/accounts/create`, `POST /api/admin/accounts/{id}/disable`). Avoid `PATCH`/`PUT` on admin endpoints — one verb convention, readable in access logs, consistent across 002–006.
6. **Path convention (SPA ↔ API split)**: router-owned **JSON APIs** live under the `/api/` prefix — `/api/admin/*` (portal APIs, incl. `/api/admin/health`) and `/api/setup/*` (installer wizard APIs). The **SPA** exclusively owns the `/admin/*` and `/setup/*` HTML trees; those paths never return JSON. A browser navigating to `/admin/settings` gets SPA HTML; the same page fetches from `/api/admin/settings`. This split removes the ambiguity of "is `/admin/settings` an API or a page?" that plagued early 002 drafts.
7. **Scope**: the envelope applies to `/api/admin/*` and `/api/setup/*`. `/v1/*` and selected `/backend-api/*` data-plane paths are explicitly and permanently **excluded** from the envelope. During setup-pending, `/v1/*`, `/backend-api`, and `/backend-api/*` data-plane prefixes return **001's native MVP error shape** (`{"error":{"code": "setup_required","message": "...","type": "service_unavailable"}}`) with **HTTP 503** — never the envelope. During setup-done, provider responses are never wrapped; API-key rows preserve the OpenAI Platform-compatible path for the 006 initial supported subset (`/v1/responses`, `/v1/conversations`, `/v1/chat/completions`, `/v1/models`) and OAuth rows use explicit ChatGPT Codex backend mappings for `/v1/responses`, `/v1/responses/compact`, Feature 006's `/v1/chat/completions` compatibility adapter, OAuth `GET /v1/models` facade, and selected `/backend-api/*` Codex-native compatibility paths. Router-local usage observability is served by the Admin API. Feature 006 defers Embeddings, Moderations, Images, Audio including `/v1/audio/transcriptions`, Videos, Files, Uploads, Vector Stores, Batches, Fine-tuning, Evals, and OpenAI Realtime WebSocket.

### Failure Semantics (Fail-Fast, No Silent Fallbacks)
1. **Default fail-fast**: DB, upstream HTTP/OAuth/LLM, parser, serializer, filesystem, and generated-client failures MUST surface as wrapped/typed errors. Never turn a real failure into an unmarked success.
2. **No silent fallback**: do not swallow errors with zero values, default business values, synthetic domain objects, or fake success. Examples: `return X{}, nil`, `return nil`, `WriteOK(...)` / `code:0` after validation, persistence, or upstream work failed.
3. **No fabricated facts**: never invent account data, token material, provider responses, health status, routing decisions, summaries, scores, or other business facts just to keep downstream code moving.
4. **Wire contract is strict**: `/api/admin/*` and `/api/setup/*` failures must return non-zero envelope `code` or HTTP 500; `/v1/*` and selected `/backend-api/*` data-plane paths must preserve provider-compatible client semantics and must not fabricate unsupported provider success/error bodies. For OAuth accounts, the documented provider transport is explicit ChatGPT Codex backend mappings, not OpenAI Platform byte-for-byte forwarding.
5. **Degraded paths are opt-in**: only allow a fallback/degraded result when the spec/ADR explicitly defines it, the contract marks it clearly (`degraded`, `usedFallback`, `listener_bound=false`, etc.), it is observable, and tests cover both normal and degraded paths.
6. **Review red flags**: `*_fallback` helpers, success-envelope returns from error paths, `catch`/`recover` returning default business values, or “best effort” synthetic domain objects are blocker-prone unless the spec explicitly allows them.

### Feature 003 Notes
1. `internal/oauth/` owns the dual-rail browser flow, device flow, refresh-before-forward hook, auth.json import, and reauth coordination; `internal/api/oauthapi/` is transport-only glue for `/api/admin/oauth/*`.
2. `internal/api/exportapi/` is the only `/api/admin/*` surface with a documented success-envelope exemption: success returns raw `auth.json` bytes, but every error branch remains enveloped.
3. `openapi/admin.yaml` is the route inventory for 003 admin surfaces, and `scripts/envelope-parity.sh` is the CI gate that proves every declared admin JSON route still obeys the envelope contract.
4. Structured OAuth events are part of the contract. At minimum, preserve `oauth_rail_rejected` for rejected browser rails and `oauth_flow_expired` for terminal expiry/reaper observations; never log token bytes in either path.
5. OAuth accounts use ChatGPT Codex transport on the data plane: `/v1/responses` → `https://chatgpt.com/backend-api/codex/responses`, `/v1/responses/compact` → `/codex/responses/compact`, plus `chatgpt-account-id` when present. Feature 006 adds `/v1/chat/completions` for OAuth accounts through a Chat Completions-to-Codex-Responses compatibility adapter, not direct OpenAI Platform forwarding. Feature 006 also includes selected Codex-native client paths: `/backend-api/codex/responses`, `WS /backend-api/codex/responses`, `/backend-api/codex/responses/compact`, `/backend-api/codex/models`, and `/backend-api/transcribe`; arbitrary `/backend-api/*` and `/api/codex/*` proxying remains excluded. Router-local usage observability belongs to `GET /api/admin/usage`. API-key accounts use the OpenAI Platform-compatible `base_url + original /v1/*` path only for the initial 006 supported subset. `/v1/audio/transcriptions` is deferred from 006 for all account types.

### Database Migrations
1. Versioned pairs: `NNNNNN_description.up.sql` + `.down.sql`.
2. **Expand/contract** pattern: add before removing, never rename in one step.
3. Backward-compatible with currently deployed binary (zero-downtime deploys).
4. Rollback plan required in every migration PR.

### Testing
Tests must exist and fail (Red phase) before implementation code. Priority: contract → integration → unit.

- Prefer real databases (SQLite in-memory for fast tests, PostgreSQL via testcontainers for CI) over mocked repositories.
- Mocks only for external services outside your control.
- Generated code excluded from coverage. Target ≥80%.
- E2E (Playwright): 5–10 critical operator journeys only.

### Security
- **MVP (Phase 1)**: No client authentication. No admin authentication. Upstream API keys stored in plaintext. Service runs on internal network only.
- **Phase 2+**: Client API key validation. Admin RBAC. Upstream key encryption at rest. Audit logs for mutations.
- Frontend (when added): `httpOnly` + `Secure` + `SameSite=Lax` cookies. CSRF tokens for state-changing requests. No `localStorage`/`sessionStorage` for tokens.
- Dependency CVEs (HIGH/CRITICAL): patch within 7 days.

### State Management (Frontend)
1. Server state → TanStack Query (never duplicate in stores)
2. URL state → TanStack Router search params (filters, pagination, sort)
3. Local state → `useState`/`useReducer`
4. Cross-route client state → Zustand, only by exception (justify in PR)

## Boundaries

- ✅ **Always**: update specs before changing scope · keep protocol rules isolated from routing policy · preserve additive compatibility · regenerate clients when API changes
- ⚠️ **Ask first**: schema changes · new third-party deps · auth changes · irreversible API contract changes · **when it is unclear whether a new requirement should be modelled as a plugin (feature-flag + capability) or as core code — ask the user**
- 🚫 **Never**: commit secrets · hardcode credentials · couple new protocols into Codex flows · skip request logging · hand-edit generated code · store auth tokens in localStorage · **bind plugins to URL paths — plugins express capabilities, not routing**

## Plugin Model

A **plugin** in this codebase is a **feature-flag-gated capability implementation**, not a generic HTTP middleware. The model:

1. Each plugin has an `ID()` (stable feature flag key, e.g. `admin_auth`, `client_keys`, `prometheus`). Enabled/disabled is **not** a plugin method — the app reads `config.Plugins.Enabled(id)` from a typed config struct before instantiating the plugin's factory, so disabled plugins are never constructed.
2. A plugin declares the capabilities it provides by **implementing named capability interfaces** (e.g. `plugin.AdminAuth`, `plugin.ClientKeyAuth`, `plugin.ProxyHook`). The app picks them up via type-assertion at boot.
3. URL routing is **not** a plugin concern. Plugins never declare "I mount at `/admin/*`". The app decides where each capability is invoked.
4. Generic middleware (logger, recoverer, request-id, body-cap) stays in `internal/api/middleware.go` — it is not a plugin.
5. Core infrastructure (e.g. the setup gate) that is always on and not user-toggleable stays in its feature package (`internal/setup/gate.go`), not in the plugin registry.

When you are unclear whether a new requirement is a plugin, ask. Default: it is **not** a plugin unless it maps to a user-visible on/off feature that has a natural capability extension point.

## Quality Gates

| Gate | Backend | Frontend |
|------|---------|----------|
| Lint | `golangci-lint run` — 0 warnings | `biome check` — 0 warnings |
| Types | Go compiler | `tsc --noEmit` — 0 errors |
| Tests | `go test ./...` — all pass | `vitest run` — all pass |
| Coverage | ≥80% (excl. generated) | ≥80% (excl. generated) |
| Build | `go build ./...` | `pnpm build` — 0 errors |

## Git Workflow

- **Branches**: `feature/[desc]`, `bugfix/[desc]`, `sdd/[NNN-name]`
- **Commits**: `type(scope): message` — types: feat, fix, refactor, test, docs, chore, ci — scopes: backend, frontend, api, infra
- **PRs**: required; CI must pass; at least one approval

## SDD Workflow

Spec-Driven Development. The specification is the source of truth.

- **Constitution**: `specs/sdd/constitution.md`
- **Feature Specs**: `specs/[NNN-feature-name]/`
- **State**: `specs/sdd/state.json`
- **Flow**: sdd-specify → sdd-plan → sdd-tasks → sdd-implement → sdd-test → sdd-verify → sdd-review → sdd-deliver

## Detailed Standards

- Product and design memory for UI work: `PRODUCT.md`, `DESIGN.md`
- Pinned toolchain versions, i18n rules, deployment strategy: `docs/standards/toolchain.md`
- Frontend conventions (routing, queries, components, forms, a11y): `frontend/AGENTS.md`
- Product direction and roadmap: `docs/platform-direction.md`
- Constitution and phase gates: `specs/sdd/constitution.md`
