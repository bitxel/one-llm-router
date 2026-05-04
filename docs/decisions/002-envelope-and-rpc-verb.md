# ADR-002-envelope-and-rpc-verb — JSON Envelope & RPC-Verb Admin Routes

- **Status**: Accepted — lands with feature 002 (setup-wizard-and-admin-portal-skeleton).
- **Deciders**: Platform Backend Guild
- **Date**: 2026-04-18
- **Related**: `ROADMAP.md` decisions #8 and #9; `specs/002-.../research.md` §Decision 8 & §Decision 9.
- **Amended**: 2026-04-25 by Feature 003. `/v1/*` remains excluded from the Admin API envelope, but upstream forwarding is now account-specific: API-key rows keep 002 Platform-compatible forwarding, while OAuth rows use the ChatGPT Codex backend for Responses traffic.

## Context

Feature 001 (Codex MVP) shipped two HTTP surfaces:

1. **`/v1/*`** — provider-compatible data-plane facade to upstream model providers. In 001/002 this was transparent OpenAI Platform forwarding. Feature 003 keeps the same client-facing route and envelope exclusion but makes upstream transport account-specific for ChatGPT OAuth accounts.
2. **`/admin/*`** — internal-only CRUD (accounts, request log, health, session resolve). Responses used ad-hoc JSON shapes with HTTP status codes for error signalling (`400`/`404`/`409`/`500`).

Feature 002 adds a setup wizard and an admin portal SPA. Both the SPA and a future fleet of internal clients need:

- **A single parse pathway** — one envelope shape at `/api/admin/*` and `/api/setup/*`, so the SPA's api-client (`frontend/src/lib/api-client.ts`) has a single unwrap routine, and so server-side structured logging can mint one stable schema per endpoint.
- **An observable correlation id** — every response, success or failure, carries an `X-Request-Id` header that pairs with a server-side `slog` record.
- **RPC-style mutations** — the wizard and settings panel perform verbs ("commit this setup", "update these settings", "disable this account"), not REST-style resource manipulations. Modelling them as `POST /api/admin/accounts/{id}/disable` rather than `PATCH /api/admin/accounts/{id}` keeps the intent explicit, matches existing 001 paths (`/admin/accounts/{id}/disable`), and avoids the PATCH-body merge-semantics ambiguity.

The existing `/v1/*` surface is a contract with third-party clients; we cannot wrap it in the Admin API envelope. Feature 003 may adapt the upstream transport behind that facade when the selected account is a ChatGPT OAuth account, as long as downstream clients still receive provider-compatible `/v1/responses` JSON/SSE semantics.

## Decision

### #8 — JSON Envelope for `/api/admin/*` and `/api/setup/*`

Every admin and setup JSON response is wrapped in:

```json
{ "code": <int>, "msg": "<symbol>", "data": <any> }
```

- **`code == 0`** — success. `msg == "ok"`.
- **`code != 0`** — failure. `msg` is the registered symbolic name from `docs/error-codes.md` (e.g. `"setup_required"`, `"invalid_dsn"`). `data` MAY carry structured diagnostics (e.g. `{"latency_ms":42,"hint":"…"}`).
- **HTTP status semantics**:
  - **Business errors** — `200 OK` with a non-zero `code`. Example: DSN probe can't reach the database. The envelope carries the business verdict.
  - **System errors** — `500 Internal Server Error` with `code` in the system range (`2900`–`2999` for 002, `1900`–`1999` for 001). Example: config write failed.
  - **Setup-gate refusals** — `/api/admin/*` during setup-pending returns `200` with envelope `code=2011 setup_required`.
  - **`/v1/*` stays outside the envelope**. During setup-pending the gate returns `503` with 001's native error shape (NOT the envelope). In setup-done state, provider responses are never wrapped; API-key accounts preserve 002 Platform-compatible forwarding, while OAuth accounts use Feature 003's ChatGPT Codex backend transport for `/v1/responses` and `/v1/responses/compact`.

The envelope lives in `internal/api/envelope.go` (`api.WriteOK`, `api.WriteBizErr`, `api.WriteSysErr`).

### #9 — RPC-Verb Route Convention for `/api/admin/*`

Mutations are modelled as verbs, not resources:

| Intent | Path |
|---|---|
| Disable an account | `POST /api/admin/accounts/{id}/disable` |
| Enable an account | `POST /api/admin/accounts/{id}/enable` |
| Delete an account | `POST /api/admin/accounts/{id}/delete` |
| Partial settings update | `POST /api/admin/settings/update` |
| Commit the wizard | `POST /api/admin/../setup/commit` |

Reads stay REST-shaped (`GET /api/admin/settings`, `GET /api/admin/accounts/{id}`).

## Consequences

### Benefits

- **Single parse path on the client** — the SPA's `api-client.ts` has one `unwrap` routine; every route hook is typed as `Promise<T>` (success) or throws `RouterApiError{code,msg,data}` (failure). No branching on HTTP status.
- **Observability** — every envelope response carries an `X-Request-Id` that appears on the corresponding server-side log record (plus any downstream dependency logs). Operators can trace a single wizard click across the stack.
- **Plain-HTTP inspection remains cheap** — `curl /api/admin/health | jq '.data.status'` always works regardless of success/failure; no need to teach operators "look at both code AND http status".
- **Intent legibility** — `POST /api/admin/accounts/{id}/disable` reads like an RPC, not "I'm patching an account with status=disabled". This matches how 001 already modelled those endpoints, so the naming is consistent across the control plane.

### Costs

- **001 admin callers must update paths** — `/admin/health` → `/api/admin/health`, plus the envelope unwrap. The 001 tech-design changelog documents both changes. The `/v1/*` envelope exclusion is unaffected; Feature 003 separately defines account-specific OAuth upstream transport.
- **Two parallel JSON conventions in the binary** — envelope (admin + setup) and native (proxy). This is intentional, but reviewers must know which is which. The envelope helpers are gated in `internal/api/` and the proxy error shape is in `internal/api/errors.go`.
- **No PATCH support** — partial updates are expressed via a dedicated `POST /<resource>/update` endpoint that takes a partial-patch body. This trades RESTful purity for a clearer "this is an operator verb" model.

## Alternatives considered

1. **REST + HTTP status** — rejected: doubles the error-classification pathway on the client (parse HTTP status AND parse native JSON error shape) and loses the correlation id guarantee.
2. **GraphQL for admin** — rejected: adds a schema + resolver layer the MVP doesn't need, and the SPA's current scope (3 pages) does not benefit from graph fetching.
3. **gRPC-web** — rejected: same complexity argument as GraphQL; plus we would need a separate envelope for error signalling anyway.

## Appendix — cross-references

- Helper API: `api.WriteOK(w, reqID, data)`, `api.WriteBizErr(w, reqID, code, msg, data)`, `api.WriteSysErr(w, reqID, code, msg)`.
- Error code registry: `docs/error-codes.md`.
- Envelope contract tests: `internal/api/envelope_test.go`, `internal/api/adminapi/settings_test.go`, `internal/api/setup/handler_test.go`.
- `/v1/*` exclusion tests: `internal/setup/gate_test.go` (asserts 503 native on /v1/* during setup-pending).
