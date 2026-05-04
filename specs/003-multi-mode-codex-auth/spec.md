# Feature Spec: Multi-mode Codex Authentication

**ID**: 003-multi-mode-codex-auth
**Created**: 2026-04-15
**Updated**: 2026-04-25
**Status**: Ready

## Overview

Today a platform operator can only bind an upstream provider to the router by pasting a long-lived API key. That is fine for service-account-style access, but it does not let the router act on behalf of a ChatGPT-subscription account the way the Codex CLI does — those accounts authenticate via OAuth + rotating tokens, not with a static key.

Feature 003 adds **multi-mode upstream-account authentication** so an operator can onboard a ChatGPT-plan account through the same browser-PKCE or device-code flow the current Codex CLI uses, while preserving the existing API-key path. The router persists the resulting tokens (plaintext on disk in 003 — same trust boundary 002 applied to `api_key`; at-rest encryption is deferred to the future key-vault plugin spec per FR-003), auto-refreshes them before they expire, keeps token bytes off every log line / admin API response / HTTP error body, and the Codex protocol adapter picks the live access token per-request. The first-install `/setup/` wizard is part of this feature surface as well: its optional upstream-account step must expose the same four auth modes as the admin portal. API-key onboarding may still complete inline via `POST /api/setup/commit.first_account`; browser OAuth, device OAuth, and `auth.json` import hand off immediately after a successful setup commit into the existing 003 onboarding routes, so a cold install does not strand the operator on a 002-only API-key page.

The client-facing data-plane route remains `/v1/*` and remains outside the router-owned JSON envelope. Its upstream transport is now account-specific: API-key accounts keep the 002 OpenAI Platform-compatible forwarding path (`base_url + original /v1/*`), while OAuth accounts are ChatGPT-subscription accounts and therefore use the ChatGPT Codex backend for Responses traffic. Concretely, OAuth `/v1/responses` maps to `https://chatgpt.com/backend-api/codex/responses`, OAuth `/v1/responses/compact` maps to `/codex/responses/compact`, the router sends `chatgpt-account-id` when the selected row has `chatgpt_account_id`, and OAuth upstream requests use `User-Agent: codex_cli_rs/0.120.0 (Mac OS 26.2; arm64) dumb`. This is an intentional Codex-compatibility transport adaptation, not an Admin API envelope change.

## User Scenarios

### US-1: Onboard a ChatGPT-plan account via OAuth in the browser (Priority: P0)

A platform operator wants to register their own ChatGPT Plus / Team account as an upstream so that Codex clients routed through `one-llm-router` consume their subscription quota — exactly the same way running `codex login` on their laptop does, but centrally and shareable with the team.

**As a** platform operator, **I want** to bind a ChatGPT-plan account to an upstream slot by signing in through my browser, **so that** I get an active upstream without having to find or mint a long-lived API key.

**Why this priority**: Without this, every ChatGPT-subscription operator has to downgrade to pay-per-token API keys to use the router, which defeats the core value proposition of "run my existing Codex quota through the router". This is the headline reason 003 exists.

**Setup entrypoint note**: On a cold install, the `/setup/` wizard's *Upstream* step exposes the same `Auth method = OAuth (browser)` choice. Choosing it completes the setup commit with `first_account` omitted, then hands the operator directly into the same browser-OAuth onboarding surface described below; the operator MUST NOT have to manually discover *Admin → Accounts → New* after setup.

US-1 ships **one browser-OAuth flow with two parallel callback rails that are always both open**. The operator never picks a mode and never toggles anything. The router always (a) tries to bind a loopback listener AND (b) always accepts a manually-pasted callback URL via the admin API — whichever rail reaches the `code + state` first wins, the other becomes a no-op. Which rail actually fires depends on environment: on a laptop the OpenAI 302 closes the loop through the loopback; on a remote router the browser hits connection-refused and the operator pastes the URL. Both land in the same `upstream_accounts` row shape.

**Acceptance Scenarios**:
1. **Given** the operator is signed into the admin portal and on *Admin → Accounts → New*, **When** they pick `Auth method = OAuth (browser)` and submit, **Then** the router generates PKCE + state, attempts to bind a dual-stack loopback listener on `localhost:1455` (no fallback port, matching the registered OpenAI Codex redirect URI), returns `{authorize_url, callback_url, listener_bound, flow_id, expires_at}` to the UI, and the UI simultaneously: (i) opens `authorize_url` in a new tab, AND (ii) renders a **"Paste callback URL"** textarea that stays visible for the entire flow (its presence is independent of `listener_bound` — it is ALWAYS rendered so the operator has a safety net if the loopback silently fails to reach them). The operator signs in with a ChatGPT Plus / Team account and the flow completes via whichever rail wins:
   - **Loopback rail** (common for laptop-hosted routers): the browser's 302 hits `http://localhost:1455/auth/callback?code=…&state=…`, the in-process `*http.Server` consumes it, Flow.Status transitions to `success`, the paste textarea auto-dismisses on the next `GET /api/admin/oauth/flow` poll with "Completed via browser redirect".
   - **Paste rail** (common when router runs on a remote host OR loopback was never reached): the operator copies the URL from their browser's address bar (even if the page shows "connection refused") into the textarea and submits; `POST /api/admin/oauth/browser/manual-callback` consumes it, Flow.Status transitions to `success`.

   Either way, the new account lands in `upstream_accounts` with `status = active`, `auth_method = oauth_browser`, non-null refresh-token record, inside ≤180s end-to-end (≤120s for the laptop loopback rail).
2. **Given** the OAuth flow has completed via either rail, **When** the admin dashboard reloads, **Then** `/api/admin/health` counts the new account as `active` and the Codex selector is willing to pick it for a `/v1/responses` request within ≤5s of commit. The forwarded upstream request uses the ChatGPT Codex backend route and includes `chatgpt-account-id` when present.
3. **Given** the operator aborts the flow (closes the OpenAI tab, clicks Cancel in the UI, or walks away without pasting), **When** `OAuthFlow.ExpiresAt` passes OR `POST /api/admin/oauth/cancel` is invoked, **Then** no `upstream_accounts` row is written, an `oauth_flow_cancelled` INFO log is emitted, any loopback listener is closed, and the UI returns to a clean form.

**Edge Cases**:
- What if two operators start an OAuth flow at the same time? → Only one flow is in flight per router instance (FR-008); the second start returns the business-error envelope `code: 3001 oauth_flow_in_progress` (HTTP 200 per the envelope policy — see `contracts/oauth-flow-api.md` + `docs/error-codes.md` §Feature 003).
- What if the loopback port `localhost:1455` is already in use (or the router lacks capability to bind it — e.g. unprivileged container)? → `/browser/start` returns `listener_bound: false` but still returns a valid `authorize_url`, `callback_url=http://localhost:1455/auth/callback`, and `flow_id`. The flow works end-to-end via the paste rail — no 500, no degraded state, no operator toggle required. (This makes "remote router" and "loopback unavailable" the same code path while preserving the OpenAI-registered redirect URI.)
- What if the redirect returns `error=access_denied`? → Whichever rail receives it MUST first **validate `state` constant-time** (same CSRF check as the happy path — an unauthenticated cancel MUST NOT tear down a live flow). Only after `state` passes does the rail CAS `Flow.Consumed`; on CAS-win it transitions Flow.Status to `error` and emits `oauth_flow_cancelled` INFO (with `rail` set to the receiver); on CAS-loss the other rail already closed the flow and this receiver is a silent no-op. No account row is written. Provider error message surfaced verbatim to the UI. On-wire shape: `contracts/oauth-flow-api.md` §`/auth/callback` and §`/browser/manual-callback`.
- What if the operator pastes a callback URL whose `state` does not match the pending flow? → Paste rail returns the business-error envelope `code: 3003 oauth_state_mismatch` (HTTP 200), no row written, `oauth_rail_rejected` INFO log with `error_code=oauth_state_mismatch` + `rail=manual_paste`. The flow's `Status` stays `pending`; the loopback rail is unaffected and can still close the flow if the real browser redirect arrives, and the operator can re-paste a corrected URL on Rail B until `ExpiresAt`.
- What if both rails fire simultaneously (loopback receives the 302 AND the operator pastes the same URL within the same second)? → Coordinator does a CAS on `Flow.Consumed`: exactly one rail wins and runs the `/oauth/token` exchange; the second reads `Consumed=true` and short-circuits with `code: 3005 already_consumed` (harmless — the first rail already succeeded, the UI's next `/flow` poll shows `status=success`).

---

### US-2: Preserve the existing API-key path (Priority: P0)

An operator who already runs a service account or who uses a non-ChatGPT provider (e.g. OpenAI platform API key, Anthropic key) must still be able to bind an upstream the old way.

**As a** platform operator, **I want** to paste an API key when I already have one, **so that** the new auth modes do not force me into a browser dance for providers that do not support OAuth.

**Why this priority**: 002 shipped with API-key only; removing it or demoting its UX would be a regression for every existing deployment and for non-OpenAI providers. Parity with 002 is non-negotiable.

**Setup entrypoint note**: The `/setup/` wizard keeps the existing inline API-key seed path. API-key remains the ONLY mode that may complete account seeding inside `POST /api/setup/commit.first_account`; the other three modes are deferred to the post-commit 003 onboarding routes.

**Acceptance Scenarios**:
1. **Given** the operator is on *Admin → Accounts → New*, **When** they pick `Auth method = API key`, paste a key, pick a provider, and submit, **Then** the account is persisted exactly the way 002 persists it — same table shape, same `api_key` column write, same `status = active`.
2. **Given** the operator registers an API-key account, **When** `/v1/*` traffic routes through the Codex selector, **Then** the selector uses that account identically to 002: it forwards to `account.base_url` (default `https://api.openai.com`) plus the original path and query, rewrites only the upstream `Authorization` bearer, and performs no OAuth refresh loop.

**Edge Cases**:
- What if the operator picks an API-key provider that also supports OAuth (e.g. OpenAI)? → Both modes are offered; operator picks. The data row carries `auth_method = api_key`, and refresh logic (US-4) is a no-op for it.
- What if the operator pastes an empty key? → Same `code: 2005 invalid_api_key` envelope as 002; no account row written.

---

### US-3: Onboard from a headless / SSH session via OAuth device code (Priority: P1)

Many operators run the router on a server they reach over SSH or through a cloud shell. Those sessions have no local browser and the loopback-callback trick does not work. They need the OAuth device-code flow that `codex login` uses in the same situation.

**As a** platform operator on a headless box, **I want** to bind a ChatGPT-plan account by typing a short user code into my laptop's browser, **so that** I can onboard without exposing the router's loopback port to my workstation.

**Why this priority**: Unblocks every remote / container / k8s deployment. Important for team adoption but the browser flow (US-1) is what individual developers do first, so this can be a fast follow-up instead of launch-day.

**Setup entrypoint note**: On a cold install, selecting `Auth method = OAuth (device code)` from `/setup/` first commits DB/runtime/plugins, then routes directly into the same device-flow UI the admin portal uses. The operator MUST NOT need to finish setup and then manually navigate to a second entrypoint.

**Acceptance Scenarios**:
1. **Given** the operator picks `Auth method = OAuth (device code)`, **When** they submit, **Then** the UI shows a `user_code` (8 characters, formatted `XXXX-YYYY`), a `verification_url` (e.g. `https://auth.openai.com/codex/device`), and an expiry countdown (≤15 min); the server starts polling the token endpoint at the interval returned by the provider (falling back to 5s per RFC 8628 §3.5 if the provider returns 0/null).
2. **Given** the operator enters the code on their own laptop and approves the app, **When** the poller sees the `tokens` response, **Then** the account row lands in `upstream_accounts` with `auth_method = oauth_device` and the UI advances to success within ≤5s of approval.
3. **Given** the device code expires without approval, **When** the poller hits `expires_at`, **Then** the flow transitions to `error`, emits `oauth_flow_expired` INFO log (shared event name with the browser-flow timeout — the `method="device"` field disambiguates), and the UI surfaces a "Try again" action.

**Edge Cases**:
- What if the operator closes the admin tab mid-flow? → Polling continues in the background until expiry; if tokens arrive with no tab listening, they are still persisted (operator can refresh the page and see the new account).
- What if the operator submits a `user_code` typo? → Poller surfaces the provider's `access_denied` / `invalid_grant` message and stops.

---

### US-4: Auto-refresh OAuth tokens before they expire (Priority: P0 — for any OAuth account)

OAuth access tokens expire (typically 28 days for ChatGPT refresh, ~1 hour for access). If the router does not refresh them, every Codex-protocol request eventually starts returning 401 and the operator has to re-run OAuth. That would make OAuth effectively unusable for production routing.

**As a** platform operator, **I want** the router to rotate OAuth access tokens in the background, **so that** Codex traffic keeps flowing without me logging in again every hour.

**Why this priority**: Without this, OAuth onboarding (US-1/US-3) regresses to a worse-UX version of API keys. Refresh is a mandatory part of shipping OAuth at all, so it is P0 — gated on US-1/US-3 landing.

**Acceptance Scenarios**:
1. **Given** an OAuth upstream account's `last_refresh` is older than the configured refresh threshold (default: half the provider-returned `expires_in`), **When** the Codex selector picks that account for a request, **Then** the router attempts `refresh_access_token` before forwarding; on success it stores the new tokens (in the sealed-bytes-ready column shape — plaintext in 003) and proceeds; the forwarded request uses the fresh access token.
2. **Given** the provider returns a permanent refresh error (`invalid_grant`, `account_deactivated`), **When** the refresh attempt fails, **Then** the account transitions to `status = disabled`, emits `oauth_refresh_failed` WARN log with a redacted reason, and the selector stops picking it.
3. **Given** the refresh endpoint is transiently unreachable (network 5xx), **When** the refresh attempt fails, **Then** the selector either serves the stale-but-not-expired access token (if still valid) or returns the provider's error to the client — it does NOT silently disable the account.

**Edge Cases**:
- What if two concurrent requests both observe a stale token? → Refresh is single-flight per account (the second waits for the first's result); both requests see the fresh token.
- What if the router is restarted mid-refresh? → Stored tokens are the last persisted pair; the next request hits the same refresh branch again (idempotent retry).

---

### US-6: Import a local Codex CLI `auth.json` in one step (Priority: P1)

The Codex CLI writes its OAuth tokens to `~/.codex/auth.json` after a successful `codex login`. Operators who already logged in on their laptop want to **hand that file to the router** instead of re-running OAuth from scratch — it is faster, works offline (no network round-trip to OpenAI from the router), and lets them transplant a known-good session onto the server.

**As a** platform operator with a working local Codex CLI session, **I want** to upload my local `~/.codex/auth.json` into the admin portal as a new upstream account, **so that** I can onboard in seconds without running the OAuth dance through the router itself.

**Why this priority**: Collapses the OAuth onboarding experience from "open a browser → sign in → wait for callback" to "paste a file". Does not add new security surface beyond US-1 (the tokens are going to end up at rest on the router anyway). Gated behind US-1/US-4 landing because it reuses the same storage + refresh plumbing, but ships alongside because it is a trivial additional handler once that plumbing exists.

**Setup entrypoint note**: On a cold install, selecting `Auth method = Import auth.json` from `/setup/` commits setup first, then routes directly into the same import surface used by the admin portal. The operator does not need to land on the dashboard before completing the import.

**Acceptance Scenarios**:
1. **Given** the operator is on *Admin → Accounts → New*, **When** they pick `Auth method = Import auth.json`, select their local `auth.json` file via a file picker (or drag-drop), and submit, **Then** the UI sends a `multipart/form-data` request carrying ONE `auth_json` part (the raw file bytes — NO `name` / `provider` fields; this shape aligns with the codex-lb reference implementation), the router validates the shape (`tokens.access_token`, `tokens.refresh_token`, `tokens.id_token` all present), decodes `id_token` to extract `email` + `chatgpt_account_id` + `plan_type`, derives the row's `name` from the `email` claim (fallback `chatgpt_account_id`), writes a new `upstream_accounts` row with `auth_method = oauth_import` and `provider = "openai"`, and lands on the imported account's detail page within ≤2s of submit.
2. **Given** the imported tokens are still inside the provider-stated validity window, **When** the Codex selector picks the account for a `/v1/responses` request, **Then** the request flows exactly as if the tokens had been obtained via US-1 (single-flight refresh from US-4 applies identically).
3. **Given** the uploaded file is missing a required field or has a malformed `id_token`, **When** the server validates, **Then** it returns the business-error envelope `code: 3011 invalid_auth_json` (HTTP 200) with a per-field `data.missing_fields` list (shape compatible with 002's validator), writes no row, logs `auth_json_import_rejected` INFO (never the token payload), and the UI surfaces the error under the upload input.

**Edge Cases**:
- What if the imported `access_token` is already expired but `refresh_token` is still valid? → Import succeeds, the first outgoing request triggers US-4's refresh, the operator sees a healthy account.
- What if the imported file is from a DIFFERENT OpenAI identity than one the operator already imported? → The row is inserted as a new account (different `chatgpt_account_id`); duplicate `chatgpt_account_id` on the same email SHOULD surface a non-fatal "looks like a duplicate of X" UI warning but MUST NOT block the insert (operator might intentionally manage two sessions).
- What if the operator sends a non-multipart body, or the `auth_json` part is missing, or its content is not valid JSON? → The router returns the business-error envelope `code: 3010 invalid_auth_json_structure` (HTTP 200) and logs `auth_json_import_rejected` INFO with a descriptive `reason` (`missing_multipart_auth_json_part` / `malformed_structure`) — never the payload bytes.
- What if the `auth_json` part body is larger than 16KB? → The 8KB body cap from 002's admin API boundary is lifted to 16KB specifically for this part (auth.json payloads can legitimately be ~6KB). Anything above that returns the business-error envelope `code: 2009 request_body_too_large` (HTTP 200, reuses 002's registered code) with `data = {scope: "part", limit_bytes: 16384}`. A separate 64KB outer cap on the whole multipart envelope defends against preamble-flood attacks; tripping it returns `data = {scope: "envelope", limit_bytes: 65536}`. See `research.md` Decision 7 for rationale.
- What if the uploaded file contains extra unknown fields (e.g. CLI-local metadata)? → The router ignores unknown top-level keys and persists only the fields it recognises; MUST NOT surface the unknown keys back to the UI or logs.

---

### US-5: Rotate / re-auth without deleting the account (Priority: P1)

Credentials sometimes need to be replaced — an operator re-auths their ChatGPT session, a compromised API key needs swapping, a team rotates shared credentials. The account's *name, provider, routing config, per-account stats* should survive.

**As a** platform operator, **I want** to re-authenticate an existing upstream account in place, **so that** downstream routing rules, nicknames, and historical audit trails stay attached.

**Why this priority**: Nice-to-have for 003 ship and can follow. Without it the operator just deletes + recreates, which loses stats; acceptable short-term.

**Acceptance Scenarios**:
1. **Given** an existing upstream account with `auth_method ∈ {oauth_browser, oauth_device, oauth_import}`, **When** the operator clicks *Re-authenticate* in *Admin → Accounts → [row]*, **Then** the server starts the OAuth flow the operator picks (browser or device) against a `target_account_id` scope, and on flow `Status=success` performs a **credential-only UPDATE** against the same `upstream_accounts.id` (the `store.PersistFlow` transaction writes the new `(access_token, refresh_token, id_token)` bytes into the existing row — the UPDATE overwrites them in place; no sidecar of the old bytes is retained, no soft-delete column, no audit-log entry carries token bytes; this satisfies "drop the previously stored tokens" literally — after commit the row observably holds only the new credentials). `auth_method` is immutable (a row onboarded via `oauth_import` can re-auth via browser or device but its stored discriminator stays `oauth_import` to preserve audit provenance). No new row is inserted. If the flow's `Status` reaches `error` or `cancelled`, the row is **untouched** and the original tokens keep serving traffic until the operator retries or explicitly disables the row.
2. **Given** an existing upstream account with `auth_method = api_key`, **When** the operator pastes a new key, **Then** the `api_key` column is overwritten and `updated_at` advances; the old key is overwritten (not archived) in place.

**Edge Cases**:
- What if rotation fails partway? → The old credentials are restored (no partial overwrite lands); account returns to its prior `status`.
- What if the operator starts rotation but abandons it? → No change to the account; the pending OAuth flow is garbage-collected on timeout.

---

## Functional Requirements

- **FR-001**: Both operator entrypoints for creating an upstream account — the `/setup/` wizard's optional *Upstream* step and the admin portal's *Admin → Accounts → New* flow — MUST offer the same four auth modes: **API key** (existing 002 path), **OAuth (browser)**, **OAuth (device code)**, **Import auth.json** (paste/upload a local `~/.codex/auth.json`). Availability per mode MAY be gated by provider (e.g. Anthropic: API-key only for 003).
- **FR-002**: Router MUST persist OAuth-authenticated accounts with the `(access_token, refresh_token, id_token)` triplet (stored in sealed-bytes-ready columns per FR-003 — plaintext in 003, wrappable by the future key-vault plugin without migration) plus `last_refresh`, `access_expires_at` (absolute UTC timestamp computed from the provider's `expires_in` — nullable when the provider omits it; derived from `tokens.OAuth.exp_in_days` on import with a 28-day codex-lb-compatibility fallback), `chatgpt_account_id` (when available), `email` (when available), and `plan_type` (when available). The plaintext key column (`api_key`) MUST remain NULL for OAuth accounts.
- **FR-003**: Router MUST persist OAuth token material (access / refresh / id tokens) to the upstream-account rows. **Encryption-at-rest is explicitly deferred to the future key-vault plugin spec (same decision 002 made for API keys).** Tokens MUST NEVER appear in any log line, error response, admin API payload, or browser-visible response body — even though they are stored plaintext on disk. The column shape MUST be chosen so a future key-vault plugin can wrap the same bytes without schema migration (i.e. the column is a sealed-bytes-capable blob, not a VARCHAR exposed through the admin API).
- **FR-004**: Router MUST auto-refresh OAuth access tokens before each Codex-protocol request when the persisted pair is older than the provider-returned `expires_in` minus a safety margin (default: 50% of `expires_in`). Single-flight locking MUST prevent duplicate refresh for concurrent picks of the same account.
- **FR-005**: Permanent refresh failures (`invalid_grant`, `account_deactivated`, `invalid_client`, equivalent) MUST set the account's `status = disabled` and MUST NOT retry until the operator re-authenticates via US-5.
- **FR-006**: OAuth *browser* flow MUST use PKCE (code-verifier + code-challenge) and a randomly generated `state` token; the callback handler MUST reject any callback whose `state` does not match the in-flight value.
- **FR-007**: OAuth *device* flow MUST poll the provider's token endpoint at the provider-returned interval, stop at `expires_in`, and persist tokens atomically on the first successful poll.
- **FR-008**: At most one OAuth flow (browser or device) MAY be in flight per router instance at a time; a second start MUST return the business-error envelope `code: 3001 oauth_flow_in_progress` (HTTP 200 per `docs/error-codes.md`) with `data = { method, flow_id, expires_at, created_at }` describing the in-flight flow.
- **FR-009**: The Codex protocol adapter MUST pass the account's **current** credential as the upstream `Authorization` header; it MUST NOT cache the bearer across requests. For `auth_method=api_key`, the adapter MUST preserve 002 behavior and forward `/v1/*` to the account's OpenAI Platform-compatible `base_url` (default `https://api.openai.com`) using the original path and query. For OAuth accounts (`oauth_browser`, `oauth_device`, `oauth_import`), the adapter MUST treat the token as a ChatGPT/Codex token and use the ChatGPT Codex backend transport:
  - OAuth `/v1/responses` → `https://chatgpt.com/backend-api/codex/responses`.
  - OAuth `/v1/responses/compact` → `https://chatgpt.com/backend-api/codex/responses/compact`.
  - OAuth accounts are eligible only for the Codex Responses paths above; other `/v1/*` paths must be served by API-key accounts or fail with the existing no-capacity router error if no eligible API-key account exists.
  - The adapter MUST include `chatgpt-account-id: <chatgpt_account_id>` when the selected row has a non-empty `chatgpt_account_id`.
  - The adapter MUST set `User-Agent: codex_cli_rs/0.120.0 (Mac OS 26.2; arm64) dumb` on OAuth ChatGPT Codex upstream requests, overriding any inbound client `User-Agent`.
  - For non-streaming `/v1/responses` requests (`stream:false` or omitted), the adapter MUST normalize the client Responses JSON into the ChatGPT Codex backend shape, force upstream streaming, collect the terminal upstream SSE into a complete Responses JSON body, return that JSON body to the downstream client, and, when upstream response-body logging is enabled, store the same collected complete Responses JSON in `request_records.upstream_response_body`. Terminal SSE events such as `response.failed` or `response.incomplete` are still terminal upstream payloads and MUST be returned as JSON rather than mislabeled as connection failures. Malformed or oversized upstream SSE MUST fail closed with the native router error `upstream_response_invalid`. For streaming requests (`stream:true`), it MUST keep downstream SSE streaming.
- **FR-010**: Re-authentication of an existing account (US-5) MUST overwrite credential material in place on the same `upstream_accounts.id`; non-credential columns (`name`, `provider`, `base_url`, routing config, stats) MUST survive unchanged.
- **FR-011**: `/api/admin/accounts` MUST expose `auth_method ∈ {api_key, oauth_browser, oauth_device, oauth_import}` on each row. By default the list/detail response MUST return only non-secret metadata (id, name, provider, auth_method, status, last_refresh, access_expires_at, email, plan_type, chatgpt_account_id). The admin API MUST NOT return token material (access/refresh/id tokens, api_key) on these routes.
- **FR-011a**: For every OAuth account, the admin portal UI MUST render **`email`** and **`plan_type`** without any extra click — on both the account list row and the account detail panel. `plan_type` MUST be rendered with a **human-readable label**: map `chatgpt-plus` → `ChatGPT Plus`, `chatgpt-team` → `ChatGPT Team`, `chatgpt-enterprise` → `ChatGPT Enterprise`; any unmapped value MUST fall back to the raw `plan_type` string so new plans are not silently dropped. API-key accounts (002 rows) are unaffected — they render the 002 field set as before and have neither `email` nor `plan_type`.
- **FR-012**: The browser-OAuth flow (US-1) MUST keep **both callback rails open at the same time, for every flow, without operator configuration**. Specifically:
  - **Rail A — Loopback listener**: on every `POST /api/admin/oauth/browser/start`, the router MUST best-effort bind a dual-stack loopback listener on `localhost:1455` (both `127.0.0.1` and `[::1]`; no alternate port is advertised to OpenAI). If binding succeeds, the response carries `listener_bound: true` and the listener accepts exactly one `GET /auth/callback` hit within the flow's lifetime. If binding fails (port in use, unprivileged container, etc.), the response carries `listener_bound: false` and the flow continues without the listener — **no 500, no degraded status, no error surfaced to the UI** beyond that bool. `listener_bound` is advisory display only; no UI decision branches on it.
  - **Rail B — Manual paste**: on the same flow, the admin UI MUST render a "Paste callback URL" textarea from the moment the flow starts until it ends, **regardless** of `listener_bound`. `POST /api/admin/oauth/browser/manual-callback` accepts a pasted URL, parses its `code` + `state`, and runs the same `/oauth/token` exchange as Rail A.
  - **First-wins CAS invariant**: the two rails race for the same `OAuthFlow`. The `oauth.Coordinator` MUST guard the code-exchange path with an atomic `CompareAndSwap` on `Flow.Consumed` (false → true). **State validation MUST run BEFORE the CAS on every branch (happy path AND `error=access_denied` cancel)** — an unauthenticated cancel MUST NOT consume a live flow. The CAS loser MUST NOT call `/oauth/token` a second time and MUST return the business-error envelope `code: 3005 already_consumed` (HTTP 200) to its caller (the admin UI for Rail B; the loopback rail serves the browser a generic success page since the flow did in fact succeed — see `contracts/oauth-flow-api.md` §`/auth/callback`). This makes double-close safe even under sub-second race between the two rails.
  - **No mode field, no toggle**: `POST /api/admin/oauth/browser/start` takes **no** `mode` / `topology` / `return_redirect_url_only` parameter. The request body is just `{"provider": "openai"}`. The UI MUST NOT expose a rail-picker to the operator.
  - **Server-side invariants**: constant-time `state` validation (FR-006), PKCE `code_verifier` possession, single-router-wide at-most-one-flow rule (FR-008), and the log-scrub rule (FR-003) apply identically on both rails.
- **FR-013**: Changes to this feature MUST preserve 002's **setup gate semantics** while upgrading the wizard's account step. Specifically: the wizard's optional seed-account behavior, SQLite probe relaxation, and DB-first/file-last `POST /api/setup/commit` contract continue to work; API-key onboarding may still be completed inline via `first_account`; browser OAuth, device OAuth, and `auth.json` import MUST be available from `/setup/` as first-class choices and MUST hand off immediately after a successful setup commit into the existing 003 onboarding routes (no manual operator detour through the dashboard). Operators MAY still skip account onboarding entirely and land in the degraded admin shell.
- **FR-014**: The admin portal MUST offer an **Export auth.json** action on each OAuth account's detail page. Behavior:
  - The account detail view by default shows only metadata (per FR-011 / FR-011a). Token material MUST NOT auto-render.
  - An explicit **Export auth.json** button (one click, operator-visible label) triggers a dedicated read endpoint (e.g. `POST /api/admin/accounts/{id}/export-auth-json`) that returns the full `{tokens: {access_token, refresh_token, id_token}, ...}` JSON payload in the response body. The payload is handed to the browser as a downloaded file (`Content-Disposition: attachment; filename=auth.json`) so the tokens do not accumulate in the SPA's in-memory state or console logs.
  - Every export call MUST emit a `oauth_auth_json_exported` **WARN** log with `request_id`, `account_id`, `operator_id` (from admin-auth plugin), `email`, and `exported_at`. The log MUST NOT carry the token bytes. These events MUST be retained at least as long as 002's INFO-tier admin audit trail.
  - The export endpoint MUST be gated behind the admin-auth plugin (when enabled) exactly like every other `/api/admin/*` mutation. When the admin-auth plugin is disabled (single-operator local deploys), the operator inherits the same trust boundary that lets them reach `/api/admin/settings/update`.
  - No dedicated rate limit is applied in 003 — export requests share the same per-operator admin-API rate envelope as any other `/api/admin/*` call. (Rationale: the auditable WARN event already gives operators a complete trail; a hard per-account throttle would block legitimate bulk-rotation workflows. Revisit if abuse is ever observed.)
  - API-key accounts (`auth_method = api_key`) do NOT get this button — their stored secret is a single opaque string and 002's read-back policy of "no plaintext API-key exposure" is preserved.

## Storage Layout

This section documents **exactly what 003 writes to disk** per account. Non-normative beyond the FRs above — included so operators can reason about what is on the box after a successful onboarding. Planning will convert this to a formal `data-model.md`.

### API-key accounts (`auth_method = api_key`)
Identical to 002. Columns: `id, name, provider, base_url, api_key, status, created_at, updated_at`. Token columns are NULL.

### OAuth accounts (`auth_method ∈ {oauth_browser, oauth_device, oauth_import}`)
On top of the 002 columns, each OAuth row carries:

| Column | Shape | Source |
|---|---|---|
| `access_token` | sealed-bytes blob (plaintext in 003) | provider token response |
| `refresh_token` | sealed-bytes blob (plaintext in 003) | provider token response |
| `id_token` | sealed-bytes blob (plaintext in 003) | provider token response |
| `last_refresh` | UTC timestamp | wall clock at token-write (import path: payload's top-level `last_refresh` when present, parseable as RFC 3339, and not in the future — else wall clock, per FR-014a) |
| `access_expires_at` | UTC timestamp (nullable) | `last_refresh + expires_in` from the provider token response (US-1 browser / US-3 device / US-4 refresh) or `last_refresh + tokens.OAuth.exp_in_days` from the imported `auth.json` (US-5 import, with 28-day codex-lb fallback when the field is absent or non-positive); NULL is legal when the provider omits `expires_in` entirely |
| `email` | string (nullable) | decoded from `id_token` claims |
| `plan_type` | string (nullable, e.g. `chatgpt-plus`, `chatgpt-team`) | decoded from `id_token` claims |
| `chatgpt_account_id` | string (nullable) | decoded from `id_token` claims — `claims["https://api.openai.com/auth"]["chatgpt_account_id"]`, with legacy `claims["auth"]["chatgpt_account_id"]` as a codex-lb-compatibility fallback |

The `api_key` column stays NULL for OAuth rows. The three token columns are typed as opaque bytes (BLOB / BYTEA / equivalent — NOT TEXT), so the future key-vault plugin can wrap the same bytes without a schema migration. Tokens are never indexed.

### What is NOT persisted
- The PKCE `code_verifier` used in US-1 (lives in memory only, discarded on success/fail)
- The OAuth `state` token (lives in memory only)
- The device-code `user_code` (lives in memory only; the long-lived `device_auth_id` is also not persisted — only the resulting tokens are)
- The originally-uploaded `auth.json` file bytes (only the extracted fields land in columns)

### Who can read the on-disk bytes
- The router process itself (it needs the tokens to call upstream)
- Anyone with read access to the database file / Postgres / MySQL row — **same trust boundary as 002's `api_key` column**. Operators who can't accept that boundary wait for the key-vault plugin spec.

## Non-Functional Requirements

| Category | Requirement | Metric | Verification |
|----------|-------------|--------|--------------|
| Performance | OAuth browser callback → account persisted | P95 ≤ 3s from callback hit to `upstream_accounts` row | Integration test with mocked IdP |
| Performance | OAuth device poll convergence after user approval | P95 ≤ 2× provider interval (typ. ≤10s) | Integration test with mocked IdP |
| Performance | Codex `/v1/responses` added latency from auto-refresh | P95 ≤ 800ms refresh cost, amortised across window | Load test with aged-token fixture |
| Compatibility | Account-specific data-plane transport | API-key accounts remain 002 Platform-compatible for `/v1/*`; OAuth accounts successfully complete `/v1/responses` through ChatGPT Codex backend with `chatgpt-account-id` when available | Forwarder tests for API-key and OAuth paths + E2E `/v1/responses` smoke |
| Security | Tokens never surfaced off-host except via the FR-014 export button | logs / default admin API / HTTP error responses emit zero bytes of access/refresh/id token material; only the explicit `export-auth-json` endpoint ever returns them, and every such call writes a WARN audit event | Log-scrub test + admin-API contract test + audit-event assertion |
| Security | OAuth `state` CSRF rejection | 100% of callbacks with mismatched/missing state rejected | Contract test covers all branches |
| Security | Sealed-bytes-ready column shape | token storage column accepts opaque bytes so the future key-vault plugin wraps without schema migration | Data-model review during planning |
| Reliability | Token-refresh single-flight | Concurrent requests on one account trigger ≤1 refresh call | Race test with 50 goroutines |
| Observability | OAuth lifecycle events structured | Structured events covering the full OAuth lifecycle (flow started / completed / cancelled / failed / expired / rail-rejected; refresh started / ok / failed / transient-fallback; auth.json import rejected; auth.json exported / export rejected). Successful `auth.json` imports deliberately reuse 002's `account_created` INFO event — there is **no** dedicated `auth_json_imported` event (avoids a per-operation event that duplicates 002's write-path event). Canonical level + field list is defined in `contracts/oauth-flow-api.md` §Observability. Every event carries `request_id` and (where applicable) `account_id`; no event ever carries plaintext token bytes. | Log-schema contract test + log-scrub CI gate |
| Compatibility | Existing API-key accounts keep working | Zero-regression pass rate on 002's upstream-account e2e suite | E2E test run against 003 binary |

## Key Entities

- **UpstreamAccount** (extends 002's entity): now carries an `auth_method` discriminator and — for OAuth rows — `access_token`, `refresh_token`, `id_token` (stored plaintext in 003; sealed-bytes-ready column shape reserves space for the future key-vault plugin per FR-003), plus `last_refresh`, `email`, `plan_type`, `chatgpt_account_id`. API-key rows keep `api_key` populated; OAuth rows keep it `NULL`. Exactly one of the two credential shapes is populated per row.
- **OAuthFlow** (new, in-memory only): the router-wide at-most-one active flow. Carries `method` ∈ {browser, device}, `state` (CSRF token, 43-char base64url), `code_verifier` (PKCE secret), `device_auth_id` + `user_code` (device flow only), `expires_at`, `callback_server` (loopback listener handle, browser flow only), `listener_bound` (bool — was the loopback listener bound successfully; browser flow only), `consumed` (atomic.Bool — CAS target serialising the two browser-flow rails), `consumed_by` ∈ {loopback, manual_paste, ""} (observability label set by the CAS winner), `status ∈ {pending, success, error, idle}`, `target_account_id` (non-zero for reauth flows — `POST /accounts/{id}/reauth`, FR-010). Canonical type definition in `data-model.md`; never persisted; killed on success, terminal failure, timeout, or restart.
- **TokenMaterial** (conceptual, never surfaced off-host): the `(access, refresh, id)` triplet. In 003 it is stored plaintext on disk but MUST be redacted from every log, admin API payload, error message, and HTTP response body. The future key-vault plugin will seal these bytes without a schema change.

## Success Criteria

- **SC-1**: An operator can onboard a ChatGPT-plan account end-to-end from a cold install in ≤2 minutes (time from selecting a non-API-key mode in `/setup/` to first successful `/v1/responses` response).
- **SC-2**: After 48h of continuous traffic on a single OAuth account, the Codex selector has never served a 401 caused by an expired access token (refresh loop caught every rotation).
- **SC-3**: Across a 50-operator synthetic load test, concurrent onboarding attempts never produce more than one account row per real IdP identity (single-flight + identity-dedup hold).
- **SC-4**: Zero occurrences of refresh/access/id token strings in any **automatic** INFO/WARN/ERROR log line or default admin API response across the 48h burn-in (automated grep in CI). The one legitimate path that returns tokens — the FR-014 `export-auth-json` button — always emits a `oauth_auth_json_exported` WARN event (without the token bytes) so every export is discoverable in audit.

## Scope

### In Scope

- Admin-portal UX for picking auth method on *New Account* and *Re-authenticate* across all four modes (API key, OAuth browser, OAuth device, Import auth.json).
- Setup-wizard UX for the optional *Upstream* step across the same four modes. The wizard remains responsible for DB/runtime/plugin commit; non-API-key setup choices hand off immediately after commit into the existing 003 onboarding routes rather than inventing a second OAuth/import backend.
- Router-side OAuth browser + device flows for the OpenAI / ChatGPT-plan provider. The browser flow ships **two parallel callback rails always-on** per FR-012 (loopback listener + always-available paste textarea, first-wins CAS) so one implementation works the same way for local-router and remote-router deployments with no mode switch and no operator toggle.
- Import path for local Codex CLI `~/.codex/auth.json` files (US-6).
- Plaintext-on-disk OAuth token storage with strict off-host redaction (tokens never cross the HTTP/log boundary); the column shape is chosen so the future key-vault plugin wraps the same bytes without migration.
- Single-flight refresh in the Codex protocol adapter's upstream-call path.
- Account-specific `/v1/responses` upstream transport: API-key accounts use the OpenAI Platform-compatible base URL; OAuth accounts use the ChatGPT Codex backend and `chatgpt-account-id`.
- Zero-migration of existing 002 API-key accounts (they keep working with `auth_method = api_key`).

### Out of Scope

- Non-OpenAI OAuth (Anthropic, Azure, local OIDC) — explicitly deferred. 003 only ships the OpenAI / ChatGPT flavour because that is where the Codex value is.
- At-rest encryption of token material. Deferred to the key-vault plugin spec (same deferral 002 applied to API-key storage). 003 picks a column shape that accepts sealed bytes, but the 003 binary itself only writes plaintext.
- External KMS / HSM / cloud-secret-manager integration — the key-vault plugin is a separate spec.
- Multi-tenant account sharing (team-level ACL on which operator may use which account) — 003 keeps the 002 model: any admin-portal user sees every account.
- Automated ChatGPT-plan quota / billing display — plan_type is stored but no UI widget is built.
- Admin-portal auth itself (operator sign-in) — orthogonal to upstream-account auth; handled by the admin-auth plugin.
- Key rotation tooling for the at-rest master key — deferred; 003 assumes the master key is set once at boot.

## Assumptions

- The Codex provider = OpenAI ChatGPT, matching the current official Codex CLI browser/device auth surface. Any additional OAuth providers are later specs.
- Exactly one router process owns the OAuth flow state; HA fan-out is deferred to a later spec (consistent with 001/002 single-process model).
- Whether the router runs co-hosted with the operator's browser (laptop) or on a remote server is irrelevant to the OAuth UX: US-1 always renders the paste textarea and always tries to bind the loopback listener (FR-012 dual-rail). Laptop deployments typically close the flow via the loopback rail (no paste needed — UI auto-dismisses the textarea on flow success); remote / reverse-proxied / SSH-tunneled deployments close via the paste rail. The device-code flow (US-3) remains the orthogonal headless-SSH path and is NOT a prerequisite for remote-router browser onboarding.
- Token material is stored plaintext on disk in 003, consistent with 002's API-key storage decision; the key-vault plugin lands in a later spec and wraps both token and API-key bytes at that point. 003 therefore does NOT ship an encryption master-key config surface.
- Operators who can't accept plaintext tokens on disk wait for the key-vault plugin spec before onboarding OAuth accounts, OR stay on API keys (same trade-off 002 documented for them).

## Clarifications

### Session 2026-04-15
- Q: FR-003 at-rest key-management strategy — inline master key, on-disk keyring, plugin keystore, or something else? → A: **None of A/B/C.** Defer encryption entirely to a later key-vault plugin spec; 003 writes token material plaintext on disk (same pattern 002 used for API keys) but MUST still redact tokens from every log line / admin API payload / HTTP response body. 003 picks a column shape that accepts sealed bytes so the future plugin wraps without schema migration.
- Q: Should the admin UI offer a "View auth.json" / "Export auth.json" panel that dumps the stored token triplet back to the operator? → A: **Option B — explicit button + audit log, no dedicated rate limit.** The account detail view defaults to metadata-only (FR-011 / FR-011a). A labelled "Export auth.json" button on each OAuth account triggers a dedicated endpoint that streams the full triplet as a downloaded file, emitting a `oauth_auth_json_exported` WARN audit event every time; the export endpoint shares the generic per-operator admin-API rate envelope (no special 10-per-24h throttle — operator feedback 2026-04-15). API-key accounts do NOT get this button (their 002 read-back policy is preserved). SC-4 amended to reflect that tokens CAN leave the host via this path, with mandatory audit.
- Q: Which OAuth account metadata does the account list / detail panel have to render without a click? → A: **email + plan_type only** (human-readable label: `chatgpt-plus` → `ChatGPT Plus`, `chatgpt-team` → `ChatGPT Team`, `chatgpt-enterprise` → `ChatGPT Enterprise`; unknown → raw string). Captured as FR-011a. API-key rows are untouched. 2026-04-15.
- Q: When the router runs on a remote server (not the operator's laptop), the browser's `localhost:1455` callback is unreachable — should the UI support manually copy-pasting the post-auth URL back into the admin portal? → A: **Yes, AND remove the need for any mode-picking entirely**: both rails stay open simultaneously on every browser-OAuth flow. The router always best-effort binds the loopback listener (Rail A) and always exposes a manual-paste admin endpoint (Rail B); the UI always shows the paste textarea. Whichever rail reaches the `code + state` first wins the flow via a `Flow.Consumed` CAS; the other rail returns envelope `code:3005 already_consumed` at HTTP 200 (harmless — project-wide no-4xx-under-`/api/admin/*` policy; Rail A's browser-facing handler shows 200 success HTML instead). No `mode` request parameter, no UI toggle, no hostname-based heuristic — the same code path handles laptop and remote-server deployments. On loopback bind failure the response carries `listener_bound: false` and the flow continues on Rail B alone with zero UX impact. Captured as rewritten FR-012 + AC-1. 2026-04-15.
