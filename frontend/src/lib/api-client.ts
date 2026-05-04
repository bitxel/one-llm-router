/**
 * API client — typed fetch wrapper over the {code, msg, data}
 * envelope shared by `/api/setup/*` and `/api/admin/*` endpoints.
 *
 * Responsibilities:
 *
 *   - Serialise the request body as JSON with a sensible `Content-
 *     Type`.
 *   - Propagate and expose the `X-Request-Id` correlation header.
 *   - Unwrap the envelope so happy-path callers get `data` directly.
 *   - Surface non-zero `code` values as typed `RouterApiError` so
 *     UI layers can pattern-match on integer codes (no string
 *     parsing).
 *
 * The client is intentionally thin. It does NOT:
 *   - Retry (that is the mutation layer's concern — see
 *     use-settings.ts).
 *   - Cache (TanStack Query owns that).
 *   - Auth-refresh tokens (plugins 003 will add that).
 */

import { PlatformUnknown } from './errcode'
import { generateRequestId, REQUEST_ID_HEADER } from './request-id'
import { RouterApiError } from './router-api-error'

export { RouterApiError } from './router-api-error'

export interface EnvelopeSuccess<T> {
  code: 0
  msg: string
  data: T
  requestId: string | null
}

export interface EnvelopeError {
  code: number
  msg: string
  data: unknown
  requestId: string | null
}

export interface ApiRequestInit extends Omit<RequestInit, 'body'> {
  body?: unknown
}

const DEFAULT_HEADERS: HeadersInit = {
  'Content-Type': 'application/json',
  Accept: 'application/json',
}

/**
 * Issue a request and unwrap the envelope. Resolves with `data` on
 * `code==0`; otherwise rejects with `RouterApiError`.
 *
 * The correlation-id generator is shared with the generated admin
 * SDK runtime (`src/lib/openapi-runtime.ts`) via
 * `src/lib/request-id.ts`, so every outbound call — setup or admin,
 * hand-authored or generated — emits a header from the same source.
 */
export async function apiFetch<T>(path: string, init: ApiRequestInit = {}): Promise<T> {
  const { body, headers, ...rest } = init
  // Merge headers so callers can override X-Request-Id (e.g. tests
  // pinning a deterministic id) but default to a fresh client-side id
  // per FR-010.
  const mergedHeaders: Record<string, string> = {
    ...(DEFAULT_HEADERS as Record<string, string>),
    [REQUEST_ID_HEADER]: generateRequestId(),
    ...((headers as Record<string, string> | undefined) ?? {}),
  }
  const res = await fetch(path, {
    ...rest,
    headers: mergedHeaders,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })

  // Prefer server-echoed id over the outbound one: the request
  // middleware echoes whatever we sent, so this is effectively the
  // same value, but if an upstream intermediary rewrote the header
  // we want the value the server logged, not the one we sent.
  const requestId = res.headers.get(REQUEST_ID_HEADER) ?? mergedHeaders[REQUEST_ID_HEADER] ?? null
  const status = res.status

  let parsed: unknown
  try {
    parsed = await res.json()
  } catch {
    throw new RouterApiError({
      code: PlatformUnknown,
      msg: `non_json_response: HTTP ${status}`,
      data: null,
      requestId,
      status,
    })
  }

  if (!isEnvelope(parsed)) {
    throw new RouterApiError({
      code: PlatformUnknown,
      msg: 'malformed_envelope',
      data: parsed,
      requestId,
      status,
    })
  }

  if (parsed.code === 0) {
    return parsed.data as T
  }

  throw new RouterApiError({
    code: parsed.code,
    msg: parsed.msg,
    data: parsed.data,
    requestId,
    status,
  })
}

export const api = {
  get<T>(path: string, init?: ApiRequestInit): Promise<T> {
    return apiFetch<T>(path, { ...init, method: 'GET' })
  },
  post<T>(path: string, body: unknown, init?: ApiRequestInit): Promise<T> {
    return apiFetch<T>(path, { ...init, method: 'POST', body })
  },
}

interface EnvelopeShape {
  code: number
  msg: string
  data: unknown
}

function isEnvelope(value: unknown): value is EnvelopeShape {
  if (typeof value !== 'object' || value === null) return false
  const v = value as Record<string, unknown>
  return typeof v.code === 'number' && typeof v.msg === 'string' && 'data' in v
}
