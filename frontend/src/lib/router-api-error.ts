/**
 * `RouterApiError` — the single error type thrown by EVERY
 * router-admin fetch wrapper in the SPA.
 *
 * Why this is a dedicated module
 * ------------------------------
 *
 * Feature 002 (`api-client.ts`, setup wizard + admin portal
 * skeleton) and Feature 003 (`router-api.ts`, envelope-aware
 * wrapper over the generated `@hey-api/openapi-ts` SDK) both raise
 * the same error shape: `{ code, msg, data, requestId, status }`.
 *
 * If each file defined its OWN class, `instanceof RouterApiError`
 * would split-decide based on which import path the catch-site used
 * — a UI component that imports from one wrapper but catches errors
 * from the other would silently fall through to `Error` handling
 * and lose the specialised request-id / error-code surfacing.
 * Consolidating here keeps every `instanceof` check correct no
 * matter which fetch wrapper produced the throw.
 *
 * `isRouterApiError` is provided as a structural fallback for code
 * paths that cross module-realm boundaries (e.g. MSW test shims,
 * HMR module reloads) where `instanceof` can yield false negatives.
 * Prefer `instanceof` in production code.
 */

export interface RouterApiErrorParams {
  code: number
  msg: string
  data: unknown
  requestId: string | null
  status: number
}

/**
 * Every non-success outcome from an admin or setup fetch wrapper —
 * HTTP 200 business errors, HTTP 500 system errors, malformed
 * envelopes, transport failures, and JSON parse errors — is
 * surfaced as an instance of this class.
 */
export class RouterApiError extends Error {
  readonly code: number
  readonly msg: string
  readonly data: unknown
  readonly requestId: string | null
  readonly status: number

  constructor(params: RouterApiErrorParams) {
    super(`${params.msg} (code=${params.code})`)
    this.name = 'RouterApiError'
    this.code = params.code
    this.msg = params.msg
    this.data = params.data
    this.requestId = params.requestId
    this.status = params.status
  }

  /**
   * 002 setup-gate convenience. Re-exported through `api-client.ts`
   * for legacy import sites; new code should prefer to compare
   * `err.code` against constants from `errcode.ts` directly.
   */
  isSetupRequired(): boolean {
    return this.code === 2011
  }
}

/**
 * Structural guard for `RouterApiError`. Prefer `instanceof` in
 * production; this fallback is useful when module graphs can emit
 * two copies of the same class (test HMR, dual-bundled JS packages).
 */
export function isRouterApiError(value: unknown): value is RouterApiError {
  if (value instanceof RouterApiError) return true
  if (typeof value !== 'object' || value === null) return false
  const v = value as Record<string, unknown>
  return (
    v.name === 'RouterApiError' &&
    typeof v.code === 'number' &&
    typeof v.msg === 'string' &&
    typeof v.status === 'number' &&
    'data' in v &&
    'requestId' in v
  )
}
