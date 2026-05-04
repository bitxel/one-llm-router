# OpenAPI specs (admin surface)

This directory hosts the OpenAPI 3.0.x specifications that are the **single
source of truth** for the router's admin APIs from feature 003 onwards,
per `AGENTS.md §API Contract` rule #1.

## Scope

| File | Status | Covers |
|---|---|---|
| [`admin.yaml`](./admin.yaml) | Authoritative (002 Settings, 003+) | Settings GET/update, Feature 003 admin endpoints — OAuth flow, accounts extensions (import/export), request-history list/detail/options, Account Playground, envelope schema. |
| 001 accounts/sessions admin endpoints | Deferred | Still tracked via Markdown contracts in `specs/001-codex-router-mvp/contracts/`. They will be folded into this OpenAPI set when a future admin-auth feature lands, because admin auth is the integration point that unifies their request/response envelope. |
| 002 setup wizard endpoints | Deferred | Markdown contracts in `specs/002-setup-wizard-and-admin-portal-skeleton/contracts/` remain authoritative until a future admin-auth feature rewrites them behind the envelope. Settings has already been migrated into `admin.yaml`. |

## Codegen pipeline

### Go server stubs — wired via T-006

`oapi-codegen` v2.6.0 emits into `internal/generated/adminapi/`.
Two sibling configs split the output so schema changes and
server-plumbing changes surface as independent diffs:

| Config | Output | Contents |
|---|---|---|
| `codegen-types.yaml`  | `internal/generated/adminapi/types.gen.go`  | Request/response/schema models (`models: true`). |
| `codegen-server.yaml` | `internal/generated/adminapi/server.gen.go` | net/http `ServerInterface` + strict-server RPC-style `StrictServerInterface` (`strict-server: true` + `std-http-server: true`). |

**Five hand-authored companion files** live alongside the generated
`.gen.go` pair (they are NEVER regenerated; the freshness gate
ignores them by design). Each one exists to close a specific
gap in the generator's default output — the gaps and the fixes
were locked in during the overall Codex review of T-006:

| File | Role |
|---|---|
| `internal/generated/adminapi/doc.go` | Package doc-comment header. |
| `internal/generated/adminapi/union_marshal.go` | Per-operation `MarshalJSON` shims that restore serialisation semantics lost by `type Op200JSONResponse Union` (Go does not inherit methods across `type X Y`). Required because `oapi-codegen` + strict-server + named oneOf components combine into a known Go-type-system sharp edge; see the file header for the full rationale. |
| `internal/generated/adminapi/union_smoke_test.go` | Branch-coverage regression test that locks both the union-unexported-field failure mode (compile-time) and the reflect-fallback `{}` failure mode (runtime) across every declared oneOf response body branch (63 branches including the five nested `FlowStatusEnvelope.data` variants). |
| `internal/generated/adminapi/strict_handler.go` | Blessed `HandlerFromMuxWithEnvelope` / `NewEnvelopeStrictHandler` constructors. Overrides `oapi-codegen`'s THREE default error paths (outer wrapper path/query parse, strict-layer request decode, strict-layer response error) with envelope-aware handlers that keep all failures compliant with `AGENTS.md §HTTP API Style` — HTTP 200 + `{"code":2008,"msg":"malformed_body","data":{}}` for generic request-side parse failures, HTTP 200 + `{"code":1006,"msg":"invalid_request_filter","data":{}}` for request-log query/path parse failures, HTTP 200 + `{"code":3010,"msg":"invalid_auth_json_structure","data":{}}` for the `import-auth-json` multipart route (contract-mandated), HTTP 500 + `{"code":-1,"msg":"unknown_error","data":{}}` for response-side handler bugs. Also installs the `contentTypeGate` middleware (rejects `text/plain` on JSON routes before decode) and the `charsetMiddleware` ResponseWriter wrapper (upgrades bare `application/json` to `application/json; charset=utf-8` per contract line 9). Paired with `strict_handler_test.go`. |
| `internal/generated/adminapi/export_visit.go` | Safe response wrappers for `POST /api/admin/accounts/{id}/export-auth-json`. Provides `ExportAuthJSONAttachmentResponse` (success branch — writes the contract header set `Content-Type: application/json; charset=utf-8`, `Cache-Control: no-store, private`, `Content-Disposition: attachment; filename="auth.json"` — filename is a hard-coded const, not caller-supplied, to close the header-injection surface) and `ExportAuthJSONEnvelopeResponse` (200-envelope error branches — writes NO attachment headers). Required because the generator's default wrapper emits attachment headers unconditionally, which poisons the error branches with empty-valued headers and defeats the client discriminator. Paired with `export_visit_test.go`. |

**OneOf response-body layout**: every admin operation whose 200
body is a discriminated union declares that union as a **named
component schema** under `components.schemas` (see admin.yaml
§"Operation response body unions"), NOT inline on the operation.
This is load-bearing — inline oneOf under `strict-server` generates
an unexported-field wrapper that the handler package cannot
construct. The TS client (T-007) will also pick up these named
schemas and emit them as named types on the frontend, so new
branches MUST be added to the existing named schema rather than
back inline on the operation.

Regenerate:

```bash
bash scripts/codegen-go.sh
```

Pin strategy:

- `tools/oapi-codegen.go` blank-imports `github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen` under the `tools` build tag, which keeps the version pinned in `go.mod` without compiling the tool into the router binary.
- The script invokes `go run <pinned path>`, never `go install @latest` — the version that ships with CI is exactly the version in `go.mod`.
- **Note**: tasks.md T-006 references the legacy `github.com/deepmap/oapi-codegen/v2` path; that repository moved to the `oapi-codegen` organisation at v2.3.0. We deliberately use the new canonical path — see the `tools/oapi-codegen.go` header for the full rationale.

### TypeScript client — wired via T-007

`@hey-api/openapi-ts` v0.96.1 emits into `frontend/src/generated/openapi/`
(never hand-edited; regeneration is a CI gate). Configuration lives in
[`frontend/openapi-ts.config.ts`](../frontend/openapi-ts.config.ts); the
script wrapper is [`scripts/codegen-frontend.sh`](../scripts/codegen-frontend.sh).

Regenerate:

```bash
bash scripts/codegen-frontend.sh
# or
cd frontend && pnpm openapi:generate
```

Output layout (emitted by `@hey-api/openapi-ts`):

| File | Role |
|---|---|
| `frontend/src/generated/openapi/types.gen.ts` | One TS type per `components.schemas` entry plus per-operation `*Data` / `*Responses` / `*Errors` tuples. |
| `frontend/src/generated/openapi/sdk.gen.ts` | Flat tree-shakeable SDK functions — one exported function per `operationId` (`oauthBrowserStart`, `oauthFlowStatus`, …). |
| `frontend/src/generated/openapi/client.gen.ts` | Client instance wired to the runtime config (see below). |
| `frontend/src/generated/openapi/client/` | Bundled `@hey-api/client-fetch` runtime (copy-in — removes the need for an external `@hey-api/client-fetch` dev-dependency). |
| `frontend/src/generated/openapi/core/` | Low-level helpers (body serialisers, path builder, SSE, query-key serialiser). |
| `frontend/src/generated/openapi/index.ts` | Re-export barrel for the SDK + types. |

**Three hand-authored companion files** live under `frontend/src/lib/`
and own the router-specific contract plumbing (envelope, request-id,
base-url). They are the TS mirror of the Go-side hand-authored
companions next to `*.gen.go` — same pattern, same invariant:

| File | Role |
|---|---|
| `frontend/src/lib/request-id.ts` | Shared `generateRequestId()` used by both `api-client.ts` (002 setup wizard / admin portal fetch wrapper) and `openapi-runtime.ts` (003 SDK runtime). One generator per SPA per FR-010. |
| `frontend/src/lib/openapi-runtime.ts` | Exports `createClientConfig` for `@hey-api/openapi-ts`'s `runtimeConfigPath` hook. Sets same-origin `baseUrl` and installs a `fetch` override that injects a fresh `X-Request-Id` on every outbound call (unless caller pinned one) and restores `Accept: application/json`. Paired with `openapi-runtime.test.ts`. |
| `frontend/src/lib/router-api.ts` | `callAdmin(sdkCallPromise)` — envelope-aware wrapper that unwraps `{code,msg,data}` on HTTP 200 + `code=0`, throws `RouterApiError` on HTTP 200 + non-zero business code (3001–3016), and throws on HTTP 500 + system-error envelope. Mirrors the Go side's `internal/generated/adminapi/strict_handler.go` on the SERIALISE axis; this file is the DESERIALISE / raise half. Paired with `router-api.test.ts`. |

**OneOf response-body layout** — same rule as the Go side: every admin
operation whose 200 body is a discriminated union declares that union
as a **named component schema** under `components.schemas`. The TS
generator emits these as a TypeScript union type (e.g.
`BrowserStartResponseBody = BrowserStartEnvelope | FlowInProgressEnvelope | InvalidOAuthProviderEnvelope`).
`callAdmin`'s `SuccessPayload<TBody>` conditional type automatically
filters out the non-`code:0` envelope members at compile time, so
caller code gets the narrowest `data` payload shape without manual
narrowing (see `router-api.ts` for the type gymnastics and why the
approach also supports the export-auth-json raw-bytes branch).

Pin strategy:

- `@hey-api/openapi-ts` is exact-pinned in `frontend/package.json`
  (`"0.96.1"`, no caret).
- The script invokes `pnpm exec openapi-ts`, never `npx` / `pnpm dlx`
  — the version that ships with CI is exactly the version in
  `pnpm-lock.yaml`.
- The fetch runtime (previously `@hey-api/client-fetch`) is
  **bundled inside `@hey-api/openapi-ts`** from v0.73.0 onward. No
  separate dev-dependency to pin.

### CI freshness gate — T-008

CI runs `scripts/check-openapi-freshness.sh` on every PR via the
`openapi-freshness` job in `.github/workflows/ci.yaml`. The script,
in order:

1. Runs `redocly lint openapi/admin.yaml --config openapi/redocly.yaml`
   to catch shape errors in the spec itself before codegen starts.
2. Runs `scripts/codegen-go.sh` (regenerates
   `internal/generated/adminapi/`).
3. Runs `scripts/codegen-frontend.sh` (regenerates
   `frontend/src/generated/openapi/`).
4. Runs `git diff --exit-code` scoped to the two generated subtrees;
   any drift aborts the job with a human-readable remediation
   message that prints the diff and the exact `git add …` command
   the author needs to run.

The `redocly` CLI is pinned as a workspace-local devDependency
(`@redocly/cli@1.34.10` exact) — same pin strategy as
`@hey-api/openapi-ts`. Inspection invocations skip `npx` / `pnpm
dlx` (registry-based) in favour of the binary under
`frontend/node_modules/.bin/redocly`, so CI stays hermetic and
offline-capable.

Local shortcut: `make check-openapi-freshness` runs the same script
with zero arguments, and the sibling targets `make codegen`,
`make codegen-go`, and `make codegen-frontend` are available as
bypass hatches when you want to regenerate without the lint+diff
ceremony.

## Non-negotiable rules (from AGENTS.md §API Contract)

1. This directory is the single source of truth for admin APIs from 003
   onwards.
2. Every API change updates the YAML + regenerates Go server + TS client
   in the **same PR** (once codegen is wired).
3. Generated code is committed; CI verifies freshness via
   `git diff --exit-code` after regeneration.
4. Data plane (`/v1/*`) is explicitly NOT in scope. Its client-facing
   JSON/SSE contract is documented in the feature specs instead of this
   Admin API OpenAPI file: API-key accounts keep the 002 Platform-compatible
   path, while OAuth accounts use Feature 003's ChatGPT Codex backend
   transport for Responses traffic.

## Cross-references

- Envelope policy → `docs/error-codes.md` §HTTP envelope policy.
- Error code registry → `docs/error-codes.md` §Feature 002 / §Feature 003 / §Feature 004.
- Endpoint prose (request/response shapes, rail semantics, observability)
  → `specs/002-setup-wizard-and-admin-portal-skeleton/contracts/admin-api.md`,
  `specs/003-multi-mode-codex-auth/contracts/oauth-flow-api.md`, and
  `specs/003-multi-mode-codex-auth/contracts/accounts-api.md`.
- Exemptions (browser-facing `GET /auth/callback` + raw-attachment success
  body of `POST /api/admin/accounts/{id}/export-auth-json`) are documented
  in the YAML under each operation's description but do NOT use the
  `Envelope` schema.
