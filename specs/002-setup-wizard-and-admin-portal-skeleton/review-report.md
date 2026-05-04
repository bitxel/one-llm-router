# Code Review Report: 002 Setup Wizard and Admin Portal Skeleton

**Feature**: `002-setup-wizard-and-admin-portal-skeleton`
**Date**: 2026-04-19
**Reviewer**: AI (sdd-review) + 1 explore subagent (staff-level parallel read)
**Status**: ✅ APPROVED (post-fix)

> Automated baseline is green for the entire commit range. The review
> below targets what lint and tests cannot catch (cross-layer
> semantics, logging policy, spec edge cases, contract drift). The
> nine findings already in `verify-report.md` are NOT re-filed here.

## Summary

| Category      | Critical | Major | Minor | Info |
|---------------|----------|-------|-------|------|
| Code Quality  | 0        | 0     | 1     | 1    |
| Security      | 0        | 0     | 2     | 1    |
| Performance   | 0        | 0     | 0     | 1    |
| Correctness   | 0        | 1→0   | 1     | 0    |
| Constitution  | 0        | 0     | 0     | 1    |
| **Total**     | **0**    | **0** | **4** | **4** |

### Verdict
- **Critical findings**: 0
- **Major findings**: 0 (1 landed + fixed this pass: RV-011)
- **Minor findings**: 4 (deferred with justification)

Per thresholds (APPROVED = 0 Critical, 0 Major), **APPROVED**.

---

## Automated Analysis Results

| Gate | Result |
|------|--------|
| `golangci-lint run ./...` | 0 issues |
| `go test -race ./...` | 16 packages OK (incl. new `TestBuildApp_SteadyState_DegradedWhenNoActiveAccounts`) |
| `pnpm lint` (biome) | 40 files, 0 fixes |
| `pnpm typecheck` | 0 errors |
| `pnpm test` (vitest) | 4 files, 19 tests passed |
| Playwright E2E | 4 tests / 4 green (wizard-happy-path, wizard-defaults, portal-shell AC-3.1/3.2) |
| `make smoke-brownfield` | OK |

---

## Findings

### Critical
*(none)*

### Major

#### [RV-011] Correctness / Spec: health always-healthy path defeats the US-1 Edge-3 banner (FIXED THIS PASS)
- **File**: `internal/app/app.go` `makeHealthHandler` (prev. ~777-797), `frontend/src/routes/admin/index.tsx` (~33-39)
- **Issue**: steady-state `/api/admin/health` called `Count(&UpstreamAccount{})` and unconditionally set `data.status = "healthy"` whenever the DB was reachable. `HealthService.GetHealth` (which returned `no_capacity` when `active == 0`) was wired to the wrapped-admin handler but **not** to the mux route; the mux used the `App`-local builder. Net effect: after V-001 added the "no healthy accounts" banner (`health.status !== 'healthy'`), the banner could only fire when the DB was outright unreachable — it could never fire for the spec's actual scenario (upstream key rejected → account disabled → zero active rows).
- **Impact**: US-1 Edge-3 silently unmet. The frontend fix landed in the prior pass without a matching backend signal.
- **Fix (applied)**:
  - `makeHealthHandler` now runs two scoped xorm counts (`status = 'active'`, `status = 'disabled'`) and maps `active == 0` → `"degraded"` (keeping the 3-state contract documented in `contracts/admin-api.md §Health endpoint`). `data.active_accounts` and `data.disabled_accounts` are additive envelope-safe fields surfaced for the frontend banner.
  - `TestBuildApp_SteadyState_RunsMigrationsAndHealthy` now seeds one active row via raw SQL against the migrated DB (test was previously asserting "healthy" with zero accounts, which was the bug the production path mirrored).
  - New `TestBuildApp_SteadyState_DegradedWhenNoActiveAccounts` pins the Edge-3 signal: zero-active ⇒ `degraded`.
  - `frontend/src/routes/admin/index.tsx` `AdminHealth.status` narrowed to `'healthy' | 'degraded' | 'unhealthy'` to match the contract (removed stale `'no_capacity'`).

### Minor

#### [RV-012] Security / Observability: setup-commit failures log full wrapped error at INFO
- **File**: `internal/api/setup/handler.go` ~501-505 (`writeCommitFailure`)
- **Issue**: `logger.Info("setup commit failed", ..., "error", cf.Err)` can surface driver-level strings containing hostnames or socket paths from wrapped `pq`/`modernc`/`migrate` errors.
- **Impact**: INFO log sinks may retain more topology detail than intended; weakens parity with plan.md §362 DSN-redaction policy.
- **Recommendation (deferred)**: log a short stable symbol at INFO; gate raw `cf.Err` behind DEBUG; or route through a DSN-stripping scrubber. Scheduled for the next logging polish pass so it can land with 003's operator-auth log-sink review in one change.

#### [RV-013] Security: probe-failure envelope includes raw driver message in `data.hint.message`
- **File**: `internal/api/setup/handler.go` ~234-237 (`ProbeDSN` error branch)
- **Issue**: `probeErr.Error()` is echoed verbatim into the envelope body. The spec/plan already note the wizard is operator-facing on a trusted boundary, but screenshot-style leaks still reveal hostnames/ports.
- **Impact**: Low; topology disclosure on a screen an operator may share.
- **Recommendation (deferred)**: map known error families to curated operator-safe strings; keep raw detail server-side at DEBUG. Documented as next-sprint hardening.

#### [RV-014] Correctness: commit payload silently ignores unknown top-level keys
- **File**: `internal/api/setup/handler.go` ~275-318 (`decodeCommitBody`), validator.go
- **Issue**: Decoder accepts any JSON object; extra keys (`"frist_account": {...}`) are silently dropped. `/api/admin/settings/update` already rejects unknowns with `2012 unknown_config_key`; the commit endpoint does not.
- **Impact**: Low — operators typing wrong keys during a one-shot wizard post would get a false "setup OK" without the expected data landing. Contracts mark unknown as rejectable; code is laxer.
- **Recommendation (deferred)**: add a `json.Decoder.DisallowUnknownFields` path or a key-allowlist check, emitting `2008 malformed_body` on extras. Low-cost but out of scope for this pass since the happy-path client (SPA wizard) never sends extras.

#### [RV-015] Correctness: `SettingsUpdater.SourceMap` stale on post-write `Load` failure
- **File**: `internal/api/adminapi/updater.go` ~133-136
- **Issue**: After `WriteAtomic` + `publisher.Store(next)` succeed, `SourceMap` is only refreshed if `config.Load(...)` also succeeds. A transient IO error between write and reload leaves the live config updated but the provenance map stale, so `2013 env_override_readonly` could mis-fire until restart.
- **Impact**: Very rare (requires disk flap within milliseconds); recoverable by the next successful `Update` or a restart.
- **Recommendation (deferred)**: on `lerr != nil`, log ERROR and either retry or pessimistically invalidate `SourcePublisher` so the guard fails closed. Hot-reload already has a retry on the reader path, so the practical window is narrow.

### Informational / Positive Notes

#### [RV-016] Security / UX: route error fallback renders full `error.message`
- **File**: `frontend/src/router.tsx` ~27-40
- **Note**: not XSS (React text node), but rare third-party throws could appear in screenshots. Keep an eye on whether any downstream hook serialises an Error containing API-key fragments; if that ever shows up, truncate or hide behind a "Details" disclosure.

#### [RV-017] Security: inbound `X-Request-Id` trusted end-to-end
- **File**: `internal/api/middleware.go` ~93-101
- **Note**: By design for 002 (trusted-boundary operator network). Flag for 003 when admin-auth lands and untrusted clients become possible — add length + charset validation then.

#### [RV-018] Performance: admin-home health polls every 15s regardless of tab visibility
- **File**: `frontend/src/routes/admin/index.tsx` ~33-37
- **Note**: `refetchInterval: 15_000` runs in background tabs too. Fine for a single-operator portal; consider a `document.visibilityState` gate later.

#### [RV-019] Constitution / Traceability: file-level task-ID annotations not audited wholesale
- **Note**: Did not exhaustively map every 002-touched file to its task ID in header comments — process compliance only, no runtime defect. Optional follow-up is a script that greps for task-ID mentions in file docstrings.

### Positive notes captured during review
- `setup.Gate` observer-disable + `MarkOpen` pairing with `postCommitReloader` / `buildHandler` is carefully documented and consistent with the race narrative in `app.go` (setup-pending mux vs steady mux).
- `setup.Commit` runs under `Serialiser` and only releases after the post-file reloader returns, matching "DB-first, file-last" semantics; reload failure is surfaced as a system error without rolling back the just-written `config.json` (an operator can re-run setup).
- `config.WriteAtomic` cleans up tmp files on error and creates with `0600`.
- `internal/setup/brownfield.go` never logs raw DSN at INFO (walks through sanitisers).
- `internal/spa/spa.go` uses `embed` + `fs.Sub` + `path.Base`, avoiding classic path traversal; non-GET returns 405.
- `internal/api/recover.go` re-panics `http.ErrAbortHandler`, excludes `/v1/*`, and bounds panic preview; all justified via `//nolint:errorlint` with inline rationale.
- Frontend has no `dangerouslySetInnerHTML`, `eval`, or unsanitised HTML injection; wizard invalidates the probe badge on DSN edits; wizard + admin routes use `lazyRouteComponent`.

---

## Checklist Summary

### Code Quality
- [x] Naming conventions followed
- [x] No file > 3000 lines; largest (`internal/app/app.go` ~850) is well-sectioned with L-001/L-002 invariants called out inline.
- [x] No copy-paste between sibling files.
- [x] Comments explain "why" not "what" (a few long explanatory blocks are justified by invariants).

### Security
- [x] SQL via xorm parameterised (no string concatenation).
- [x] Body caps enforced at the ServeMux layer and per-handler (16 KiB settings, 8 KiB commit).
- [x] DSN not returned by `/api/admin/settings` (contract `db.url` forbidden).
- [x] `0600` perms on `config.json`.
- [x] Gate correctly latches open; allow-list covers only `/api/setup/status`, `/api/admin/health`, `/v1/*` native, and `/setup/*` + `/assets/*` for the SPA shell.
- [ ] ⚠️ Commit-failure INFO log may carry driver strings (RV-012 — deferred).
- [ ] ⚠️ DSN probe error text surfaced in the envelope (RV-013 — deferred).

### Performance
- [x] No N+1 patterns observed in 002 paths.
- [x] Hot-reload uses `atomic.Pointer` for lock-free reads; writer uses tmp+fsync+rename.
- [x] Background retention + recorder use `context.Context` for shutdown.
- [ ] ℹ️ Admin-home polling (RV-018) — informational only.

### Constitution
- [x] Simplicity gate (§2) — no speculative abstraction introduced.
- [x] Anti-abstraction — framework (xorm / `net/http` / React) used directly.
- [x] Integration-first (§7) — real DB integration tests cover commit, gate, brownfield, hot-reload; Playwright exercises the SPA against a live router.
- [x] Test-first (§3) — spec/plan/tasks predate implementation for each phase.

---

## Recommendations

### Must Fix (Critical)
*(none)*

### Should Fix (Major)
*(none — RV-011 landed this pass)*

### Nice to Have (Minor)
1. **RV-012** — scrub / downgrade `cf.Err` from INFO to DEBUG in `writeCommitFailure`.
2. **RV-013** — curate DSN-probe `data.hint.message` to stable operator-safe strings.
3. **RV-014** — reject unknown top-level keys on `/api/setup/commit` with `2008`.
4. **RV-015** — fail-closed `SourceMap` when the post-write `Load` errors in `SettingsUpdater.Update`.

### Informational
- **RV-016** — optional truncation / details-disclosure pattern in `RouteErrorFallback`.
- **RV-017** — revisit trusted `X-Request-Id` echo in 003 (admin-auth).
- **RV-018** — visibility-aware polling if the portal ever runs with many idle tabs.
- **RV-019** — optional traceability-annotation audit script.

---

## State transition

- `specs/sdd/state.json` → `002-setup-wizard-and-admin-portal-skeleton.phase = "REVIEWED"`.
- Next SDD step: **sdd-deliver** (CI/CD artifact packaging).
