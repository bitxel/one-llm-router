# Tasks: Dashboard Observability

**Feature**: 005-dashboard-observability
**Plan**: `specs/005-dashboard-observability/plan.md`
**Spec**: `specs/005-dashboard-observability/spec.md`
**Created**: 2026-04-25
**Status**: Implemented

## Phase 1: Contracts and Tests

- [x] T-001 Register 005 error codes in docs, Go, and TS.
- [x] T-002 Extend OpenAPI with `GET /api/admin/dashboard` and regenerate Go/TS clients.
- [x] T-003 Add envelope parity probes for the dashboard route.
- [x] T-004 Write RED tests for SSE TTFT capture.
- [x] T-005 Write RED tests for dashboard aggregation and filter validation.
- [x] T-006 Write RED tests for the dashboard Admin API handler.
- [x] T-007 Write RED frontend tests for cards, filters, and settings.

## Phase 2: Backend

- [x] T-010 Add nullable `ttft_ms` migrations and domain field.
- [x] T-011 Capture TTFT in SSE forwarding and persist it through the proxy.
- [x] T-012 Implement dashboard aggregation over retained request records.
- [x] T-013 Implement dashboard service validation and active-account count.
- [x] T-014 Implement dashboard Admin API handler and app wiring.

## Phase 3: Frontend

- [x] T-020 Add dashboard URL search params.
- [x] T-021 Add Recharts dependency following the project chart standard.
- [x] T-022 Replace the static admin index with live dashboard cards.
- [x] T-023 Add range/account filters and loading/error/empty states.
- [x] T-024 Add dashboard settings for card visibility and order.

## Phase 4: Verification

- [x] T-030 Run targeted Go tests.
- [x] T-031 Run OpenAPI codegen and envelope parity.
- [x] T-032 Run frontend typecheck/tests.
- [x] T-033 Review generated diffs and summarize residual risk.
