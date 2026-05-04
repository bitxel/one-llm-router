# Feature Spec: Account Playground

**ID**: 004-account-playground
**Created**: 2026-04-23
**Updated**: 2026-04-25
**Status**: Ready

## Overview

Operators can onboard, rotate, and inspect upstream accounts in the Admin Portal, but today they still need an external client, a command-line request, or a Codex session to prove that one account can complete a real text request. That slows down OAuth onboarding, API-key rotation, and account-health triage because the operator has to leave the portal to answer a basic question: "Can this account respond right now?"

Feature 004 adds an **Account Playground** to the Admin Portal. A platform operator enters a single text prompt, chooses either automatic account selection or one explicit account, starts the run, waits for the final response, and sees safe diagnostic output: selected account, selection mode, response text when available, outcome, latency, usage metadata when available, and sanitized error detail when the router or upstream fails. The Playground is an operational debugging surface for account confidence; it is not a general chat product.

## User Scenarios

### US-1: Run a Text Probe with Automatic Account Selection (Priority: P0)

An operator wants to confirm that the router as a whole has at least one active usable account, without configuring a local client or waiting for production traffic.

**As a** platform operator, **I want** to submit a short text prompt using automatic account selection, **so that** I can verify the router can complete a real model call from the Admin Portal.

**Why this priority**: Automatic selection is the core router behavior. If the Playground cannot exercise that path, it cannot answer the first operational question after setup or account onboarding: "Is the router able to serve traffic now?"

**Acceptance Scenarios**:
1. **Given** at least one active upstream account exists, **When** the operator opens the Playground, enters non-empty text, leaves selection mode as Automatic, and submits, **Then** the page enters a waiting state within 200 ms, creates exactly one run, and eventually displays the selected account, selection mode, response text when available, outcome, latency, and usage metadata when provided.
2. **Given** no active upstream account exists, **When** the operator opens the Playground, **Then** the page shows an empty state explaining that no active account can be tested, disables the run action, and provides a visible path to account onboarding.
3. **Given** at least one active account exists when the page loads but all accounts become unavailable before submit, **When** the operator submits in Automatic mode, **Then** the run fails with a router no-capacity outcome, no upstream response text is fabricated, and the operator sees a correlation identifier through the existing Admin Portal error pattern.
4. **Given** the automatically selected account requires credential refresh before use, **When** the run starts, **Then** the same refresh-before-use policy that protects normal routed traffic is applied before the upstream request, and the operator sees only the safe final outcome.

**Edge Cases**:
- What if the operator double-clicks Run or presses Enter repeatedly while a run is pending? → Exactly one run is created; subsequent submits are ignored until the pending run reaches a terminal state.
- What if the active account list changes while the page is open? → The next Automatic run uses the current eligible account set and never relies solely on stale browser state.
- What if the selected upstream returns a non-success response? → The result identifies the selected account and provider/router error outcome without marking the run as a successful answer.
- What if the operator repeats Automatic runs with a sticky playground session value? → Runs with the same session value follow the router's normal consistency policy; runs without a session value follow the router's normal non-sticky selection behavior.

---

### US-2: Run a Text Probe Against a Specific Account (Priority: P0)

An operator has just added, imported, re-authenticated, or suspects a specific upstream account. They need to prove that exact account works, instead of waiting for automatic routing to pick it.

**As a** platform operator, **I want** to choose one active upstream account for a Playground run, **so that** I can diagnose whether that account's credentials, quota, plan, or upstream endpoint works.

**Why this priority**: Account-specific debugging is the main reason to add a Playground instead of relying on production traffic. Without it, OAuth onboarding and account repair still require external tooling.

**Acceptance Scenarios**:
1. **Given** active accounts exist, **When** the operator switches selection mode to Account and opens the account picker, **Then** active accounts are selectable and each option exposes enough non-secret metadata to distinguish accounts: name, provider, status, auth method, and OAuth email/plan when available.
2. **Given** the operator selects an active account, enters text, and submits, **When** the run completes successfully, **Then** the result identifies that same account and does not silently substitute another account.
3. **Given** the selected account is disabled, deleted, or otherwise ineligible before submit completes, **When** the run reaches the server, **Then** it fails with a business outcome stating that the selected account cannot be used and does not fall back to Automatic mode.
4. **Given** the selected account is OAuth-backed and receives a permanent credential-refresh failure, **When** the run attempts to use that account, **Then** the account follows the existing permanent-failure handling policy and the Playground shows a sanitized account failure.

**Edge Cases**:
- What if an account is visible when the picker loads but becomes disabled before submit? → The run fails clearly; the Playground does not test a disabled account.
- What if account metadata cannot be loaded? → The page shows a non-blocking error with a correlation identifier and preserves the prompt text.
- What if the operator selects Account mode but no account is selected? → The page shows an inline validation error and creates no run.
- What if the account has secret material stored for routing? → The account picker and result never expose API keys, OAuth access tokens, refresh tokens, ID tokens, raw authorization headers, or any derived bearer value.

---

### US-3: Understand Waiting, Result, and Error States (Priority: P0)

The Playground must make request progress and outcome unambiguous, because operators use it while debugging failures and need to know whether the issue is input validation, account selection, credentials, the provider, or the router.

**As a** platform operator, **I want** clear waiting, success, and error states, **so that** I can decide the next action without guessing what failed.

**Why this priority**: A debugging surface that hides state creates more ambiguity than it removes. The first release must be explicit about whether a run is pending, succeeded, failed before upstream, failed at upstream, timed out, or returned no extractable text.

**Acceptance Scenarios**:
1. **Given** a run is pending, **When** the operator views the Playground, **Then** the submitted prompt remains visible, the run control shows an in-progress state, duplicate submit is disabled, and assistive technology receives a status update.
2. **Given** a run succeeds and response text can be extracted, **When** the result is displayed, **Then** the page shows response text, selected account, selection mode, outcome, latency, and usage metadata when available.
3. **Given** validation fails before any provider call, **When** the operator submits empty or over-limit text, **Then** the page shows an inline field error, creates no run, and preserves focus in the relevant input.
4. **Given** the provider, network, or router fails after submit, **When** the run reaches a terminal state, **Then** the page shows an error panel with stable error symbol, safe message, correlation identifier when available, selected account when known, and no credential material.
5. **Given** the upstream returns a safe response that contains no extractable text, **When** the run completes, **Then** the page states that no text answer was extracted and offers the safe raw result area instead of inventing response text.

**Edge Cases**:
- What if the operator navigates away while waiting? → No portal-wide error is triggered; returning to the Playground starts from a clean non-running state.
- What if the operator edits text or account after an error? → The previous error remains visible until the next run or explicit reset, and the new input is not overwritten.
- What if the upstream response is malformed or too large to render safely? → The Playground shows a parse or size error and no fabricated output.
- What if an upstream error includes provider text that might contain operator input? → The Playground treats it as sensitive diagnostic data and displays only the documented safe subset.

---

### US-4: Correlate Playground Runs Without Leaking Secrets (Priority: P0)

Operators need Playground runs to appear in operational traces and request history so they can correlate a failed debug run with router logs, while preserving the same secret-handling boundary as production routing.

**As a** platform operator, **I want** Playground runs to be observable without exposing credentials, **so that** I can debug failures using logs and request history safely.

**Why this priority**: The router is an operational control plane. If Playground runs are invisible, they cannot support incident diagnosis. If they leak credentials, the feature is unsafe.

**Acceptance Scenarios**:
1. **Given** a Playground run is submitted, **When** it completes or fails, **Then** structured observability data includes request id, selection mode, selected account id when known, auth method when known, upstream status when known, latency, and outcome.
2. **Given** runtime body logging is disabled, **When** a Playground run is recorded, **Then** prompt text and response body are not stored in request history.
3. **Given** runtime body logging is enabled, **When** a Playground run is recorded, **Then** prompt and response body capture follows the same retention and redaction expectations as production proxy records.
4. **Given** any Playground run succeeds or fails, **When** logs, admin responses, and recorded metadata are inspected, **Then** they contain zero API key bytes, OAuth access token bytes, refresh token bytes, ID token bytes, authorization header values, or provider bearer values.

**Edge Cases**:
- What if a client cancels while the router is waiting on upstream? → The run records a cancelled or router-error outcome without leaking partial credential state.
- What if multiple operators run the Playground concurrently? → Each browser session sees only its own pending/result state; shared logs and request history distinguish runs by request id and account metadata.
- What if an explicit account run fails before account resolution? → Observability records the selection mode and submitted account reference when safe, but does not fabricate a selected account.

---

### US-5: Keep the Playground Narrow and Non-Destructive (Priority: P1)

The Playground should help diagnose accounts, not become another place to mutate account state, change routing policy, or build a chat workflow.

**As a** platform operator, **I want** the Playground to be focused and non-destructive, **so that** I can test accounts without risking accidental configuration changes.

**Why this priority**: A narrow first release lowers operational risk and keeps account debugging separate from account management, routing policy, and future client-key simulation.

**Acceptance Scenarios**:
1. **Given** the operator runs the same text multiple times, **When** each run completes, **Then** the Playground does not create a persistent prompt history beyond the router's existing request-recording behavior.
2. **Given** the operator is on the Playground, **When** they need to repair a failed account, **Then** the page provides a path to the account detail surface rather than embedding credential rotation, re-authentication, enable, disable, delete, import, or export actions.
3. **Given** a disabled or deleted account exists, **When** the operator tries to use it for debugging, **Then** the Playground refuses the run instead of bypassing normal account eligibility.
4. **Given** the operator needs to call the router from an external client, **When** they open the Playground integration guide, **Then** a simplified modal layer shows copyable examples for cURL, Python, JavaScript, and Go that target the current router origin's `/v1/responses` data-plane URL, reserve an empty API-key placeholder, and state the downstream response behavior: OAuth ChatGPT accounts return JSON for `stream:false` and SSE for `stream:true`. The cURL example is a single command with no shell variable definitions.

**Edge Cases**:
- What if the operator wants a multi-turn conversation? → The first release does not preserve conversation memory and each run is a single-turn probe.
- What if the operator wants streaming output? → The first release waits for a final terminal result; streaming is deferred to a later feature decision.
- What if the operator wants to simulate a client API key or future routing policy? → The first release tests account reachability only and does not simulate client identity.
- What if the router is served from a non-localhost origin? → The guide derives the base URL from the browser's current origin instead of hard-coding `localhost`.

## Functional Requirements

- **FR-001**: The Admin Portal MUST include a live Playground entry reachable from the portal shell once setup is complete.
- **FR-002**: The Playground MUST allow the operator to enter a single plain-text prompt for a single-turn probe.
- **FR-003**: The Playground MUST define a prompt length limit and MUST reject empty or over-limit prompts before any run is created, with inline validation that states the violated rule.
- **FR-004**: The Playground MUST support exactly two mutually exclusive selection modes in the first release: Automatic and Account.
- **FR-005**: Automatic mode MUST use the router's normal eligible-account selection behavior and MUST reveal the selected account after the run reaches a terminal state when an account is known.
- **FR-006**: Account mode MUST require exactly one active upstream account and MUST NOT silently fall back to another account.
- **FR-007**: Disabled, deleted, missing, or otherwise ineligible accounts MUST NOT be runnable from Account mode.
- **FR-008**: Playground runs MUST use the same account-specific provider transport as the data-plane proxy: API-key accounts call the OpenAI Platform-compatible `base_url + /v1/responses` path with the API-key bearer; OAuth accounts refresh before use, call the ChatGPT Codex backend `/codex/responses` path with the OAuth bearer, include `chatgpt-account-id` when the selected row has `chatgpt_account_id`, and set `User-Agent: codex_cli_rs/0.120.0 (Mac OS 26.2; arm64) dumb`.
- **FR-009**: The account picker MUST expose non-secret metadata sufficient to distinguish accounts: name, provider, status, auth method, and OAuth email/plan when available.
- **FR-010**: The browser-facing Playground flow MUST use the router-owned Admin Portal API boundary and MUST NOT require the operator's browser to call the data-plane provider-compatible route directly.
- **FR-011**: Playground admin responses MUST follow the router-owned JSON response convention: success uses the success envelope, business failures use non-zero registered codes, system failures are system errors, and the correlation identifier is not duplicated into the response body.
- **FR-012**: A successful run MUST display response text when available, selected account, selection mode, outcome, upstream status when available, response mode when available, latency, and usage metadata when available.
- **FR-013**: If no text answer can be extracted from an otherwise safe successful provider response, the Playground MUST state that explicitly and MUST NOT fabricate response text.
- **FR-014**: Upstream non-success responses MUST NOT be converted into successful Playground answers; they MUST surface as documented failure outcomes with sanitized diagnostics.
- **FR-015**: Failed runs MUST display a stable error symbol, safe message, correlation identifier when available, and selected account when known.
- **FR-016**: Router, provider, and validation failures MUST preserve the operator's prompt and selected mode so the operator can adjust and retry.
- **FR-017**: The Playground MUST prevent duplicate submit while a run is pending.
- **FR-018**: The Playground MUST distinguish Playground-originated runs from normal client traffic in observability and request history.
- **FR-019**: Prompt body and response body persistence MUST follow the existing runtime body-logging policy; metadata required for observability remains recordable.
- **FR-020**: The Playground MUST never display, log, return, or record upstream credential material, including API keys, access tokens, refresh tokens, ID tokens, authorization headers, or provider bearer values.
- **FR-021**: The Playground MUST not mutate account status, account credentials, routing policy, setup state, plugin configuration, or admin authentication state.
- **FR-022**: The Playground MUST preserve existing client data-plane behavior by sharing the same provider transport logic rather than inventing a separate upstream client. Normal Codex clients continue to call `/v1/*`; API-key client traffic remains OpenAI Platform-compatible, and OAuth Responses traffic continues to use the ChatGPT Codex backend adaptation defined by Feature 003.
- **FR-023**: The Playground MUST expose an operator integration guide in a simplified modal layer with language tabs for cURL, Python, JavaScript, and Go. Each example MUST use the browser's current origin plus `/v1/responses` as the router data-plane URL and MUST include an API-key variable/header placeholder whose value is empty in the current build. The cURL example MUST be a single command with no shell variable definitions. The guide MUST avoid secondary metadata badges, MUST use an icon-only close button, MUST omit a separate response-mode summary card, and MUST state that OAuth ChatGPT accounts return JSON for `stream:false` and SSE for `stream:true`.

## Non-Functional Requirements

| Category | Requirement | Metric | Verification |
|----------|-------------|--------|--------------|
| Usability | Operator can validate a newly onboarded active account from the portal | A first-time operator completes one successful Account-mode probe in <=60 seconds after page load during manual usability pass | Manual usability test |
| Usability | Waiting state is unambiguous | Submit control enters pending state within 200 ms of submit in 100% of UI tests | Frontend interaction test |
| Accessibility | Keyboard-only operation | 100% of Playground controls reachable and operable by keyboard; focus order matches visual order | Accessibility test |
| Accessibility | Screen-reader status | Loading, validation error, success, timeout, and run-failure states are announced through semantic status or alert regions | Accessibility test |
| Performance | Validation failures avoid upstream work | Empty, over-limit, and missing-account validation failures return within 200 ms P95 and create zero upstream calls | Contract/integration test |
| Performance | Router overhead is bounded | P95 router processing overhead excluding provider wait and credential refresh is <=500 ms on a local development machine | Integration benchmark |
| Reliability | Duplicate submit protection | Double-click or Enter-repeat while pending creates exactly one run in 100% of interaction tests | Integration/UI test |
| Reliability | No silent account substitution | In Account mode, 100% of successful results identify the requested account; stale or ineligible selected accounts fail instead of falling back | Integration test |
| Timeout Recovery | Slow upstreams do not strand the UI | A run that exceeds the configured wait limit returns to an actionable error state with preserved prompt | Integration/UI test |
| Security | No credential exposure | 0 occurrences of stored API key or OAuth token bytes in Playground UI, Admin Portal responses, automatic logs, and request-history metadata | Log-scrub plus contract test |
| Observability | Runs are traceable | 100% of completed, failed, timed-out, and cancelled runs carry request id, selection mode, outcome, latency, and selected account when known in structured observability data | Log/request-record test |
| Compatibility | Playground transport matches data-plane transport | API-key probes hit Platform-compatible `/v1/responses`; OAuth probes hit ChatGPT Codex `/codex/responses` with `chatgpt-account-id`; existing data-plane regression suite remains green | Regression test + fake-upstream transport assertions |

## Key Entities

- **Playground Run**: A single operator-initiated text probe. It has submitted text, selection mode, selected or resolved account when known, outcome, latency, optional usage metadata, and safe result or error content. It is not a long-lived conversation.
- **Selection Mode**: The operator's choice between Automatic routing and one explicitly selected Account. Automatic follows router account-selection policy; Account must use the chosen eligible account or fail clearly.
- **Run Result**: The displayed terminal outcome of a Playground Run. It may be success, validation failure, router failure, upstream/provider failure, timeout, cancellation, or no-extractable-text.
- **Eligible Account**: An upstream account the router is allowed to use for a Playground Run. Disabled, deleted, missing, and otherwise unavailable accounts are not eligible.
- **Safe Diagnostic Data**: Non-secret metadata that helps an operator debug a run, such as account id/name/provider/auth method, selection mode, upstream status, response mode, outcome, latency, usage metadata, and sanitized provider/router error detail.

## Success Criteria

- **SC-1**: An operator can run a successful text probe against a newly onboarded active account from the Admin Portal without using curl, Codex CLI, or any external client.
- **SC-2**: In a usability pass, 100% of tested operators can distinguish Automatic mode from Account mode and can identify which account handled the latest completed run.
- **SC-3**: Automated tests cover and pass distinct terminal states for empty input, over-limit input, no active accounts, stale selected account, provider failure, timeout, no-extractable-text, and success.
- **SC-4**: No stored credential bytes appear in Playground UI, Admin Portal payloads, automatic logs, or request-history metadata across the security test suite.
- **SC-5**: Existing account onboarding, account list/detail, settings, setup wizard, and data-plane proxy regression tests continue to pass without behavior changes.

## Scope

### In Scope

- Admin Portal Playground page for single-turn text probes.
- Automatic account selection mode.
- Explicit active-account selection mode.
- Account picker showing non-secret account metadata.
- Loading, empty, validation, success, provider-error, router-error, timeout, cancellation, and no-extractable-text states.
- Safe display of selected account metadata, response text when available, latency, outcome, upstream status when available, response mode when available, usage metadata when available, and correlation identifier on errors.
- Observability for Playground-originated runs that is distinguishable from normal client traffic.
- Accessibility requirements for form controls, waiting state, result state, and error state.
- Preservation of the existing Admin Portal shell style and error-surfacing conventions.
- Integration-guide examples for external client calls to the router data-plane endpoint.

### Out of Scope

- Multi-turn chat sessions or conversation memory.
- Streaming response display in the first release.
- File, image, audio, tool-call, function-call, or multimodal Playground inputs.
- Prompt templates, saved prompt history, shared run history, comparison notebooks, or replay tooling.
- Editing, enabling, disabling, deleting, creating, importing, exporting, or re-authenticating accounts from the Playground itself.
- Bypassing disabled, deleted, or otherwise ineligible account restrictions for debugging.
- Client API-key simulation, client identity simulation, or per-client routing-policy debugging.
- Quota dashboards, account-health scoring, automatic quarantine, retry-policy editing, or usage aggregation.
- Provider-specific advanced parameter tuning beyond the minimum needed for a text probe.
- Admin authentication, authorization, or RBAC changes.
- A generic arbitrary upstream proxy exposed through the Admin Portal.

## Assumptions

- The Playground is an operator debugging surface, not an end-user chat interface.
- The first release is non-streaming: the operator waits for a final terminal result or safe failure state.
- Automatic mode follows the same account eligibility rules as normal routing.
- Account mode is intentionally stricter than normal routing: it must test the selected active account or fail clearly.
- Existing request-recording and body-logging policy remains authoritative for whether prompt and response bodies are persisted.
- Safe raw result display is allowed only for valid, bounded provider payloads that contain no credential material; exact size and field limits are deferred to planning.
- The Playground keeps the existing Admin Portal visual direction: dense operational layout, left navigation, panel-based content, correlation-id error surfacing, and visible disabled entries for features that remain planned.
- This feature is assigned feature number 004 by directory order. Existing roadmap or error-code reservations that also refer to 004 must be reconciled during planning before implementation begins.

## Clarifications

### Session 2026-04-23

- Q: Should the first Playground release provide live streaming output? → A: No. The first release is a non-streaming single-turn probe so the operator waits for one terminal result; streaming is deferred because it changes response and UI semantics.
- Q: Should Account mode allow testing disabled or deleted accounts for deeper debugging? → A: No. Account mode must respect account eligibility and fail clearly rather than bypassing router safety policy.
- Q: Should the Playground include account repair actions such as re-authenticate, enable, disable, import, or export? → A: No. It may link to account detail, but mutation flows stay in account-management surfaces.
- Q: Should the prompt limit be character-based or byte-based? → A: Keep the operator-facing prompt limit at 16,000 trimmed Unicode characters. Planning raises the Playground JSON request body cap so non-ASCII prompts can fit this character limit.
- Q: Should the Playground expose model selection? → A: Yes. The page exposes a model input with default `gpt-5.4-mini`.
- Q: Should 004 introduce the future `react-i18next` text system? → A: No. 004 follows the current Admin Portal `.strings.ts` page-string pattern.
- Q: Should duplicate submits be API-idempotent? → A: No for P0. Duplicate-submit protection is UI-only while a run is pending; direct repeated API requests may create separate runs.
