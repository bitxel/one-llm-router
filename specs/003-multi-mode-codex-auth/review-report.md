# Code Review Report: 003 Multi-mode Codex Authentication

**Feature**: `003-multi-mode-codex-auth`  
**Date**: 2026-04-23  
**Reviewer**: AI (`sdd-review`) + multi-pass focused subagent reviews across Phases 3–10  
**Status**: ✅ APPROVED (post-fix)

> Automated baseline is green. This review records the final cross-layer
> judgment after the implementation reviews and bug-fix passes that landed
> across setup wizard handoff, browser OAuth, device OAuth, import/export,
> reauth, proxy refresh, and the Phase 10 release gates.

---

## Summary

| Category | Critical | Major | Minor | Info |
|----------|----------|-------|-------|------|
| Correctness | 0 | 0 | 0 | 1 |
| Security / Privacy | 0 | 0 | 0 | 1 |
| Contract / Spec | 0 | 0 | 0 | 1 |
| Test / CI Quality | 0 | 0 | 0 | 1 |
| **Total** | **0** | **0** | **0** | **4** |

### Verdict

- Critical findings: 0
- Major findings: 0
- Minor findings: 0

Per the project threshold, 003 is **APPROVED**.

## Automated Baseline

| Gate | Result |
|------|--------|
| `go test ./...` | PASS |
| `golangci-lint run` | PASS |
| `pnpm -C frontend test` | PASS |
| `pnpm -C frontend typecheck` | PASS |
| `pnpm -C frontend lint` | PASS |
| `pnpm -C frontend build` | PASS |
| `make go-build` | PASS |
| `pnpm -C frontend test:e2e` | PASS |
| `bash scripts/envelope-parity.sh` | PASS |
| `bash scripts/coverage-floor.sh` | PASS |
| `bash scripts/migration-rollback-test.sh` | PASS |
| `bash scripts/log-scrub.sh frontend/test-results` | PASS |

## What Was Reviewed

The final review covered:

- setup wizard parity: four-mode cold-install entrypoint, deferred-mode
  post-commit handoff, and preserved 002 commit semantics
- browser OAuth flow lifecycle: start, loopback/manual rails, flow polling,
  cancel, CAS behavior, expiry, and app wiring
- device OAuth lifecycle: start, provider polling, shutdown behavior, frontend
  device UX, and route wiring
- import/export lifecycle: `auth.json` import validation, export read path,
  round-trip fidelity, and audit behavior
- reauth behavior: API-key parity, OAuth overwrite-in-place, deleted-row races,
  and frontend inline mismatch/error surfacing
- refresh-before-forward: selector hook, app wiring, proxy bearer swap,
  fallback/error semantics
- release gates: envelope parity, malformed probe suite, rollback usability,
  coverage floor, Playwright harness fail-fast, and log scrub

## Findings Fixed Before Approval

These were the highest-signal review findings encountered during 003 and fixed
before this approval:

1. **Admin OAuth contract mismatches**
   - system failures that were collapsing to `500 / -1 unknown_error` were
     mapped back to the registered 003 system codes (`3900`, `3901`, `3902`)
   - cancel/manual-callback race cases stopped reporting false success

2. **Phase-surface exposure drift**
   - unsupported onboarding routes were gated until their backend support
     existed, preventing dead-end UI paths during intermediate phases

3. **Refresh hook miswire risk**
   - OAuth proxying no longer has a silent-success path when hook wiring is
     missing or wrong; app-level tests now prove `/v1/*` uses the OAuth access
     token path correctly

4. **Device-flow correctness gaps**
   - device start now fails fast when a flow is already in progress
   - shutdown fences prevent late callback/poller writes after `App.Stop()`
   - semantic provider terminal errors no longer retry like transport noise

5. **Phase 10 release-gate weaknesses**
   - envelope parity is now required at probe-row granularity, not just
     per-operation coverage
   - rollback verifies usability of the rolled-back 002 path, not just schema
     reversibility
   - Playwright fixtures now fail fast on real browser exceptions
   - reauth negative UI paths are exercised through the real DOM flow, not only
     via direct backend requests

6. **Reopened setup-entrypoint scope drift**
   - the first-install wizard no longer misrepresents 003 as an admin-only
     onboarding feature
   - cold-install now exposes the same four auth modes as the admin picker and
     hands deferred modes directly into the matching 003 onboarding routes

## Informational Notes

- `frontend build` still produces the pre-existing Google Fonts `@import`
  ordering warning in `frontend/src/styles/globals.css`; it is not a 003
  correctness issue and does not affect approval.
- `tasks.md` now documents `internal/api/envelope_parity_test.go` as
  `package api_test`, which is the correct black-box shape for the export-route
  parity gate.
- No source-level `[CONFIRM]` / `[HUMAN]` markers remain in 003 implementation
  files.
- No open blocker remains in the Phase 10 quality gates; the remaining risk is
  normal regression risk covered by the landed CI suite.

## Approval Decision

003 is approved for closeout because:

1. The feature meets the implemented spec surface for browser/device/import/
   export/reauth/refresh behavior, including the reopened setup-entrypoint
   scope.
2. The final test and CI gates cover both runtime behavior and the feature’s
   release-risk areas.
3. No unresolved blocker or major issue remains after the final review passes.
