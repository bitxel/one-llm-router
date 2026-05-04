# Feature Spec: Dashboard Observability

**ID**: 005-dashboard-observability
**Created**: 2026-04-25
**Updated**: 2026-04-25
**Status**: Ready

## Overview

Operators need the Admin Dashboard to answer the first operational questions without leaving the portal: how much traffic is flowing, which accounts are involved, how many tokens are consumed, whether errors are rising, and whether streaming users are waiting too long for the first token. Feature 005 replaces the static admin landing page with live observability cards backed by retained request records.

The first release keeps the scope intentionally focused: one read-only Admin API endpoint, one schema expansion for `request_records.ttft_ms`, server-side aggregation over retained records, and a configurable dashboard layout in the browser. The dashboard does not mutate routing policy, credentials, account status, retention policy, or plugin configuration.

## User Scenarios

### US-1: See Current Capacity at a Glance (P0)

As a platform operator, I want the dashboard to show the current active upstream account count, so that I know whether the router has serving capacity before inspecting detailed pages.

**Acceptance Scenarios**:
1. Given the dashboard loads, when active accounts exist, then the active-account card shows the current count from stored account state.
2. Given no active accounts exist, when the dashboard loads, then the active-account card shows zero and does not fabricate capacity.
3. Given an account filter is selected for traffic cards, when active accounts are counted, then the active-account card still represents the whole router capacity, not a filtered historical request subset.

### US-2: Inspect Request Volume by Time Range and Account (P0)

As a platform operator, I want request volume displayed as a metric card with a time-series curve and filters for `1h`, `1d`, `7d`, `30d`, and account, so that I can quickly identify traffic spikes and account-specific load.

**Acceptance Scenarios**:
1. Given request records exist in the latest seven days, when the dashboard opens with no query params, then the requests card shows the latest seven-day total and a bucketed time-series curve.
2. Given the operator changes the range to `1h`, `1d`, or `30d`, then the cards refresh from the API with the selected range in URL search params.
3. Given the operator selects an account, then time-ranged cards show only request records for that account while account options remain available.

### US-3: Break Down Token Usage (P0)

As a platform operator, I want token usage split into non-cached input, cached input, and output, so that I can understand cost and cache impact instead of seeing one opaque token total.

**Acceptance Scenarios**:
1. Given request records include usage metadata, then the token card shows range totals for cached input, non-cached input, and output.
2. Given `cached_input` is present and `input` is present, then non-cached input is computed as `max(input - cached_input, 0)`.
3. Given usage metadata uses provider variants such as `input_tokens`, `output_tokens`, or `total_tokens`, then aggregation normalizes the known fields without inventing missing categories.

### US-4: Track Error Rate (P0)

As a platform operator, I want an error-rate card with a trend curve, so that I can see whether traffic quality is degrading.

**Acceptance Scenarios**:
1. Given records in range, then error rate is computed as error records divided by total records.
2. Given zero records in range, then error rate is zero with an empty series rather than a division-by-zero failure.
3. Given outcomes are `success` or `no_extractable_text`, then they are treated as non-error terminal outcomes; other outcomes are counted as errors.

### US-5: Capture and Display TTFT (P0)

As a platform operator, I want TTFT recorded for streaming responses and displayed on the dashboard, so that I can detect poor first-token latency separately from total request latency.

**Acceptance Scenarios**:
1. Given an SSE response emits a first `response.output_text.delta` event with a non-empty `delta`, then the request record stores `ttft_ms` as the elapsed time from request start to receiving that event.
2. Given an SSE stream emits only lifecycle or completion events, then `ttft_ms` remains absent.
3. Given non-streaming JSON responses, then `ttft_ms` remains absent.
4. Given TTFT values exist in range, then the TTFT card shows P95 TTFT and a P95 bucketed series.

### US-6: Customize Dashboard Cards (P1)

As a platform operator, I want to reorder cards and hide cards I do not need, so that the dashboard matches my monitoring workflow.

**Acceptance Scenarios**:
1. Given the operator clicks the dashboard settings button, then an editing surface opens from the dashboard page.
2. Given the operator hides a card, then that card is removed from the grid without deleting the underlying server data.
3. Given the operator moves a card up or down, then the grid order updates immediately and persists across reloads in browser-local non-secret settings.
4. Given all cards are hidden, then the dashboard shows a safe empty layout state and the settings button remains available.

## Functional Requirements

- **FR-001**: The Admin Portal dashboard MUST replace the static admin landing content with live metric cards.
- **FR-002**: The dashboard MUST include cards for active accounts, request count, token usage, error rate, and TTFT.
- **FR-003**: Time-ranged cards MUST default to the latest `7d` and support `1h`, `1d`, `7d`, and `30d`.
- **FR-004**: Time-ranged cards MUST support filtering by one upstream account id or all accounts.
- **FR-005**: Time-ranged cards MUST include a bottom time-series curve using bucketed server data.
- **FR-006**: The dashboard API MUST be `GET /api/admin/dashboard` and MUST use the standard Admin API envelope.
- **FR-007**: Invalid dashboard filters MUST return a registered 005 business error with HTTP 200.
- **FR-008**: Dashboard system failures MUST return a registered 005 system error with HTTP 500.
- **FR-009**: `request_records` MUST store optional `ttft_ms` for streaming responses where the first text delta is observed.
- **FR-010**: Dashboard aggregation MUST derive facts only from account rows and retained request records; it MUST NOT fabricate traffic, usage, health, or latency values.
- **FR-011**: Card order and visibility settings MUST be adjustable by the user from a dashboard settings control.
- **FR-012**: Dashboard settings MUST store only non-secret UI preferences.

## Non-Functional Requirements

| Category | Requirement | Verification |
|---|---|---|
| Contract | OpenAPI is source of truth for the dashboard endpoint | OpenAPI codegen and envelope parity pass |
| Reliability | Aggregation handles empty ranges and missing token/TTFT fields | Store and handler tests |
| Performance | One dashboard read over retained records completes without per-card DB round trips | Store aggregation test and code review |
| Accessibility | Settings controls and filters are keyboard-operable with labels | Frontend tests |
| Security | No credential material is returned by the dashboard endpoint | Schema review and tests |
| Compatibility | Existing request-log, playground, proxy, and setup behavior remains unchanged | Regression tests |

## Key Entities

- **Dashboard Snapshot**: The API response for one selected range and account filter. Contains active-account count, account options, and card metric payloads.
- **Dashboard Range**: One of `1h`, `1d`, `7d`, or `30d`, mapped server-side to a fixed time window and bucket interval.
- **Dashboard Series Point**: A bucketed aggregate with timestamp and metric-specific values.
- **TTFT**: Time to first token, measured in milliseconds from router request start until the first non-empty streamed `response.output_text.delta`.
- **Dashboard Card Setting**: Browser-local card visibility and ordering preference. It stores card ids only.

## Scope

### In Scope

- `GET /api/admin/dashboard`.
- Optional `ttft_ms` persistence.
- Request count, token usage, error rate, TTFT, and active-account cards.
- Range and account filters.
- Time-series charts in dashboard cards.
- User-configurable card visibility and ordering.

### Out of Scope

- Cost or spend charts.
- Per-model, per-client-key, per-session, or per-provider drilldowns.
- Alerting, SLO thresholds, anomaly detection, or notifications.
- New retention policy controls.
- Admin auth or RBAC changes.
- Prometheus/exporter endpoints.
- Plugin modeling; this is core dashboard behavior, not a user-toggleable plugin.

## Assumptions

- Retained request records are the authoritative usage source for this release.
- The dashboard can aggregate over retained rows without adding a separate rollup table in 005.
- TTFT applies only to streaming responses with observable text deltas.
- Browser-local dashboard layout preferences are acceptable because no admin identity model exists yet.
