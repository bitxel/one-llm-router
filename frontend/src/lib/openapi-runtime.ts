/**
 * Runtime configuration for the generated `@hey-api/openapi-ts`
 * fetch client (admin API — feature 003+).
 *
 * How this is wired:
 *
 *   - `frontend/openapi-ts.config.ts` registers this module via the
 *     `runtimeConfigPath` option on the `@hey-api/client-fetch`
 *     plugin. The generator then emits
 *     `src/generated/openapi/client.gen.ts` which imports
 *     `createClientConfig` from here, rather than using the
 *     generator's built-in defaults.
 *   - `src/lib/router-api.ts` (the hand-authored envelope-aware
 *     wrapper) calls `callAdmin(oauthFlowStatus, …)` which in turn
 *     invokes the generated SDK function — that SDK function uses
 *     the pre-configured client returned from the generated
 *     `client.gen.ts` using this `createClientConfig` as the seed.
 *
 * Why this module is HAND-AUTHORED (not under `src/generated/`):
 *
 *   - Generated code must stay regeneration-stable — we cannot add
 *     business logic (request-id injection, base-url pinning) there
 *     without breaking the `git diff --exit-code` freshness check
 *     that CI enforces per AGENTS.md §API Contract rule #3.
 *   - By isolating runtime config to one file under `src/lib/`, we
 *     can unit-test it with vitest (the generated SDK stays
 *     behaviour-free and only pushes bytes on the wire).
 *   - Mirrors the Go side: `internal/generated/adminapi/*.gen.go`
 *     emit types/wrappers, and `strict_handler.go` (hand-authored,
 *     same directory) owns envelope semantics. Symmetry makes the
 *     codegen pipeline easier to reason about.
 *
 * What this file DOES NOT do:
 *
 *   - It does NOT unwrap the `{code, msg, data}` envelope. That is
 *     the job of `router-api.ts` — the fetch client sees each
 *     response as opaque JSON and yields it upstream.
 *   - It does NOT decide request-id policy (override-vs-inject,
 *     response echo preference). The SDK-level interceptor attaches
 *     the header; the `router-api.ts` wrapper is responsible for
 *     reading the server-echoed id from the response and attaching
 *     it to any `RouterApiError` thrown.
 */

import type { CreateClientConfig } from '../generated/openapi/client.gen'
import { generateRequestId, REQUEST_ID_HEADER } from './request-id'

/**
 * Same-origin base URL for the admin API.
 *
 * The SPA is served from the Go binary via `go:embed` in production
 * and via Vite's dev server (which proxies `/api/*` to the backend)
 * in development, so an empty string — which yields browser-relative
 * URLs — is correct in BOTH modes.
 *
 * IMPORTANT: we OVERRIDE the generator's seeded baseUrl
 * unconditionally. The `@hey-api/openapi-ts` generator plucks
 * `servers[0].url` from `openapi/admin.yaml` (`http://localhost:8080`
 * at the time of writing) and bakes it into
 * `src/generated/openapi/client.gen.ts` as the initial config. That
 * value is CORRECT for OpenAPI tooling (Swagger UI, codegen'd SDKs
 * consumed cross-origin) but WRONG for the admin SPA:
 *
 *   - In production the SPA is served from the SAME origin as the
 *     API (Go binary + `go:embed`), so absolute URLs pinned at build
 *     time would force cross-origin requests with no same-origin
 *     cookie/CSRF protection.
 *   - In dev the Vite server proxies `/api/*` to `localhost:8080`,
 *     so a relative URL hits the proxy; an absolute `localhost:8080`
 *     bypasses the proxy and loses Vite's CORS-free tunnelling.
 *
 * Callers that genuinely need a non-same-origin base (Storybook,
 * integration-harness tests, external admin tool) use
 * `client.setConfig({ baseUrl: … })` or pass `baseUrl` in per-call
 * options — both override AFTER this seed runs, so the same-origin
 * default here is the safe floor.
 *
 * WE DO NOT INHERIT `import.meta.env.VITE_API_BASE_URL`: feature 002
 * ships on same-origin only (see `docs/platform-direction.md` §3)
 * and introducing an env-driven base-url here would allow
 * cross-origin calls that bypass the setup-gate middleware. If a
 * future feature needs cross-origin, it must re-document FR-010
 * request-id echoing and CSRF posture before flipping the default.
 */
const ADMIN_API_BASE_URL = ''

export const createClientConfig: CreateClientConfig = (config) => ({
  ...config,
  // Unconditional override — see the ADMIN_API_BASE_URL doc comment
  // for why we ignore `config.baseUrl` (the generator seeds it from
  // the OpenAPI `servers[0]` which is wrong for same-origin SPAs).
  baseUrl: ADMIN_API_BASE_URL,
  // `fetch` override: inject a fresh `X-Request-Id` on every call
  // (FR-010) unless the caller explicitly set one. We can't use a
  // static `headers` default here because the generator would fix
  // the id at module-load time; we need a fresh uuid per call.
  //
  // The wrapper respects Request objects and string/url inputs. A
  // passed-in `Request` is cloned with an augmented `Headers` because
  // `Request.headers` is frozen — this is the same pattern used by
  // MSW/jsdom harnesses and is supported on all evergreen browsers
  // plus Node 22 LTS.
  fetch: (input, init) => {
    const headers = new Headers(
      init?.headers ?? (input instanceof Request ? input.headers : undefined),
    )
    if (!headers.has(REQUEST_ID_HEADER)) {
      headers.set(REQUEST_ID_HEADER, generateRequestId())
    }
    // Preserve `Accept: application/json` — the generator already
    // emits it, but if a call-site stripped headers in an
    // interceptor we restore the contract.
    if (!headers.has('Accept')) {
      headers.set('Accept', 'application/json')
    }
    const mergedInit: RequestInit = { ...init, headers }
    return (config?.fetch ?? globalThis.fetch)(input, mergedInit)
  },
})
