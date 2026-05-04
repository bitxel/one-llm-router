# Research: Setup Wizard and Admin Portal Skeleton

**Feature**: 002-setup-wizard-and-admin-portal-skeleton
**Date**: 2026-04-17
**Scope**: Research decisions captured ahead of Phase 1 design. Most architectural choices were confirmed in `ROADMAP.md` ("Architectural decisions — apply across 002–006") on 2026-04-16; this document restates them in the Spec-Decision format and adds the choices that are unique to 002.

---

## Decision 1: Frontend delivery model

- **Decision**: Stand up the canonical frontend stack — **React 19 + Vite 6 + TypeScript 5 (strict) + shadcn/ui + Tailwind CSS v4 + TanStack Router v1 + TanStack Query v5 + React Hook Form + Zod + Biome + Vitest + Playwright**, as already specified in `frontend/AGENTS.md` and `docs/standards/toolchain.md` (previously labelled "deferred beyond MVP"). `pnpm build` emits `frontend/dist/*` which the Go binary embeds via `go:embed`. Dev mode runs Vite (`:5173`) and Go (`:8080`) separately with a Vite proxy forwarding `/api/*` (all router JSON APIs) and `/v1/*` (upstream proxy) to Go; Vite itself serves the `/admin/*` and `/setup/*` SPA routes from HMR. Visual language follows the **v9 design system** captured in `mocks/v9-neoretro-grafana.html`; Tailwind theme tokens derive from the v9 CSS custom properties. 002 ships only the installer wizard and a minimal portal shell with a "Setup complete" landing card — **not** the KPI dashboard from `v9-dashboard.html` (that is the 005 design target).
- **Decision history**: An earlier draft of this document chose "native ESM + `go:embed` static HTML" to minimise 002 scope. That decision was **revised on 2026-04-18** after considering the 003/004/005 portal-page cost curve: every subsequent feature adds CRUD forms, dialogs, and tables that would either force a React migration on top of a throwaway shell in 003, or require hand-rolling accessible primitives in vanilla — both strictly worse than landing the canonical stack once in 002.
- **Rationale**:
  1. `frontend/AGENTS.md` and `docs/standards/toolchain.md` already define the stack in detail — 002 is the first feature that needs a browser frontend, so 002 is the natural place to activate it.
  2. shadcn/ui provides accessible, themeable primitives (button, input, form, dialog, select, alert, sonner, skeleton) — every one of which 003+ will reuse. Tailwind v4's CSS-first config lets the v9 theme tokens land as `@theme` variables with no runtime cost.
  3. TanStack Query's cache + mutation invalidation matches the hot-reload semantics of `POST /api/admin/settings/update` (Decision 2): update succeeds (envelope `code = 0`) → invalidate `['settings']` → all dependent UI re-reads the server.
  4. Tooling costs are bounded: pnpm-lock, Biome, Vitest, Playwright all drop in with config files already specified in `frontend/AGENTS.md`. +1 engineer-week for 002; amortises across 003/004/005 (each future portal page is 1–2 days not 3–5 days).
  5. Production deployment stays "single binary": `go:embed frontend/dist/*` is identical in operator experience to the earlier vanilla-ESM approach.
- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |---|---|---|---|
  | Native-ESM + `go:embed` static HTML (earlier 2026-04-17 draft) | Zero npm, zero node_modules in CI; smallest possible 002 scope | 003 must either migrate shell to React (on top of a throwaway skeleton) or hand-roll accessible dialogs/forms/selects/tables in vanilla — both are strictly worse over the 003–005 arc | Over-optimises for 002 at the cost of 003/004/005 |
  | Server-rendered Go `html/template` pages | Simplest possible; no JS needed | No live updates on Settings page without full reload; v9 design uses dynamic theme toggling that is awkward in template-only flows; CRUD dialogs need heavy JS glue regardless | Fails FR-011 (hot-reload observability) UX ergonomics; pushes the JS back in through a side door |
  | Separate static site generator (Hugo/Astro) output committed to repo | Simpler than Vite; still produces a single static tree | Still introduces a build step the Go team has no reason to learn; SEO/SSG features are useless for an admin portal; diverges from `frontend/AGENTS.md` canonical stack | Duplicates Vite's job without gain; strands us off the standard path |
  | Next.js (SSR / App Router) | Batteries-included | SSR has no value for authenticated admin panels; the Go binary would need to embed a Node runtime or the deployment story splits | Wrong tool for a single-binary admin panel |

## Decision 2: Configuration storage shape — single file, **no** DB setup marker

- **Decision**: One on-disk file `config.json` holds the full operator-editable record — DB driver + DSN + runtime toggles + plugin intents — **and also serves as the setup-completion marker** (presence ↔ done). Precedence is `env > config.json > defaults`, loaded once at boot and published through `atomic.Value` for hot-reload. 002 introduces **no new DB tables** — the 001 schema (`upstream_accounts`, `upstream_sessions`) is unchanged. FR-007 ("deleting the file MUST NOT re-enable setup on a populated install") is satisfied by the **brownfield auto-materializer** (Decision 6): if `config.json` is missing on boot but env has DB creds and `upstream_accounts` has rows, the router synthesizes a fresh `config.json` from env + defaults before the gate decision.
  - Resolves spec Clarifications Session 2026-04-16: "Single file configuration [...] the portal writes back to the **same file** for hot-reloadable fields."
  - Supersedes earlier drafts that (a) split DB credentials into a file and runtime toggles into DB rows, and (b) kept the file + used a separate `system_config.setup.state` DB row as the authoritative gate.
- **Rationale**:
  1. DB credentials cannot live in the DB (chicken-and-egg). Once they are in a file, there is no reason to push a *different* subset of settings to a *different* store — it doubles the number of code paths for loading, writing, and atomically publishing.
  2. **No `fsnotify` dependency** is needed: the router does **not** watch the file. Hot-reload works like this: `POST /api/admin/settings/update` writes a new `config.json` atomically (tmp+rename) **and** publishes a new `*Config` through `atomic.Value` in-process. Out-of-band edits to `config.json` (e.g. `jq`) are NOT live-reloaded — operators restart to pick them up. This matches the spec's FR-011 scope (hot-reload only when edits go through the `POST /api/admin/settings/update` endpoint) and removes a whole class of file-vs-memory races.
  3. One file = one audit trail, one permission surface (`0600`), one thing to back up.
  4. Env-var override keeps working exactly as before (`env > file > defaults`), matching 001 conventions.
  5. Removing the `system_config` DB row collapses the R-1 crash recovery matrix: there is only one "is setup done?" datum to keep consistent (the file), and the brownfield auto-materializer provides the FR-007 invariant without a second persistence layer. See plan.md Trade-off T-2 (revised).
- **Key invariants (for commit atomicity, see Decision 3 and plan.md Risk R-1)**:
  - The DB COMMIT happens **before** the `config.json` rename — the reverse of an earlier draft. Rationale below.
  - Stale `.tmp.<pid>` files next to `config.json` are swept on startup.
- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |---|---|---|---|
  | Two-layer: `bootstrap.json` + `system_config` runtime rows (earlier draft) | Runtime edits are one SQL statement; DB is the operational datastore | Two atomic-write surfaces, two sources of truth, two loaders; makes commit atomicity a four-body problem (tmp-file, DB tx on accounts, DB tx on system_config, file rename); spec clarification explicitly preferred the single file | Over-engineered for the actual read/write volume; contradicts spec clarification |
  | Single file **plus** DB `setup.state` marker (earlier 2026-04-17 draft) | Satisfies FR-007 directly — deleting `config.json` cannot re-engage setup against a populated DB | Two sources of truth for one bit; crash between file rename and DB COMMIT leaves the pair inconsistent (R-1); "which is authoritative?" becomes a recurring question in every reviewer's head | Brownfield auto-materializer (Decision 6) satisfies FR-007 with **one** source of truth; dropped the second marker in 2026-04-18 PM revision |
  | Env-only (MVP parity) | No new code | Operators who cannot edit env vars cannot change settings → fails FR-011 | Defeats the purpose of the feature |
  | Dedicated column per runtime field | Typesafe at SQL layer | Schema migration for every new 003–005 toggle; violates Simplicity Gate | The app-layer JSON schema gives us typing where it matters; per-column DDL for two booleans is waste |

## Decision 3: Atomic Commit and file-write semantics — **DB-first, file-last**

- **Decision**: The wizard's final Commit runs the DB transaction **first** and atomically renames `config.json` into place **last**:
  1. `os.Stat(configPath)` pre-check — if file already exists, return envelope `code = 2001 setup_already_done` (HTTP 200; see `docs/error-codes.md`).
  2. Open DB + run `migrate up` (idempotent).
  3. `BEGIN TX` → `INSERT upstream_accounts(...) ON CONFLICT DO NOTHING` → `COMMIT TX`.
  4. Write `config.json.tmp.<pid>` with mode `0600`, fsync, then `os.Rename` into place.
  5. Publish fresh `*Config` through `atomic.Value`.

  Any failure before step 3's COMMIT rolls back the DB transaction; a failure at step 4 after a successful COMMIT is recoverable on next boot via Decision 6 (brownfield auto-materialize). Startup sweeps stale `*.tmp.*` siblings of `ROUTER_CONFIG_PATH`.
- **Rationale**:
  1. `rename(2)` is atomic on same filesystem on Linux + macOS (the supported hosts per `AGENTS.md`). `os.WriteFile` is NOT atomic and leaves a 0-byte file if interrupted.
  2. **DB-first ordering** eliminates the historic "file exists but DB half-committed" state. The previous file-first draft relied on an idempotent re-run of Commit to self-heal; with the DB-first order, a crash between step 3's COMMIT and step 4's rename leaves the DB in a fully valid state (one account, migrations applied) and the file absent — **exactly the state the brownfield auto-materializer is designed to recover from silently**. No operator intervention, no re-prompting for inputs the operator already typed. See plan.md R-1 (downgraded P/I).
  3. `0600` file perms set at creation time via `os.OpenFile(..., os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)` — not via `chmod` after write, which would race.
  4. Tmp file name includes PID (`config.json.tmp.<pid>`) so a crashed instance's tmp never collides with a live rewrite.
  5. Concurrent Commits (US-1 Edge-2): the winner's DB COMMIT + rename completes; the loser's step 1 (or step 3 via `ON CONFLICT DO NOTHING` + subsequent `os.Stat`) detects the file and returns envelope `code = 2001 setup_already_done` (HTTP 200). No row lock on a `setup.state` table is needed because there is no such table.
- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |---|---|---|---|
  | File-first (earlier draft): prepare tmp → DB tx → file rename → DB commit | Orphaned tmp files are easy to clean on boot | Crash between rename and COMMIT leaves `config.json` on disk pointing at a DB without the account row; the operator gets a confusingly pre-filled wizard on re-entry; the recovery story depends on Commit idempotency via a DB-side `setup.state` marker that no longer exists | Swapped to DB-first in 2026-04-18 PM alongside removing the DB marker; see plan.md T-2 revised |
  | `syscall.Flock` on the config file | Guards against concurrent wizard commits on the same host | Not cross-platform on Windows; not needed because the `os.Stat` pre-check + `rename(2)` atomicity already serialize on the filesystem layer | Adds complexity without cross-plat win |
  | Skip the file entirely; use only DB | One less thing to write | Would require DB credentials to live in env forever, breaking FR-001 and the single-file-install promise | — |

## Decision 4: Plugin registry seam — feature-flag + capability model

- **Decision**: `internal/plugin/` package hosts a `Plugin` root interface whose only method is `ID() string` and a set of **capability interfaces** (`AdminAuth`, `ClientKeyAuth`, `ProxyHook`, ...). A plugin participates in an extension point by satisfying the corresponding Go interface; the app discovers capabilities via type-assertion at boot and invokes them at pre-defined extension points. The registry is a `[]Binding{ID, Factory}` populated via package-level `init()` calls; duplicate IDs panic.

  Enabled/disabled is **not** a plugin concern in this model. The on-disk schema holds a per-plugin object at `config.json.plugins.<id>` with shape `{ "enabled": bool, <future plugin-specific keys> }`, decoded into a typed `config.PluginsConfig` struct (one sub-struct per known plugin, each starting with `Enabled bool` in 002 and growing additively in 003/004). Before calling `Factory()` for binding `b`, the boot sequence checks `cfg.Plugins.Enabled(b.ID)` and skips disabled entries — a plugin whose flag is false has its factory invoked **zero times**. Centralising the feature-flag read in `internal/config` means the wizard, the `POST /api/admin/settings/update` handler, and `BuildApp` all read the same typed value; there is no "plugin thinks it is enabled but app thinks otherwise" drift.

  Importantly: **plugins never declare URL paths, order, or middleware-wrap functions.** URL routing is the app's concern. Generic infrastructure middleware (logger, recoverer, request-id, body-cap) lives in `internal/api/middleware.go`. The setup gate lives in `internal/setup/gate.go` as always-on infrastructure — **not** a plugin. 002 ships the interfaces + registry + zero concrete plugins; 003/004/005 add them.

- **Rationale**:
  1. An earlier draft modelled plugins as generic HTTP middleware with `Bindings(cfg, deps) ([]Binding, error)` and `Binding{PluginID, Order, Scope, Wrap}`. That coupled plugin authors to URL layout and ordering concerns that belong to the application, and forced every plugin to think about middleware mechanics even when its actual job was "authenticate admin requests". The capability model directly expresses the real mental model: a plugin is *what capability does this feature provide when enabled*, not *where does this middleware mount*.
  2. Matches established Go-ecosystem patterns. Caddy's [`caddy.Module`](https://caddyserver.com/docs/extending-caddy) assigns each module to a *module namespace* (a capability category); go-micro's [plugin registry](https://github.com/go-micro/plugins) slots implementations into typed extension points (registry, broker, transport). In both, plugin authors implement a capability, not a URL wrapper.
  3. Compile-time typesafe, no reflection, no Go `.so` plugins. The Go type system is the enumeration of capabilities — adding a new extension point is "define one interface + add one type-assertion arm in `BuildApp`".
  4. 002 ships the seam only. No concrete plugin means the seam is provably minimal: if `TestRegistry_EmptyIsValid` passes, the seam supports the "everything off" baseline, and 003 can layer `admin_auth` by (a) adding fields to `config.AdminAuthPluginConfig`, (b) registering a binding, and (c) implementing the `AdminAuth` capability — no changes to the `Plugin` root interface, no changes to `BuildApp`'s enable-check loop.
  5. The setup gate is explicitly excluded from the plugin mechanism because it must be **always on**. Making it a plugin would imply operators can disable it — which is exactly what the gate protects against (routing to an unconfigured `/v1/*`).

- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |---|---|---|---|
  | Generic HTTP-middleware model (`Plugin.Wrap(surface, next)` + `Order` + `Scope`) | Extremely flexible — a plugin can mount anywhere | Plugin authors must reason about URL topology and order collisions with other plugins. Leaks routing into every plugin package. The `SetupGate=100 < AdminAuth=200 < Metrics=900` order convention is load-bearing but fragile (silent reorderings). | Capability interfaces move the "where" decision into `BuildApp` where it belongs. Order is an app concern, not a plugin concern. |
  | One monolithic `Plugin` interface with every extension-point method (authenticate admin, authenticate client, observe proxy, …) | Single concept for plugin authors | Every plugin has to stub out methods it doesn't implement. Adding an extension point forces every existing plugin to recompile against a new method. | Capability interfaces scale cleanly — a plugin implements only what it offers. |
  | Event-bus pattern (plugins subscribe to typed events) | Decouples producers and consumers fully | Runtime registration adds reflection or string-matching; invocation order is non-obvious. Overkill for 3–5 extension points known at design time. | Revisit if/when the extension-point count exceeds ~10. |
  | Use `store/dialect.go` pattern verbatim | Team already knows it | `dialect.go` registers *alternative implementations* of a single concept (the DB dialect). Plugins here are *additive capabilities*, not alternatives — they compose. Different problem shape. | Inspiration kept (compile-time self-registration + duplicate-ID panic), shape rejected. |

  See `contracts/plugin-interface.md` for the full interface definitions, lifecycle, and testing contract.

## Decision 5: Design system and visual anti-pattern gate

- **Decision**: The "v9 design system" is the visual **direction** for 002 — the north star the implementation is polished toward — not a pixel-for-pixel contract:
  - Layout: Neo Retro grid (brand column + top bar + left sidebar + main)
  - Surface chrome: Grafana Ops (square corners, 1px lines, `var(--panel)` fill)
  - Typography: IBM Plex Sans (UI) + IBM Plex Mono (numbers, tags, timestamps)
  - Accent: Flame-orange `#F46800` (dark) / `#E7712B` (light), one single accent
  - Theme: dark (default) + light, toggle persisted in `localStorage`, `prefers-color-scheme` respected as initial
  - Anti-pattern hygiene enforced by a locally-runnable `impeccable detect --fast` against Playwright-rendered screenshots of the built SPA (automated CI gate de-scoped with T-502; tracked as follow-up)
  - 002 ships only the wizard shell and a "Setup complete" landing card; the KPI dashboard visible in `mocks/v9-dashboard.html` is the design target for 005.
- **Rationale**:
  1. Closes the single open clarification in the spec (resolved 2026-04-17 after a nine-mock exploration round; v9 won on information density + ops-tool credibility).
  2. Running `impeccable` locally during design review prevents the portal from drifting into "AI slop" (glass, gradient text, hero metrics, glow halos) during 003–005 additions. A future feature may promote it to a CI gate once the tooling stabilises.
  3. IBM Plex is self-hosted (subset, ~60KB) so the portal has no runtime CDN dependency.
  4. Framing v9 as *direction* rather than *spec* lets us evolve the implementation when real user data or accessibility reviews demand changes, without needing a new round of mockups to approve each tweak.
- **Alternatives Considered**: nine explored mocks (Linear, Grafana, Vercel, Supabase, Stripe, Brutalist, Notion, Neo Retro, Neo Retro × Grafana). Details captured in git history of `specs/002-.../mocks/` prior to cleanup on 2026-04-17. Final selection notes captured in `spec.md` Clarifications session.

## Decision 6: Brownfield auto-materialization for MVP upgrades

- **Decision**: On boot, `internal/setup/brownfield.go` runs a tiny probe after `config.Load` reports "file absent":
  1. Are `ROUTER_DB_DRIVER` + `ROUTER_DB_URL` both set in the environment?
  2. Does `upstream_accounts` have at least one row?

  If both yes, the router synthesizes a fresh `config.json` from env + defaults (writing it through the same tmp + rename path as Commit, `0600`) and re-runs `config.Load`. The setup gate now sees the file and opens. Satisfies FR-002 (existing MVP deployments upgrade silently to 002) and preserves FR-007 (deleting `config.json` on a populated install does not re-engage setup).
- **Rationale**:
  1. Detection moves to the runtime so there is no `system_config` table to migrate. The 001 → 002 upgrade is purely additive to application code; zero schema changes.
  2. Idempotent: if `config.json` already exists, the probe is skipped. If the probe writes the file and a crash leaves things in a mixed state, the next boot's probe re-runs the same logic.
  3. A fresh DB (no `upstream_accounts` row) correctly lands in the wizard; env alone is not enough to short-circuit.
  4. Env-pinned `db.*` fields remain server-side `source = "env:*"` in the internal `SourceMap` after auto-materialize — env is re-evaluated each boot and still wins over the synthesized file. Note: after Round-3 (D6) this source value is no longer surfaced in `GET /api/admin/settings`'s `data.db` block; it lives only inside the loader for internal routing of write-attempt rejections (`2013 env_override_readonly`, dormant in 002).
- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |---|---|---|---|
  | Migration-side backfill of a `system_config.setup.state='done'` row (earlier draft) | Backfill happens once, no per-boot probe | Requires the `system_config` table (dropped in 2026-04-18 PM revision); two sources of truth for "is setup done?" | Moot once the DB marker is removed |
  | Detect at startup with a feature-flag env var (e.g. `ROUTER_ASSUME_SETUP_DONE=1`) | Explicit, auditable | Breaks SC-2 "zero manual steps"; operators upgrading from 001 have no reason to know about a new env var | — |
  | Require operator to run `one-llm-router migrate up` + touch `config.json` manually | Explicit | Breaks SC-2 "zero downtime, zero manual steps" | — |

## Decision 7: Library / dependency choices

- **Go**: **No new Go dependencies.** 002 reuses `xorm`, `stretchr/testify`, `modernc.org/sqlite`, `lib/pq`, `go-sql-driver/mysql`, `golang-migrate/v4` — all already in `go.mod`. 002 does, however, promote `golang-migrate/v4` from a `go run` tool (as used in 001 dev scripts) to an **imported library** (`internal/store/migrator.go`, `cmd/one-llm-router/migrate_subcmd.go`) for boot-time `migrate up`, dirty-state detection, and the new `one-llm-router migrate` CLI subcommand.
- **Frontend**: all npm packages are specified in `frontend/AGENTS.md` + `docs/standards/toolchain.md` (the canonical stack) — React 19, Vite 6, TS 5 strict, shadcn/ui (copy-paste), Tailwind v4, TanStack Router/Query, React Hook Form + Zod, Biome, Vitest + RTL + MSW, Playwright, pnpm 9, Node 22 LTS.
- **Rationale**: every rejected Go candidate (`fsnotify`, `cobra`, `chi`, `templ`) mapped to complexity we do not need in 002. The stdlib + the existing five Go deps cover all 002 Go work. On the npm side, 002 is executing the stack already committed to in `frontend/AGENTS.md` — no net-new decisions.
- **Alternatives Considered (Go)**: `fsnotify` for out-of-band live reload — rejected because spec FR-011 scopes hot-reload to changes that go through `POST /api/admin/settings/update`. `cobra` for CLI flags — stdlib `flag` is sufficient for the ~3 flags main.go will own.

## Decision 8: Unified JSON response envelope for router-owned endpoints

- **Decision**: Every router-owned JSON endpoint — `/api/admin/*`, `/api/setup/*`, and 001's relocated admin endpoints at `/api/admin/*` — returns the envelope `{ "code": int, "msg": string, "data": any }`. `code = 0` is success; non-zero is an error. **HTTP 200** is used for successes and for **business errors** (invalid input, not-found, gone, conflict, rate-limit). **HTTP 500** is reserved for **system errors** (panic, DB connection lost, out-of-memory). `/v1/*` is explicitly and permanently excluded from the Admin API envelope — it uses 001's native MVP error shape with native HTTP status codes while setup is pending (HTTP 503), and provider responses are never wrapped in setup-done state. Feature 003 amends the setup-done upstream transport: API-key accounts preserve 002 Platform-compatible forwarding, while OAuth accounts use the ChatGPT Codex backend for Responses paths. Non-JSON routes (SPA HTML at `/admin/*` and `/setup/*`, redirects, static assets, `405 Method Not Allowed`, connection-reset on oversized bodies) also stay raw. Integer codes are registered in `docs/error-codes.md` (0 success, -1 unknown, 1000–1999 for 001, 2000–2999 for 002). Correlation id is returned via the `X-Request-Id` response header only; it is **not** duplicated into the body.
- **Decision history**: Introduced 2026-04-18 PM after the user flagged two specific shapes in `admin-api.md` as "why are there three vocabularies?" (HTTP status + business code + message text). An earlier REST-idiomatic shape (`{"error": {"code": "...", "message": "..."}}` with HTTP 4xx/5xx on errors) was replaced wholesale.
- **Rationale**:
  1. **One parse path for clients.** The SPA's `api-client.ts` peels `{code,msg,data}` once; every caller receives the typed `data` or a typed `RouterApiError{code,msg}`. No branching on HTTP status vs body shape.
  2. **Integer code registry.** `docs/error-codes.md` gives operators a single numeric space to alert on and a single file to grep when an SLO alert fires `code=2900`. The 1000-range segregation (1xxx for 001, 2xxx for 002, …) scales feature-by-feature without namespace collisions.
  3. **HTTP status stays meaningful for infra.** Load balancers, HTTP-based alerts, and uptime probes still see `200 OK` for expected responses and `500` for "the server itself is unhappy" — preserving the signal those tools were built to surface.
  4. **`/v1/*` stays outside the envelope.** The data-plane contract is defined by provider-compatible client semantics, not the Admin API envelope. In 002 that meant Platform-compatible forwarding; Feature 003 narrows that to API-key rows and defines OAuth rows as ChatGPT Codex backend transport for Responses traffic.
  5. **`X-Request-Id` header-only** avoids duplicating correlation bookkeeping. Matches HTTP convention (`Request-Id` / `X-Request-Id` is a header, not a body field, in every major tracing standard: W3C Trace Context, Zipkin, Jaeger, AWS X-Ray).
- **Backward-compatibility cost**: 001's `/admin/health` moves to `/api/admin/health` AND its body is wrapped. Before 002: `GET /admin/health` → bare `{"status":"ok"}` with HTTP 200. After 002: `GET /api/admin/health` → `{"code":0,"msg":"ok","data":{"status":"healthy","checks":{...},"setup_state":"done"}}` (the `setup_state` field is carried on `/api/admin/health` exclusively, so always-reachable clients can discriminate pending vs done without hitting a gated endpoint). Operators with liveness probes must (a) update the URL, (b) migrate from `.status == "ok"` to `.data.status == "healthy"`. HTTP-status-based probes are unchanged (still `200 OK`) but now must consider HTTP 500 as the system-error channel. This is announced in `ROADMAP.md`, the 002 release notes, and 001's tech-design changelog.
- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |---|---|---|---|
  | REST-idiomatic — HTTP 4xx/5xx + `{error:{code,message}}` body | Matches common Go / OpenAPI conventions; interoperates with generic client libraries | Two response shapes (success vs error), three vocabularies for clients to handle (HTTP status + string error code + message); every future endpoint has to re-decide "is this a 400 or a 409?" | Trades client simplicity for REST purism that nothing in our stack actually consumes |
  | gRPC status codes over JSON (e.g. `{"status":7,"message":"permission denied"}`) | Well-known integer code space | 15 fixed codes do not partition cleanly into per-feature buckets; maps poorly onto HTTP semantics for operator tooling | Too coarse for per-feature error taxonomy |
  | JSON:API error envelope (`{"errors":[...]}`) | Structured for multi-error arrays | Our endpoints never need to return N errors for one request; adds list indirection for the 99% single-error case | Over-specified for our shape |
  | String codes instead of integers (e.g. `"code":"setup_already_done"`) | Human-readable | Cannot be summed/grouped by alerting systems; no cheap "range 2000–2999 is 002" segregation; risk of typo drift between Go / TS sides | Integer + registry gives both machine and operator a better tool |

- **Implementation seam**: `internal/api/envelope.go` (Go) + `frontend/src/lib/errcode.ts` (TS mirror of `docs/error-codes.md`). Every handler returns through `envelope.WriteOK` / `envelope.WriteBizErr` / `envelope.WriteSysErr`; middleware panics are recovered into `WriteSysErr(-1, "internal error")`.

## Decision 9: Admin mutation verb convention — RPC-style `POST /.../update`

- **Decision**: All router-owned mutation endpoints use the RPC-style `POST /resource/verb` convention under the `/api/` prefix. For 002, the settings mutation is `POST /api/admin/settings/update`. The request body semantics are still partial-update (only the keys being modified). Future admin mutations follow the same pattern (`POST /api/admin/accounts/create`, `POST /api/admin/accounts/{id}/disable`, `POST /api/admin/accounts/{id}/rotate`, …).
- **Rationale**:
  1. **Readable verb in every log line.** Operators grepping access logs see `POST /api/admin/settings/update` directly — no need to cross-reference the HTTP verb column with the path column to infer intent.
  2. **One verb convention.** Every admin mutation is `POST /.../verb`; no recurring "is this a PATCH or a PUT" debate when the shape of a new endpoint is borderline.
  3. **Matches project house style.** Internal Alibaba/Taobao-style JSON-RPC services and most gRPC gateways that project reviewers know look like this. Pattern recognition is free.
  4. **Body semantics unchanged**, so Go and TS migration is a pure verb + path rename — `PATCH → POST`, `/admin/settings → /api/admin/settings/update`.
- **Cost**: non-idiomatic for reviewers who expect pure REST. Documented as a house convention in `AGENTS.md §HTTP API style` (added alongside this decision) so newcomers see the rule immediately.
- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |---|---|---|---|
  | Keep `PATCH /api/admin/settings` (REST) | Canonical REST; documented in every HTTP textbook | Every mutation requires rethinking verb choice (PATCH vs PUT vs POST on a sub-resource); produces inconsistent families as endpoints accrete | Loses the readability win above |
  | `PUT /api/admin/settings` (full replace) | Simpler semantics | 002 has ≥5 fields and 003+ will grow the runtime block; forcing the UI to send the full snapshot on every edit is a backward step | Scales badly |
  | gRPC / Connect-style RPC over `/admin.v1.SettingsService/Update` | Fully typed; codegen on both sides | Adds Buf/Connect toolchain; overkill for 002's two mutation endpoints; frontend has no gRPC client in scope | Not justified at 002's scope |

## Decision 10: Settings endpoint shape — flattened, registry-only plugins

- **Decision (Round-3 review 2026-04-19)**: The response of `GET /api/admin/settings` is intentionally small and registry-driven:
  - `data.runtime` is a **flat** primitive-valued object (no `{value, source}` wrappers) with exactly the five 002 runtime keys: `log_client_request_body` (bool), `log_upstream_request_body` (bool), `log_upstream_response_body` (bool), `log_retention_days` (int `[1,365]`), `log_level` (enum `debug|info|warn|error`). All five are file-only (not env-overridable) in 002.
  - `data.db` carries three non-secret fields: `driver` (enum), `host` (`"host:port"` for postgres/mysql; `"local"` for sqlite3), `database_name` (basename for sqlite3). `db.url` is never surfaced; the wizard writes it, the server reads it, the UI never sees it.
  - `data.plugins[]` is a row per **actually-registered** plugin in the Go build (`{id, label, enabled}`). In 002, this array is empty. The SPA sidebar does not read this array; its nav entries and "coming soon" badges are hard-coded client-side TS constants.
  - `data.system` is three build fields: `router_version` (git tag — `git describe --tags` output), `router_git_sha` (7-char short), `router_built_at` (ISO 8601 UTC).

- **Decision history**: Surfaced during the 2026-04-19 per-field walkthrough. The predecessor shape (v3.1 in `contracts/admin-api.md`) carried five-field `plugins[]` rows including `shipped_in_feature` and `source`, a roadmap-only projection branch that needed the D3 decision to avoid warn logs, a `{value, source}` wrapper on every `runtime` key, a nested `db` with `source`, and `system.{setup_state, config_version}`. Every one of those items failed the "what client actually reads this?" check in the walkthrough.

- **Rationale**:
  1. **Fewer things can lie.** If the server does not announce `source`, clients cannot disagree with the server about what the source is. 002 has no runtime key whose source varies, so there's nothing to announce.
  2. **Sidebar independence.** Hard-coding nav on the client means the shell renders identically on a broken/empty server response — the shell is static by design. This reduces the blast radius of a Settings endpoint regression.
  3. **D3 obsoleted.** No roadmap-only rows in the response means no unknown-plugin warn path to suppress. The Go code no longer needs a `roadmap.go` table or a `wired_in_pluginsconfig` gate.
  4. **`db` UI affordance.** Adding `host` and `database_name` (parsed from the DSN, never the DSN itself) lets the portal render a meaningful "Connected: Postgres @ db.internal:5432 / router_prod" line without any extra endpoint.
  5. **`router_version` as a git tag** aligns the value that appears in the UI with the value operators grep for in release notes, docker image tags, and CHANGELOG entries.

- **Cost**: FR-008's UI-visibility clause is softened (server still enforces `2013` on env-pinned writes; the UI just doesn't paint a "pinned by env" badge in 002). This is acceptable because no 002 runtime key triggers the path anyway. When 003+ lands an env-overridable runtime key and the UI needs to distinguish it from editable keys, the `runtime._env_pinned: [keys…]` sidecar sketched in the Round-1 discussion becomes the cheapest add-on.

- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |---|---|---|---|
  | Keep `{value, source}` on `runtime` | UI can paint env-pinned as read-only uniformly across keys | Extra nesting on every read; no 002 consumer because no key is env-pinned | Pay cost now for benefit that arrives in 003+ — defer |
  | Keep the five-field `plugins[]` row + roadmap table | Portal could drive nav from the server | Requires `shipped_features.go` + `roadmap.go` + D3 projection gate; sidebar changes require a backend deploy | Hard-coding sidebar on the client is simpler for 002's shell and matches how the v9 mocks envision the surface |
  | Return `{runtime, db, plugins, system}` in a nested `settings` envelope | Leaves room for adding top-level keys in 003 | All callers already destructure `.data`; adding a second shell just moves the problem | No net benefit |

- **Implementation seam**: the Go handler lives at `internal/api/adminapi/settings.go`. The contract test in `settings_contract_test.go` asserts the exact field set per the snapshot above (absence is just as important as presence — the test fails if a stray field appears).

## Dependency Versions (Verified 2026-04-17)

### Go (no bumps)

| Package | Verified Version | Registry | Notes |
|---|---|---|---|
| `xorm.io/xorm` | `v1.3.11` | go.sum (existing) | pinned as-is; no bump |
| `modernc.org/sqlite` | `v1.48.2` | go.sum (existing) | no bump; floor for Go 1.25 |
| `github.com/lib/pq` | `v1.12.3` | go.sum (existing) | no bump |
| `github.com/go-sql-driver/mysql` | `v1.9.3` | go.sum (existing) | no bump |
| `github.com/stretchr/testify` | `v1.11.1` | go.sum (existing) | no bump |
| `github.com/golang-migrate/migrate/v4` | `v4.19.1` | go.sum (existing) | imported by `internal/store/migrator.go` (boot-time `migrate up` + dirty detection) and by `cmd/one-llm-router/migrate_subcmd.go` (`one-llm-router migrate {up\|down\|force\|version\|status}`). Previously "used via `go run`, never imported" — updated 2026-04-18 PM. |

### npm (new — exact version matrix lives in `frontend/package.json`)

See `plan.md §Dependency Versions` for the per-package version floor. All versions match `frontend/AGENTS.md` + `docs/standards/toolchain.md` Pinned Toolchain table (React 19, Vite 6, TS 5, Tailwind v4, TanStack Router v1 / Query v5 / Table v8, Biome v2, pnpm 9, Node 22 LTS).

Font assets (IBM Plex subset) are vendored into `frontend/public/fonts/` as .woff2 files under their original SIL OFL 1.1 license.

---

**Done.** All Phase 0 research questions closed. Proceed to Phase 1 data model and contracts.
