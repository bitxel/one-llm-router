# Consistency Report: 002 Setup Wizard and Admin Portal Skeleton

**Feature**: `002-setup-wizard-and-admin-portal-skeleton`
**Date**: 2026-04-19
**Status**: ⚠️ DRIFT DETECTED (no Critical findings; 1 Major + 8 Minor)

Green-on-the-runtime: all automated checks pass — `go test -race ./...`,
`golangci-lint run` (with `bodyclose`/`errorlint`/`revive` enabled),
`pnpm lint` + `typecheck` + `vitest` (19 tests), 4/4 Playwright E2E
(`wizard-happy-path`, `wizard-defaults`, `portal-shell AC-3.1/3.2`),
and `make smoke-brownfield`.

The findings below are **specification/documentation drift** against a
working implementation, not correctness bugs.

---

## Summary

| Metric | Value |
|--------|-------|
| FR items checked | 14 |
| AC items checked | 13 |
| NFR items checked | 9 |
| Fully consistent | ≈ 80% |
| Drift detected | 9 findings (1 Major, 8 Minor) |
| Gaps (requires code) | 2 |
| Extra implementations | 1 (commit response has extra fields) |
| Stale references | 3 |
| Pending `[CONFIRM]`/`[HUMAN]` markers in source | **0** |

## Consistency Score

Overall: **85 / 100**

| Artifact Pair | Score | Findings |
|---------------|-------|----------|
| Spec ↔ Code | 86/100 | V-001, V-003, V-004 |
| Spec ↔ Plan | 88/100 | V-002, V-003 |
| Plan ↔ Code | 84/100 | V-008, V-009 |
| Spec ↔ Tests | 82/100 | V-001, V-005 + NFR-manual items |
| Tasks ↔ Code | 83/100 | V-005, V-008 |

---

## Findings

### Major Findings

| ID | Type | Spec Item | Code Location | Description |
|----|------|-----------|---------------|-------------|
| V-001 | GAP | `spec.md` US-1 Edge-3 — admin home "no healthy accounts" banner | `frontend/src/routes/admin/index.tsx` (static "Setup complete" copy) | When the seeded upstream API key is rejected by OpenAI, the spec requires the admin home to show a "no healthy accounts" banner. The current admin landing renders only the static "Setup complete" card. |

### Minor Findings

| ID | Type | Spec / plan reference | Code / doc | Description |
|----|------|-----------------------|------------|-------------|
| V-002 | DRIFT | spec.md §Clarifications + plan.md R-8 ("`impeccable detect` as CI gate") | `.github/workflows/ci.yaml` has neither Playwright nor `impeccable` | T-502 (impeccable gate) was dropped from `tasks.md` per operator decision; spec/plan copy still promises a CI gate. |
| V-003 | DRIFT | plan.md Open Questions — yellow "admin authentication not enabled" banner | `frontend/src/components/shared/AppShell.tsx` has no conditional banner | Plan declares this banner; shell ships without it. |
| V-004 | GAP | spec.md US-3 Edge-2 — unhandled JS error: visible banner, nav stays usable | `frontend/src/router.tsx`, `main.tsx` — no `errorComponent` / error boundary at root | Runtime render errors are not caught at the router shell level. |
| V-005 | STALE | tasks.md T-301 ("10 concurrent POSTs to `/api/admin/settings/update`, last-writer-wins") | `internal/api/adminapi/*_test.go` has no concurrent POST race isolated for this endpoint (writer-level concurrency is covered). | Traceability claim vs code is looser than tasks.md asserts. |
| V-006 | EXTRA / DRIFT | contracts/setup-api.md §commit success `data: { redirect }` | `internal/api/setup/handler.go` ~335–339 also returns `config_version`, `account_id` | Additive, envelope-safe, but not documented in the contract. |
| V-007 | DRIFT | contracts/setup-api.md §GET status "no errors expected" | `internal/api/setup/handler.go` ~117–129 returns HTTP 500 on `ProbeState` error | Intentional; contract copy predates the decision. |
| V-008 | STALE | data-model.md §Validation references `internal/api/admin/settings.go` | Actual path `internal/api/adminapi/settings.go` | Package rename not reflected in data-model. |
| V-009 | DRIFT | plan.md §Module table — same `admin/settings.go` path | Actual: `adminapi/` | Cosmetic path drift in plan. |

### Pending Markers
- `[CONFIRM]` (L2) in source: **0**.
- `[HUMAN]` (L3) in source: **0**.
- `tasks.md` T-028 text contains a `[HUMAN]` verification note; it is
  process documentation (BuildApp ordering review) and has no matching
  code placeholder.

---

## Test Gaps (informational — spec allows manual verification)

- US-1 Edge-3 — no automated test for the "no healthy accounts" banner (tied to V-001).
- US-3 Edge-1 — no test that the `<noscript>` fallback renders (only `index.html` inspection).
- US-3 Edge-2 — no render-throw / error-boundary test (tied to V-004).
- US-4 AC-1 — no wall-clock assertion that settings save completes within 2 s.
- T-301 — no explicit concurrent POST race test (tied to V-005).
- NFR Perf P95 for wizard steps / commit / settings — manual only per plan; no CI benchmark.
- NFR Reliability "crash during settings save" — writer is atomic (tmp+fsync+rename) and has concurrency tests, but no explicit SIGKILL-mid-write simulation.

## Out-of-Scope Leak

None material. Checked:
- Admin auth / client API keys / Prometheus: not enforced as product features (intents + plugin seam only). ✅
- OpenAPI for 002 admin API: not present. ✅
- Clustering / driver-migration / plugin marketplace: not implemented. ✅
- KPI dashboard (`v9-dashboard.html`): not shipped (reference mock only). ✅
- i18n: frontend may contain scaffolding; no localized wizard surface. ✅

---

## Recommendations

### V-001 — "no healthy accounts" banner on admin home (Major, GAP)
- **Spec says**: US-1 Edge-3 — "admin home page shows a 'no healthy accounts' banner".
- **Code does**: renders a static "Setup complete" card; no banner.
- **Recommendation**:
  - [ ] **A. Fix code** — drive the banner off `/api/admin/health` (already returns `status: degraded|unhealthy`) in a follow-up small task; ≤ 30 LOC frontend change.
  - [ ] **B. Update spec** — descope the banner to 005 (observability) with justification that 002's admin home is explicitly "minimum to prove the shell".
  - [ ] **C. Defer** — document as known gap; revisit when operators report it.

### V-002 — `impeccable` CI gate wording (Minor, DRIFT)
- **Spec/plan says**: design-lint via `impeccable detect` is a CI gate.
- **Code does**: `ci.yaml` runs lint/test/build only. T-502 was removed.
- **Recommendation**:
  - [ ] **A. Update spec** (preferred) — strike "CI gate" language from §Clarifications and plan R-8; refer to local run only.
  - [ ] **B. Fix CI** — add an `impeccable detect` job (low-cost, parallelizable).

### V-003 — Unauthenticated-admin banner (Minor, DRIFT)
- **Plan says**: Open Questions — "show a yellow banner when admin auth is not enabled".
- **Code does**: no banner in `AppShell`.
- **Recommendation**:
  - [ ] **A. Update plan** — mark deferred until 003 (admin auth plugin).
  - [ ] **B. Fix code** — 1-file change in `AppShell.tsx`.

### V-004 — React root error boundary (Minor, GAP)
- **Spec says**: US-3 Edge-2 — "a visible error banner replaces the content area without freezing the navigation".
- **Code does**: no root `errorComponent` / React error boundary.
- **Recommendation**:
  - [ ] **A. Fix code** — add a root `errorComponent` on the TanStack router + a small error boundary around the portal outlet.
  - [ ] **B. Narrow spec** — "best-effort" degrade; rely on browser default.

### V-005 — Concurrent settings test claim (Minor, STALE)
- **Tasks say**: T-301 verify includes "10 concurrent POSTs, last-writer-wins".
- **Code has**: writer-level concurrency tests, no handler-level race harness.
- **Recommendation**:
  - [ ] **A. Update tasks.md** — restate T-301 verify as "writer concurrency (covered); handler-level race deferred".
  - [ ] **B. Fix tests** — add a small `httptest`-driven race.

### V-006 — Commit response extras (Minor, EXTRA)
- **Contract says**: `data: { redirect }`.
- **Code returns**: `{ redirect, config_version, account_id }`.
- **Recommendation**:
  - [ ] **A. Update contract** — document `config_version`, `account_id` as optional, additive fields (envelope-safe).
  - [ ] **B. Fix code** — drop the extras.

### V-007 — Setup status error mode (Minor, DRIFT)
- **Contract says**: "No errors expected" for `GET /api/setup/status`.
- **Code does**: HTTP 500 envelope when `ProbeState` fails (transient stat errors).
- **Recommendation**:
  - [ ] **A. Update contract** (preferred) — allow `500` envelope for probe failure; operators already rely on this behaviour.
  - [ ] **B. Fix code** — swallow stat errors and always return 200.

### V-008 / V-009 — Stale module paths (Minor, STALE)
- **Spec/plan references**: `internal/api/admin/settings.go`.
- **Actual path**: `internal/api/adminapi/settings.go` (package rename).
- **Recommendation**: **Update spec + plan** with the current path (cosmetic).

---

## Proposed Decisions (default stance)

Unless the operator overrides:

- V-001: **Update spec** (descope banner to 005; admin home stays minimal for 002).
- V-002: **Update spec/plan** (remove "CI gate" language; T-502 was explicitly dropped).
- V-003: **Update plan** (defer unauthenticated banner to 003).
- V-004: **Fix code** (cheap, adds real safety).
- V-005: **Update tasks.md** (T-301 verify wording).
- V-006: **Update contract** (document extras).
- V-007: **Update contract** (document 500 on probe failure).
- V-008 / V-009: **Update spec/plan paths** (cosmetic).

---

## Closed-loop action plan

1. Apply the "Update spec / plan / tasks / contracts" edits above (pure doc drift).
2. Optionally implement V-004 (root error boundary) as a ≤ 30 LOC follow-up.
3. After edits land, re-run a read-only pass to confirm no cascade.
4. Update `specs/sdd/state.json` → phase `VERIFIED`.

---

## Post-fix reconciliation — 2026-04-19 PM

Operator decisions applied:

| ID | Decision | Landed change |
|----|----------|---------------|
| V-001 | **Fix code (A)** — ≤30 LOC frontend banner driven off `/api/admin/health`. | `frontend/src/routes/admin/index.tsx` now queries `/api/admin/health` with TanStack Query and renders an `AlertTriangle` banner whenever `health.status !== 'healthy'`. |
| V-002 | **Update spec/plan (A)** — remove "CI gate" language. | `spec.md` §Clarifications, `plan.md` R-8, `research.md`, `quickstart.md`, and `checklists/spec-quality.md` now describe `impeccable detect` as a local spot-check with the CI gate tracked as follow-up. |
| V-003 | **Update plan (A)** — defer unauthenticated-admin banner to 003. | `plan.md` Open Question #1 rewritten — banner deferred to 003 alongside the admin-auth plugin. |
| V-004 | **Fix code (A)** — root error component. | `frontend/src/router.tsx` gained `RouteErrorFallback` wired into `rootRoute.errorComponent`. |
| V-005 | **Update tasks.md (A)** — restate T-301 verify. | `tasks.md` T-301 verify note + traceability matrix row for "US-4 Edge-1" updated: writer-level `-race` test is the real gate; handler-level harness deferred. |
| V-006 | **Update contract (A)** — document extras. | `contracts/setup-api.md` §Commit success now lists `config_version` + `account_id` as additive envelope-safe fields. |
| V-007 | **Update contract (A)** — allow 500 on `ProbeState` failure. | `contracts/setup-api.md` §GET status now documents `2900 setup_probe_failed` + `-1 unknown_error` as possible HTTP 500 envelopes. |
| V-008 / V-009 | **Update spec/plan paths (A)** — `internal/api/admin/` → `internal/api/adminapi/`. | `data-model.md`, `plan.md`, `tasks.md`, and `research.md` references updated. URL paths (`/api/admin/*`) unchanged. |

All automated checks still green (`go test -race ./...`, `golangci-lint run`, `pnpm lint`/`typecheck`/`vitest`, 4/4 Playwright E2E, `make smoke-brownfield`). State machine: advanced to `VERIFIED`.

---

## Addendum — 2026-04-15 (scope relax: optional `first_account` + SQLite probe-skip)

Operator feedback: in routine installs the wizard should let operators defer
upstream-account seeding to the admin portal (avoid entering a production API
key in the setup step just to unblock the gate), and SQLite installs should
not require a pointless `probe-dsn` round-trip (the file is created at migrate
time anyway). Both changes stay inside 002 — no new feature spec, no ADR.

### Scope delta

- **`first_account` is now optional** on `POST /api/setup/commit`. Empty block
  → commit runs migrations and writes `config.json`; `upstream_accounts`
  stays empty; `/api/admin/health` reports `degraded` and the admin dashboard
  surfaces the V-001 "no healthy accounts" banner until the operator seeds an
  account via `POST /api/admin/accounts`. Partial payloads (present block
  missing required scalars) are still rejected per-field — `2004
  invalid_account_name` / `2015 invalid_account_provider` / `2005
  invalid_api_key` / `2016 invalid_base_url` (see `internal/api/errcode/codes.go`).
- **SQLite skips `probe-dsn`**. The wizard UI hides the "Test" button and
  advances to step 2 on click; the commit handler's "prior successful probe"
  pre-condition is relaxed for `sqlite3` (file-based DSN; connectivity is
  checked when migrations run). Postgres / MySQL unchanged — button text
  renames from "Probe" to "Test" but the two-phase UX is preserved.

### Artefacts landed

| Artefact | Change |
|---|---|
| `spec.md` | AC-4 added (skip-account flow); new edge-case entry for "operator skips upstream-account seeding"; FR-003 marks the seed account OPTIONAL; FR-004 splits DSN validation by server vs file driver. |
| `data-model.md` | New subsection "Skipping the first upstream account (setup-api.md v2.4)"; top-level growth note updated to "0 or 1 row per commit". |
| `contracts/setup-api.md` | Request table: `first_account` required=no; `db.driver` note adds sqlite3 exemption. Response section: `account_id` documented as omitted when seeding was skipped; `setup_committed` log carries `account_seeded`. Changelog v2.4 added. |
| `plan.md` | Data-flow US-1 step 3 updated to `IF req.first_account is non-empty INSERT ELSE skip`; commit response `data` annotated with optional `account_id`. |
| `internal/setup/validator.go` | `AccountRequestBlock.IsEmpty()`; `Commit` validator walks first-account rules only when the block is non-empty. |
| `internal/setup/commit.go` | Account INSERT wrapped in `if !req.FirstAccount.IsEmpty()`; `CommitResult.AccountID == 0` propagated; structured log gains `account_seeded`. |
| `internal/api/setup/handler.go` | Response omits `account_id` when `result.AccountID == 0`; `setup_committed` log attr `account_seeded` added. |
| `frontend/src/routes/setup/wizard.tsx` | `accountSkipped` state + "Skip for now" CTA + "Register a key instead" undo CTA; account-step form bypassed when skipped; review-step body + "Account" row both branch on skip state; SQLite path skips `probe-dsn` entirely; non-SQLite button reads "Test" / "Testing…"; `⌘ T` handler wired to `probeDsn()` (only fires on the Database step for non-SQLite drivers), rail shortcut list suppresses `⌘ T` row on SQLite; DB error banner reads "Database test failed" (renamed from "Probe failed"); API-key hint corrected to reflect plaintext-at-rest in 002 (encryption deferred to a future spec). |
| Tests | New: `internal/setup/validator_test.go TestValidator_Commit_FirstAccountOptional`, `internal/setup/commit_test.go TestCommit_SkipsFirstAccount`, `internal/api/setup/handler_test.go TestCommit_SkipsFirstAccount`, `frontend/tests/e2e/wizard-skip-account.spec.ts`. Updated: `wizard-happy-path`, `wizard-defaults`, `portal-shell` adapted to sqlite3 no-probe path. |

### Verification (2026-04-15)

- `go test -race ./...` — PASS (new unit tests included).
- `pnpm --dir frontend lint`, `pnpm --dir frontend typecheck`, `pnpm --dir frontend test` — PASS.
- Playwright E2E — 5/5 PASS (`wizard-happy-path`, `wizard-defaults`, `wizard-skip-account`, `portal-shell-3.1`, `portal-shell-3.2`).
- Manual smoke: fresh install → wizard skip flow → `/admin/` renders the degraded banner → `POST /api/admin/accounts` clears it.

### Constitution / invariants check

- **Setup-completion marker** (`config.json` file presence): unchanged — commit still goes DB-first / file-last; the only difference is that the DB tx may be a no-op schema migration.
- **`/v1/*` no-envelope rule**: unchanged — absence of a seed account maps to the existing `503 no_available_account` proxy native-shape error (`internal/api/proxy.go` → `ErrCodeNoAvailableAccount`); the gate passes (setup is done), the downstream selector rejects. No new error code is introduced.
- **Crash-window invariants** (R-1): unchanged — see `data-model.md` §"Skipping the first upstream account" for the derivation (step (b) becomes additive-absent; file-last publish is the single observable moment as before).
- **Envelope discipline**: new response omits `account_id` for setup commit; existing envelope clients ignoring unknown keys are unaffected.

### Residual drift

None. Spec, plan, data-model, contract, code, and tests agree. Traceability matrix unchanged (AC-4 and the new edge case inherit from US-1 / FR-003 which were already tracked).
