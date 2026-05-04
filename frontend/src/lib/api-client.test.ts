import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api, apiFetch, RouterApiError } from './api-client'

describe('apiFetch', () => {
  const realFetch = globalThis.fetch
  beforeEach(() => {
    globalThis.fetch = vi.fn()
  })
  afterEach(() => {
    globalThis.fetch = realFetch
  })

  function mockResponse(body: unknown, init: { status?: number; requestId?: string } = {}) {
    const headers = new Headers({ 'Content-Type': 'application/json' })
    if (init.requestId) headers.set('X-Request-Id', init.requestId)
    ;(globalThis.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      ok: (init.status ?? 200) < 400,
      status: init.status ?? 200,
      headers,
      json: async () => body,
    } as unknown as Response)
  }

  it('unwraps envelope on code=0', async () => {
    mockResponse({ code: 0, msg: 'ok', data: { state: 'done' } }, { requestId: 'req_abc' })
    const result = await api.get<{ state: string }>('/api/setup/status')
    expect(result).toEqual({ state: 'done' })
  })

  it('throws RouterApiError with typed code on biz error', async () => {
    mockResponse({ code: 2001, msg: 'setup_already_done', data: {} }, { requestId: 'req_biz' })
    await expect(apiFetch('/api/setup/commit', { method: 'POST' })).rejects.toMatchObject({
      code: 2001,
      msg: 'setup_already_done',
      requestId: 'req_biz',
    })
  })

  it('rejects malformed response with code=-1', async () => {
    mockResponse(null)
    await expect(api.get('/api/admin/settings')).rejects.toBeInstanceOf(RouterApiError)
  })

  it('surfaces X-Request-Id even for system errors', async () => {
    mockResponse(
      { code: -1, msg: 'unknown_error', data: {} },
      { status: 500, requestId: 'req_panic' },
    )
    try {
      await api.get('/api/admin/settings')
      throw new Error('expected throw')
    } catch (e) {
      expect(e).toBeInstanceOf(RouterApiError)
      expect((e as RouterApiError).requestId).toBe('req_panic')
      expect((e as RouterApiError).status).toBe(500)
    }
  })

  it('sends POST body as JSON', async () => {
    const f = globalThis.fetch as ReturnType<typeof vi.fn>
    mockResponse({ code: 0, msg: 'ok', data: {} })
    await api.post('/api/admin/settings/update', { runtime: { log_level: 'debug' } })
    expect(f).toHaveBeenCalledWith(
      '/api/admin/settings/update',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ runtime: { log_level: 'debug' } }),
      }),
    )
  })

  it('sends X-Request-Id header on every call (FR-010)', async () => {
    const f = globalThis.fetch as ReturnType<typeof vi.fn>
    mockResponse({ code: 0, msg: 'ok', data: {} })
    await api.get('/api/setup/status')
    const call = f.mock.calls[0]
    const headers = call[1].headers as Record<string, string>
    expect(headers['X-Request-Id']).toMatch(/^[0-9a-f-]{16,}|^req-/i)
  })

  it('falls back to the outbound request id when server does not echo it', async () => {
    const headers = new Headers({ 'Content-Type': 'application/json' })
    ;(globalThis.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
      ok: false,
      status: 500,
      headers,
      json: async () => ({ code: -1, msg: 'unknown', data: {} }),
    } as unknown as Response)
    try {
      await api.get('/api/admin/settings')
      throw new Error('expected throw')
    } catch (e) {
      expect((e as RouterApiError).requestId).toBeTruthy()
    }
  })

  it('respects caller-supplied X-Request-Id override', async () => {
    const f = globalThis.fetch as ReturnType<typeof vi.fn>
    mockResponse({ code: 0, msg: 'ok', data: {} })
    await apiFetch('/api/setup/status', { headers: { 'X-Request-Id': 'pinned-id' } })
    const call = f.mock.calls[0]
    const headers = call[1].headers as Record<string, string>
    expect(headers['X-Request-Id']).toBe('pinned-id')
  })
})
