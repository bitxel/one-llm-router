/**
 * `router-api.ts` — envelope-aware wrapper over the generated
 * `@hey-api/openapi-ts` admin SDK (feature 003+).
 *
 * Why this file exists
 * --------------------
 *
 * The router's universal response contract is `{ code, msg, data }`
 * at HTTP 200 (business successes AND business errors) or at HTTP
 * 500 (system errors — panic recovery, DB down, handler bug). See
 * AGENTS.md §"HTTP API Style (router-owned endpoints)" rules 1–3
 * and `docs/error-codes.md`.
 *
 * The generator (`@hey-api/openapi-ts`) has no built-in concept of
 * "one-envelope-for-everything"; it emits a tuple
 *   `{ data: TBody, error: undefined } | { data: undefined, error: TErr }`
 * discriminated purely by `Response.ok`. At HTTP 200 — which is
 * where BUSINESS errors live — it always populates `data` regardless
 * of the inner `code` value. So a raw SDK consumer would see
 *
 *     const { data } = await oauthBrowserStart({ ... })
 *     data?.flow_id // (union narrows on `code: 0`) — verbose at every call site
 *
 * every single call site. This wrapper unwraps the envelope ONCE
 * and pattern-matches `code === 0` for the caller, mirroring the
 * hand-authored 002 `apiFetch` in `api-client.ts` so UI code that
 * crosses the 002/003 boundary behaves identically.
 *
 * What it does
 * ------------
 *
 *   - Reads the server-echoed `X-Request-Id` off the `Response`
 *     (falls back to the outbound id so every thrown error carries
 *     a correlation token — FR-010).
 *   - On HTTP 500 → throws `RouterApiError` with
 *     `status=500, code=env.code, msg=env.msg, data=env.data`.
 *   - On HTTP 200:
 *       * if the response carries
 *         `Content-Disposition: attachment` (the documented
 *         raw-bytes exemption — only
 *         `POST /accounts/{id}/export-auth-json` uses this):
 *         returns the body as-is (a `CodexAuthJson`).
 *       * if body is `{ code, msg, data }` with `code === 0`:
 *         returns `env.data` (the inner payload).
 *       * if body is `{ code, msg, data }` with `code !== 0`:
 *         throws `RouterApiError(code, msg, data, status=200)`.
 *       * otherwise (non-envelope body without an attachment
 *         header — e.g. Cloudflare HTML error page, upstream
 *         interstitial, programmer-level server bug): throws
 *         `RouterApiError(code=-1, msg="malformed_envelope")`.
 *         NEVER silently returns a non-envelope body — per Codex
 *         review feedback, doing so hides origin-identity spoofing
 *         and backend contract drift behind a successful-looking
 *         `await`.
 *   - Transport failures (`TypeError` from `fetch`), JSON parse
 *     errors (invalid UTF-8, truncated body), and abort signals
 *     — all thrown SYNCHRONOUSLY from within the generated SDK —
 *     are caught and normalised to `RouterApiError(code=-1,
 *     msg="transport_error")`. Callers never see a raw `TypeError`
 *     or `SyntaxError`.
 *
 * Traceability
 * ------------
 *
 *   - `data-model.md` §"OAuth state transitions" — business error
 *     codes 3001–3016 are all HTTP 200.
 *   - `docs/error-codes.md` — authoritative registry.
 *   - `internal/generated/adminapi/strict_handler.go` — Go-side
 *     counterpart that SERIALISES the same envelope; this file is
 *     its mirror image (deserialise + raise).
 *
 * Non-goals (handled elsewhere)
 * -----------------------------
 *
 *   - Retry / backoff — TanStack Query layer.
 *   - Caching — TanStack Query.
 *   - Admin-auth token refresh — admin_auth plugin (004+).
 *   - Base URL resolution — `openapi-runtime.ts`.
 *   - Fresh `X-Request-Id` on every call — `openapi-runtime.ts`
 *     interceptor (the generator attaches the header BEFORE this
 *     wrapper sees the Response; this wrapper only READS it).
 */

import { PlatformUnknown } from './errcode'
import { REQUEST_ID_HEADER } from './request-id'
import { RouterApiError } from './router-api-error'

export { isRouterApiError, RouterApiError } from './router-api-error'

/**
 * Shape of the universal router envelope at the TypeScript type
 * level. The generated SDK emits a STRUCTURALLY equivalent type
 * per operation — we accept anything that matches this shape so the
 * wrapper stays SDK-version-independent.
 */
interface EnvelopeShape {
  code: number
  msg: string
  data: unknown
}

export interface AdminAttachmentPayload {
  blob: Blob
  filename: string
}

function isEnvelope(value: unknown): value is EnvelopeShape {
  if (typeof value !== 'object' || value === null) return false
  const v = value as Record<string, unknown>
  return typeof v.code === 'number' && typeof v.msg === 'string' && 'data' in v
}

function envelopeFromUnknown(value: unknown): EnvelopeShape | null {
  if (isEnvelope(value)) {
    return value
  }
  if (typeof value !== 'string') {
    return null
  }
  try {
    const parsed = JSON.parse(value) as unknown
    return isEnvelope(parsed) ? parsed : null
  } catch {
    return null
  }
}

/**
 * Sniffs whether a `Response` is a `POST
 * /accounts/{id}/export-auth-json` success body (raw `CodexAuthJson`
 * bytes). Per contract, success is discriminated by
 * `Content-Disposition: attachment; filename="auth.json"` (see
 * `openapi/admin.yaml` and
 * `internal/generated/adminapi/export_visit.go`). No other admin
 * endpoint emits an attachment header, so a pure-structural header
 * check is both sufficient and future-proof.
 */
function isAttachmentResponse(response: Response): boolean {
  const cd = response.headers.get('Content-Disposition')
  if (!cd) return false
  return /(^|;\s*)attachment\b/i.test(cd)
}

function attachmentFilename(response: Response): string {
  const cd = response.headers.get('Content-Disposition')
  if (!cd) return 'auth.json'
  const quoted = cd.match(/filename="([^"]+)"/i)
  if (quoted?.[1]) return quoted[1]
  const bare = cd.match(/filename=([^;]+)/i)
  if (bare?.[1]) return bare[1].trim()
  return 'auth.json'
}

/**
 * Shape of the result emitted by a `@hey-api/openapi-ts` SDK
 * invocation in the default `responseStyle: 'fields'` +
 * `throwOnError: false` mode. We re-declare it locally because:
 *
 *   - the runtime ClientOptions type from the generated tree is
 *     versioned (the union includes the `baseUrl` literal set),
 *     which we don't want leaking into our wrapper's signature.
 *   - the emitter version may change; this minimal surface is
 *     the narrowest contract we need.
 */
type SdkResult<TBody> =
  | {
      data: TBody
      error: undefined
      request: Request
      response: Response
    }
  | {
      data: undefined
      error: unknown
      request: Request
      response: Response
    }

/**
 * Distributively remove non-zero-code envelope members from a union,
 * leaving only: (a) zero-code envelope variants, and (b) non-envelope
 * success types (e.g. raw `CodexAuthJson` for the export endpoint).
 *
 * Examples:
 *   - `BrowserStartResponseBody`
 *       = BrowserStartEnvelope (code:0)
 *       | FlowInProgressEnvelope (code:3001)
 *       | InvalidOAuthProviderEnvelope (code:3002)
 *     ⇒ BrowserStartEnvelope
 *   - `ExportAuthJsonResponseBody`
 *       = CodexAuthJson (no code)
 *       | AccountNotFoundEnvelope (code:1001)
 *       | NotOAuthAccountEnvelope (code:3014)
 *     ⇒ CodexAuthJson
 *   - `FlowStatusEnvelope` (code:0, data: status-union)
 *     ⇒ itself (passes through)
 */
type ExcludeBizError<T> = T extends { code?: infer C }
  ? Extract<C, 0> extends never
    ? never
    : T
  : T

/**
 * Compute the success payload type for an envelope union.
 *
 *   - For zero-code envelopes, returns `data` (the inner payload).
 *     The `Exclude<_, undefined>` strip deals with the generator
 *     emitting every property as `?:` — `code: 0` + `data?: X`
 *     ⇒ `data: X | undefined` after EnvelopeBase intersection,
 *     which we narrow because AT RUNTIME the server always sets
 *     `data`.
 *   - For non-envelope successes (the raw-bytes export path),
 *     returns the whole body as-is — the caller gets a
 *     `CodexAuthJson`, not `never`.
 *   - `null` is preserved (it's a valid JSON value).
 */
export type SuccessPayload<TBody> = TBody extends { 200: infer Body }
  ? SuccessPayloadBody<Body>
  : SuccessPayloadBody<TBody>

type SuccessPayloadBody<TBody> = TBody extends unknown
  ? ExcludeBizError<TBody> extends never
    ? never
    : NonNullable<ExcludeBizError<TBody>> extends {
          data?: infer D
        }
      ? Exclude<D, undefined>
      : NonNullable<ExcludeBizError<TBody>>
  : never

interface SdkResultMeta<TBody> {
  result: SdkResult<TBody>
  requestId: string | null
  response: Response
  status: number
}

function transportError(cause: unknown): RouterApiError {
  return new RouterApiError({
    code: PlatformUnknown,
    msg: 'transport_error',
    data: { cause: cause instanceof Error ? cause.message : String(cause) },
    requestId: null,
    status: 0,
  })
}

function malformedEnvelopeError(
  requestId: string | null,
  status: number,
  body: unknown,
): RouterApiError {
  return new RouterApiError({
    code: PlatformUnknown,
    msg: 'malformed_envelope',
    data: body,
    requestId,
    status,
  })
}

async function resolveSdkResult<TBody>(
  call: Promise<SdkResult<TBody>>,
): Promise<SdkResultMeta<TBody>> {
  let result: SdkResult<TBody>
  try {
    result = await call
  } catch (err) {
    throw transportError(err)
  }

  const { response, request } = result
  if (!response || typeof response.status !== 'number') {
    throw transportError('sdk_result_missing_response')
  }

  return {
    result,
    response,
    requestId:
      response.headers.get(REQUEST_ID_HEADER) ?? request?.headers?.get?.(REQUEST_ID_HEADER) ?? null,
    status: response.status,
  }
}

function routerApiErrorFromEnvelope(
  value: unknown,
  requestId: string | null,
  status: number,
): RouterApiError | null {
  const env = envelopeFromUnknown(value)
  if (!env) {
    return null
  }
  return new RouterApiError({
    code: env.code,
    msg: env.msg,
    data: env.data,
    requestId,
    status,
  })
}

/**
 * Unwrap an in-flight admin-API SDK call.
 *
 * Usage:
 *
 *     const data = await callAdmin(
 *       oauthBrowserStart({ body: { provider: 'openai' } }),
 *     )
 *     // `data` is the zero-code envelope's `data` payload, e.g.
 *     // `{ flow_id, authorize_url, expires_at }`.
 *
 * Throws `RouterApiError` on:
 *   - HTTP 500 system error (any code in -1 / 1900 / 3900 / 3901 /
 *     3902 per the 003 contract).
 *   - HTTP 200 with non-zero business code (3001–3016, 2xxx, 1xxx).
 *   - HTTP 200 with a body that is neither an envelope nor a
 *     documented raw-attachment response (code `-1`,
 *     `msg="malformed_envelope"`, `data=body`).
 *   - Transport failures / JSON parse errors propagated from the
 *     generated SDK (code `-1`, `msg="transport_error"`,
 *     `data={ cause: String(error) }`).
 *
 * The `request` and `response` are DROPPED — call sites that need
 * the `Response` (e.g. the `auth.json` download button that wants
 * to blob the body) should use the generated SDK directly. This
 * wrapper is the happy-path ergonomics layer, not a kitchen sink.
 */
export async function callAdmin<TBody>(
  call: Promise<SdkResult<TBody>>,
): Promise<SuccessPayload<TBody>> {
  const { result, response, requestId, status } = await resolveSdkResult(call)

  if (result.error !== undefined) {
    const err = routerApiErrorFromEnvelope(result.error, requestId, status)
    if (err) {
      throw err
    }
    throw malformedEnvelopeError(requestId, status, result.error)
  }

  const body = result.data

  // Raw-bytes exemption (export-auth-json only). Gated by the
  // server-emitted `Content-Disposition: attachment` header —
  // purely structural detection avoids hard-coding the operation
  // path and keeps this wrapper future-proof if another raw-body
  // endpoint is ever spec'd. ANY other non-envelope HTTP 200 body
  // is rejected below (treating a Cloudflare HTML error page as
  // success would hide real failures from the UI).
  if (isAttachmentResponse(response)) {
    return body as SuccessPayload<TBody>
  }

  if (!isEnvelope(body)) {
    throw malformedEnvelopeError(requestId, status, body)
  }

  if (body.code === 0) {
    return body.data as SuccessPayload<TBody>
  }

  throw new RouterApiError({
    code: body.code,
    msg: body.msg,
    data: body.data,
    requestId,
    status,
  })
}

/**
 * Unwrap an admin SDK call whose SUCCESS branch is a raw attachment
 * but whose error branches still use the normal envelope.
 *
 * Callers SHOULD request the operation with `parseAs: 'text'` so the
 * raw JSON bytes can be written into a Blob without first touching
 * React state as a parsed object. The helper still accepts a parsed
 * object defensively and falls back to `JSON.stringify(...)`.
 */
export async function callAdminAttachment(
  call: Promise<SdkResult<string | Blob | ArrayBuffer | unknown>>,
): Promise<AdminAttachmentPayload> {
  const { result, response, requestId, status } = await resolveSdkResult(call)

  if (result.error !== undefined) {
    const err = routerApiErrorFromEnvelope(result.error, requestId, status)
    if (err) {
      throw err
    }
    throw malformedEnvelopeError(requestId, status, result.error)
  }

  if (!isAttachmentResponse(response)) {
    const err = routerApiErrorFromEnvelope(result.data, requestId, status)
    if (err) {
      throw err
    }
    throw malformedEnvelopeError(requestId, status, result.data)
  }

  let blob: Blob
  switch (true) {
    case result.data instanceof Blob:
      blob = result.data
      break
    case result.data instanceof ArrayBuffer:
      blob = new Blob([result.data], {
        type: response.headers.get('Content-Type') ?? 'application/json; charset=utf-8',
      })
      break
    case typeof result.data === 'string':
      blob = new Blob([result.data], {
        type: response.headers.get('Content-Type') ?? 'application/json; charset=utf-8',
      })
      break
    default:
      blob = new Blob([JSON.stringify(result.data)], {
        type: response.headers.get('Content-Type') ?? 'application/json; charset=utf-8',
      })
      break
  }

  return {
    blob,
    filename: attachmentFilename(response),
  }
}
