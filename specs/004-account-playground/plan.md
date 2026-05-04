# Implementation Plan: Account Playground

**Feature**: 004-account-playground
**Spec**: `specs/004-account-playground/spec.md`
**Created**: 2026-04-23
**Status**: Ready
**Complexity**: complex
**Complexity Reason**: The feature touches both backend and frontend, adds a new OpenAPI Admin API route, changes error-code registries, and modifies more than two modules.
**Complexity Override**: none

## Summary

Add a focused Admin Portal Playground that runs one non-streaming text probe through either the router's normal automatic account selection or one explicitly selected active account. The implementation adds one new Admin API operation, reuses existing account selection/OAuth refresh/provider-transport/request-recording paths, adds a new portal route, and preserves the data-plane proxy's account-specific transport contract: API-key probes use the OpenAI Platform-compatible `/v1/responses` path, while OAuth probes use the ChatGPT Codex backend `/codex/responses` path.

## Technical Context

| Item | Value |
|------|-------|
| Backend | Go 1.25.0, `net/http`, `slog` |
| Persistence | Existing SQLite/PostgreSQL/MySQL through `xorm.io/xorm v1.3.11`; no schema migration planned |
| OpenAPI | `openapi/admin.yaml` remains source of truth for 003+ Admin API routes; `oapi-codegen v2.6.0`, `@hey-api/openapi-ts 0.96.1` |
| Provider call | Existing `internal/provider/openai.Client`; P0 targets Responses-shaped non-streaming text. `RunPlayground` must use Platform-compatible `/v1/responses` for API-key accounts and ChatGPT Codex `/codex/responses` with `chatgpt-account-id` plus `User-Agent: codex_cli_rs/0.120.0 (Mac OS 26.2; arm64) dumb` for OAuth accounts. |
| Frontend | React 19, Vite 6, TypeScript strict, TanStack Router/Query, existing Admin Portal shell |
| Testing | Go tests with `testify v1.11.1`, SQLite in-memory integration tests, Vitest/RTL, Playwright smoke as needed |
| New dependencies | None |

Dependency version verification is recorded in `research.md` §Dependency Versions.

## Resolved Decisions

Confirmed on 2026-04-23:

1. **Prompt length and body cap**: keep the operator-facing prompt limit at 16,000 trimmed characters. Raise the Playground JSON request body cap to 96 KiB so non-ASCII prompts can satisfy the character limit without tripping the transport cap. Tests must include non-ASCII boundary coverage.
2. **Model control**: expose a model input in the Playground UI. The UI default is `gpt-5.4-mini`; the API still requires a non-empty `model` so direct callers must be explicit.
3. **Frontend text system**: follow the existing Admin Portal `.strings.ts` pattern for 004 instead of introducing `react-i18next` in this feature.
4. **Duplicate submit scope**: P0 duplicate protection is UI-only while a run is pending. The Admin API remains non-idempotent and does not accept `client_run_id`.

## Constitution Check

### 第一性原理 Gate

- [X] Starts from the operator problem: account reachability/debugging currently requires external clients.
- [X] Goal is clear: run one safe text probe, identify the selected account, and show safe diagnostics.
- [X] No over-design: P0 excludes streaming, chat history, prompt templates, client-key simulation, and account mutation.
- [X] Shortest path: add one admin operation and one portal route; reuse existing selector, OAuth refresh, forwarder, recorder, and account list projection.

### Simplicity Gate

- [X] Uses the existing router service and frontend workspace; no new deployable sub-project.
- [X] No speculative provider SDK, streaming framework, or separate run-history store.
- [X] No migration unless later implementation proves existing request records cannot distinguish runs, which this plan does not expect.

### Anti-Abstraction Gate

- [X] Uses `net/http`, existing envelope helpers, generated OpenAPI handlers, existing TanStack Query patterns.
- [X] Keeps a single account representation and a transient Playground Run model; no new persistent account subtype.
- [X] Adds only one narrow selector extension for explicit active-account selection rather than a generic policy engine.

### Integration-First Gate

- [X] New Admin API contract is defined in `contracts/playground-api.md` before implementation.
- [X] OpenAPI update and generated Go/TS clients are required in the same implementation PR.
- [X] Integration tests use real SQLite and fake upstream servers; external provider network is mocked.

### Test-First Gate

- [X] Contract tests are planned before handler/service implementation.
- [X] P0 acceptance scenarios map to backend integration tests and frontend interaction tests.
- [X] Security/log-scrub tests cover the no-credential-exposure requirement.

## Architecture

### Module Boundaries

| Module | Responsibility | Change Type |
|--------|----------------|-------------|
| `openapi/admin.yaml` | Add Playground route, request/response schemas, oneOf envelope branches, and operationId | Modified |
| `docs/error-codes.md` | Reassign 4xxx range to Account Playground and register 4001..4007 + 4900 | Modified |
| `internal/api/errcode/` | Add Go constants/symbols/tests for 004 and keep TS parity | Modified |
| `frontend/src/lib/errcode.ts` | Mirror 004 constants and symbols | Modified |
| `internal/core/account_selector.go` | Add explicit active-account selection using the same PreForward credential hook | Modified |
| `internal/core/playground_service.go` | Own validation, selection, provider request construction, response parsing, and request-record creation | New |
| `internal/api/playgroundapi/` | Envelope handler for the Playground run operation | New |
| `internal/provider/openai/` | Add helpers to build Responses request body and extract output text from Responses JSON | Modified |
| `internal/app/app.go` | Wire Playground service/handler in steady state | Modified |
| `internal/generated/adminapi/` | Regenerate from OpenAPI; do not hand-edit generated files | Modified/generated |
| `frontend/src/generated/openapi/` | Regenerate TS client from OpenAPI | Modified/generated |
| `frontend/src/router.tsx` | Add `/admin/playground` route | Modified |
| `frontend/src/routes/admin/layout.tsx` | Add Playground breadcrumb | Modified |
| `frontend/src/components/shared/Sidebar.tsx` | Add live Playground nav item | Modified |
| `frontend/src/routes/admin/playground.tsx` | Playground page: form, picker, run state, result/error panels | New |
| `frontend/src/routes/admin/playground.strings.ts` | Page strings/constants following the existing portal pattern | New |
| `frontend/src/routes/admin/playground.test.tsx` | UI interaction tests | New |
| `scripts/envelope-parity.sh` | Include the new Admin API route in envelope parity gate if route inventory is explicit | Modified if needed |

### Data Flow

**US-1 Automatic run**

```
Operator submits Playground form
 → Frontend generated SDK call via router-api wrapper
 → POST /api/admin/playground/run
 → playground handler validates envelope/request body
 → PlaygroundService validates text/model/selection
 → AccountSelector.Select(ctx, session_key) picks active account and applies PreForward
 → Service builds non-streaming Responses-shaped request
 → openai.Client.RunPlayground sends account-specific upstream call
    ├─ API-key: account.EffectiveBaseURL() + /v1/responses
    └─ OAuth: ChatGPTBackendBaseURL + /codex/responses + chatgpt-account-id when present + fixed Codex CLI User-Agent
 → Service reads bounded JSON body, extracts usage/output/error
 → RequestRecorder records submitted run under /api/admin/playground/run
 → Handler returns admin envelope
 → UI renders selected account, outcome, output/error, latency, usage
```

**US-2 Explicit account run**

```
Operator chooses active account in picker
 → Frontend submits selection_mode=account + account_id
 → PlaygroundService calls AccountSelector.SelectByID(ctx, account_id)
 → Selector loads row, requires status=active, applies PreForward
 → Same provider forward/read/record/response path as Automatic
 → If row is missing/disabled/deleted, service returns playground_account_unavailable and never auto-falls back
```

**US-3 No extractable text**

```
Provider returns HTTP 200 JSON
 → Service parses bounded JSON
 → Output extractor checks known Responses text locations
 → No text found
 → Result outcome=no_extractable_text, text_available=false, raw response included only if safe/bounded
```

**US-4 Observability**

```
Every valid submitted run
 → structured log with request_id, selection_mode, account_id when known, auth_method when known, status/outcome/latency
 → RequestRecorder record with path=/api/admin/playground/run and body fields controlled by runtime logging toggles
 → log-scrub tests assert no credential bytes
```

### Trade-off Analysis

| Decision | Chosen | Rationale |
|----------|--------|-----------|
| Browser boundary | Browser calls Admin API, not `/v1/*` | Keeps the client data-plane facade untouched and avoids custom account-selection headers |
| Provider operation | Responses-shaped API, non-streaming | Tests the Codex-relevant path and fits the Admin API envelope; provider transport is account-specific per Feature 003 |
| Explicit account selection | Selector extension, active-only | Reuses credential refresh and prevents policy bypass |
| Error range | 4xxx for Account Playground | Aligns state/spec feature number with registry; old admin-auth feature number references become stale |
| Request history | Reuse existing `request_records` path | Distinguishes Playground by path without a migration |
| Raw response | Bounded safe JSON only | Gives debugging detail without unbounded payloads or fabricated facts |
| Streaming | Deferred | Would require separate response semantics and UI state |

## Project Structure

```text
specs/004-account-playground/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
└── contracts/
    └── playground-api.md

openapi/admin.yaml
docs/error-codes.md
internal/api/errcode/
internal/api/playgroundapi/
internal/core/playground_service.go
internal/core/account_selector.go
internal/provider/openai/usage.go
internal/provider/openai/playground.go
internal/app/app.go
frontend/src/routes/admin/playground.tsx
frontend/src/routes/admin/playground.strings.ts
frontend/src/routes/admin/playground.test.tsx
frontend/src/router.tsx
frontend/src/routes/admin/layout.tsx
frontend/src/components/shared/Sidebar.tsx
frontend/src/lib/errcode.ts
```

Generated outputs after OpenAPI changes:

```text
internal/generated/adminapi/*.gen.go
frontend/src/generated/openapi/*
```

## API and Error Codes

The new operation is `POST /api/admin/playground/run`. Exact schema is in `contracts/playground-api.md` and must be mirrored into `openapi/admin.yaml`.

The Playground JSON request body cap is **96 KiB**. Oversized bodies return existing `2009 request_body_too_large` with `data = {"scope":"envelope","limit_bytes":98304}`. The prompt itself is capped at 16,000 trimmed characters. The UI exposes a model input defaulting to `gpt-5.4-mini`; the API requires a non-empty model value in `1..128` trimmed characters. `max_output_tokens` is optional; the service applies a server-side default of `1024` when the field is omitted and rejects values outside `1..4096`.

Planned new codes:

| Code | Symbol | HTTP | Meaning |
|------|--------|------|---------|
| 4001 | `invalid_playground_request` | 200 | Validation failed for text/model/selection fields |
| 4002 | `playground_no_active_account` | 200 | Automatic selection found no active eligible account |
| 4003 | `playground_account_unavailable` | 200 | Explicit account missing/deleted/disabled/ineligible |
| 4004 | `playground_upstream_error` | 200 | Provider returned non-2xx with sanitized details |
| 4005 | `playground_upstream_timeout` | 200 | Provider call timed out |
| 4006 | `playground_response_malformed` | 200 | Successful upstream response could not be safely parsed as JSON |
| 4007 | `playground_response_too_large` | 200 | Upstream body exceeded bounded read cap |
| 4900 | `playground_internal_error` | 500 | Unexpected handler/service failure |

The implementation also reuses:

- `2008 malformed_body` for invalid JSON/content type.
- `2009 request_body_too_large` for oversized Admin API request body.

Provider diagnostics are bounded and sanitized: raw response is off by default, capped at 1 MiB when requested, recursively redacts secret-like keys and token-like values, and is omitted when safe rendering cannot be guaranteed. `provider_error` is capped at 128 characters and `provider_message` at 512 characters after redaction. Known-account failures include a safe account summary so the UI can satisfy FR-015 without fetching secret-bearing account rows.

The P0 upstream wait limit is 30 seconds. Timeout maps to `4005`; client cancellation may prevent an envelope response, but the service must record `outcome=cancelled` when cancellation is observed.

## Risk Assessment

| Risk | Probability | Impact | Mitigation | Verification |
|------|-------------|--------|------------|--------------|
| Data-plane behavior changes accidentally | M | H | Keep Playground on `/api/admin/*`; do not add data-plane selection headers | Existing proxy regression tests |
| Explicit account bypasses disabled/deleted status | M | H | Selector/service active-only check and tests for stale selected account | Backend integration test |
| Credential material leaks through raw response/logs | M | H | Never include auth headers/tokens; log-scrub tests; raw JSON only provider response body | Security/log-scrub tests |
| Upstream response is large or malformed | M | M | Bounded read cap and explicit malformed/too-large outcomes | Fake upstream tests |
| OpenAPI oneOf generated code drifts | M | M | Follow existing named component union pattern and run codegen freshness | OpenAPI freshness gate |
| Error-code range conflicts with stale admin-auth roadmap text | H | M | Update docs/openapi extension to make 004 Account Playground authoritative | Errcode parity tests and docs review |
| UI duplicate submit creates multiple upstream calls | M | M | Disable pending submit while a run is pending; P0 does not add API-level idempotency | RTL interaction test |
| Body logging captures operator prompt unexpectedly | L | M | Follow existing runtime body logging toggles and make UI expectations clear | Request recorder integration tests |

## Security Considerations

- **Authentication/authorization**: In the current MVP trust boundary, admin endpoints are unauthenticated on an internal network. When admin-auth is enabled in a future plugin, this route must be gated like other `/api/admin/*` routes.
- **Input validation**: Text, model, selection mode, account id, session key, and output cap are validated before upstream calls. Malformed JSON and oversized bodies use existing envelope codes.
- **Sensitive data**: API keys, OAuth access tokens, refresh tokens, ID tokens, authorization headers, and bearer values must never appear in response bodies, logs, frontend display state, or request-history metadata.
- **Provider data**: Raw upstream JSON may contain operator-entered text and model output. Persisting it follows runtime response-body logging settings; UI display is bounded and safe.
- **SSRF/generic proxy risk**: The Playground is not an arbitrary URL proxy. It uses the selected account's configured provider base URL and a fixed text-probe operation.
- **Correlation id**: `X-Request-Id` remains header-only. UI error objects may read the header, but response `data` and `msg` do not duplicate it.

## Test Hints

Backend must-test scenarios:

- Contract test for `POST /api/admin/playground/run` success and every business error branch.
- Automatic mode with one active account records selected account and usage.
- Automatic mode with no active accounts returns `4002` and makes zero upstream calls.
- Account mode uses the requested active account and never substitutes another.
- Account mode with disabled/deleted/missing account returns `4003`.
- OAuth stale account goes through existing refresh-before-use hook.
- OAuth account probes use the same ChatGPT Codex backend transport as data-plane OAuth requests; API-key probes remain on the OpenAI Platform-compatible transport.
- Upstream non-2xx returns `4004` with sanitized provider details.
- Upstream timeout returns `4005`.
- Malformed/too-large upstream body returns `4006`/`4007`.
- Request recorder obeys runtime body logging toggles.
- Log-scrub covers prompt/response tests containing fake token-like secrets.

Frontend must-test scenarios:

- `/admin/playground` route renders in shell and sidebar active state works.
- Empty/over-limit text validates inline without calling API.
- Account mode requires account selection.
- Pending state disables duplicate submit and announces status.
- Success state shows account, selection mode, output, latency, usage.
- No-extractable-text state does not fabricate text.
- Error state preserves prompt/mode and displays correlation id via existing error model.

Regression areas:

- `/v1/*` proxy response shape and SSE behavior.
- Account list/detail pages.
- OAuth reauth/import/export routes.
- Settings and setup gate behavior.
- OpenAPI generated union wrappers.

## Complexity Tracking

No Constitution gate violations. Complexity is rule-based, not a justification: backend + frontend + OpenAPI + registry changes make this a complex feature.
