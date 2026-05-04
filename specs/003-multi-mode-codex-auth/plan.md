# Implementation Plan: Multi-mode Codex Authentication

**Feature**: 003-multi-mode-codex-auth
**Spec**: `specs/003-multi-mode-codex-auth/spec.md`
**Created**: 2026-04-15
**Status**: Reviewed

## Summary

Add a four-way auth picker to both operator entrypoints that create an upstream account: the existing 002 admin portal and the 002 setup wizard's optional *Upstream* step. The four modes are **API key** (unchanged), **OAuth browser (PKCE)**, **OAuth device**, and **Import auth.json**. Persist the resulting `(access, refresh, id)` token triplet as sealed-bytes-ready `BLOB`/`BYTEA`/`VARBINARY` columns on `upstream_accounts` (plaintext in 003, per FR-003; the key-vault plugin lands later). Before every Codex-protocol forward, a new `internal/oauth` package refreshes the access token via `golang.org/x/sync/singleflight` (one refresh per account per burst), and the proxy passes the selected account plus current credential to the OpenAI provider client. The provider client is the account-method switch: API-key rows preserve 002 OpenAI Platform-compatible forwarding to `account.base_url + original /v1/*`; OAuth rows use the ChatGPT Codex backend transport for `/v1/responses` and `/v1/responses/compact`, including `chatgpt-account-id` when present and `User-Agent: codex_cli_rs/0.120.0 (Mac OS 26.2; arm64) dumb`. Non-streaming OAuth Responses calls are forced upstream as Codex SSE and returned downstream as collected JSON; when upstream response-body logging is enabled, the router records the same complete JSON in `request_records.upstream_response_body`. Streaming OAuth Responses calls keep downstream SSE. A separate `POST /api/admin/accounts/{id}/export-auth-json` endpoint emits a Codex-CLI-byte-compatible `auth.json` whenever the operator explicitly hits **Export**, with every hit audited as a WARN log (FR-014). Frontend delta is additive — the admin shell gets its new picker sub-routes (`/admin/accounts/new-oauth`, `/admin/accounts/new-oauth-device`, `/admin/accounts/new-import`), while the setup wizard upgrades its *Upstream* step to expose the same four auth modes. API-key setup seeding still completes inline via `POST /api/setup/commit.first_account`; browser OAuth, device OAuth, and `auth.json` import commit setup with `first_account` omitted and then hand off immediately into the existing 003 onboarding routes, so we do not introduce a second setup-scoped OAuth/import backend. Backend delta remains **one migration version** (`000002_multi_mode_auth`, shipped as six files — up+down for each of sqlite/postgres/mysql), three new Go packages inside the same monolithic service (`internal/oauth`, `internal/api/oauthapi`, `internal/api/exportapi`), two new handler files under the existing `internal/api/adminapi` (`import_auth_json.go` + `reauth.go`), and small edits to existing packages (`internal/core/account_selector.go` gains a `PreForward` function-hook field; `internal/api/proxy.go` forwards with the selector's current credential; `internal/provider/openai` implements the API-key vs OAuth transport split). No new sub-projects.

---

## Technical Context

| Item | Value | Source |
|---|---|---|
| Language | Go 1.25 | inherited from 002 plan |
| HTTP | `net/http` + `http.ServeMux` | existing pattern |
| Database | SQLite / PostgreSQL / MySQL via `xorm v1.3.11` | inherited |
| Migrations | `golang-migrate v4.19.1` via the 002 `one-llm-router migrate` subcommand | inherited |
| OAuth endpoints (upstream) | `https://auth.openai.com/{oauth/authorize, oauth/token, api/accounts/deviceauth/*}` | `research.md` Decision 1 |
| ChatGPT Codex backend (OAuth data plane) | `https://chatgpt.com/backend-api/codex/responses` and `/codex/responses/compact` | `research.md` Decision 10 + FR-009 |
| OAuth client_id / originator / scope / redirect_uri | `app_EMoamEEZ73f0CkXaXp7hrann` / `codex_cli_rs` / `openid profile email offline_access api.connectors.read api.connectors.invoke` / `http://localhost:1455/auth/callback` | `research.md` Decision 1 |
| Single-flight refresh | `golang.org/x/sync/singleflight` **v0.19.0** (already in `go.sum`; verified 2026-04-15 via `go list -m golang.org/x/sync`) | `research.md` Decision 3 + 9 |
| JWT handling | stdlib `encoding/base64` + `encoding/json` — no `jwt-go` / `go-jose` added | `research.md` Decision 6 + 9 |
| Frontend framework | React 19 + Vite 6 + TS 5 + TanStack Router/Query + shadcn/ui + Tailwind v4 + RHF + Zod | inherited from 002 — **no stack change** |
| Frontend new npm packages | **none** | all shadcn components + RHF/Zod validators exist in 002 |
| Embedding | `go:embed frontend/dist/*` — unchanged | inherited |
| Logging | `slog` structured JSON — 13 new event names (the full list and per-event field schemas are authoritative in `contracts/oauth-flow-api.md` §Observability): `oauth_flow_started`, `oauth_flow_completed`, `oauth_flow_cancelled`, `oauth_flow_failed`, `oauth_rail_rejected`, `oauth_flow_expired`, `oauth_refresh_started`, `oauth_refresh_ok`, `oauth_refresh_failed`, `oauth_refresh_transient_fallback`, `oauth_auth_json_exported`, `oauth_auth_json_export_rejected`, `auth_json_import_rejected`. Successful `auth.json` imports reuse 002's `account_created` INFO — there is deliberately no dedicated `auth_json_imported` event. | |
| Tests | `go test`, `stretchr/testify v1.11.1`, Vitest + RTL + Playwright | inherited |
| Body-cap relaxation | **Dual-layer cap** on `POST /api/admin/accounts/import-auth-json`: outer **64 KB** (`http.MaxBytesReader` on the multipart envelope, defends against preamble flood) + inner **16 KB** (`io.LimitReader` on the `auth_json` part — the semantic payload budget). Both trips emit envelope `code:2009 request_body_too_large` + `data.scope ∈ {envelope, part}` + `data.limit_bytes`. Global admin cap on other `/api/admin/*` endpoints unchanged at 8 KB. | `research.md` Decision 7 |

> **No new third-party deps** (Go or npm) beyond verifying `golang.org/x/sync` is in `go.sum`. See `research.md` §Dependency Versions for the verification plan.

---

## Constitution Check

### 第一性原理 Gate
- [x] Every design choice traces to a spec FR/US (see Traceability below). No feature added "because codex-lb does it" — the choices where codex-lb is copied (endpoint bag, client_id, ID-token claim-extract shortcut) are explicitly justified in `research.md` Decisions 1 + 6.
- [x] Goals are clear: operator onboards an OAuth account in ≤2 min (SC-1), refresh keeps the account 24×7 (SC-2), no token bytes leak into logs/admin-API-default (SC-4). All three are already sharp in the spec.
- [x] No over-engineering. The new code surface is three packages (`oauth`, `oauthapi`, `exportapi`) plus two one-line insertion points in existing packages. Every package maps to one spec FR cluster.
- [x] Shortest path: reuse 002's React + TanStack + shadcn frontend, reuse 002's admin-auth gating, reuse 002's migration runner. No parallel "OAuth shell".

### Simplicity Gate (Constitution Principle 2)
- [x] Sub-project count stays at **1** (the existing router service). Everything new lives inside it — `internal/oauth/` on the Go side + a sibling set of routes in the existing `internal/api/adminapi` family + a new route folder under the existing frontend workspace. No new deployable services.
- [x] No future-proofing: we do NOT ship an encryption master-key surface (FR-003 defers that entirely), NOT ship multi-provider OAuth (OpenAI only — spec Scope), NOT ship team-level ACL (spec Scope out), NOT introduce a config surface for OAuth client_id (operator can't change it in 003 — it ships as a hardcoded constant because changing it is equivalent to "impersonate a different OAuth app" and operators cannot do that anyway).
- [x] No "might need later" hooks: the `OAuthProvider` interface is **concrete** with one implementation (OpenAI). We will only add the interface shape once a second provider lands. For 003 we accept a `type openAIProvider struct{}` and call its methods directly from `internal/oauth/flow.go`.

### Anti-Abstraction Gate (Constitution Principle 2)
- [x] Uses `net/http`, `crypto/rand`, `encoding/base64`, `encoding/json` directly. No wrapper around net/http. No JWT library (plain base64 + JSON per Decision 6).
- [x] `UpstreamAccount` stays a single struct — the new fields are additive pointer/nullable-string fields on the same Go type, not a separate `OAuthAccount` subtype. One representation.
- [x] `OAuthFlow` is an in-memory struct held via `atomic.Pointer[oauth.Flow]` on `*app.App`. No "FlowStore" interface layer — with at-most-one flow per process, the interface would be one-implementation rotational waste.
- [x] The refresh hook is a `func(context.Context, *domain.UpstreamAccount) (accessToken []byte, usedFallback bool, err error)` accepted by `AccountSelector` — a plain function, not an interface. The return type is `[]byte` (not `string`) for consistency with the storage shape (`access_token` is a `BLOB`/`BYTEA`/`VARBINARY(8192)` column per `data-model.md`), so there is **one** byte-slice traveling from DB → coordinator → selector → proxy with no intermediate copy through the `string` heap. The proxy converts `[]byte → string` exactly once at the `Authorization: Bearer …` header boundary inside `internal/provider/openai`. Rejecting `nil`/length-0 slices at the selector boundary is the selector's responsibility; the proxy assumes non-empty. The `usedFallback` boolean is carried back to the proxy boundary for observability and future policy decisions.

### Integration-First Gate (Constitution Principle 7)
- [x] Every new HTTP endpoint has a contract file written BEFORE handler code: `contracts/oauth-flow-api.md` + `contracts/accounts-api.md`.
- [x] Contract tests precede implementation: each of the six new endpoints gets a `*_contract_test.go` asserting request/response shape against the contracts file before its handler ships. The 002 contract-test harness already exists and is reused.
- [x] Real env: OAuth provider calls in unit tests go through `httptest.Server` fixtures that return the real `codex-lb`-observed JSON shapes; the single-flight + refresh integration test runs against an in-memory SQLite so the DB round-trip is real. Only the network-level call to `auth.openai.com` is mocked (explicitly allowed by Principle 7 "external services outside your control").

### Test-First Gate (Constitution Principle 3)
- [x] Every Acceptance Scenario in the spec maps to at least one test (traceability matrix below).
- [x] Red phase before green: the contract-test files are written first and MUST fail (`handler not found`) before any handler function exists — enforced by the 002 TDD harness.

**Gate verdict**: all five PASS with no exceptions. `Complexity Tracking` section below stays empty.

---

## Architecture

### Module Boundaries

| Module | Responsibility | Change Type |
|---|---|---|
| `cmd/one-llm-router/main.go` | Unchanged entry; `BuildApp` wires the new oauth module | Unchanged |
| `internal/app/app.go` | Wires `oauth.Coordinator` into `AccountSelector` + admin router; holds the `atomic.Pointer[oauth.Flow]` | **Modified** (+~50 LOC: one constructor call; one `RegisterBrowserHandlers` invocation for Phase 3 / US-1; one `RegisterDeviceHandlers` invocation for Phase 5 / US-3 — Phase 3 / Phase 5 split isolates handler wiring per user-story landing) |
| `internal/oauth/` | **New**. Pure domain. Exports: `Provider` (concrete `openAIProvider` struct; `BuildAuthorizeURL`, `ExchangeCode`, `Refresh`, `RequestDeviceCode`, `PollDeviceCode`); `Flow` struct (carries `ListenerBound bool`, `Consumed atomic.Bool` (CAS target), `ConsumedBy Rail` (observability label)) + `FlowMethod = "browser"\|"device"` + `Rail = "loopback"\|"manual_paste"\|""` + `FlowStatus = idle\|pending\|success\|error`; `Coordinator` owning `atomic.Pointer[Flow]` + `singleflight.Group`; `Coordinator.StartBrowser(ctx, provider) (*Flow, error)` (generates PKCE pair + state token; best-effort binds dual-stack loopback on canonical port 1455 only and sets `Flow.ListenerBound` to whether the bind succeeded; returns a ready-to-start Flow regardless of bind outcome; ends with `TryStartFlow`); `Coordinator.StartDevice(ctx, provider) (*Flow, error)`; `Coordinator.TryStartFlow(*Flow) error` (atomic CAS on the flow slot; returns `ErrFlowInProgress` if an in-flight Flow is already set); `Coordinator.CurrentFlow() *Flow` (nil-safe read); `Coordinator.ReleaseFlow(flowID string)` (CAS-nils only if the current Flow.ID matches; Closes listener iff `Flow.ListenerBound`); `Coordinator.Cancel(flowID string)` (same CAS-guard + conditional `listener.Close()` + Status=error); `Coordinator.ConsumeCode(ctx, flowID string, code, state string, rail Rail) (*domain.UpstreamAccount, error)` (the ONE code-exchange entry point used by BOTH rails — the loopback `CallbackHandler` calls it with `rail=loopback`, the `POST /browser/manual-callback` handler calls it with `rail=manual_paste`; does constant-time state compare → `Flow.Consumed.CompareAndSwap(false, true)` → on CAS-win runs `/oauth/token` + persists the row + sets `Flow.ConsumedBy=rail` + `Flow.Status=success` + closes listener; on CAS-loss returns `ErrAlreadyConsumed` without calling upstream); `Coordinator.RefreshIfStale(ctx, *UpstreamAccount) (accessToken []byte, usedFallback bool, err error)` (the single-flight-deduped refresh-before-forward hook called by `AccountSelector.PreForward`), plus an unexported `CallbackHandler` wired into the loopback `*http.Server` (delegates to `ConsumeCode(..., rail=loopback)`). | **New** |
| `internal/api/oauthapi/` | **New**. HTTP handlers for `/api/admin/oauth/{browser,device,flow,cancel,browser/manual-callback}` per `contracts/oauth-flow-api.md`. The loopback `CallbackHandler` that receives the browser's 302 on `http://localhost:1455/auth/callback` does NOT live here — it's wired into the `*http.Server` built by `oauth.Coordinator.StartBrowser` (Rail A is a side-channel, not an admin-API endpoint). | **New** |
| `internal/api/exportapi/` | **New**. HTTP handler for `POST /api/admin/accounts/{id}/export-auth-json` per `contracts/accounts-api.md` | **New** |
| `internal/api/adminapi/accounts.go` | Existing 002 `/api/admin/accounts` GET/POST — add `auth_method` + OAuth metadata to the row projection per `data-model.md`; teach POST to accept `auth_method='api_key'` explicitly and reject the other three with envelope `code:3013 oauth_mode_requires_flow_endpoint` (HTTP 200) | **Modified** (+~80 LOC) |
| `internal/api/adminapi/import_auth_json.go` | **New**. Handler for `POST /api/admin/accounts/import-auth-json`. Lives under `adminapi` (not `oauthapi`) because it's account-mutating, not flow-related | **New** |
| `internal/api/adminapi/reauth.go` | **New**. Handler for `POST /api/admin/accounts/{id}/reauth` (US-5 / FR-010). API-key branch: validates the new key + `store.RotateAPIKey(id, key)`. OAuth branch: delegates to `oauth.Coordinator.TryStartFlow(method, targetAccountID=id)` so the resulting token triplet overwrites the existing row's credential columns without touching `name` / `provider` / `base_url` / routing config. | **New** |
| `internal/domain/account.go` | Add `AuthMethod` discriminator + seven new fields on `UpstreamAccount`; add the single-row invariant helper `Validate()` | **Modified** (+~50 LOC) |
| `internal/store/accounts.go` | Extend SELECT/INSERT/UPDATE to cover the seven new columns; add a `ListForAdminAPI` read-side projection that never selects token bytes | **Modified** (+~60 LOC) |
| `internal/store/migrations/{sqlite,postgres,mysql}/000002_multi_mode_auth.{up,down}.sql` | New migration per `data-model.md` | **New** (×6 files) |
| `internal/core/account_selector.go` | Accept an optional `PreForward(ctx, *acct) (accessToken []byte, usedFallback bool, err error)` hook; if nil, fall through to 002 behavior (returns `[]byte(acct.APIKey), false, nil`). Expose `SelectEligible(ctx, sessionKey, eligible)` so the proxy can filter OAuth rows to supported Codex paths before sticky routing. The token return type is `[]byte` (not `string`) — see Anti-Abstraction Gate for why. | **Modified** (+~30 LOC) |
| `internal/api/proxy.go` | Calls the selector to get the selected account and current credential, sets `X-Request-Id`, and delegates account-specific upstream construction to `internal/provider/openai.Client.ForwardAccountRequest`. It also records request/response metadata and redacted body captures. | **Modified** |
| `internal/provider/openai/forwarder.go` | Implements the data-plane transport split. API-key accounts use `ForwardRequest(account.EffectiveBaseURL(), original /v1/*)`. OAuth accounts use `ForwardCodexRequest`: `/v1/responses` → `/codex/responses`, `/v1/responses/compact` → `/codex/responses/compact`, `Authorization: Bearer <access_token>`, optional `chatgpt-account-id`, fixed `User-Agent: codex_cli_rs/0.120.0 (Mac OS 26.2; arm64) dumb`, non-streaming Responses body normalization, upstream SSE collection into downstream JSON, downstream SSE preservation for streaming requests, and complete JSON capture for request-history body logging. | **Modified** |
| `frontend/src/routes/admin/accounts/` | Add `new-oauth.tsx` (picker + browser/device/import sub-forms), `detail.tsx` gains the **Export auth.json** button + metadata card (`email`, `plan_type_label`) | **Modified** |
| `frontend/src/routes/setup/` | Upgrade the wizard's *Upstream* step to surface the same four auth modes as the admin picker. API-key remains inline in `POST /api/setup/commit.first_account`; browser/device/import commit setup first, then hand off directly into the matching `/admin/accounts/new-*` route. | **Modified** |
| `frontend/src/lib/oauth-flow.ts` | **New**. TanStack Query hook polling `GET /api/admin/oauth/flow` at 1 Hz while a flow is pending | **New** |
| `openapi/admin.yaml` | **New**. OpenAPI 3.0 authoritative spec for the 003 admin surface (per `AGENTS.md §API Contract` rule #1 — OpenAPI is the single source of truth from 003 onwards). Covers every endpoint + envelope shape + error code listed in `contracts/oauth-flow-api.md` and `contracts/accounts-api.md`. Existing 001/002 endpoints remain on their Markdown contracts until the admin-auth feature (004+) folds them into this spec. | **New** |
| `openapi/redocly.yaml` | **New**. Redocly lint config that disables the `operation-4xx-response` rule (the envelope policy intentionally forbids HTTP 4xx on `/api/admin/*`). | **New** |
| `openapi/README.md` | **New**. Explains scope, deferrals, codegen pipeline. | **New** |
| `frontend/src/lib/plan-label.ts` | **New**. Pure function mapping `plan_type` → human-readable label per FR-011a (client-side mirror of the server's `plan_type_label`; used as a fallback when the server omits the label) | **New** |

**3 new Go packages**, all inside the single existing router service. The Constitution's Simplicity Gate ceiling of **3 sub-projects** is about *independently deployable services*, not Go packages — 003 stays at **0 new sub-projects**, so the ceiling is not approached. (002 already sits at 1 sub-project: the router.)

### Sentinel errors (package `internal/oauth`)

These are the canonical sentinels returned by `Coordinator` methods; callers (the two rail handlers, the admin API layer, the `/reauth` handler) map them to HTTP status codes via `errors.Is`. No other sentinel is expected; any other error returned is wrapped with `fmt.Errorf("…: %w", err)` and surfaced as `500 internal` by the handler.

All admin-API mappings below are **envelope-coded** (HTTP 200 + `{code, msg, data}`) per `docs/error-codes.md` and the 003 contracts. The `GET /auth/callback` loopback rail is **not** admin-API and is envelope-exempt (plaintext/HTML to the browser).

| Sentinel | Returned by | Envelope mapping (admin API) — HTTP 200 + code |
|---|---|---|
| `ErrFlowInProgress` | `Coordinator.TryStartFlow` when the atomic slot is already non-nil | `code: 3001 oauth_flow_in_progress`; `data = {method, flow_id, expires_at, created_at}` describing the in-flight flow (FR-008, `contracts/oauth-flow-api.md` §`POST /browser/start`) |
| `ErrFlowNotFound` | `Coordinator.Cancel` / `ConsumeCode` when `flow_id` does not match the current slot (or the slot is nil) | `code: 3008 flow_id_mismatch` on `/cancel`; `code: 3004 no_flow_in_progress` on `/manual-callback` |
| `ErrStateMismatch` | `Coordinator.ConsumeCode` on constant-time `state` mismatch | Rail A (loopback): `400 Bad Request` plaintext page (envelope-exempt); Rail B (paste): envelope `code: 3003 oauth_state_mismatch`; either way `oauth_rail_rejected` INFO log; flow stays `pending`. **State validation runs BEFORE the `access_denied` cancel branch on both rails** (FR-006) — an unauthenticated cancel MUST NOT tear down a live flow. |
| `ErrAlreadyConsumed` | `Coordinator.ConsumeCode` when `Flow.Consumed.CompareAndSwap(false, true)` returns false | Rail A: 200 success HTML (the other rail has already closed the flow — double-close is harmless; no log event); Rail B: envelope `code: 3005 already_consumed` with `data = {rail_won: "loopback"\|"manual_paste"}` so the UI polls `/flow` once more and discovers the success; `oauth_rail_rejected` INFO log with `error_code=already_consumed` |
| `ErrFlowExpired` | `Coordinator.ConsumeCode` when `time.Now().After(Flow.ExpiresAt)` | Rail A: 410 Gone plaintext (envelope-exempt); Rail B: envelope `code: 3006 flow_expired`; `oauth_flow_expired` INFO log (already logged by the expiry reaper, so this is the fallback when reaper lost the race) |

### Data Flow (P0 user stories)

**US-1: OAuth browser onboard** — `research.md` Decision 2 covers the loopback-listener pattern. FR-012 dual-rail: both callback rails are always open for every flow. The server always best-effort binds the loopback AND always accepts a pasted URL via `/browser/manual-callback`. Whichever rail delivers a valid `code+state` first wins via a `Flow.Consumed` CAS; the other short-circuits. There is NO mode parameter, NO hostname heuristic, NO operator toggle.

```
UI clicks Start
 → POST /api/admin/oauth/browser/start  {provider}                            (oauthapi.StartBrowser)
   → oauth.Coordinator.StartBrowser(ctx, provider)                            (internal/oauth/flow.go)
     → generate PKCE pair + state token (32-byte crypto/rand, base64url, 43-char)
     → best-effort bind DUAL loopback listeners: 127.0.0.1:1455 + [::1]:1455
         ├─ at least one binds on 1455 → Flow.ListenerBound = true, CallbackServer wired
         └─ 1455 bind fails            → Flow.ListenerBound = false, CallbackServer = nil
                                             (NO 500 — this is a normal success; Rail B alone will close the flow)
     → persist Flow via coordinator.TryStartFlow(flow)                        (atomic.Pointer swap; ErrFlowInProgress if non-nil,
                                                                              surfaced as envelope `code:3001 oauth_flow_in_progress`)
     → return {flow_id, authorize_url, callback_url, listener_bound, expires_at, method}
                                                                              (callback_url always present — it's the literal
                                                                               redirect_uri baked into authorize_url, useful even
                                                                               when listener_bound=false so the UI can show the
                                                                               operator "this is the URL your browser will land on")
 → UI opens authorize_url in new tab AND renders the "Paste callback URL" textarea unconditionally
   (textarea visibility is NEVER conditioned on listener_bound per FR-012 — operator always has a safety net)
 → Operator signs in at auth.openai.com; browser 302s to http://localhost:1455/auth/callback?code=…&state=…
   → Two rails race for the same Flow:

   ┌─ Rail A (loopback) — fires when listener_bound=true AND the browser's redirect actually reaches this host:
   │    listener handler (CallbackHandler):
   │      oauth.Coordinator.ConsumeCode(ctx, flowID, code, state, rail="loopback")
   │        ├─ constant-time state compare vs Flow.State (runs BEFORE any mutation, including error=access_denied)
   │        │     ├─ mismatch → 400 plaintext page (envelope-exempt; the listener is browser-facing),
   │        │     │             Flow stays pending, paste rail still alive,
   │        │     │             INFO oauth_rail_rejected {rail=loopback, error_code=oauth_state_mismatch}
   │        │     └─ state OK → branch:
   │        │              ├─ error=access_denied (cancel) → Flow.Consumed.CompareAndSwap(false,true)
   │        │              │       ├─ CAS lost → 200 success HTML (paste rail already closed the flow)
   │        │              │       └─ CAS won  → Flow.Status=error, Flow.ConsumedBy="loopback",
   │        │              │                     INFO oauth_flow_cancelled {rail=loopback}, 200 cancel page
   │        │              └─ happy path (code present) → Flow.Consumed.CompareAndSwap(false,true)
   │        │                      ├─ CAS lost → 200 success HTML (no double-exchange)
   │        │                      └─ CAS won  → POST auth.openai.com/oauth/token (code + code_verifier)
   │        │                                    ├─ 4xx/5xx → Flow.Status=error, 502 plaintext page,
   │        │                                    │            INFO oauth_flow_failed {rail=loopback, error_code, error_message}
   │        │                                    └─ ok      → parse OAuthTokens → extract id_token claims
   │        │                                                 (email, plan_type, chatgpt_account_id)
   │        │                                                 → store.InsertUpstreamAccount(auth_method=oauth_browser, tokens, metadata)
   │        │                                                 → Flow.Status=success, Flow.ConsumedBy="loopback", listener.Close()
   │        │                                                 → INFO oauth_flow_completed {rail=loopback}, 200 success HTML
   │
   └─ Rail B (manual paste) — fires when the operator submits the textarea (common when listener_bound=false,
       OR when the browser redirect never reached a listener that IS bound — e.g. remote router, corporate proxy):
         Operator copies http://localhost:1455/auth/callback?code=…&state=… from their browser address bar, pastes, Submit
         → POST /api/admin/oauth/browser/manual-callback {callback_url}       (oauthapi.ManualCallback)
         → handler does URL prefix validation (must start with http://localhost:1455/auth/callback;
             mismatch → envelope code:3007 invalid_callback_url, data.reason="url_prefix_mismatch", flow stays pending)
         → handler requires `code` OR `error` query param (both missing → envelope code:3007, data.reason="missing_code_and_error")
         → oauth.Coordinator.ConsumeCode(ctx, flowID, code, state, rail="manual_paste")
              ├─ constant-time state compare (runs BEFORE any branch, including access_denied)
              │     └─ mismatch → envelope code:3003 oauth_state_mismatch, Flow stays pending,
              │                   loopback rail still alive, INFO oauth_rail_rejected {rail=manual_paste, error_code=oauth_state_mismatch}
              └─ state OK → branch:
                    ├─ error=access_denied (cancel) → Flow.Consumed.CompareAndSwap(false,true)
                    │       ├─ CAS lost → envelope code:3005 already_consumed, data.rail_won="loopback"
                    │       └─ CAS won  → Flow.Status=error, Flow.ConsumedBy="manual_paste",
                    │                     INFO oauth_flow_cancelled {rail=manual_paste},
                    │                     envelope code:0 msg:"ok" data:{status:"cancelled", rail:"manual_paste"}
                    ├─ other error=<provider_error> → envelope code:3016 oauth_upstream_error,
                    │       data={provider_error, provider_message}, Flow stays pending, no /oauth/token call,
                    │       INFO oauth_rail_rejected {rail=manual_paste, error_code=<provider_error>}  (NOT oauth_flow_failed — flow is still pending)
                    └─ happy path (code present) → Flow.Consumed.CompareAndSwap(false,true)
                          ├─ CAS lost → envelope code:3005 already_consumed, data.rail_won="loopback"
                          └─ CAS won  → POST auth.openai.com/oauth/token (code + code_verifier)
                                        ├─ invalid_grant → envelope code:3009 oauth_invalid_grant,
                                        │                  Flow.Status=error, INFO oauth_flow_failed {rail=manual_paste}
                                        ├─ other 4xx/5xx → envelope code:3016 oauth_upstream_error,
                                        │                  Flow.Status=error, INFO oauth_flow_failed {rail=manual_paste}
                                        └─ ok            → same persist path as Rail A
                                                           → Flow.Status=success, Flow.ConsumedBy="manual_paste",
                                                             listener.Close() iff bound
                                                           → INFO oauth_flow_completed {rail=manual_paste}
                                                           → envelope code:0 msg:"ok"
                                                             data:{account, rail:"manual_paste", status:"success"}

 → UI polls GET /api/admin/oauth/flow; sees {status:"success", rail:"…", account:{…}}; navigates to admin/accounts
```

Key invariants: (1) exactly ONE `ConsumeCode` entry point is used by both rails — no per-rail code duplication. (2) `Flow.Consumed atomic.Bool` CAS is the single serialization point — no mutex, no channel, no lock contention in the happy path. (3) `Flow.State` + `Flow.CodeVerifier` validation is identical across rails. (4) `Flow.ListenerBound` is the canonical teardown discriminator (only Close the listener when bound). (5) The UI renders the paste textarea regardless of `listener_bound` — operator UX is identical on laptop vs server deployments.

**US-4: Auto-refresh before forward** — `research.md` Decision 3 covers single-flight.
```
Client POST /v1/responses
 → proxy.ProxyHandler.ServeHTTP
   → eligible := proxyAccountEligible(original.URL.Path)
   → account, accessToken, usedFallback, err := selector.SelectEligible(ctx, sessionKey, eligible)
                                                                           SelectEligible filters unsupported
                                                                           OAuth rows before sticky routing, then
                                                                           calls the AccountSelector.PreForward
                                                                           func hook wired by BuildApp to
                                                                           oauth.Coordinator.RefreshIfStale.
                                                                           When nil (002 bench tests only) the
                                                                           selector falls back to acct.APIKey.

     // inside oauth.Coordinator.RefreshIfStale(ctx, acct) ([]byte, bool, error):
     if acct.AuthMethod == "api_key":  return []byte(acct.APIKey), false, nil  (pass-through for 002 rows)
     if !shouldRefresh(acct.LastRefresh, nowFn, thresholdFn):                  (no refresh needed)
         return acct.AccessToken, false, nil
     v, err, _ := group.Do(strconv.FormatInt(acct.ID, 10), func() {        singleflight.Group lives on
         defer recoverToError(&err)                                        oauth.Coordinator, NOT on
         tokens := provider.Refresh(ctx, acct.RefreshToken)                AccountSelector.
         store.UpdateTokens(acct.ID, tokens, lastRefresh=acct.LastRefresh) optimistic lock on last_refresh
         emit INFO oauth_refresh_ok (fields in contracts/oauth-flow-api.md Observability §)
         // leader emits oauth_refresh_transient_fallback WARN exactly once when the refresh
         // failed transiently and the leader is returning the stale access_token as a bridge;
         // waiters MUST NOT emit a second WARN (they just observe usedFallback=true via the
         // return tuple). See HR-15/HR-19 / T-039.
         return refreshResult{token: tokens.AccessToken, usedFallback: false}, nil
     })
     r := v.(refreshResult)
     return r.token, r.usedFallback, err

  → client.ForwardAccountRequest(ctx, account, accessToken, original)
      ├─ auth_method=api_key:
      │    target = strings.TrimRight(account.EffectiveBaseURL(), "/") + original.URL.Path
      │    query = original.URL.RawQuery
      │    Authorization = "Bearer " + string(accessToken)
      │    response body/headers remain provider-compatible, excluding hop-by-hop and router-owned trace headers
      └─ auth_method in oauth_*:
           target = strings.TrimRight(ChatGPTBackendBaseURL, "/") + codexBackendPath(original.URL.Path)
           /v1/responses          → /codex/responses
           /v1/responses/compact  → /codex/responses/compact
           Authorization = "Bearer " + string(accessToken)
           chatgpt-account-id = account.ChatGPTAccountID when present
           non-streaming /v1/responses: normalize request JSON, ask Codex upstream for SSE,
             return collected JSON downstream and store the same JSON in upstream_response_body when logging is enabled
           streaming /v1/responses: keep SSE streaming to the downstream client
```

Key invariant: `singleflight.Group` is OWNED by `oauth.Coordinator` and keyed on the `account_id`. `AccountSelector.PreForward` is a dumb dispatcher that just calls the hook — it holds no concurrency primitive. That keeps the 002 selector compilable without the oauth package imported at all. The returned token bytes are ready for the `Authorization: Bearer …` header; `usedFallback=true` signals the caller that the leader returned the stale access token after a transient refresh failure (see `contracts/oauth-flow-api.md §oauth_refresh_transient_fallback`). `ForwardAccountRequest` is the credential-shape switch: API-key rows keep 002's Platform-compatible forwarding (`base_url + /v1/*`), while OAuth rows use the ChatGPT Codex backend and add `chatgpt-account-id` when the row has one. OAuth rows are eligible only for `/v1/responses` and `/v1/responses/compact`; other `/v1/*` paths must be handled by API-key rows or return the existing no-capacity router error if none is available.

**US-5: Re-authenticate in place** — `adminapi.Reauth` dispatches on the row's existing `auth_method`.
```
UI → POST /api/admin/accounts/{id}/reauth  (body and query vary by branch)
 → adminapi.Reauth (new)
   → row := store.GetAccount(id)          (missing → envelope code:1001 account_not_found, HTTP 200)
   → branch on row.AuthMethod:

     API-key branch (row.AuthMethod == "api_key"):
       → require request body {"api_key": "sk-..."}, else envelope code:3012 auth_method_mismatch,
           data.row_auth_method="api_key"
       → validate api_key shape (same 002 validator); envelope code:2005 invalid_api_key on fail
       → store.RotateAPIKey(id, newKey)   // updates only api_key + updated_at
       → envelope code:0 msg:"ok" data:{account: AccountListItem}

     OAuth branch (row.AuthMethod starts with "oauth_"):
       → require empty JSON object body {} + ?method=browser|device, else envelope code:3012 auth_method_mismatch,
           data.row_auth_method=row.AuthMethod
       → oauth.Coordinator.TryStartFlow(Flow{Method, TargetAccountID: id})
         → on flow success the same callback/poll path that US-1/US-3 uses
           calls store.UpdateOAuthCredentials(id, newTokens) — a credential-only
           UPDATE keyed on id, leaving name / provider / base_url / created_at
           / routing config untouched (FR-010); after commit the row observably
           holds only the new (access, refresh, id) bytes (US-5 AC-1 "drop the
           previously stored tokens") — the UPDATE overwrites them in place
       → envelope code:0 msg:"ok" data:{flow_id, authorize_url|user_code,
           callback_url|verification_url, listener_bound|interval_seconds,
           expires_at, method, target_account_id}
```

**US-6: Import auth.json**
```
UI POST multipart/form-data (single `auth_json` part) → POST /api/admin/accounts/import-auth-json
 → adminapi.ImportAuthJSON (new)
   → r.ParseMultipartForm(16<<10+4<<10)        envelope code:3010 invalid_auth_json_structure
                                                (content-type not multipart OR no `auth_json` part)
   → read `auth_json` part via                  envelope code:2009 request_body_too_large (reuses 002's code)
     io.LimitReader(part, 16<<10+1)             data.limit_bytes=16384
   → Decode JSON (fail-fast on shape)          envelope code:3010 invalid_auth_json_structure (non-JSON / non-object)
                                                OR envelope code:3011 invalid_auth_json
                                                  data.missing_fields=[...]
   → oauth.ExtractClaims(id_token)             → email, plan_type, chatgpt_account_id
   → derive account name: email  (fallback: chatgpt_account_id)   — server-side, NOT from the request
   → provider is hard-coded "openai" in 003
   → store.InsertUpstreamAccount(auth_method=oauth_import, tokens, metadata)
   → envelope code:0 msg:"ok" data:{account: AccountListItem}
```

### Trade-off Analysis

| Decision | Approach A | Approach B | Chosen | Rationale |
|---|---|---|---|---|
| Token column type | `TEXT` (readable) | `BLOB`/`BYTEA`/`VARBINARY` (opaque bytes) | **B** | `research.md` Decision 5 — future key-vault plugin lands without schema migration |
| Refresh dedup | per-account `sync.Mutex` | `singleflight.Group` keyed on `acct.ID` | **singleflight** | `research.md` Decision 3 — mutex serializes refresh cost into every caller's latency |
| ID-token verify | JWKS-backed signature check | base64+JSON decode only | **decode only** | `research.md` Decision 6 — token was just received over TLS from the issuer |
| Export endpoint verb | `GET` (curl-friendly) | `POST` (audit-friendly, no proxy-log leak) | **POST** | `research.md` Decision 8 — `GET` would log `account_id` in every intermediate proxy |
| OAuthFlow persistence | DB row | in-memory `atomic.Pointer` | **memory** | `research.md` Decision 4 — persisting `code_verifier`/`state` violates FR-003's redaction posture |
| AuthMethod as separate Go type | `type AuthMethod int` with const | `type AuthMethod string` with const | **string** | Matches the 002 `status` pattern; strings round-trip through JSON without custom marshallers |
| `POST /api/admin/accounts` accepting OAuth modes | Yes (one entry point) | No, OAuth goes through dedicated flow endpoints | **No — envelope `code:3013 oauth_mode_requires_flow_endpoint`** | OAuth onboarding is intrinsically multi-step (start → callback); cramming it into the sync POST leaks half-baked rows or forces a fake synchronous response; cleaner contract |
| Refresh threshold | `expires_in / 2` | `expires_in - 5min` | **`expires_in / 2`** | `codex-lb` uses half; empirically it leaves enough slack even when expires_in collapses during an outage |

### Project Structure (NEW + MODIFIED files only)

```
internal/
├── oauth/                                        [NEW — 4 files, ≤450 LOC total]
│   ├── doc.go
│   ├── flow.go              # Flow struct + StartBrowser + StartDevice + CancelFlow + PollFlow
│   ├── exchange.go          # PKCE gen, authorize-URL build, code-exchange, refresh, device-poll
│   ├── claims.go            # decode id_token → {email, plan_type, chatgpt_account_id}
│   └── coordinator.go       # Coordinator: atomic.Pointer[Flow] guard + ConsumeCode (the single code-exchange entry point shared by both rails, CAS-guarded on Flow.Consumed) + singleflight-backed RefreshIfStale hook + sentinel errors (ErrAlreadyConsumed, ErrFlowExpired, ErrStateMismatch)
├── api/
│   ├── oauthapi/                                 [NEW — 2 files]
│   │   ├── doc.go
│   │   ├── handlers.go      # 5 HTTP handlers per contracts/oauth-flow-api.md
│   │   └── handlers_contract_test.go
│   ├── exportapi/                                [NEW — 2 files]
│   │   ├── export_auth_json.go
│   │   └── export_auth_json_contract_test.go
│   └── adminapi/
│       ├── accounts.go                           [MODIFIED]
│       ├── import_auth_json.go                   [NEW]
│       ├── import_auth_json_contract_test.go     [NEW]
│       ├── reauth.go                             [NEW]
│       └── reauth_contract_test.go               [NEW]
├── core/account_selector.go                      [MODIFIED — PreForward hook + SelectEligible]
├── api/proxy.go                                  [MODIFIED — call SelectEligible and ForwardAccountRequest]
├── domain/account.go                             [MODIFIED — +AuthMethod + 7 fields]
├── store/accounts.go                             [MODIFIED]
├── store/migrations/sqlite/000002_multi_mode_auth.up.sql    [NEW]
├── store/migrations/sqlite/000002_multi_mode_auth.down.sql  [NEW]
├── store/migrations/postgres/000002_multi_mode_auth.up.sql  [NEW]
├── store/migrations/postgres/000002_multi_mode_auth.down.sql[NEW]
├── store/migrations/mysql/000002_multi_mode_auth.up.sql     [NEW]
└── store/migrations/mysql/000002_multi_mode_auth.down.sql   [NEW]
frontend/
├── src/routes/admin/accounts/
│   ├── new-oauth.tsx                             [NEW]
│   └── detail.tsx                                [MODIFIED — export button + metadata card]
├── src/routes/setup/
│   └── wizard.tsx                                [MODIFIED — four-mode upstream step + post-commit handoff]
├── src/lib/
│   ├── oauth-flow.ts                             [NEW — TanStack Query hook]
│   └── plan-label.ts                             [NEW — client-side label fallback]
└── tests/e2e/
    ├── oauth-browser-dual-rail.spec.ts           [NEW — US-1 dual-rail: both rails happy paths + CAS race, FR-012]
    ├── oauth-device-happy.spec.ts                [NEW]
    ├── setup-multi-mode-onboarding.spec.ts       [NEW — cold-install handoff from /setup/ into 003 onboarding]
    ├── auth-json-import.spec.ts                  [NEW]
    ├── auth-json-export.spec.ts                  [NEW]
    └── account-metadata-render.spec.ts           [NEW]
openapi/                                          [NEW — 003 introduces the OpenAPI source of truth for admin APIs per AGENTS.md §API Contract rule #1]
├── admin.yaml                                    [NEW — 003 admin surface; 001/002 endpoints deferred to admin-auth (004+)]
├── redocly.yaml                                  [NEW — lint config, disables the 4xx-required rule since envelope policy forbids HTTP 4xx on /api/admin/*]
└── README.md                                     [NEW — scope, deferrals, codegen pipeline]
specs/003-multi-mode-codex-auth/
├── plan.md                                       [THIS FILE]
├── research.md                                   [NEW]
├── data-model.md                                 [NEW]
├── quickstart.md                                 [NEW]
└── contracts/
    ├── oauth-flow-api.md                         [NEW]
    └── accounts-api.md                           [NEW]
```

### Traceability (spec → plan)

| Spec item | Plan artifact(s) |
|---|---|
| US-1 (AC-1 dual-rail) / FR-006 / FR-012 | `internal/oauth/flow.go` (`Coordinator.StartBrowser` — best-effort dual-stack loopback bind; `Flow.ListenerBound` captures outcome; `Flow.Consumed atomic.Bool` guards the code-exchange path), `internal/oauth/coordinator.go` (`ConsumeCode(ctx, flowID, code, state, rail)` — the ONE code-exchange entry point used by both rails, runs constant-time state compare + CAS + `/oauth/token`), `internal/oauth/exchange.go` (authorize-URL builder + `/oauth/token` exchange), `oauthapi/handlers.go` `POST /browser/start` (single envelope `code:0` response with `listener_bound` in `data`), `oauthapi/handlers.go` `POST /browser/manual-callback` (delegates to `ConsumeCode` with `rail="manual_paste"`, returns envelope `code:3005 already_consumed` on CAS-loss), loopback `CallbackHandler` (delegates to `ConsumeCode` with `rail="loopback"`, envelope-exempt plaintext/HTML to the browser), `frontend/src/routes/admin/accounts/new-oauth.tsx` (always renders the paste textarea regardless of `listener_bound`; polls `/flow` and dismisses itself on `status=success`) |
| US-2 | `adminapi/accounts.go` — unchanged POST path when `auth_method='api_key'`; contract assertion in `accounts-api.md` |
| US-3 / FR-007 | `oauth/flow.go` (`StartDevice` + device-polling goroutine), `oauth/exchange.go` (`RequestDeviceCode` / `PollDeviceCode`), `oauthapi/handlers.go` `/device/start` |
| US-4 / FR-004 / FR-009 | `oauth/coordinator.go` (`singleflight.Group` + `RefreshIfStale`), `core/account_selector.go` (adds the `PreForward` function-hook field — a plain `func`, not an interface — which `app.App` wires to `Coordinator.RefreshIfStale`; exposes `SelectEligible` so path capability filtering happens before routing), `api/proxy.go` (uses the selector-returned access token instead of reading `acct.APIKey` directly, then delegates transport construction to `ForwardAccountRequest`) |
| US-5 / FR-010 | `adminapi/reauth.go` (NEW — handler for `POST /api/admin/accounts/{id}/reauth`: API-key branch writes `store.RotateAPIKey`; OAuth branch delegates to `oauth.Coordinator.TryStartFlow` with `target_account_id` tagged so on success the existing row is overwritten, not a new row created), `store/accounts.go` credential-only UPDATE (`name` / `provider` / `base_url` / routing stats untouched) |
| US-6 | `adminapi/import_auth_json.go`, `oauth/claims.go` (id_token decode) |
| FR-002 / FR-011 / FR-011a | `data-model.md` schema, `store/accounts.go` `ListForAdminAPI`, `adminapi/accounts.go` projection including `plan_type_label`, `frontend/src/lib/plan-label.ts` |
| FR-003 | `data-model.md` BLOB/BYTEA/VARBINARY column types; log-scrub CI gate (inherited from 002) asserting zero token bytes in any log line |
| FR-008 | `atomic.Pointer[oauth.Flow]` on `*App`; envelope `code:3001 oauth_flow_in_progress` path covered by `oauth-flow-api.md` |
| FR-012 | See US-1 traceability row above (dual-rail always-on: best-effort loopback bind + always-rendered paste textarea + `Flow.Consumed` CAS + unified `ConsumeCode` entry point + envelope `code:3005 already_consumed` on CAS-loss for Rail B, 200 success HTML for Rail A). E2E coverage: `frontend/tests/e2e/oauth-browser-dual-rail.spec.ts` exercises both rails (loopback happy path AND paste happy path AND CAS-race — both rails fire, one wins, the other is harmless). |
| FR-013 | `frontend/src/routes/setup/wizard.tsx` (same four auth-mode choices on the wizard's Upstream step), `POST /api/setup/commit` remains 002-compatible for API-key and skip paths, while browser/device/import choices hand off post-commit into `frontend/src/routes/admin/accounts/{new-oauth,new-oauth-device,new-import}.tsx`; dedicated cold-install coverage lands in `frontend/tests/e2e/setup-multi-mode-onboarding.spec.ts` |
| FR-014 | `exportapi/export_auth_json.go`, `data-model.md` `AuthJSON` projection, `frontend/src/routes/admin/accounts/detail.tsx` export button + blob-download |

---

## Risk Assessment

| Risk | P | I | Mitigation | Verification |
|---|---|---|---|---|
| **Token bytes leak** into a log line, admin-API response, or HTTP error body (violates FR-003, SC-4) | M | H | (a) token columns NEVER populated into any struct read by a handler other than the export endpoint; (b) `slog` handlers always construct log attrs from `email`/`plan_type`/`account_id`, never from `*tokens`; (c) `stringer`-style `String()` on the token-bearing struct returns `"<redacted>"` | CI grep-gate on the test run (already exists for 002's API-key path; extended to the 3 token keys) + log-schema contract test |
| **Single-flight loses refresh result on panic** → next caller re-refreshes; double refresh invalidates older token → 401 storm | L | H | `singleflight.Do` returns the error; `oauth.Coordinator` always paths `recover()`s inside the Do fn and surfaces the panic as an error; refresh-on-error is retried by the NEXT request, not re-entered by the same burst | Race test with 50 goroutines + one forced panic in the Do fn |
| **Loopback listener port race** between two operators on the same box | L | M | Bind only canonical port 1455; envelope `code:3001 oauth_flow_in_progress` on second start (FR-008 already forbids two flows anyway); if 1455 is occupied by another process, Rail B paste remains available | 1455-bound/unbound unit test + E2E test that starts the flow twice in parallel |
| **Migration runs on a dirty 002 DB** (operator modified rows by hand with a raw SQL client mid-upgrade) | L | M | Migration is pure `ADD COLUMN` + `DROP NOT NULL` — idempotent and safe against concurrent readers. `api_key` stays populated for 002 rows (its NOT NULL drop just removes the constraint, not the values) | Integration test: seed 100 rows from 002 schema, run migration, assert all 100 rows still pass the `Validate()` invariant |
| **OpenAI rotates `client_id` or adds a new required `/oauth/authorize` param** | L | H | Hardcoded in one place (`internal/oauth/exchange.go`); a new spec (a thin `003.1`) can patch it. Alerting via the `oauth_flow_failed` rate |  Monitor the production `oauth_flow_failed` rate; no code mitigation |
| **`auth.json` import payload bombs the server** with a 100 MB "auth.json" | L | L | Endpoint has a **dual-layer body cap** (see `research.md` Decision 7 + `tasks.md T-065`): outer `http.MaxBytesReader` caps the entire multipart envelope at **64 KB** (blocks preamble-flood); inner `io.LimitReader` caps the `auth_json` part alone at **16 KB**. Both trip with HTTP 200 + `code:2009 request_body_too_large`, discriminated by `data.scope ∈ {envelope, part}`. | `TestAuthJSONImport_InnerPartCap_EnvelopeRequestBodyTooLarge` (part>16 KB) + `TestAuthJSONImport_OuterEnvelopeCap_PreambleFlood` (envelope>64 KB preamble) |
| **Race between refresh-successful-write and concurrent UPDATE from `/admin/reauth`** | L | M | Refresh UPDATE uses a `WHERE id=? AND last_refresh=?` optimistic lock. If mismatch, `oauth.Coordinator.RefreshIfStale` returns a sentinel `errConcurrentRefreshConflict` (an **internal-only** Go error; **never** surfaced as an HTTP error code to any client) — the outer caller (proxy → selector → `PreForward`) logs INFO `oauth_refresh_conflict_retry` and re-reads the account row before retrying once more. If the retry also races, the original 5xx from the upstream flows to the Codex client with no attempt at further magic. | Race test: concurrent refresh + reauth on the same `account_id` with `-race` |
| **`chatgpt_account_id` collision across sessions of the same user** (US-6 edge case) | L | L | Row insert is unconditional (operator chose to import). A non-fatal UI warning fires on duplicate detection during `/accounts` render; it does NOT block | Integration test |
| **`id_token` claim vocabulary drift** (new `plan_type` values) | M | L | `plan_type_label` computation is a default-map with raw-string fallback (FR-011a). New values just render the raw string | Contract test includes a fixture id_token with `plan_type = "chatgpt-pro"` (unmapped) and asserts the API returns `plan_type_label = "chatgpt-pro"` |

No risks escalated to "H probability" → plan ships with the above mitigations.

---

## Security Considerations

- **Authentication on every endpoint**: every new `/api/admin/oauth/*`, `/api/admin/accounts/*`, and `/api/admin/accounts/{id}/export-auth-json` route is wired through the existing 002 admin-auth plugin chain. When the admin-auth plugin is disabled (single-operator local deploys), the operator inherits the 002 trust boundary — documented in FR-014.
- **Authorization**: 003 does not introduce role-based ACL. Any admin who can call `/api/admin/settings/update` can call these; that's the 002 model, explicitly in scope.
- **Input validation**:
  - `POST /api/admin/accounts/import-auth-json`: **multipart/form-data** wire shape with ONE required part `auth_json` (raw `auth.json` bytes); NO `name` / `provider` fields in the request (aligned with codex-lb — server derives `name` from the id_token `email` claim and hard-codes `provider = "openai"`). **Dual-layer body cap** (research.md Decision 7): outer `http.MaxBytesReader` caps the whole multipart envelope at **64 KB** (defence against multipart-preamble flood DoS), inner `io.LimitReader` caps the `auth_json` part at **16 KB** (semantic payload budget). JSON-shape validator with per-field error messages (envelope `code:3011 invalid_auth_json` for missing/invalid fields; `code:3010 invalid_auth_json_structure` for non-multipart content-type / missing part / non-JSON or non-object part body; `code:2009 request_body_too_large` with `data.scope ∈ {envelope, part}` + `data.limit_bytes` when either cap trips). No path traversal because the endpoint takes no filenames; no SSRF because no operator-controlled URL flows to an outbound fetch. Unknown top-level keys inside the parsed JSON are **ignored** (US-6 edge case 4) — the spec chose this deliberately; a `reject unknown fields` mode would surprise operators whose Codex CLI version added a new key.
  - OAuth `state` validation (FR-006): constant-time compare (`crypto/subtle.ConstantTimeCompare`) inside `Coordinator.ConsumeCode` in `internal/oauth/coordinator.go`. Two rails, two response shapes — but in **both** cases zero DB side-effects, the other rail stays alive (can still close the flow), and the Flow does NOT transition to `error`:
    - **Rail A (loopback, `GET /auth/callback`)**: state mismatch ⇒ the listener returns `400 Bad Request` with a plaintext human page ("authentication was rejected — state mismatch") — this is the envelope-exempt browser-facing rail; `oauth_rail_rejected` INFO log with `error_code=oauth_state_mismatch` + `rail=loopback`, listener stays up until the flow's hard `ExpiresAt` deadline so Rail B can still close the flow on operator paste.
    - **Rail B (paste, `POST /api/admin/oauth/browser/manual-callback`)**: state mismatch ⇒ envelope `code:3003 oauth_state_mismatch` at HTTP 200 (per `contracts/oauth-flow-api.md`) so the UI can render the right banner; `oauth_rail_rejected` INFO log with `error_code=oauth_state_mismatch` + `rail=manual_paste`.
  - CAS-loss path (per-rail): when `Flow.Consumed.CompareAndSwap(false, true)` returns false (the other rail already won), Rail A renders a 200 success page (flow is heading to success — no operator-facing error), Rail B returns envelope `code:3005 already_consumed` at HTTP 200 (the UI's next `/flow` poll will show `status=success` and auto-navigate). Neither path calls `/oauth/token` a second time. This is the core FR-012 safety property.
  - OAuth loopback listener route surface: **one** path only (`GET /auth/callback`). The default `http.ServeMux` returns `404 Not Found` (no banner, no version header) for anything else — a belt-and-suspenders check against drive-by scans during the 5-minute window the listener is up.
  - OAuth callback → upstream exchange failures: if `state` + `code` validate but the follow-on `POST auth.openai.com/oauth/token` fails (network, 4xx, 5xx), the loopback listener returns `502 Bad Gateway` to the browser with a plaintext human page (envelope-exempt, browser-facing), Flow.Status=`error`, `oauth_flow_failed` INFO log with the provider-supplied `error_code` + `rail=loopback`, no row written. The `/manual-callback` variant returns envelope `code:3009 oauth_invalid_grant` / `code:3016 oauth_upstream_error` at HTTP 200 per the contract table with the same `oauth_flow_failed` INFO log + `rail=manual_paste`. (`error=access_denied` arrives as a query param on the pasted URL, NOT as a `/oauth/token` response — the server short-circuits to the cancel path BEFORE calling `/oauth/token` but AFTER validating `state`: `Flow.Status=error` + `oauth_flow_cancelled` + envelope `code:0` with `data.status="cancelled"`, per `contracts/oauth-flow-api.md` §`POST /browser/manual-callback`. Rail A's `access_denied` path is identical in substance but browser-facing: renders a plaintext cancel page.)
  - Device-code input: `user_code` never comes from the operator — we render what the provider gave us; no validation needed on our side.
- **Sensitive data**:
  - Tokens stored plaintext (FR-003 accepted); sealed-bytes-ready column shape so future encryption is zero-migration.
  - The `stringer` redaction trick on the token-bearing DTO is the single reason we can Safely `slog.Info("account created", "account", acct)` elsewhere — verify this with the log-scrub test.
  - `id_token` claims (`email`, `plan_type`, `chatgpt_account_id`) ARE allowed in logs and admin-API responses — they are metadata, NOT secrets.
- **Export endpoint sovereignty**: `POST` verb (not GET), `Content-Disposition: attachment`, `Cache-Control: no-store, private`, `X-Content-Type-Options: nosniff`; every hit audited as WARN.
- **AGENTS.md Boundaries check**:
  - ✅ "Always update specs before changing scope" — spec is in place and clarified.
  - ✅ "Keep protocol rules isolated from routing policy" — OAuth lives in `internal/oauth/`, not in any `protocol/` package; the Codex adapter only consumes the resolved access token.
  - ✅ "Preserve additive compatibility for existing clients" — API-key rows keep the 002 Platform-compatible routing and bearer-token semantics.
  - 🚫 "Never hardcode provider credentials" — the OAuth `client_id` is a public OAuth-app identifier, NOT a credential; hardcoding it is correct (it's the same value the Codex CLI binary embeds). The token **material** is operator-supplied and never hardcoded.
  - 🚫 "Never skip request logging for routed traffic" — new `/v1/responses` path still records via `RequestRecorder` from 002; OAuth refresh does NOT generate a `request_records` row (those are downstream client requests, not refresh IO).

---

## Test Hints (for downstream `sdd-test`)

### Must-test scenarios (mapped from Acceptance Criteria)

| Test | Category | Source |
|---|---|---|
| `TestOAuthBrowser_RailA_Loopback_HappyPath_Within120s` | Integration + E2E | US-1 AC-1 + AC-2 (loopback rail closes the flow) |
| `TestOAuthBrowser_RailB_ManualPaste_HappyPath` | Integration | US-1 AC-1 + FR-012 (paste rail closes the flow, listener_bound may be true or false) |
| `TestOAuthBrowser_RailA_StateMismatch_FlowRemainsOpen` | Integration | FR-006 + FR-012 (loopback state mismatch → 400 plaintext (envelope-exempt), flow stays pending, paste rail can still succeed) |
| `TestOAuthBrowser_RailB_StateMismatch_EnvelopeOAuthStateMismatch_FlowRemainsOpen` | Integration | FR-006 + FR-012 (paste state mismatch → HTTP 200 envelope `code:3003 oauth_state_mismatch`, flow stays pending, loopback rail can still succeed) |
| `TestOAuthBrowser_CAS_BothRailsRace_ExactlyOneUpstreamExchange` | Race | FR-012 first-wins CAS (50 goroutines firing both rails simultaneously; assert exactly 1 `/oauth/token` call on the mock provider + 1 `upstream_accounts` row + the losing rail observes its CAS-loss response) |
| `TestOAuthBrowser_CancelledFlowLeavesNoRow` | Integration | US-1 AC-3 |
| `TestOAuthBrowser_AccessDeniedOnPasteRail_FiresCancelled_NoRow` | Integration | US-1 AC-3 (paste a URL carrying `?error=access_denied&state=<valid>` into `/browser/manual-callback`; assert HTTP 200 envelope `code:0` with `data.status="cancelled"`, Flow.Status=error, `oauth_flow_cancelled` INFO log with `rail=manual_paste`, NO `/oauth/token` call, NO row written) |
| `TestOAuthBrowser_AccessDeniedOnPasteRail_WithoutState_RejectsRail_FlowStillPending` | Integration | Contract §`/browser/manual-callback` (paste `?error=access_denied` WITHOUT `state` or with mismatched state; assert HTTP 200 envelope `code:3003 oauth_state_mismatch`, flow stays pending, `oauth_rail_rejected` INFO log) |
| `TestOAuthBrowser_AccessDeniedOnLoopbackRail_FiresCancelled_NoRow` | Integration | US-1 AC-3 symmetric (GET the loopback with `?error=access_denied&state=<valid>`; assert 200 cancel-page HTML (envelope-exempt), Flow.Status=error, `oauth_flow_cancelled` INFO log with `rail=loopback`, NO `/oauth/token` call, NO row written) |
| `TestOAuthBrowser_ExpiresAt_FiresFlowExpired_BothRailsShutDown` | Integration | FR-012 + FR-008 (start a browser flow, sleep past `ExpiresAt` — using a test clock; assert `oauth_flow_expired` INFO log with `method=browser` + `listener_bound`, Flow.Status=error, Rail A returns 410 plaintext (envelope-exempt), Rail B returns HTTP 200 envelope `code:3006 flow_expired`, atomic.Pointer slot back to nil within the next `/flow` poll) |
| `TestOAuthBrowser_PasteInvalidPrefix_EnvelopeInvalidCallbackURL` | Integration | Contract §`/browser/manual-callback` (paste `https://evil.com/auth/callback?code=…&state=…`; assert HTTP 200 envelope `code:3007 invalid_callback_url` with `data.reason="url_prefix_mismatch"`, flow stays pending, `oauth_rail_rejected` INFO log) |
| `TestOAuthBrowser_CanonicalPort1455_Unavailable` | Unit | US-1 Edge 2 (`listener_bound=false` when canonical port 1455 is unavailable; paste rail remains usable) |
| `TestOAuthBrowser_AllPortsBusy_ListenerBoundFalse_FlowStillStarts` | Integration | US-1 Edge 2 (listener_bound=false, HTTP 200 envelope `code:0`, paste rail alone closes the flow) |
| `TestOAuthBrowser_NoModeFieldRequired_UnknownKeysIgnored` | Integration | FR-012 (POST /browser/start with body `{"provider":"openai"}` succeeds with envelope `code:0`; POST with `{"provider":"openai","mode":"auto-listener"}` OR `{"provider":"openai","topology":"…"}` ALSO succeeds with envelope `code:0` — unknown keys are silently ignored for forward compatibility, NOT rejected) |
| `TestUI_DualRail_PasteTextareaAlwaysRendered` | Frontend Vitest | FR-012 (renders paste textarea when listener_bound=true AND when false; hides it only when flow transitions to success/error) |
| `TestUI_DualRail_AutoDismissOnLoopbackSuccess` | Frontend Vitest | FR-012 (when /flow polling reports status=success with rail=loopback, paste textarea auto-dismisses) |
| `TestOAuthBrowser_SecondStart_EnvelopeOAuthFlowInProgress` | Integration | FR-008 (second `/browser/start` while a flow is active → HTTP 200 envelope `code:3001 oauth_flow_in_progress`) |
| `TestAPIKeyPath_PlatformCompatibleForwarding` | Regression | US-2 |
| `TestOAuthDevice_PollConvergesWithin2xInterval` | Integration | US-3 AC-2 |
| `TestOAuthDevice_ExpiresHitsErrorState` | Integration | US-3 AC-3 |
| `TestRefresh_SingleFlight_50ConcurrentGoroutines_ExactlyOneRefreshCall` | Race | FR-004 |
| `TestRefresh_PermanentErrorDisablesAccount` | Integration | FR-005 |
| `TestRefresh_TransientError_NoDisable_NoOperatorAction` | Integration | US-4 AC-3 |
| `TestReauth_PreservesRowIdentity_OverwritesCredentialsOnly` | Integration | FR-010 |
| `TestReauth_Envelope_Echoes_target_account_id` | Contract | FR-010 (POST `/api/admin/accounts/{id}/reauth` against an OAuth row; assert HTTP 200 envelope `code:0` with `data` shape matching `/browser/start` AND carrying `target_account_id = {id}`; assert subsequent `GET /api/admin/oauth/flow` pending envelope also carries `target_account_id`) |
| `TestAuthJSONImport_HappyPath_AllClaimsExtracted` | Integration | US-6 AC-1 |
| `TestAuthJSONImport_MissingField_EnvelopeInvalidAuthJSON_WithoutPayloadInLog` | Integration + log-schema | US-6 AC-3 + FR-003 (HTTP 200 envelope `code:3011 invalid_auth_json` with `data.missing_fields=[...]`; logs carry NO token bytes) |
| `TestAuthJSONImport_InnerPartCap_EnvelopeRequestBodyTooLarge` | Integration | US-6 Edge 3 — part body = 16 KB + 1 byte (inner `io.LimitReader` cap trips) → HTTP 200 envelope `code:2009 request_body_too_large`, `data.scope="part"`, `data.limit_bytes=16384` |
| `TestAuthJSONImport_OuterEnvelopeCap_PreambleFlood` | Integration | US-6 Edge 3 (preamble flood) — 72 KB multipart preamble + legitimate ≤ 16 KB part (outer `http.MaxBytesReader` 64 KB cap trips) → HTTP 200 envelope `code:2009 request_body_too_large`, `data.scope="envelope"`, `data.limit_bytes=65536` |
| `TestAuthJSONImport_UnknownKeys_Ignored` | Integration | US-6 Edge 4 |
| `TestExportAuthJSON_200_RawAuthJSON_ContentDispositionAttachment_WARNAudit` | Integration + log-schema | FR-014 (success body is raw JSON bytes — envelope-exempt — with `Content-Disposition: attachment; filename="auth.json"`, `Cache-Control: no-store, private`, WARN audit event) |
| `TestExportAuthJSON_APIKeyRow_EnvelopeNotOAuthAccount` | Integration | FR-014 last bullet (HTTP 200 envelope `code:3014 not_oauth_account`, `data.auth_method="api_key"`; NO raw attachment emitted — matches `openapi/admin.yaml §NotOAuthAccountEnvelope`, `contracts/accounts-api.md §export 3014`, and `tasks.md T-080 verify`). Note: `3012 auth_method_mismatch` is reserved for `/reauth` body↔row shape mismatches and is NOT used by export. |
| `TestAdminAccountsList_OAuthRowsRenderEmailPlanTypeLabel_APIKeyRowsOmitKeys` | Contract | FR-011 + FR-011a |
| `TestLogScrub_NoTokenBytesAnywhere` | CI-global | SC-4 |

### Performance test parameters

- **Refresh cost**: measured as P95 over 1,000 aged-token request bursts of 50 goroutines each; target ≤ 800 ms refresh cost amortised; fixture uses an `httptest.Server` with a fixed 200 ms response delay for `/oauth/token`.
- **Callback→persist**: measured as P95 over 100 flows with a hot-path `code` exchange; target ≤ 3 s (NFR).
- **Device-code convergence**: integration test runs with a mocked OpenAI that flips to `tokens` on the third poll; target ≤ 2× provider-returned `interval`.

### Regression areas (will break from this change)

- **002 `/api/admin/accounts` contract**: the response gains fields — 002 clients that do strict key-set validation may break. 002 ships no such client; treat as additive OK.
- **002 selector → forwarder path**: the new `PreForward` hook is introduced with `nil` fallback (returns `acct.APIKey` with `usedFallback=false`) so a 002-only build tree still compiles. 003 binary wires the actual coordinator. Test `TestAPIKeyPath_PlatformCompatibleForwarding` locks this down.
- **002 migration runner**: now runs a 2nd migration on the same boot. Existing 002 CI covers multi-migration boot; 003 adds a seeded-DB integration test on top.

---

## Complexity Tracking

> Fill ONLY if Constitution Check has violations.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|---|---|---|

*(empty — no violations)*

---

## Phase checkpoints

| Phase | Entry criterion | Exit criterion |
|---|---|---|
| Phase 0 Research | spec Status=Ready, no [NEEDS CLARIFICATION] | `research.md` exists with 9 decisions + dependency-version table |
| Phase 1 Design | Phase 0 complete | `data-model.md` + `contracts/oauth-flow-api.md` + `contracts/accounts-api.md` + `quickstart.md` + `openapi/admin.yaml` + this `plan.md` all land; Constitution Check PASS; `redocly lint` passes |
| sdd-tasks (next) | Phase 1 complete | `tasks.md` with atomic tasks traceable to spec + plan; MUST include (a) `oapi-codegen` wiring into `internal/generated/adminapi/`, (b) `@hey-api/openapi-ts` wiring into `frontend/src/generated/`, (c) CI freshness gate (`git diff --exit-code` after regeneration) per `AGENTS.md §API Contract` rules #2–#3 |

---

## Next Step

To generate the task breakdown, use **sdd-tasks**:

> "Use sdd-tasks for 003-multi-mode-codex-auth"
