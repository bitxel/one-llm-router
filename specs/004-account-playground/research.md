# Research: Account Playground

**Feature**: 004-account-playground
**Spec**: `specs/004-account-playground/spec.md`
**Created**: 2026-04-23
**Status**: Ready

The feature uses established project patterns. No new Go or npm dependencies are required. Research focused on the boundaries that affect correctness: admin API envelope, OpenAPI coverage, account selection, provider request shape, raw response handling, and the feature-number/error-code conflict left by earlier roadmap text.

---

## Decision 1: Playground is an Admin API operation, not a browser call to the data plane

- **Decision**: The browser calls a new router-owned Admin API operation for Playground runs. It never calls the provider-compatible data-plane route directly.
- **Rationale**:
  - The data-plane route is explicitly excluded from the admin envelope and must preserve provider-compatible wire format for clients.
  - Explicit account selection is router-owned control-plane behavior. Adding account-selection hints to data-plane requests would either leak internal headers upstream or require special stripping rules in the data-plane provider transport path.
  - The browser must never receive API keys, OAuth access tokens, refresh tokens, ID tokens, or authorization headers.
  - Admin API gives the operator a stable envelope, error code, and request-id handling model consistent with the rest of the Admin Portal.
- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |-------------|------|------|--------------|
  | Browser calls `/v1/responses` directly | Minimal backend code | No admin envelope, no explicit account selection, special error parsing, and no safe control over provider-compatible contract | Violates data-plane boundary |
  | Add `X-Router-Account-Id` to `/v1/*` | Reuses proxy | Custom header could be forwarded upstream unless the proxy grows route-specific stripping; changes client-facing semantics | Breaks data-plane facade discipline |
  | Add a generic arbitrary upstream proxy in Admin Portal | Flexible | Scope explosion and unsafe SSRF-like surface | Spec says account debugging only |

---

## Decision 2: P0 model surface is OpenAI Responses, non-streaming

- **Decision**: P0 builds a single-turn non-streaming text probe against the router's Responses-shaped `/v1/responses` surface: model plus text input, with `stream=false`. The provider adapter still chooses the account-specific upstream transport: API-key accounts use the OpenAI Platform-compatible `/v1/responses` path, while OAuth ChatGPT accounts use the ChatGPT Codex backend `/codex/responses` path with `chatgpt-account-id` and `User-Agent: codex_cli_rs/0.120.0 (Mac OS 26.2; arm64) dumb`.
- **Rationale**:
  - The router currently serves Codex-style traffic through `/v1/responses`; using the same provider operation tests the account path operators care about.
  - The OpenAI Responses API accepts text input and a model, and streaming is explicitly an optional mode. The feature spec asks the operator to wait for a reply, not to watch live tokens.
  - Non-streaming results fit the Admin API envelope. Streaming would require a separate exemption or a job/status model.
- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |-------------|------|------|--------------|
  | Chat Completions | Familiar legacy shape | Codex/router path is Responses-oriented; new projects are recommended to use Responses | Tests the wrong path for this router |
  | Streaming in P0 | Better for long outputs | Conflicts with the Admin API envelope and adds cancellation/reconnect state | Deferred |
  | Provider-specific custom forms | More power | Expands beyond account health debugging | Out of scope |

Reference checked 2026-04-23: OpenAI Responses create API and streaming guide document text input and optional SSE streaming.

---

## Decision 3: Explicit account selection reuses the selector credential path, but does not bypass eligibility

- **Decision**: Automatic mode uses the existing eligible-account selector. Account mode selects one account by id, verifies it is active/eligible, and then uses the same credential-preparation path as normal routed traffic.
- **Rationale**:
  - The feature must test "can this account be used by the router", not "can this database row be forced through an upstream request".
  - OAuth accounts need the same refresh-before-use semantics as production traffic.
  - Silent fallback would hide the account-specific problem the operator is trying to debug.
- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |-------------|------|------|--------------|
  | Let Account mode run disabled accounts | Deeper debugging | Bypasses router safety policy and can surprise operators | Spec forbids bypassing eligibility |
  | Duplicate credential logic in the handler | Fast to write | High drift risk from proxy/OAuth refresh path | Reuse selector/pre-forward path |
  | Always auto-select even after explicit account becomes invalid | More likely to get a response | Hides the selected account failure | Violates US-2 |

---

## Decision 4: Safe raw JSON is bounded and optional in display, not a second source of truth

- **Decision**: The run result includes parsed output text when available and a bounded safe raw JSON payload when available. If text cannot be extracted, the result explicitly marks parsed output as unavailable. If raw JSON is too large or malformed, the result says why it was omitted.
- **Rationale**:
  - Operators need enough diagnostic detail to debug provider/account issues.
  - The feature must never invent output text.
  - Raw provider payloads may include operator-entered prompt/output, so persistence continues to follow runtime body-logging policy.
- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |-------------|------|------|--------------|
  | Always hide raw JSON | Simple UI | Harder to debug malformed or no-text responses | Too little diagnostic value |
  | Always return unlimited raw JSON | Maximum fidelity | Unbounded memory/UI risk; can expose too much operator content | Unsafe |
  | Parse text only and drop raw data | Compact | Makes parser drift impossible to inspect | Weak debugging surface |

---

## Decision 5: Request history uses the existing request-record model

- **Decision**: Playground runs are recorded through the existing request-record path using the Admin API path as the distinguishing source. No schema migration is introduced.
- **Rationale**:
  - The existing request record already has request id, account id, method, path, status, latency, outcome, error code, model, model params, response mode, token usage, and optional bodies.
  - The feature only needs to distinguish Playground-originated runs from client traffic; the path `/api/admin/playground/run` is sufficient and queryable.
  - Adding a new `origin` column would be a migration for a display/filter convenience, not a requirement.
- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |-------------|------|------|--------------|
  | Add `origin=playground` column | Explicit filtering | Requires migration and touches request list contracts | Not needed for P0 |
  | Do not record Playground runs | Less data | Violates observability requirements | Not acceptable |
  | Store full run history separately | Rich UX | Turns the feature into saved prompt history | Out of scope |

---

## Decision 6: Feature 004 owns the 4000 range for Account Playground

- **Decision**: Because this feature is now `004-account-playground` by SDD directory order and state, the `4000-4999` registry range becomes Account Playground. Earlier roadmap text that named "Feature 004 — Admin auth + Client API keys + routing policy" must be updated during implementation to point to a future feature instead of feature 004.
- **Rationale**:
  - The current SDD state is the workflow source of truth for this feature.
  - Reusing 4xxx for a different unstarted feature would make contract tests and frontend constants ambiguous.
  - The admin-auth OpenAPI extension can keep the aspirational auth shape without claiming feature number 004.
- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |-------------|------|------|--------------|
  | Renumber Playground to 005 | Preserves old prose | Conflicts with SDD "next number" flow and existing generated spec directory | User asked to continue this new spec |
  | Put Playground errors in 5xxx | Avoids 4xxx text | Breaks feature-range partitioning and collides with observability reservation | Inconsistent |
  | Reuse 001/003 codes only | Fewer registry updates | Playground-specific failures become indistinct | Weak operator diagnostics |

---

## Decision 7: No new dependencies

- **Decision**: Use the existing Go and frontend stacks. No new Go module and no new npm package is required for planning or implementation.
- **Rationale**:
  - Backend can reuse `net/http`, existing `openai.Client`, `AccountSelector`, `RequestRecorder`, OpenAPI codegen, and envelope helpers.
  - Frontend can reuse React, TanStack Router/Query, existing generated SDK wrapper, existing hand-authored `api-client` for inherited account list routes, and existing UI primitives.
  - Adding a provider SDK would duplicate the router's transparent forwarding behavior and risk version drift from the data-plane implementation.
- **Alternatives Considered**:

  | Alternative | Pros | Cons | Why Rejected |
  |-------------|------|------|--------------|
  | Add official/provider SDK | Higher-level API | Duplicates existing forwarder, adds dependency, and hides wire details we need for diagnostics | Not needed |
  | Add form/state library | Convenience | Existing stack already has React Hook Form/Zod/TanStack Query | Existing stack sufficient |
  | Add streaming helper library | Future streaming support | P0 is non-streaming | Out of scope |

## Dependency Versions (Verified 2026-04-23)

No dependency versions change in this feature. Versions below are verified from `go list -m`, `go.mod`, and `frontend/package.json`; implementation must not float them while landing Playground.

| Package | Verified Version | Source | Notes |
|---------|------------------|--------|-------|
| Go | 1.25.0 | `go.mod` | Project toolchain directive |
| `xorm.io/xorm` | v1.3.11 | `go list -m` | Existing ORM |
| `github.com/golang-migrate/migrate/v4` | v4.19.1 | `go list -m` | Existing migration tool; no migration expected |
| `modernc.org/sqlite` | v1.48.2 | `go list -m` | Existing SQLite driver |
| `github.com/oapi-codegen/oapi-codegen/v2` | v2.6.0 | `go list -m` | Existing Go OpenAPI codegen |
| `github.com/oapi-codegen/runtime` | v1.4.0 | `go list -m` | Existing OpenAPI runtime |
| `github.com/stretchr/testify` | v1.11.1 | `go list -m` | Existing Go test helper |
| `golang.org/x/sync` | v0.19.0 | `go list -m` | Existing OAuth refresh dependency; no new use beyond current code path |
| `react` | ^19.1.0 | `frontend/package.json` | Existing frontend runtime |
| `react-dom` | ^19.1.0 | `frontend/package.json` | Existing frontend runtime |
| `@tanstack/react-router` | ^1.121.0 | `frontend/package.json` | Existing routing |
| `@tanstack/react-query` | ^5.71.0 | `frontend/package.json` | Existing server-state management |
| `@hey-api/openapi-ts` | 0.96.1 | `frontend/package.json` | Exact-pinned TS client generator |
| `typescript` | ~5.8.2 | `frontend/package.json` | Existing type checker |
| `vite` | ^6.2.5 | `frontend/package.json` | Existing build tool |
| `vitest` | ^3.1.1 | `frontend/package.json` | Existing unit test runner |
| `@playwright/test` | ^1.51.1 | `frontend/package.json` | Existing E2E runner |
| `@biomejs/biome` | ^2.0.0-beta.5 | `frontend/package.json` | Existing lint/format tool |
