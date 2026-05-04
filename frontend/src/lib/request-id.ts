/**
 * `X-Request-Id` generator — the single source of truth for
 * client-originated correlation ids across:
 *
 *   - `src/lib/api-client.ts` (hand-authored setup + admin envelope
 *     fetch wrapper — feature 002).
 *   - `src/lib/openapi-runtime.ts` (runtime config passed to the
 *     generated `@hey-api/openapi-ts` fetch client — feature 003).
 *   - `src/lib/router-api.ts` (thin `callAdmin(...)` wrapper over the
 *     generated SDK — feature 003).
 *
 * Why a dedicated module:
 *
 *   - FR-010 requires that every outbound admin/setup call carries
 *     an `X-Request-Id`, generated browser-side so the UI can
 *     surface it in toasts/error banners BEFORE the server
 *     responds. Two generators would risk UI banners carrying a
 *     different id than what the server eventually logs.
 *   - `api-client.ts` (002) and the generated SDK (003) must agree
 *     on the shape of the id so log-correlation scripts
 *     (`rg "$id"` across server logs + browser console exports)
 *     work uniformly.
 *   - Tests pin deterministic ids via header overrides; moving the
 *     generator to a shared module means unit tests of BOTH clients
 *     exercise identical fallback branches.
 *
 * The implementation prefers `crypto.randomUUID` (v4) when
 * available, falls back to `crypto.getRandomValues` (fixing RFC 4122
 * version/variant bits by hand), and finally emits a
 * non-cryptographic `req-<timestamp>-<random>` token so unit tests
 * running in bare-bones jsdom environments still produce a non-empty
 * string. The server treats whatever we send as an opaque token.
 */

/**
 * Generate a fresh correlation id. SAFE to call on every request;
 * callers MUST NOT cache the result (a new id per HTTP call is the
 * entire point of the header).
 */
export function generateRequestId(): string {
  const c = globalThis.crypto as Crypto | undefined
  if (c && typeof c.randomUUID === 'function') {
    return c.randomUUID()
  }
  if (c && typeof c.getRandomValues === 'function') {
    const buf = new Uint8Array(16)
    c.getRandomValues(buf)
    // RFC 4122 v4 layout — fix version and variant bits.
    buf[6] = (buf[6] & 0x0f) | 0x40
    buf[8] = (buf[8] & 0x3f) | 0x80
    const hex = Array.from(buf, (b) => b.toString(16).padStart(2, '0')).join('')
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
  }
  // Last-resort fallback — non-cryptographic, tests only. Prefixed
  // so log-scraping can distinguish from real UUIDs.
  return `req-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

/**
 * HTTP header name used by both the router backend and the admin
 * SPA. Exported as a constant so `rg '"X-Request-Id"'` can find
 * every emission/consumption site across the codebase.
 */
export const REQUEST_ID_HEADER = 'X-Request-Id'
