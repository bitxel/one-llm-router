# Feature Spec: Setup Wizard and Admin Portal Skeleton

**ID**: 002-setup-wizard-and-admin-portal-skeleton
**Created**: 2026-04-16
**Status**: Draft

## Overview

Before one-llm-router can accept Codex traffic, an operator has to know how to connect it to a database, which optional plugins to enable, and how to register at least one upstream account. Today all of that happens through environment variables edited by hand, which blocks anyone who is not already familiar with the MVP deployment pattern and makes settings changes feel risky. This feature introduces a browser-based first-install wizard and the admin portal shell that every future admin page will sit inside, so that operators can bring a fresh instance from zero to a working router without touching shell, and can change non-critical settings on a live instance with the changes taking effect immediately and without a restart.

## User Scenarios

### US-1: First-Install Walkthrough (Priority: P0)

A platform operator is evaluating or deploying one-llm-router for the first time and wants to get to a working state without reading a wiki. They point a browser at the router and expect to be guided through the minimum choices.

**As a** platform operator, **I want** a guided browser setup flow on first boot, **so that** I can configure the database, register my first upstream account, and land on the admin home page without editing config files or shelling into the host.

**Why this priority**: Operator friction on first install is the single biggest reason a new deployment stalls. Without this, every new deployment looks like the MVP experience: read the README, set environment variables, restart, debug. The whole point of shipping 002 is to make install self-service.

**Acceptance Scenarios**:
1. **Given** a freshly deployed router with no `config.json` and no `ROUTER_DB_DRIVER` / `ROUTER_DB_URL` environment variables, **When** the operator visits any HTTP path on the router, **Then** the response redirects to the setup wizard home page (a single screen titled "Welcome to one-llm-router") and no routing traffic is served.
2. **Given** the operator is on the setup wizard, **When** they complete all five wizard steps (welcome, database, upstream account, plugin preview, confirm) with valid inputs, **Then** the router writes its configuration to disk, marks setup as complete in the database, redirects the operator to `/admin/`, and begins accepting `/v1/*` traffic on the next request.
3. **Given** the operator is on step 2 (database), **When** they submit a DSN that cannot be reached, **Then** the wizard shows a human-readable failure next to the DSN field within 5 seconds, the wizard does not advance, and no configuration is written to disk.
4. **Given** the operator reaches step 3 (upstream account) but does not yet have an API key at hand, **When** they click "Skip for now", **Then** the wizard advances to plugins without collecting credentials; on commit the router still migrates the database and writes `config.json`, and the admin dashboard surfaces the "no healthy accounts" banner until the operator registers at least one account in Admin → Accounts (see §Edge Cases — "operator skips upstream account seeding").

**Edge Cases**:
- What if the operator closes the browser tab halfway through the wizard? → Next visit resumes at step 1 with fields unpopulated; no partial configuration is persisted until the final confirm step succeeds.
- What if two operators open the wizard in two browsers and both click "confirm" at roughly the same time? → Exactly one request succeeds and writes configuration; the other receives an error stating that setup has already been completed and is redirected to `/admin/`.
- What if the operator provides a seed upstream account with an API key that the upstream rejects? → The router still completes setup (the router cannot validate third-party credentials in advance), but the first `/v1/*` request returns the upstream's error transparently; the admin home page shows a "no healthy accounts" banner.
- What if the operator skips upstream-account seeding during the wizard? → The commit path still runs migrations and writes `config.json`; the `upstream_accounts` table stays empty, `/api/admin/health` reports `degraded`, and every `/v1/*` request returns `503` with the existing proxy native-shape error (code `no_available_account`) until the operator registers an active account via Admin → Accounts. This is the supported "defer to admin portal" path added 2026-04-15 (`setup-api.md v2.4`).

---

### US-2: Upgrade Without Re-Setup (Priority: P0)

An operator is running the existing MVP (001), configured entirely through environment variables, and wants to upgrade to the 002 binary without being forced through a wizard that would block their production traffic.

**As a** platform operator with an existing MVP deployment, **I want** the new binary to keep working with my existing environment-variable configuration, **so that** upgrading does not interrupt my service or require me to re-register accounts.

**Why this priority**: Any solution that breaks existing deployments is unshippable. The wizard must be the first-install experience for *new* installs only.

**Acceptance Scenarios**:
1. **Given** an existing MVP deployment whose `ROUTER_DB_DRIVER` and `ROUTER_DB_URL` environment variables point at a database that already contains at least one upstream account, **When** the 002 binary starts, **Then** the router serves `/v1/*` traffic normally on the first request and never renders the setup wizard.
2. **Given** an existing MVP deployment that has never run 002 before, **When** the 002 binary starts for the first time, **Then** the router records setup as complete in the database without any operator action, so subsequent restarts also bypass the wizard.

**Edge Cases**:
- What if the operator has environment variables set but the database is empty (fresh DB pointed at by env)? → The router treats this as a new install and does render the wizard, because there is nothing to preserve.
- What if the operator starts 002 with environment variables pointing at a database that was last touched by a very early MVP build before the 001 migration set was final? → The migration set runs as usual; the auto-mark-complete step runs after migrations succeed, not before.

---

### US-3: Portal Shell Foundation (Priority: P0)

Every admin-facing page that ships in 003 and beyond needs to sit inside a consistent frame: navigation, layout, error handling, request identification. If every feature reinvents the shell, the admin experience becomes a patchwork.

**As a** platform operator, **I want** a consistent admin portal frame with a predictable navigation structure, **so that** future features feel like parts of one product instead of a collection of ad-hoc pages.

**Why this priority**: The shell is a prerequisite for 003. Without it, 003 either has to build its own shell (then throws it away later) or ships pages in an inconsistent style.

**Acceptance Scenarios**:
1. **Given** the operator has completed setup, **When** they visit `/admin/`, **Then** the page renders the portal shell with a top bar, a left navigation containing live entries for Dashboard, Accounts, Playground, Requests, and Settings plus disabled planned entries for Client Keys and Observability, and a content area that displays a "Setup complete" confirmation and a link to the roadmap.
2. **Given** any admin portal page is rendered, **When** a client-side fetch to the router fails, **Then** the portal shows a non-blocking error toast, preserves the user's current page, and surfaces the router-returned correlation identifier so the operator can quote it in a ticket.

**Edge Cases**:
- What if the operator's browser blocks ES modules? → The portal shows a single-line static fallback message instructing the operator to use a modern browser; no white page.
- What if the portal JavaScript throws an unhandled error after load? → A visible error banner replaces the content area without freezing the navigation, so the operator can still log out or retry.

---

### US-4: Live Settings Edit (Priority: P1)

An operator realises they want to change a non-structural setting — for example the request-body logging toggle or the log retention window — without downtime.

**As a** platform operator, **I want** to change selected runtime settings from the admin portal and have the changes take effect on the next request, **so that** I do not have to schedule a maintenance window for every small operational tweak.

**Why this priority**: Without this, 002 only solves first-install UX. Delivering live-settings edit in the same feature is what justifies choosing a file-plus-in-memory config design over "always edit environment and restart".

**Acceptance Scenarios**:
1. **Given** the operator is on the Settings page, **When** they change any of the 5 hot-reloadable fields (`log_client_request_body`, `log_upstream_request_body`, `log_upstream_response_body`, `log_retention_days`, `log_level`) and click Save, **Then** within 2 seconds the Save control reports success, subsequent routed requests honor the new setting, and the on-disk configuration file reflects the change with file permissions 0600.
2. **Given** a future release introduces a runtime field that is env-overridable and the operator has pinned it via an environment variable, **When** they click Save on the Settings page for that field, **Then** the router rejects the change with error code `2013 env_override_readonly` and no write to disk occurs. (Not exercised in 002 — none of the 5 runtime fields is env-overridable — but the server path is enforced from 002.)

**Edge Cases**:
- What if two operators press Save concurrently with different values? → Exactly one write wins; the losing operator is shown the stored value and asked to reload.
- What if the disk write fails after the in-memory update already succeeded? → The in-memory update is rolled back to the previous value, the operator sees an error, and subsequent requests use the previous value.
- What if the operator edits a critical field (database driver, database DSN) from the Settings page? → The portal does not expose these fields as editable in 002; they are read-only for display.

---

### US-5: Sensible Defaults with Zero Friction (Priority: P1)

The operator should not have to become a database administrator or a plugin architect to get started.

**As a** platform operator, **I want** the wizard to start with safe, single-node defaults pre-filled, **so that** I can click through the wizard in under 2 minutes for a local or demo install.

**Why this priority**: If the wizard asks ten questions with no defaults, it is not meaningfully better than editing environment variables. The wizard's value comes from the combination of a guided flow *and* defaults that make sense for the most common case.

**Acceptance Scenarios**:
1. **Given** a fresh install, **When** the operator opens the wizard, **Then** the database step is pre-selected to SQLite with a local file path default, the plugin toggles are all pre-set to off (MVP parity), and the upstream provider field is pre-selected to the first-launch supported provider.
2. **Given** the operator accepts every default through the wizard and only fills the required upstream-account fields, **When** they click Confirm, **Then** setup completes in under 10 seconds on a local machine and the first `/v1/*` request succeeds.

**Edge Cases**:
- What if the operator opens the wizard on a host where the default SQLite file path is not writable? → The wizard surfaces the write failure on step 2 Save and offers the chance to pick a different path or switch database.

## Functional Requirements

- **FR-001**: The system MUST detect on startup whether any persisted configuration exists (either an on-disk configuration file or the relevant environment variables) and MUST route the operator to the setup wizard only when neither source is present.
- **FR-002**: The system MUST treat an operator-provided database that already contains upstream-account data as an already-configured installation and MUST automatically record the install as complete on first 002 startup.
- **FR-003**: The setup wizard MUST cover at least: choice of supported database, database connection string, an OPTIONAL seed upstream account, a preview of the available plugin toggles, and a final confirmation step. Operators MAY skip the seed account and register one later via the admin portal (see US-1 AC-4 and the "operator skips upstream account seeding" edge case). When skipped, the commit still runs migrations and writes `config.json`; no credentials are stored; `/api/admin/health` reports `degraded` until an active account exists.
- **FR-004**: The setup wizard MUST validate the database connection string before the operator can advance past the database step. For server-based drivers (Postgres, MySQL) this means actually connecting to the database; for file-based drivers (SQLite) the local file path IS the DSN, so the wizard defers the connectivity check to the commit-time `migrate up` invocation and renders no probe control (2026-04-15 UX refinement).
- **FR-005**: The setup wizard MUST complete its final confirmation as an all-or-nothing operation — if any part fails (database write, account insert, config file write, state flag), the installation MUST remain in a state where the wizard can be re-run safely, and the operator MUST see a specific error pointing to the failing step.
- **FR-006**: The system MUST refuse to serve `/v1/*` until setup is recorded as complete, and MUST refuse to serve admin pages other than the wizard itself until setup is recorded as complete.
- **FR-007**: The system MUST refuse to re-render the wizard if setup has already been recorded as complete; an operator who deletes the on-disk configuration file but leaves a populated database intact (at least one row in `upstream_accounts`) MUST still be blocked from re-running setup until both are reset. An operator who committed a skipped-seed install (config.json present, `upstream_accounts` empty) and then deletes `config.json` is NOT blocked — the brownfield probe (FR-002) sees zero accounts and intentionally falls through to the wizard so the seed step can be completed.
- **FR-008**: For fields that 002 declares env-overridable, environment variables provided at process start MUST take precedence over values in the on-disk configuration file for the same field. 002 declares **no** runtime field as env-overridable (the 5 runtime keys — `log_client_request_body`, `log_upstream_request_body`, `log_upstream_response_body`, `log_retention_days`, `log_level` — are file-only). Database connection fields (`db.driver`, `db.url`) are env-overridable at boot (see `ROUTER_DB_DRIVER`, `ROUTER_DB_URL`) but are wizard-only writes, not Settings-page writes. The admin portal therefore does not need to surface per-field "managed externally" annotations in 002; when 003+ introduces env-overridable runtime fields, this requirement expands to cover UI visibility.
- **FR-009**: The admin portal MUST render a consistent shell across pages: a top bar, a left navigation with live entries for Dashboard, Accounts, Playground, Requests, and Settings, and a content area. Navigation entries for features not yet shipped MUST render as visibly disabled rather than hidden.
- **FR-010**: The admin portal MUST propagate each outbound client-side request's correlation identifier to the router and MUST display the returned correlation identifier to the operator whenever an error toast is shown.
- **FR-011**: The admin portal MUST apply the following runtime configuration changes without requiring a process restart: request-body logging toggle, response-body logging toggle, log retention window, log level, and any plugin toggle that has been shipped in a later feature. Changes MUST be persisted to the on-disk configuration file atomically (an incomplete write MUST NOT leave the file in a half-written state).
- **FR-012**: If a client attempts to edit a field whose effective value is supplied by environment variables at process start, the router MUST refuse the write and return a diagnostic error code (`2013 env_override_readonly` in `docs/error-codes.md`). This requirement is always enforced server-side. 002 does not trigger this path in practice because no 002 runtime key is env-overridable; the requirement remains in effect for future runtime keys that will be.
- **FR-013**: The wizard and the admin portal MUST be served from the same HTTP port as `/v1/*` and `/admin/*`; no additional port is required.
- **FR-014**: First-install security posture MUST default to MVP-parity: no admin authentication is enforced, because the 002 wizard itself is unauthenticated and a fresh install has no admin credentials yet. Enabling admin authentication in the wizard's plugin preview MUST record the operator's intent in the configuration so that when the authentication plugin ships in a later feature, the recorded intent is honored at first boot.

## Non-Functional Requirements

| Category | Requirement | Metric | Verification |
|----------|-------------|--------|--------------|
| Performance | The wizard must feel interactive | Each wizard step (excluding the Commit step) must complete server-side processing in under 500 ms P95 on a local development machine | Manual timing + integration benchmark |
| Performance | The Commit step must complete within a time the operator will wait | P95 under 10 seconds for the default SQLite path on a local machine | Integration benchmark |
| Performance | Live settings edit must feel "immediate" | Server-side processing of a Settings Save request under 1 second P95, and the next `/v1/*` request MUST observe the new value | Integration test that saves, then routes, and asserts |
| Reliability | Setup Commit is atomic | Under injected failure at each of the subtasks (database write, config file write, state flag write), the system MUST remain re-runnable; no half-configured states allowed | Fault injection test |
| Reliability | Config file writes are atomic | A crash during a Settings Save MUST leave the on-disk file either fully old or fully new, never truncated | Simulated crash / rename-semantics test |
| Security | The on-disk configuration file MUST NOT be world-readable | File permissions 0600 (owner read/write only) on POSIX systems at creation time | Integration test asserts mode |
| Security | The configuration file MUST NOT contain any admin credentials or upstream API keys stored in 002's scope | Static review of the written file schema | Code + spec review |
| Observability | Setup and settings changes MUST be attributable | Every setup-commit request and every settings-save request is recorded with a correlation identifier and outcome, queryable via the existing request history | Log audit |
| Usability | The wizard must be completable without documentation | A first-time operator completes the happy path in under 2 minutes on a local machine with default database | Manual usability pass |

## Key Entities

- **Configuration**: The single source of truth for operator-editable runtime choices — database driver and connection string, which plugins are recorded as enabled, log retention window, log level, request-body logging toggle, response-body logging toggle. Lives in one logical place (a file) so that "what is this router configured for" has one answer. Does not contain credentials or secrets beyond what is needed to connect to the database itself.
- **Setup State**: A persistent marker that remembers whether the router has already been through first-install, so that a deleted configuration file cannot trick a populated router into re-running the wizard and overwriting accounts. The marker is materialised as **the presence of the on-disk configuration file itself**; the file is the sole authority for "setup done?". Brownfield upgraders — operators who already ran 001 and therefore have database rows but no configuration file — are handled by a boot-time auto-materialisation step that synthesises the file from the `upstream_accounts` table plus environment variables, so that a missing file on a populated database cannot re-trigger the wizard.
- **Admin Portal Shell**: The shared navigation, layout, and error-surfacing structure that every admin page renders inside. Owns navigation entries for future features so that adding 003/004/005 pages is purely additive.

## Success Criteria

- **SC-1**: A first-time operator on a local machine reaches a working `/v1/*` by following the wizard end-to-end in under 2 minutes without reading any documentation outside the wizard itself.
- **SC-2**: An existing MVP operator upgrading to 002 observes zero downtime and zero new manual steps: the first `/v1/*` request after the upgrade succeeds exactly as it did on the MVP binary.
- **SC-3**: 100% of wizard-initiated setup attempts that reach the final Confirm step either complete fully or leave the router in a state where the wizard can be re-run. There is no observable "half-configured" state across a representative fault-injection test suite.
- **SC-4**: A Settings change saved from the admin portal is observed to take effect on the next routed `/v1/*` request (no process restart required) in 100% of the 5 operator-editable runtime fields (`log_client_request_body`, `log_upstream_request_body`, `log_upstream_response_body`, `log_retention_days`, `log_level`).
- **SC-5**: The admin portal shell is consumed by every feature shipped in 003 and beyond without modification; the shell is not versioned or forked per feature.

## Scope

### In Scope

- Browser-based first-install wizard covering welcome, database choice, DSN entry and validation, seed upstream account, plugin preview, final commit.
- Redirect of all non-setup paths to the wizard while setup is incomplete.
- Automatic "already configured" detection for existing MVP deployments.
- On-disk configuration file as the sole persistent record of operator-editable runtime choices, with environment-variable override precedence.
- Atomic write semantics for the configuration file and for the Commit transaction.
- Admin portal shell (top bar, left navigation, content area, error toast, correlation-identifier surfacing) rendered at `/admin/` after setup completes.
- Settings page capable of displaying and editing the 5 runtime fields that are safe to hot-reload (`log_client_request_body`, `log_upstream_request_body`, `log_upstream_response_body`, `log_retention_days`, `log_level`) plus, when later features ship them, plugin toggles.
- Regression safety for all existing MVP behavior — `/v1/*`, `/api/admin/accounts`, `/api/admin/requests`, `/api/admin/health` continue to behave according to the 001/002 amended contracts.

### Out of Scope

- Admin authentication or authorization of any kind (shipped in 003).
- Client API key management or client-side credentialing (shipped in 004).
- Metrics endpoint, Prometheus format, per-account health probing (shipped in 005).
- Migration of an existing database from one driver to another (SQLite → Postgres etc.); the wizard picks a driver once.
- Multi-node or clustered deployments; 002 assumes a single-node install.
- A general plugin marketplace or dynamic plugin loading; plugin toggles are limited to the compile-time plugin set declared by later features.
- Localisation; the wizard ships in English only.
- Full design-system maturity, animated transitions beyond page loads, and any style-override hook for operators. 002 ships a fixed **v9 design system**: Neo Retro grid layout × Grafana-Ops chrome, IBM Plex Sans/Mono typography, Flame-orange (`#F46800` dark / `#E7712B` light) single accent, dark and light themes toggle-persisted in `localStorage` with `prefers-color-scheme` as default. Canonical mocks: `mocks/v9-neoretro-grafana.html` (installer — ships in 002) and `mocks/v9-dashboard.html` (**design target for 005 observability** — kept as a reference artifact; 002 ships only the portal shell + "Setup complete" landing card, not the KPI dashboard).
- OpenAPI generation for the admin API; endpoints in 002 are hand-wired and 003 will revisit code generation.

## Assumptions

- The wizard is served from the same HTTP port as the rest of the router because first-install happens on a single process and splitting ports would hurt UX without improving security.
- The default database choice is SQLite with a local file path, because the primary first-install persona is evaluating on a single machine, and SQLite removes the hardest infrastructure prerequisite.
- Plugin toggles recorded during the wizard are simply persisted intents — a toggle for a plugin not yet shipped has no effect at first boot and will only become effective when that plugin's feature ships.
- The on-disk configuration file is the single file that the wizard writes; no separate "bootstrap" file is introduced in 002.
- Atomic file write means "write to a temp file in the same directory, fsync, then rename over the target" — the spec does not enforce a specific file system, but the implementation must preserve this semantic on the filesystems Linux and macOS expose.
- Environment-variable-managed fields are visible as read-only in the Settings UI; operators manage those through their existing deployment tooling, not through the portal.
- Setup completion is a one-way marker; re-running the wizard on a populated router is never allowed without explicit destructive operator action (outside of 002's scope).

## Clarifications

### Session 2026-04-16

- Q: Which configuration storage shape — single file vs split bootstrap-plus-DB-table vs env-only — is appropriate for a single-node MVP that wants live settings edits? → A: **Single configuration file is the sole authority.** The presence of the file on disk is itself the "setup done" marker — there is no separate DB row or state flag. The portal writes back to the same file for hot-reloadable fields. The fail-safe for brownfield upgraders (DB has rows, file does not exist) is a boot-time auto-materialiser that synthesises the file from the database + environment variables, so FR-002 ("never re-run the wizard on a populated router") holds without a second source of truth.
- Q: Should the wizard pre-enable admin authentication by default? → A: No; the wizard records the operator's intent but the default is MVP-parity (no auth). Auth enforcement lights up when the authentication plugin ships in 003.
- Q: Should client API keys shown by the portal be hashed or stored plaintext for this family of features? → A: Plaintext, per explicit product decision for the 004 feature; 002 does not touch client API keys at all.
- Q: Should the wizard and admin portal ship an OpenAPI specification with a generated typed client? → A: No in 002; 003 will revisit code generation when the admin API surface has grown enough to justify it.
- Q: Which visual style / aesthetic should the wizard and portal shell adopt? → A: **v9 design system** — Neo Retro layout × Grafana-Ops surface chrome, IBM Plex Sans / Plex Mono, single Flame-orange accent, dark + light themes (toggle persisted in `localStorage`, system preference as default). Canonical references: `mocks/v9-neoretro-grafana.html` for the 002 installer. `mocks/v9-dashboard.html` is the **design target for 005 observability** and is kept as a reference artifact — 002 ships only the portal shell + "Setup complete" landing, not the KPI dashboard. V9 is treated as visual **direction**, not pixel-for-pixel contract. Anti-pattern hygiene (no glass, no gradient text, no glow halos, no hero metrics) is a constraint on all 002 UI work. Originally planned as an automated `impeccable detect` CI gate (T-502), but de-scoped during implementation — enforcement for 002 is a design-review responsibility and a locally-runnable `impeccable detect` command; restoring the CI gate is tracked for a follow-up feature.
