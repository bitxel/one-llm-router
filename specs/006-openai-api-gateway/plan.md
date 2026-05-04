# Implementation Plan: Full OpenAI API Gateway

**Feature**: 006-openai-api-gateway
**Spec**: `specs/006-openai-api-gateway/spec.md`
**Created**: 2026-04-26
**Status**: Ready
**Complexity**: complex
**Complexity Reason**: Expands the provider-compatible data plane across explicit OpenAI Platform operations, Codex OAuth compatibility mappings, WebSocket relay, multipart forwarding, Admin usage observability, request recording, SDK smoke, and unsupported-route gates.
**Complexity Override**: none

## Summary

Feature 006 turns the current broad `/v1/` proxy into an explicit data-plane gateway. API-key accounts direct-forward only the supported OpenAI Platform operation set from the spec coverage matrix. ChatGPT OAuth accounts remain eligible only for explicit Codex-backed mappings and adapters. Router-local usage observability is deliberately moved to an Admin API operation (`GET /api/admin/usage`) because it is operator telemetry, not an OpenAI or Codex provider-compatible data-plane route. `/api/codex/usage` and `/api/codex/usage/` are not registered as data-plane prefixes; `/v1/audio/transcriptions` and other deferred or unsupported data-plane families fail before any upstream attempt when they hit the classifier.

The plan preserves the control-plane split: `/api/admin/*` and `/api/setup/*` stay enveloped router-owned APIs; `/v1/*` and selected `/backend-api/*` stay native data plane. No `/api/codex/*` route is treated as data plane in 006.

## Source Inputs

| Source | Result |
|---|---|
| `specs/006-openai-api-gateway/spec.md` | Scope, operation matrix, credential matrix, non-support decisions |
| `specs/sdd/constitution.md` | Spec-first, test-first, traceability, reversibility gates |
| `AGENTS.md` | Data-plane envelope exclusion, failure semantics, plugin boundary, route ownership |
| OpenAI OpenAPI feed via OpenAI docs MCP, checked 2026-04-26 | Current `/v1` endpoint inventory; 006 selects Responses, Conversations, Chat Completions, and read-only Models |
| `codex-lb/app/modules/proxy/api.py`, checked 2026-04-26 and revised on 2026-04-27 | Compatibility references for `WS /v1/responses`, OAuth `GET /v1/models` facade, and `/backend-api/transcribe`; codex-lb usage paths are explicitly not adopted as data-plane routes, and `/v1/audio/transcriptions` deliberately remains deferred here |
| Brownfield scan of `internal/api/proxy.go`, `internal/provider/openai/forwarder.go`, `internal/setup/gate.go`, `internal/domain/request_record.go` | Existing implementation is wildcard `/v1/`, has OAuth Responses/compact mapping, request recording, SSE TTFT, and no `/backend-api/*` or usage route support |

## Constitution Check

- [x] Spec-first: no implementation scope is added beyond the 006 spec matrix; spec was corrected before planning for Admin usage observability wording.
- [x] Simplicity: one backend service remains; no new deployable sub-project and no plugin. Admin usage observability uses the existing Admin OpenAPI/codegen pipeline.
- [x] Test-first: contract and integration tests are planned before gateway code changes.
- [x] Traceability: every module change maps to FR-001 through FR-015 and the operation-level matrix.
- [x] Reversibility: no schema migration is required for the base plan; route-classifier changes can be rolled back by restoring the previous `/v1/` proxy registration.
- [x] Integration-first: local mock upstreams and in-memory SQLite cover normal/error paths; live provider smoke remains opt-in.

## Brownfield Findings

| Area | Current State | 006 Implication |
|---|---|---|
| Route registration | `internal/app/app.go` registers `mux.Handle("/v1/", proxy)` only | Add explicit registrations for `/v1/`, exact `/backend-api`, and subtree `/backend-api/`; classifier decides support before selection/upstream. `/api/codex/*` is not a data-plane prefix in 006 |
| Route filtering | `ProxyHandler` forwards API-key accounts for arbitrary `/v1/*`; OAuth eligibility is only `/v1/responses` and `/v1/responses/compact` | Add operation-level classifier and reject deferred/blocked/unlisted paths with native data-plane error |
| Setup gate | Setup-pending native error applies only to `/v1/*` | Extend setup-pending handling to selected 006 data-plane prefixes (`/v1/*`, `/backend-api`, `/backend-api/*`) so new data-plane paths never return admin envelope or SPA HTML |
| Provider forwarding | API-key forwarding preserves original path; OAuth `codexBackendPath` only maps `/v1/responses` and `/v1/responses/compact` | Replace path-only mapping with route metadata so backend-api, chat adapter, and transcribe behavior cannot leak through wildcard forwarding |
| Body handling | Proxy reads full request body up to 32 MiB before forwarding | Keep JSON capture for model/usage extraction; disable or stream bounded capture for multipart/audio/WebSocket paths |
| Request records | Existing string `response_mode`, token usage map, TTFT, body columns; Admin request-log OpenAPI enum currently exposes only `json`/`sse` | Reuse table; add `websocket` response mode without DB migration; keep multipart request-body policy separate from `response_mode`; update Admin request-log enum contract and generated clients |
| WebSocket | Middleware preserves `Hijacker`, but no WebSocket relay exists | Add a real WebSocket relay. This requires an approved mature library; do not hand-roll RFC 6455 |
| Usage observability | Dashboard aggregates request records; no dedicated Admin usage endpoint | Add `GET /api/admin/usage` over existing request records/account metadata, with explicit absence instead of fabricated quota facts |

## Architecture

### Module Boundaries

| Module | Responsibility | Change |
|---|---|---|
| `internal/api/proxy.go` | Keep HTTP data-plane response copying, error writing, recording orchestration | Modified/split as needed |
| `internal/api/proxy_routes.go` | Classify method/path/upgrade into supported, local, deferred, blocked, unsupported route outcomes | New |
| `internal/api/websocket_proxy.go` | Relay supported Codex Responses WebSockets, turn-state headers, close/error recording | New |
| `internal/api/sse.go` | Preserve SSE forwarding and TTFT extraction for new SSE paths | Modified if classifier needs route hints |
| `internal/setup/gate.go` | Native setup-required handling for `/v1/*`, exact `/backend-api`, and `/backend-api/*` data-plane paths | Modified |
| `internal/app/app.go` | Register gateway handler for `/v1/`, exact `/backend-api`, and `/backend-api/`; wire WebSocket relay deps | Modified |
| `internal/provider/openai/forwarder.go` | Route-aware API-key and Codex forwarding; header policy; no wildcard OAuth mapping | Modified |
| `internal/provider/openai/chat_adapter.go` | OAuth Chat Completions-to-Codex Responses request/response/SSE adapter | New |
| `internal/provider/openai/codex_ws.go` | Upstream Codex WebSocket connection helpers using `github.com/coder/websocket@v1.8.14` | New |
| `internal/provider/openai/transcribe.go` | OAuth `/backend-api/transcribe` multipart mapping to ChatGPT Codex `/transcribe` | New |
| `internal/core/gateway_usage_service.go` | Build Admin usage summary data from existing accounts/request records | New |
| `internal/api/adminapi/usage.go` | Serve `GET /api/admin/usage` through the Admin API envelope | New |
| `internal/domain/request_record.go` | Add response-mode constant for `websocket`; no schema change | Modified |
| `internal/store/records.go` | Add focused aggregation helpers if dashboard aggregation cannot be reused cleanly | Modified if needed |
| `openapi/admin.yaml` | Request log `RequestResponseMode` enum used by `/api/admin/requests*`, plus Admin usage operation contract | Modified: add `websocket` and `GET /api/admin/usage` |
| `internal/generated/adminapi/`, `frontend/src/generated/openapi/` | Generated Admin API clients | Regenerated after `openapi/admin.yaml` change |
| `internal/api/adminapi/request_logs.go` | Request log row/filter conversion for `response_mode` | Modified/tested to accept `websocket` after regeneration |
| `frontend/src/routes/admin/requests.tsx` | Request log response-mode filters and badges | Modified if the hard-coded filter list still omits regenerated modes |
| `scripts/openai-sdk-compat-smoke.py` | Extend opt-in SDK smoke for supported/unsupported route cases | Modified |
| `frontend/tests/e2e/data-plane-compat.spec.ts` | Local compatibility matrix for JSON, SSE, provider error, router error, and Admin usage observability | Modified |

### Gateway Route Classifier

The first implementation slice must introduce a pure classifier:

```text
Classify(method, path, upgrade) -> GatewayRoute
```

The classifier owns:

- exact supported operation matching from the 006 matrix;
- API-key vs OAuth eligibility;
- route kind: API-key direct forward, OAuth Codex mapping, OAuth adapter, unsupported, blocked;
- upstream path for Codex mappings, not derived by string prefix;
- response mode expectation: JSON, SSE-capable JSON, WebSocket, local JSON;
- body-capture policy: JSON safe, multipart/audio disabled, WebSocket disabled.

No account selection, OAuth refresh, upstream call, or business request-body read may run before classification says the route is supported for at least one credential class. Unsupported/deferred/blocked routes return native data-plane router errors and record no upstream account. Server-level body protection may still cap the connection, but the gateway classifier must not read or capture deferred binary/multipart bodies such as `/v1/audio/transcriptions`, files, or uploads.

### Request Flow

```text
Request enters middleware
 -> RequestID + HTTP request log
 -> SetupGate native setup check for data-plane prefixes
 -> GatewayHandler
 -> classify method/path/upgrade
 -> if unsupported/blocked: native router error + request record
 -> select eligible account for route credential policy
 -> refresh OAuth credential if selected account is OAuth
 -> route-aware forwarder / adapter / WebSocket relay
 -> copy provider-compatible status/body/headers or native router error
 -> request record with account/status/latency/outcome/response_mode/usage when extractable
```

### Provider Transport Rules

| Client Route Kind | API-key Account | OAuth Account |
|---|---|---|
| Supported Platform `/v1/*` rows from the coverage matrix | Direct forward to `account.base_url + original path/query` | Only explicit OAuth rows are eligible |
| `POST /v1/responses` | Direct JSON/SSE | Map to ChatGPT `/backend-api/codex/responses` |
| `WS /v1/responses` | Unsupported in 006 | Map to ChatGPT Codex Responses WebSocket |
| `POST /v1/responses/compact` | Direct pass-through | Map to ChatGPT `/backend-api/codex/responses/compact` |
| `POST /v1/chat/completions` | Direct JSON/SSE | Adapter over Codex Responses; unsupported Chat fields fail visibly |
| `GET /v1/models` | Direct JSON | Codex model list through OpenAI-compatible facade; `GET /v1/models/{model}` remains unsupported for OAuth |
| Listed `/backend-api/codex/responses`, `WS /backend-api/codex/responses`, `/backend-api/codex/responses/compact`, `GET /backend-api/codex/models`, and `/backend-api/transcribe` | Unsupported | Explicit Codex mappings only |

### WebSocket Dependency Decision

Go's standard library does not provide a production WebSocket client/server relay. User approval was granted on 2026-04-26 to add `github.com/coder/websocket` pinned to `v1.8.14` for `WS /v1/responses` and `WS /backend-api/codex/responses`. Implementation must add this dependency in the WebSocket task with `go get github.com/coder/websocket@v1.8.14` or an equivalent pinned `go.mod` edit, then commit `go.mod` and `go.sum` together with the relay code.

Required package-use rules:

- Use `websocket.Accept` for downstream upgrades and `websocket.Dial` for upstream Codex socket connections.
- Run both relay directions with request-context cancellation.
- Propagate close status and reason where possible.
- Keep ping/pong, masking, and frame parsing delegated to the library.
- Never log or persist WebSocket frame payloads.

Do not hand-roll frame parsing, masking, ping/pong, close-code propagation, or backpressure.

## Data Model

No persistent migration is planned.

Existing `request_records` supports 006:

- `path` stores exact client-facing data-plane path, including selected `/backend-api/*` paths.
- `response_mode` stores existing `json`/`sse` plus new string value `websocket`; multipart `/backend-api/transcribe` requests record the provider-compatible response mode, normally `json`.
- `token_usage` stores extracted usage when present.
- `model_params` stores safe client/model parameters and adapter-derived model metadata.
- `router_metadata` stores router-owned audit metadata such as route kind, credential class, and operation bridge details.
- body columns remain controlled by runtime logging toggles, but multipart/audio/WebSocket payload capture is disabled regardless of toggle.

`GET /api/admin/usage` is computed from retained request records and active account metadata. Because the current product has no client-key identity or cached ChatGPT quota table, 006 uses global retained request records and exposes empty/absent quota facts rather than inventing limits. The endpoint is a router-owned Admin API, so it uses the `{code,msg,data}` envelope and OpenAPI-generated server/client types.

## Contracts

Data-plane contracts are Markdown, not OpenAPI:

- `contracts/v1-platform-gateway.md`
- `contracts/codex-compatibility.md`
- `contracts/unsupported-routes.md`

Admin OpenAPI regeneration is required because 006 adds `websocket` to request-record `response_mode`, and because router-local usage observability is exposed as `GET /api/admin/usage`. Implementation must update `openapi/admin.yaml`, regenerate Go and TS clients, and update request-log/frontend/admin tests in the same change set. Multipart request-body handling is represented by route/body policy metadata, not by a new `response_mode`.

## Implementation Phases

1. **Red tests: classifier and setup gate**
   - Matrix tests for every supported row and representative deferred/blocked families.
   - Setup-pending tests for `/v1/*`, `/backend-api`, and `/backend-api/*`; `/api/codex/*` must not be registered as a data-plane prefix.
   - Tests proving unsupported/deferred routes classify before any business request-body read, using unreadable and over-limit deferred-route bodies.
2. **Route classifier and native errors**
   - Add `unsupported_endpoint` and `blocked_endpoint` native data-plane codes.
   - Stop API-key wildcard forwarding for deferred/unlisted `/v1/*`.
3. **Request-record enum contract**
   - Add `websocket` to Admin `RequestResponseMode` and add `GET /api/admin/usage` in `openapi/admin.yaml`.
   - Regenerate Go server and TS client, then prove request-log rows and response-mode filters accept the new value.
4. **API-key direct forwarding**
   - Prove Responses, Conversations, Chat Completions, read-only Models, and provider errors preserve status/body/content type/headers.
5. **OAuth Codex mappings**
   - Preserve existing Responses behavior; add backend-api responses/compact/models/transcribe route-aware mapping and the OAuth `GET /v1/models` OpenAI-compatible facade over Codex models.
6. **OAuth Chat Completions adapter**
   - Implement `contracts/v1-platform-gateway.md#oauth-chat-completions-adapter`: strict request allowlist/reject list, Chat-to-Responses request mapping, Responses-to-Chat object/SSE mapping, usage mapping, and adapter-generated id recording.
7. **Admin usage observability endpoint**
   - Implement `GET /api/admin/usage` with Admin envelope semantics, OpenAPI-generated types, explicit empty/absent quota fields, and no downstream client-key/quota semantics.
8. **WebSocket relay**
   - Add `github.com/coder/websocket@v1.8.14`, downstream and upstream relay, turn-state handling, required `OpenAI-Beta: responses_websockets=2026-02-06` upstream header handling, close/error recording, and token-safe logging.
9. **Compatibility and smoke**
   - Extend Playwright local matrix and opt-in SDK smoke.
   - Keep live tests disabled by default and credential-free CI safe.

## Test Plan

Backend tests must be written before implementation code:

- Classifier table tests for all initial supported operations and deferred families.
- `ProxyHandler`/GatewayHandler tests proving unsupported routes classify before account selection, body capture, business body read, OAuth refresh, and upstream calls.
- Setup gate tests for native setup-required behavior on new data-plane paths.
- Admin request-log row/filter tests proving `websocket` `response_mode` values no longer fail enum validation.
- API-key direct-forward tests for path/query/header preservation and provider error propagation.
- OAuth mapping tests for `/v1/responses`, `/v1/responses/compact`, `GET /v1/models`, backend-api responses, models, and transcribe.
- OAuth Chat Completions adapter tests for non-streaming, SSE, allowed-field mapping, unsupported/unknown field rejection, provider errors, malformed terminal events, adapter-generated ids, and usage extraction.
- WebSocket relay tests with mock downstream/upstream sockets: connect, generated/reused `x-codex-turn-state`, required upstream `OpenAI-Beta: responses_websockets=2026-02-06`, bidirectional relay, upstream close, client abort.
- Usage service tests for empty records, JSON/SSE usage totals, cached input totals, and absent quota facts.
- Body policy tests proving multipart/audio/WebSocket bodies are not captured even when body logging is enabled, and unsupported/deferred binary routes are not business-read before rejection.

Integration and E2E:

- `go test ./internal/api ./internal/provider/openai ./internal/core ./internal/store ./internal/app`
- Playwright local mock upstream matrix for JSON, SSE, Codex WebSocket handshake/close, `/backend-api/transcribe` multipart, router error, provider error, Admin usage endpoint, and unsupported `/v1/audio/transcriptions`.
- Opt-in SDK smoke for first-party SDK Responses, Chat Completions, Models, and unsupported-route parsing.
- `scripts/log-scrub.sh` over Go, Playwright, and SDK smoke logs.

## Risk Assessment

| Risk | Probability | Impact | Mitigation |
|---|---|---|---|
| API-key wildcard forwarding accidentally exposes deferred OpenAI APIs | High | High | Classifier before selection/upstream; unsupported-route no-upstream tests |
| Unsupported/deferred routes read large binary bodies before rejection | Medium | High | Pre-body classifier; unreadable/over-limit body tests; no body capture on router errors |
| OAuth token sent to wrong OpenAI Platform endpoint | Medium | High | Credential-class eligibility in classifier; route-aware Codex upstream path; tests assert upstream host/path |
| WebSocket relay leaks goroutines or mishandles close frames | Medium | High | Mature library, context cancellation, close propagation tests, no hand-rolled protocol |
| Multipart/audio body captured in logs | Medium | High | Per-route body policy disables capture; log/DB scrub tests |
| Admin request logs reject new `response_mode` values | Medium | High | Update Admin OpenAPI enum, regenerate clients, and test row/filter handling for `websocket` |
| Admin usage endpoint fabricates quota facts | Medium | Medium | Return Admin-envelope data with null/empty quota fields when unknown |
| Chat Completions adapter overpromises field support | Medium | Medium | Strict unsupported-field rejection for OAuth adapter; API-key direct pass-through remains transparent |
| Setup-pending new data-plane paths return SPA/404/envelope | Medium | Medium | Gate tests for every data-plane prefix |
| New WebSocket dependency violates project boundary | Medium | Medium | Approved `github.com/coder/websocket@v1.8.14`; document pinned dependency reason in PR |

## Quality Gates

- `go generate ./internal/generated/...` after `openapi/admin.yaml` changes, followed by `git diff` review for generated Go freshness
- `gofmt -w` on changed Go files
- `golangci-lint run`
- `go test ./...`
- `go build ./...`
- `bash scripts/log-scrub.sh test-output/` after compatibility runs
- `cd frontend && pnpm biome check .` when frontend files change
- `cd frontend && pnpm tsc --noEmit` when frontend files change
- `cd frontend && pnpm build` when frontend files change
- `cd frontend && pnpm playwright test tests/e2e/data-plane-compat.spec.ts` when E2E is updated
- Opt-in only: `bash scripts/run-openai-sdk-compat-smoke.sh`

## Complexity Tracking

No constitution deviation is accepted in this plan. The feature is complex because it has multiple transport shapes, but it stays within one backend service and existing test/script surfaces.

WebSocket dependency approval is resolved as of 2026-04-26: use `github.com/coder/websocket@v1.8.14`. No open dependency blocker remains; `sdd-tasks` should include the pinned dependency addition in the WebSocket relay task.
