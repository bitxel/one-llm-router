# Test Plan: Account Playground

**Feature**: 004-account-playground
**Created**: 2026-04-23
**Test Framework**: Go `testing`/`testify`, SQLite in-memory integration tests, Vitest/RTL, Playwright
**Status**: Ready; product decisions are resolved in `plan.md`

This plan is derived from `spec.md`, `contracts/playground-api.md`, and `quickstart.md`. Tests must be written from the spec first, not from implementation details.

## Test Layers

### Backend Contract and Integration Tests

| Test ID | Source | Target | Description | Priority |
|---------|--------|--------|-------------|----------|
| CT-001 | FR-010, FR-011, Contract | `internal/api/playgroundapi` | `POST /api/admin/playground/run` success returns HTTP 200 envelope, `code=0`, non-null `data`, selected account summary, latency, upstream status, output, and usage | P0 |
| CT-002 | Contract, FR-011 | `internal/api/playgroundapi` | Malformed JSON and wrong content type return HTTP 200, `2008 malformed_body`, `data={}` | P0 |
| CT-003 | Contract, FR-003 | `internal/api/playgroundapi` | Oversized JSON body returns HTTP 200, `2009 request_body_too_large`, `scope=envelope`, `limit_bytes=98304` | P0 |
| CT-004 | AC-3.3, FR-003 | `internal/api/playgroundapi` | Empty, over-limit, invalid mode, missing account id, invalid model, invalid session key, and invalid max output return `4001 invalid_playground_request` with field data and no upstream call | P0 |
| CT-005 | AC-1.3, FR-005 | `internal/api/playgroundapi` | Automatic mode with no active accounts returns `4002 playground_no_active_account` and no upstream call | P0 |
| CT-006 | AC-2.3, FR-006, FR-007 | `internal/api/playgroundapi` | Explicit missing/disabled/deleted/ineligible account returns `4003 playground_account_unavailable` and never falls back | P0 |
| CT-007 | AC-3.4, FR-014, FR-015 | `internal/api/playgroundapi` | Provider non-2xx returns `4004 playground_upstream_error` with sanitized diagnostics and selected account summary when known | P0 |
| CT-008 | NFR Timeout Recovery | `internal/api/playgroundapi` | Upstream timeout returns `4005 playground_upstream_timeout`, preserves account summary when known, and records timeout outcome | P0 |
| CT-009 | EC-3 malformed response | `internal/api/playgroundapi` | Upstream HTTP 2xx malformed JSON returns `4006 playground_response_malformed` without fabricated output | P0 |
| CT-010 | EC-3 too-large response | `internal/api/playgroundapi` | Upstream HTTP 2xx over response cap returns `4007 playground_response_too_large` with cap metadata | P0 |
| CT-011 | FR-011 | `internal/api/playgroundapi` | Unexpected service failure returns HTTP 500, `4900 playground_internal_error`, `data={}` | P0 |
| CT-012 | FR-011, Security | `internal/api/playgroundapi` | `X-Request-Id` appears only as a response header and is absent from `data` and `msg` on success and failures | P0 |
| CT-013 | T-004 | `internal/api/testutil/admin_probe_matrix.go` | Envelope parity matrix contains required `playgroundRun_*` probes and declared OpenAPI operation is covered | P0 |
| IT-001 | AC-1.1, AC-1.4 | `internal/core` | Automatic mode selects one active eligible account and applies existing refresh-before-use hook | P0 |
| IT-002 | AC-1.3, EC-1 list changes | `internal/core` | Automatic mode uses current backend eligibility, not stale browser state | P0 |
| IT-003 | AC-1.4, AC-2.4 | `internal/core` | OAuth refresh permanent failure follows existing policy and surfaces sanitized account failure | P0 |
| IT-004 | AC-2.2, FR-006 | `internal/core` | Account mode uses exactly the requested active account on success | P0 |
| IT-005 | AC-4.1, FR-018 | `internal/core` | Observability/log fields include request id, mode, selected/requested account, auth method, upstream status, latency, and outcome | P0 |
| IT-005a | FR-008, FR-022 | `internal/provider/openai` | API-key Playground runs call `base_url + /v1/responses` without `chatgpt-account-id`; OAuth Playground runs call ChatGPT Codex `/codex/responses` with `chatgpt-account-id` when present | P0 |
| IT-006 | AC-4.2, AC-4.3, FR-019 | `internal/core` | Request/response body persistence obeys runtime body logging toggles | P0 |
| IT-007 | AC-4.4, FR-020 | `internal/core`, `scripts/log-scrub.sh` | Stored fake API keys and OAuth token bytes are absent from responses, logs, request-history metadata, and deliberate scrub fixtures fail when unsanitized | P0 |
| IT-008 | EC-4 client cancel | `internal/core` | Request context cancellation while upstream is blocked records `outcome=cancelled` and leaks no partial credential state | P0 |

### Backend Unit Tests

| Test ID | Source | Module | Description | Priority |
|---------|--------|--------|-------------|----------|
| UT-001 | Contract Request | `internal/core` | Validate auto/account request fields, trim behavior, exact 16,000-character prompt boundary including non-ASCII input, max-output default/range, session-key range | P0 |
| UT-002 | EC-2 stale account | `internal/core` | `AccountSelector.SelectByID` rejects non-positive, missing, disabled, deleted, and pre-forward failures with distinguishable errors | P0 |
| UT-003 | AC-3.2, AC-3.5 | `internal/provider/openai` | Build non-streaming Responses request and extract text from `output_text` and nested `output[].content[].text` | P0 |
| UT-004 | AC-3.5 | `internal/provider/openai` | Safe success with no extractable text returns `text_available=false` and no fabricated output | P0 |
| UT-005 | Security | `internal/provider/openai` / `internal/core` | Raw JSON and provider messages are bounded and sanitized according to the contract | P0 |
| UT-006 | Error registry | `internal/api/errcode`, `frontend/src/lib/errcode.ts` | Go and TS registries expose 4001..4007 and 4900 with matching symbols | P0 |

### Frontend Unit / Interaction Tests

| Test ID | Source | Module | Description | Priority |
|---------|--------|--------|-------------|----------|
| FE-001 | FR-001 | `frontend/src/router.tsx`, `Sidebar` | `/admin/playground` renders in the portal shell; sidebar active state and breadcrumb are correct | P0 |
| FE-002 | AC-1.2 | `playground.tsx` | No active accounts renders empty state, disables Run, and links to account onboarding | P0 |
| FE-003 | AC-2.1, FR-009 | `playground.tsx` | Active account picker labels show name, provider, status, auth method, email, and plan; no secret fields render | P0 |
| FE-004 | AC-3.3 | `playground.tsx` | Empty/over-limit text and missing account selection validate inline without API calls and preserve focus | P0 |
| FE-005 | AC-3.1, FR-017 | `playground.tsx` | Pending state disables duplicate submit; double-click/Enter-repeat calls API once and announces status | P0 |
| FE-006 | AC-3.2, FR-012 | `playground.tsx` | Success renders account, selection mode, output text, latency, upstream status, usage, and safe raw area only when available | P0 |
| FE-007 | AC-3.5, FR-013 | `playground.tsx` | No-extractable-text renders explicit no-text state and never invents response text | P0 |
| FE-008 | AC-3.4, FR-015, FR-016 | `playground.tsx` | 4003/4004/4005 errors render distinct states, prompt/mode are preserved, correlation id is shown from the error object | P0 |
| FE-009 | US-4, FR-020 | `playground.tsx` | UI does not render fake API key/OAuth/access/refresh/id token bytes from account metadata, raw response, or error details | P0 |
| FE-010 | US-5 | `playground.tsx` | Account repair links go to detail surfaces and no mutation actions are present on Playground | P1 |
| FE-011 | US-2 Edge | `playground.tsx` | Account-list load failure shows non-blocking error with correlation id, preserves prompt, and does not block Automatic mode submit | P1 |
| FE-012 | NFR Accessibility | `playground.tsx` | Keyboard reaches all controls; loading/success/error states use status/alert regions; validation returns focus | P0 |
| FE-013 | US-5, FR-023 | `playground.tsx` | Integration guide opens as a simplified modal layer, exposes cURL/Python/JavaScript/Go tabs, uses current-origin `/v1/responses`, keeps the API-key placeholder empty, renders cURL as a single command without shell variables, omits secondary metadata/response-mode summary chrome, and states OAuth `stream:false` JSON / `stream:true` SSE behavior | P1 |

### Playwright E2E Tests

| Test ID | Source | Flow | Description | Priority |
|---------|--------|------|-------------|----------|
| E2E-001 | Quickstart 1 | Account mode happy path | Seed an active account and fake upstream, run Account mode, verify selected account, output, latency, usage, no secrets, no console errors | P0 |
| E2E-002 | Quickstart 2 | Automatic mode happy path | Run Automatic mode and verify selected account is shown and route remains inside `/api/admin/playground/run` | P0 |
| E2E-003 | Quickstart 3 | No active accounts | Disable/delete all accounts, open Playground, verify empty state and disabled Run; direct API returns `4002` | P0 |
| E2E-004 | Quickstart 4/6 | Stale selected account / provider error | Select account, make it ineligible or make fake upstream return non-2xx, verify no fallback, safe error, correlation id, no secrets | P0 |
| E2E-005 | Quickstart 7 | No extractable text | Fake upstream returns success JSON without text; UI shows no-text state and no fabricated text | P0 |

## Edge Case Coverage

| Edge Case | Covered By |
|-----------|------------|
| US-1 double-click / repeated Enter | FE-005, E2E-001 |
| US-1 account list changes while open | IT-002, E2E-004 |
| US-1 upstream non-success | CT-007, FE-008, E2E-004 |
| US-1 sticky session value | IT-001, CT-001 |
| US-2 stale disabled account | UT-002, CT-006, E2E-004 |
| US-2 account metadata cannot load | FE-011 |
| US-2 account mode missing account | CT-004, FE-004 |
| US-2 secret material in picker/result | FE-003, FE-009, IT-007 |
| US-3 navigate away while pending | FE-005, E2E smoke with no portal-wide error |
| US-3 edit after error | FE-008 |
| US-3 malformed/too-large upstream body | CT-009, CT-010 |
| US-3 provider text may contain operator input | UT-005, CT-007, FE-008 |
| US-4 client cancel | IT-008 |
| US-4 concurrent operators | CT-012, E2E-001/E2E-002 request id checks |
| US-4 explicit account fails before resolution | CT-006, IT-005 |
| US-5 multi-turn requested | FE-010, E2E-001 single-turn assertion |
| US-5 streaming requested | CT-001 verifies non-streaming JSON; FE-006 waits for terminal result |
| US-5 client key / routing policy simulation requested | FE-010 verifies no such controls |
| US-5 non-localhost router origin | FE-013 current-origin assertion |

## Completeness Verification

| User Story | Total AC | AC Covered | Total EC | EC Covered | Status |
|------------|----------|------------|----------|------------|--------|
| US-1 | 4 | 4 | 4 | 4 | PASS |
| US-2 | 4 | 4 | 4 | 4 | PASS |
| US-3 | 5 | 5 | 4 | 4 | PASS |
| US-4 | 4 | 4 | 3 | 3 | PASS |
| US-5 | 4 | 4 | 4 | 4 | PASS |
| **Total** | **21** | **21 (100%)** | **19** | **19 (100%)** | **PASS** |

## Coverage Target

- P0 acceptance criteria: 100% automated coverage required before release.
- P1 acceptance criteria: at least 80% automated or documented manual coverage required before release.
- Edge cases: at least 70% coverage required; current plan targets 100%.
- New backend packages must pass the repository coverage floor once added to `scripts/coverage-floor.sh`.
- E2E must record pass/fail evidence and must check console errors plus unexpected network 4xx/5xx for every scenario.
