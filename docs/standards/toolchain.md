# Toolchain, i18n, and Deployment Standards

This document supplements the root `AGENTS.md` with detailed toolchain configuration, i18n conventions, and deployment strategy.

## Pinned Toolchain Versions

Keep these in sync across local dev, CI, and documentation. Update deliberately, not accidentally.

| Tool | Version | Pinning mechanism |
|------|---------|-------------------|
| Go | 1.25 | `go.mod` (directive `go 1.25.0`) |
| Node.js | 22 LTS | `.node-version` or `.nvmrc` |
| pnpm | 10.30.3 | `packageManager` field in `frontend/package.json` |
| Python | 3.12 | GitHub Actions `live-upstream-smoke` job only |
| OpenAI Python SDK | 2.32.0 | `OPENAI_PYTHON_VERSION` / Makefile default for opt-in live smoke |
| golangci-lint | 2.11.4 | `.github/workflows` |
| Biome | 2.x | `frontend/biome.json` |
| oapi-codegen | v2.6.x | `go.mod` tool directive |
| @hey-api/openapi-ts | exact pin | `frontend/package.json` |

## Backend Tech Stack Detail

| Component | Choice | Notes |
|-----------|--------|-------|
| Language | Go 1.25 | Minimum 1.25 because `modernc.org/sqlite@v1.48.2` requires it |
| HTTP | `net/http` (Go 1.22+ built-in routing) | Zero external router dependencies |
| Database | SQLite (default) / PostgreSQL 16 | SQLite for dev & small deploys; PG for production scale |
| ORM | `xorm.io/xorm` | Single codebase for SQLite, PostgreSQL, MySQL |
| SQLite Driver | `modernc.org/sqlite` | Pure Go, no CGO required |
| PostgreSQL Driver | `github.com/lib/pq` | Used via XORM engine |
| Migrations | `golang-migrate` v4 | Per-dialect migration files (expand/contract) |
| Logging | `log/slog` (stdlib) | Structured JSON logging; enriched per-request with context |
| Config | Environment variables | 12-factor; `.env` files for local dev only |
| Lint | `golangci-lint` 2.11.4 | |
| Testing | `go test` + `testify` | Mocks only for true external boundaries |
| CI/CD | GitHub Actions | |

## Frontend Tech Stack Detail

Frontend work stands up in feature 002-setup-wizard-and-admin-portal-skeleton. See `frontend/AGENTS.md` for detailed conventions.

| Component | Choice | Notes |
|-----------|--------|-------|
| Framework | React 19 + Vite 6 | SPA; no SSR/SSG needed for authenticated admin panel |
| Language | TypeScript 5 (strict mode) | |
| UI | shadcn/ui + Tailwind CSS v4 | Copy-paste components; theme-token driven |
| Routing | TanStack Router v1 | Type-safe routes, search params, loaders |
| Server State | TanStack Query v5 | Caching, background refetch, deduplication |
| Data Tables | TanStack Table v8 | Use with TanStack Virtual for large datasets |
| Forms | React Hook Form + Zod | Type-safe validation |
| Charts | Recharts v2.15+ | Requires `react-is` version override for React 19 |
| i18n | react-i18next | English + Chinese (zh-CN) |
| Client State | React local state + URL state first | Add Zustand only by exception |
| Lint/Format | Biome v2 | Single tool; CI also runs `tsc --noEmit` as separate gate |
| Unit Test | Vitest + React Testing Library + MSW | |
| E2E Test | Playwright | Critical user journeys only |
| Package Manager | pnpm 10.30.3 | Strict dependency resolution |

## API Contract Detail

| Component | Choice | Notes |
|-----------|--------|-------|
| Spec | OpenAPI 3.0.x | Source of truth; 3.1 blocked by `oapi-codegen` upstream |
| Go Server | `oapi-codegen` v2 (strict server + nethttp-middleware) | Generates server interfaces from spec |
| TS Client | `@hey-api/openapi-ts` (pin exact version) | Generates TypeScript SDK; TanStack Query hooks via plugin |

**Generated code policy**: Generated files are committed to the repository so builds do not require codegen tooling. CI verifies freshness: regenerate and `git diff --exit-code`.

## i18n Standards

- Default locale: `en`. Supported locales: `en`, `zh-CN`.
- Translation files organized by feature module: `frontend/src/locales/{locale}/{module}.json`.
- Every user-facing string MUST use `t('key')` — no inline untranslated text.
- Dates, numbers, and currency use `Intl` APIs, not hardcoded formats.
- Translation keys use dot-notation namespaces: `accounts.table.status`, `usage.chart.title`.

## Security Standards Detail

### MVP (Phase 1) — Internal Network Only
- No client authentication — clients connect directly without API keys.
- No admin authentication — Admin API is unauthenticated.
- Upstream API keys stored in plaintext in the database.
- Service runs on internal network only.

### Phase 2+ (Production Hardening)
- **Authentication**: Every operator API call requires a valid auth token. Every routed client request requires a valid API key.
- **RBAC**: Operator actions are role-gated. Role changes require audit logging.
- **Audit trail**: Account and API key mutations are recorded with actor, timestamp, and before/after state.
- **Secret handling**: Never log, return, or store plaintext secrets. Credentials are stored hashed or encrypted.
- **Frontend auth cookies**: Use `httpOnly`, `Secure`, `SameSite=Lax` cookies for session tokens.
- **CSRF protection**: All state-changing requests from the frontend use a CSRF token.
- **Dependency scanning**: Enable Dependabot / GitHub security alerts. Review and patch HIGH/CRITICAL CVEs within 7 days.

## Deployment Strategy

| Environment | Strategy |
|-------------|----------|
| Development | Vite dev server (`:5173`) proxies API to Go server (`:8080`); both run independently |
| Production | `go:embed` packages `internal/spa/dist` into Go binary after staging from `frontend/dist`; single process, single port |

Production builds:
1. `cd frontend && pnpm build` produces static files in `frontend/dist/`
2. `make spa-stage` copies `frontend/dist/` into `internal/spa/dist/` for `go:embed`
3. Go binary embeds `internal/spa/dist/` and serves it at `/` with API routes mounted alongside
4. Single binary deployed to container or VM — no separate Nginx or Node.js runtime needed

## Testing Strategy Detail

Priority order follows the Constitution (contract → integration → unit). Tests must exist and fail (Red phase) before implementation code is written.

### Backend

| Layer | Scope | Tools | When |
|-------|-------|-------|------|
| Contract | API spec compliance | `oapi-codegen` validation middleware | Before implementation |
| Integration | Service ↔ database, HTTP handlers E2E | `go test` + real SQLite / PostgreSQL (Docker) | Every PR |
| Unit | Pure logic, utility functions | `go test` + `testify` | Every PR |

### Frontend

| Layer | Scope | Tools | When |
|-------|-------|-------|------|
| Integration | Components with real hooks + MSW-mocked APIs | Vitest + React Testing Library + MSW | Every PR |
| Unit | Pure utility functions, complex hooks | Vitest | Every PR |
| E2E | 5–10 critical operator journeys | Playwright | Pre-release |

Data-plane compatibility has a local mock-upstream gate: `make data-plane-compat`. It covers representative Feature 006 JSON, SSE, Chat Completions, read-only Models, router-local usage, selected Codex `/backend-api/*` mappings, Codex WebSocket relay, Codex transcribe multipart, provider-error, router-error, and deferred `/v1/audio/transcriptions` cases. The Playwright fixture builds and starts a temporary `cmd/one-llm-router-e2e` binary with the `e2e` build tag, then injects its loopback Codex mock through `ROUTER_E2E_CODEX_BACKEND_BASE_URL`; the production `cmd/one-llm-router` entrypoint rejects Codex backend override environment variables instead of accepting test-only upstream rewrites.

Live upstream compatibility is opt-in only: run `make live-upstream-compat` with `LIVE_UPSTREAM_SMOKE=1`, `LIVE_OPENAI_API_KEY`, `LIVE_OPENAI_BASE_URL`, and `LIVE_OPENAI_MODEL`. `LIVE_OPENAI_BASE_URL` is the provider origin, without `/v1`. Chat Completions JSON and streaming checks use `LIVE_OPENAI_MODEL` by default; set `LIVE_OPENAI_CHAT_MODEL` only when that endpoint needs a different model. Optional live cases use `LIVE_OPENAI_IMAGE_URL` and `LIVE_OPENAI_ENABLE_WEB_SEARCH=1`. The same live target also runs an OpenAI Python SDK smoke through the router, pinned to `openai==2.32.0` unless `OPENAI_PYTHON_VERSION` is set. Do not run live compat on PR CI; use an API-key test account, not OAuth/auth.json secrets.
