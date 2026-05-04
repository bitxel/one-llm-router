# Implementation Plan: Dashboard Observability

**Feature**: 005-dashboard-observability
**Spec**: `specs/005-dashboard-observability/spec.md`
**Created**: 2026-04-25
**Status**: Ready
**Complexity**: complex
**Complexity Reason**: The feature touches backend persistence, SSE proxy behavior, OpenAPI/generated clients, Admin API envelope handling, frontend routing/state, charts, and UI tests.
**Complexity Override**: none

## Summary

Add a live Admin Dashboard backed by retained request records. The backend records TTFT for streaming text responses, aggregates dashboard metrics through one Admin API endpoint, and returns envelope-compliant data. The frontend replaces the static dashboard with metric cards, filters, Recharts curves, and browser-local card settings for order and visibility.

## Technical Context

| Item | Value |
|---|---|
| Backend | Go 1.25, `net/http`, `slog`, `xorm` |
| Persistence | Existing SQLite/PostgreSQL/MySQL migrations; add nullable `request_records.ttft_ms` |
| OpenAPI | `openapi/admin.yaml` is source of truth; regenerate Go and TS clients |
| Frontend | React 19, TanStack Router/Query, Tailwind CSS v4, existing Neo admin shell |
| Charts | Recharts v2.15+ per `frontend/AGENTS.md`; add dependency and React 19-compatible `react-is` override if needed |
| Settings persistence | Browser `localStorage` for non-secret dashboard card ids/order only |

## Resolved Decisions

1. **TTFT event**: TTFT starts at router request entry and stops at the first SSE event whose JSON payload has `type = "response.output_text.delta"` and a non-empty `delta`.
2. **Range buckets**: `1h` uses 1-minute buckets; `1d` uses 1-hour buckets; `7d` uses 6-hour buckets; `30d` uses 1-day buckets.
3. **Error-rate numerator**: Outcomes `success` and `no_extractable_text` are non-errors. All other outcomes count as errors.
4. **Token normalization**: Use `input`/`input_tokens`, `cached_input`/`cached_tokens`, `output`/`output_tokens`; compute non-cached input as `max(input - cached, 0)`.
5. **TTFT card value**: The card headline is P95 TTFT over the selected range. Each series bucket also uses P95 for that bucket.
6. **Plugin model**: Dashboard observability is core code, not a plugin, because it is not a user-visible on/off capability implementation.

## Constitution Check

- [x] Spec-first: new behavior is captured in `spec.md` before code changes.
- [x] Simplicity: one API endpoint and one nullable field; no rollup table until scale evidence requires it.
- [x] Test-first: contract, store aggregation, SSE TTFT, handler, and frontend tests are defined before implementation behavior.
- [x] Traceability: every implementation area maps to FRs in the spec.
- [x] Reversibility: migration is additive and nullable; frontend settings are local and resettable.

## Architecture

| Module | Responsibility | Change |
|---|---|---|
| `openapi/admin.yaml` | Add dashboard operation, schemas, business/system errors | Modified |
| `docs/error-codes.md` | Register 005 codes | Modified |
| `internal/api/errcode` | Mirror 005 code constants/symbols | Modified |
| `internal/domain/request_record.go` | Add optional `TTFTMs` field | Modified |
| `internal/store/migrations/*` | Add/drop nullable `ttft_ms` | New |
| `internal/api/sse.go` | Capture first streamed text delta timing | Modified |
| `internal/api/proxy.go` | Persist TTFT on SSE request records | Modified |
| `internal/core/dashboard_service.go` | Validate filters, call aggregation, count active accounts | New |
| `internal/store/records.go` | Aggregate retained records into dashboard snapshot data | Modified |
| `internal/api/adminapi/dashboard.go` | Envelope handler and route registration | New |
| `internal/app/app.go` | Wire dashboard handler in steady state | Modified |
| `frontend/src/routes/admin/index.tsx` | Live dashboard UI | Modified |
| `frontend/src/router.tsx` | URL-backed dashboard search params | Modified |
| `frontend/package.json` / lockfile | Add Recharts dependency | Modified |

## API

`GET /api/admin/dashboard`

Query parameters:

- `range`: optional enum `1h | 1d | 7d | 30d`, default `7d`.
- `account_id`: optional positive int64.

Success envelope data includes:

- `range`, `window_start`, `window_end`, `bucket_seconds`
- `active_accounts`
- `account_options`
- `cards.requests`
- `cards.tokens`
- `cards.error_rate`
- `cards.ttft`

Planned codes:

| Code | Symbol | HTTP | Meaning |
|---|---|---|---|
| 5001 | `dashboard_invalid_filter` | 200 | Invalid range or account filter |
| 5900 | `dashboard_internal_error` | 500 | Unexpected dashboard handler/service/storage failure |

## Verification

- `go test ./internal/api ./internal/api/adminapi ./internal/core ./internal/store`
- `go test ./internal/app`
- `bash scripts/codegen-go.sh && bash scripts/codegen-frontend.sh`
- `bash scripts/envelope-parity.sh`
- `pnpm --dir frontend typecheck`
- `pnpm --dir frontend test -- dashboard`
