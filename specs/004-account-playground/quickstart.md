# Quickstart: Account Playground

**Feature**: 004-account-playground
**Spec**: `specs/004-account-playground/spec.md`
**Created**: 2026-04-23
**Status**: Ready

Key validation scenarios after implementation.

## Validation Status

Executed against the Playwright test harness and repository quality gates on 2026-04-24.

| Scenario | Coverage | Status |
|----------|----------|--------|
| 1. Account Mode Happy Path | `frontend/tests/e2e/playground.spec.ts` — `account mode submits the selected active account without falling back` | Automated |
| 2. Automatic Mode Happy Path | `frontend/tests/e2e/playground.spec.ts` — `automatic mode renders a successful probe result` | Automated |
| 3. No Active Accounts | `frontend/tests/e2e/playground.spec.ts` — `no active accounts shows the empty state and direct API returns 4002` | Automated |
| 4. Selected Account Becomes Ineligible | `frontend/tests/e2e/playground.spec.ts` — `account mode surfaces 4003 when the selected account becomes unavailable` | Automated |
| 5. Validation Errors | `frontend/src/routes/admin/playground.test.tsx` — inline validation + focus preservation | Automated |
| 6. Upstream Error | `frontend/tests/e2e/playground.spec.ts` — `provider errors preserve prompt state, show correlation id, and link to account repair`; plus `scripts/log-scrub.sh` verification | Automated |
| 7. No Extractable Text | `frontend/tests/e2e/playground.spec.ts` — `successful no-text probes show the explicit no-text state and raw JSON` | Automated |
| 8. Integration Guide | `frontend/src/routes/admin/playground.test.tsx` — simplified current-origin `/v1/responses` examples for cURL, Python, JavaScript, and Go with empty API-key placeholder; cURL is a single command without shell variables | Automated |
| 9. Regression Smoke | `go test ./...`, `go build ./...`, `golangci-lint run`, `bash scripts/envelope-parity.sh`, `bash scripts/coverage-floor.sh`, `pnpm --dir frontend typecheck`, `pnpm --dir frontend test`, `pnpm --dir frontend lint`, `pnpm --dir frontend build` | Automated |

No quickstart deviation was observed in the automated harness. No scenario required live provider credentials.

## 1. Account Mode Happy Path

1. Start the router with setup complete and at least one active upstream account.
2. Open `/admin/playground`.
3. Select **Account** mode.
4. Choose an active account.
5. Enter `Say hello in one sentence.`.
6. Run the probe.
7. Verify the page shows a terminal success state with:
   - selected account id/name matching the picker
   - selection mode `Account`
   - response text when available
   - latency
   - usage metadata when provider supplies it
   - no credential material

## 2. Automatic Mode Happy Path

1. Open `/admin/playground`.
2. Leave selection mode as **Automatic**.
3. Enter a non-empty prompt.
4. Run the probe.
5. Verify the page shows which account the router selected.
6. Repeat with the same session value when supported and verify the selection follows the router's normal consistency policy.

## 3. No Active Accounts

1. Disable or delete every account.
2. Open `/admin/playground`.
3. Verify the page shows an empty/no-active-accounts state and disables Run.
4. Submit directly to the API if needed and verify HTTP 200 with non-zero `playground_no_active_account`.

## 4. Selected Account Becomes Ineligible

1. Open `/admin/playground` with at least one active account.
2. Select a specific account.
3. Disable that account from another tab or direct API call.
4. Run the probe.
5. Verify the Playground fails with `playground_account_unavailable` and does not fall back to Automatic mode.

## 5. Validation Errors

1. Submit empty text.
2. Verify inline validation appears and no run is created.
3. Submit text longer than the configured limit.
4. Verify inline validation states the limit and no upstream request is made.

## 6. Upstream Error

1. Configure a fake upstream server or an account fixture that returns a provider error.
2. Run the Playground against that account.
3. Verify the UI shows an upstream/provider failure, selected account metadata, and correlation id.
4. Verify the response and logs contain no credential bytes.

## 7. No Extractable Text

1. Configure fake upstream success JSON without a known text output field.
2. Run the Playground.
3. Verify the result says no text answer was extracted and does not invent output text.
4. Verify bounded raw JSON is available only when safe and enabled.

## 8. Integration Guide

1. Open `/admin/playground`.
2. Click **Integration guide**.
3. Verify the modal layer opens and contains tabs for cURL, Python, JavaScript, and Go.
4. Verify each example uses the current browser origin plus `/v1/responses`.
5. Verify each example reserves an empty API-key variable/header placeholder for the current build, and the cURL example uses no shell variable definitions.
6. Verify the guide has no `data plane` badge, no separate response-mode summary card, an icon-only close button, and states that OAuth accounts return JSON for `stream:false` and SSE for `stream:true`.
7. Close the layer with the close action or Escape.

## 9. Regression Smoke

1. Run existing account list/detail/onboarding flows.
2. Run an existing `/v1/responses` proxy request outside the Playground for an API-key account and verify it still forwards to the account's OpenAI Platform-compatible `base_url + /v1/responses`.
3. Run an OAuth-backed `/v1/responses` request outside the Playground and verify it still uses the ChatGPT Codex backend with `chatgpt-account-id` when present and `User-Agent: codex_cli_rs/0.120.0 (Mac OS 26.2; arm64) dumb`.
