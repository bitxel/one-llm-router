/**
 * Unit tests for the generated-SDK runtime config
 * (`createClientConfig`).
 *
 * Why these tests matter
 * ----------------------
 *
 * `createClientConfig` is the ONLY place the generated @hey-api
 * client receives runtime defaults — base URL, fetch override,
 * X-Request-Id injection. A regression here means every admin API
 * call across the entire SPA silently loses its correlation id
 * (FR-010 violation). We unit-test the fetch wrapper directly so
 * the contract is pinned independently of:
 *
 *   - the generated `client.gen.ts` (re-emitted on every spec
 *     change — we don't want its shape leaking into tests).
 *   - MSW / jsdom (integration layer — tested at the consumer
 *     level, e.g. OAuthFlowCard.test.tsx).
 *
 * Coverage:
 *
 *   1. Injects `X-Request-Id` when caller did not supply one.
 *   2. Preserves caller-supplied `X-Request-Id` verbatim (tests can
 *      pin deterministic ids).
 *   3. Defaults `baseUrl` to empty (same-origin) when caller omitted.
 *   4. Preserves caller-supplied `baseUrl` (the override contract).
 *   5. Uses an injected `config.fetch` if provided (tests/mocks).
 *   6. Restores `Accept: application/json` when missing.
 *   7. Works with `Request` input objects (their headers are
 *      frozen — we must clone-and-extend).
 *   8. Does NOT mutate the caller's `init.headers` object (pure
 *      transformation — important for MSW/vitest harnesses that
 *      reuse header objects across multiple requests).
 */

import { afterEach, describe, expect, it, vi } from 'vitest'

import { createClientConfig } from './openapi-runtime'

describe('openapi-runtime › createClientConfig', () => {
  const realFetch = globalThis.fetch
  afterEach(() => {
    globalThis.fetch = realFetch
  })

  function mockFetch() {
    const f = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => {
      return new Response('{}', {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    })
    globalThis.fetch = f as unknown as typeof fetch
    return f
  }

  function captureInitHeaders(call: readonly unknown[]): Record<string, string> {
    const init = call[1] as RequestInit | undefined
    const headers = init?.headers
    if (!headers) return {}
    if (headers instanceof Headers) {
      const out: Record<string, string> = {}
      headers.forEach((v, k) => {
        out[k] = v
      })
      return out
    }
    return headers as Record<string, string>
  }

  it('injects a fresh X-Request-Id when caller has none', async () => {
    const f = mockFetch()
    const cfg = createClientConfig()
    await cfg.fetch?.('/api/admin/test', {})
    const headers = captureInitHeaders(f.mock.calls[0])
    // jsdom's header keys are lower-cased.
    expect(headers['x-request-id']).toMatch(/^[0-9a-f-]{16,}|^req-/i)
  })

  it('preserves caller-supplied X-Request-Id', async () => {
    const f = mockFetch()
    const cfg = createClientConfig()
    await cfg.fetch?.('/api/admin/test', { headers: { 'X-Request-Id': 'pinned-id' } })
    const headers = captureInitHeaders(f.mock.calls[0])
    expect(headers['x-request-id']).toBe('pinned-id')
  })

  it('defaults baseUrl to empty (same-origin)', () => {
    const cfg = createClientConfig()
    expect(cfg.baseUrl).toBe('')
  })

  it('IGNORES generator-seeded baseUrl in favour of same-origin default', () => {
    // The generator bakes `servers[0].url` from `openapi/admin.yaml`
    // (currently `http://localhost:8080`) into `client.gen.ts` via
    // `createConfig({ baseUrl: 'http://localhost:8080' })`. For the
    // admin SPA that value is wrong — see the ADMIN_API_BASE_URL
    // comment. Pin the override behaviour so a future emitter
    // change cannot silently re-introduce the cross-origin footgun.
    const cfg = createClientConfig({ baseUrl: 'http://localhost:8080' })
    expect(cfg.baseUrl).toBe('')

    const cfgAlt = createClientConfig({ baseUrl: 'https://unit.test' })
    expect(cfgAlt.baseUrl).toBe('')
  })

  it('uses injected config.fetch for request dispatch', async () => {
    // `globalThis.fetch` is untouched; the injected fn is the one
    // that should fire. Proves the wrapper respects the override
    // chain (Storybook/MSW mocking relies on this).
    const injected = vi.fn(async () => new Response('{}', { status: 200 }))
    const cfg = createClientConfig({ fetch: injected })
    await cfg.fetch?.('/api/admin/test', {})
    expect(injected).toHaveBeenCalledTimes(1)
  })

  it('restores Accept: application/json when missing', async () => {
    const f = mockFetch()
    const cfg = createClientConfig()
    await cfg.fetch?.('/api/admin/test', { headers: {} })
    const headers = captureInitHeaders(f.mock.calls[0])
    expect(headers.accept).toBe('application/json')
  })

  it('works with Request input (frozen headers)', async () => {
    const f = mockFetch()
    const cfg = createClientConfig()
    const req = new Request('https://unit.test/api/admin/test', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: '{}',
    })
    await cfg.fetch?.(req)
    const headers = captureInitHeaders(f.mock.calls[0])
    expect(headers['x-request-id']).toMatch(/^[0-9a-f-]{16,}|^req-/i)
  })

  it('does not mutate caller-supplied headers object', async () => {
    mockFetch()
    const cfg = createClientConfig()
    const callerHeaders = { 'Content-Type': 'application/json' }
    await cfg.fetch?.('/api/admin/test', { headers: callerHeaders })
    // caller's object stays pristine — no X-Request-Id leaked in.
    expect(callerHeaders).toEqual({ 'Content-Type': 'application/json' })
  })
})
