/**
 * Unit tests for the `callAdmin` envelope-aware wrapper.
 *
 * Test strategy
 * -------------
 *
 * `callAdmin(call)` is a pure envelope-processor — it takes a
 * generator-shaped promise and applies the router's universal
 * response contract. We stub the generator shape directly (no MSW,
 * no real SDK invocation) so the tests describe ONLY this
 * wrapper's behaviour — a bug in the generated SDK or the fetch
 * runtime shows up in `api-client.test.ts` / integration tests,
 * not here. This keeps the responsibility boundaries tight:
 *
 *   - This file: "given a result from the SDK, does callAdmin
 *     extract the right thing?"
 *   - `api-client.test.ts`: "does the fetch wrapper emit the right
 *     headers and parse JSON correctly?"
 *   - Integration tests (handler-side): "does the full stack
 *     round-trip correctly?"
 *
 * What we cover (mirrors `internal/generated/adminapi/strict_handler_test.go`
 * — the Go counterpart verifies the same contract on the serialise
 * side):
 *
 *   1. Happy path — HTTP 200 + envelope + code=0 → returns `data`.
 *   2. Business error — HTTP 200 + envelope + code=3001 → throws
 *      `RouterApiError` with `status=200, code=3001`.
 *   3. System error — HTTP 500 + envelope + code=3900 → throws
 *      `RouterApiError` with `status=500, code=3900`.
 *   4. Raw-body success (export-auth-json) — HTTP 200 +
 *      Content-Disposition: attachment + non-envelope JSON →
 *      returns body as-is.
 *   5. Malformed success body — HTTP 200 + non-envelope + no
 *      attachment header (e.g. Cloudflare HTML error page) →
 *      throws `code=-1 malformed_envelope`. REGRESSION GUARD for
 *      the 2026-04-21 Codex review finding #1 (never silently
 *      return a non-envelope success body).
 *   6. Malformed system-error body — HTTP 500 + non-envelope →
 *      throws with `code=-1` and the offending body on `data`.
 *   7. Request-id extraction — server-echoed header wins over
 *      outbound-only headers.
 *   8. Outbound fallback — when server strips `X-Request-Id`, the
 *      outbound request's header is used.
 *   9. No request-id anywhere — `requestId` is `null` in the
 *      thrown error (never throws for missing header).
 *  10. Transport failure — the awaited SDK promise rejects with
 *      `TypeError` (network) or `SyntaxError` (JSON parse) → the
 *      wrapper converts to `RouterApiError(code=-1,
 *      msg="transport_error")`. REGRESSION GUARD for the
 *      2026-04-21 Codex review finding #2 (never leak raw
 *      transport errors).
 *  11. Missing Response — a malformed SDK shim hands us a result
 *      without a `Response` object → same transport_error path.
 *  12. Shared RouterApiError — `instanceof RouterApiError` works
 *      for errors thrown from both `router-api` AND `api-client`
 *      catch-sites (Codex review finding #3).
 */

import { describe, expect, it } from 'vitest'

import { RouterApiError as ApiClientRouterApiError } from './api-client'
import { callAdmin, callAdminAttachment, isRouterApiError, RouterApiError } from './router-api'

function makeResult(options: {
  status: number
  body: unknown
  role: 'data' | 'error'
  responseRequestId?: string | null
  outboundRequestId?: string | null
  contentDisposition?: string
  contentType?: string
}) {
  const responseHeaders = new Headers({ 'Content-Type': options.contentType ?? 'application/json' })
  if (options.responseRequestId) {
    responseHeaders.set('X-Request-Id', options.responseRequestId)
  }
  if (options.contentDisposition) {
    responseHeaders.set('Content-Disposition', options.contentDisposition)
  }
  const response = {
    status: options.status,
    ok: options.status < 400,
    headers: responseHeaders,
  } as unknown as Response

  const requestHeaders = new Headers()
  if (options.outboundRequestId) {
    requestHeaders.set('X-Request-Id', options.outboundRequestId)
  }
  const request = { headers: requestHeaders } as unknown as Request

  if (options.role === 'data') {
    return Promise.resolve({
      data: options.body as never,
      error: undefined,
      request,
      response,
    })
  }
  return Promise.resolve({
    data: undefined as never,
    error: options.body,
    request,
    response,
  })
}

describe('callAdmin', () => {
  it('unwraps an HTTP 200 envelope with code=0', async () => {
    const result = await callAdmin(
      makeResult({
        status: 200,
        role: 'data',
        body: { code: 0, msg: 'ok', data: { flow_id: 'f_abc', authorize_url: 'https://…' } },
        responseRequestId: 'req_ok',
      }),
    )
    expect(result).toEqual({ flow_id: 'f_abc', authorize_url: 'https://…' })
  })

  it('throws RouterApiError on HTTP 200 business error', async () => {
    await expect(
      callAdmin(
        makeResult({
          status: 200,
          role: 'data',
          body: {
            code: 3001,
            msg: 'oauth_flow_in_progress',
            data: { method: 'browser', flow_id: 'f_old' },
          },
          responseRequestId: 'req_biz',
        }),
      ),
    ).rejects.toMatchObject({
      code: 3001,
      msg: 'oauth_flow_in_progress',
      status: 200,
      requestId: 'req_biz',
    })
  })

  it('throws RouterApiError on HTTP 500 system error', async () => {
    await expect(
      callAdmin(
        makeResult({
          status: 500,
          role: 'error',
          body: { code: 3900, msg: 'oauth_internal_error', data: {} },
          responseRequestId: 'req_panic',
        }),
      ),
    ).rejects.toMatchObject({
      code: 3900,
      msg: 'oauth_internal_error',
      status: 500,
      requestId: 'req_panic',
    })
  })

  it('returns raw non-envelope body on HTTP 200 with Content-Disposition: attachment (export-auth-json path)', async () => {
    const authJson = {
      OPENAI_API_KEY: null,
      tokens: { access_token: 'a.b.c', refresh_token: 'r', id_token: 'i' },
    }
    const result = await callAdmin(
      makeResult({
        status: 200,
        role: 'data',
        body: authJson,
        contentDisposition: 'attachment; filename="auth.json"',
        responseRequestId: 'req_raw',
      }),
    )
    expect(result).toEqual(authJson)
  })

  it('recognises attachment header with varied casing and quoting', async () => {
    // Content-Disposition header casing varies across proxies;
    // the sniffer must be case-insensitive on the directive.
    const authJson = { tokens: { access_token: 'a' } }
    const result = await callAdmin(
      makeResult({
        status: 200,
        role: 'data',
        body: authJson,
        contentDisposition: 'ATTACHMENT; filename=auth.json',
      }),
    )
    expect(result).toEqual(authJson)
  })

  it('rejects non-envelope HTTP 200 body WITHOUT attachment header as malformed_envelope', async () => {
    // REGRESSION GUARD for 2026-04-21 Codex review finding #1:
    // a Cloudflare HTML error page served with status 200 MUST
    // NOT silently pass through as success — it must surface as
    // RouterApiError so the UI shows the correlation id and the
    // "server contract violated" message.
    await expect(
      callAdmin(
        makeResult({
          status: 200,
          role: 'data',
          body: '<!doctype html><title>520 origin down</title>',
          responseRequestId: 'req_cf',
        }),
      ),
    ).rejects.toMatchObject({
      code: -1,
      msg: 'malformed_envelope',
      status: 200,
      requestId: 'req_cf',
    })
  })

  it('rejects a scalar non-envelope body without attachment as malformed_envelope', async () => {
    await expect(
      callAdmin(makeResult({ status: 200, role: 'data', body: 42 })),
    ).rejects.toMatchObject({
      code: -1,
      msg: 'malformed_envelope',
      data: 42,
    })
  })

  it('wraps a malformed system-error body with code=-1', async () => {
    await expect(
      callAdmin(
        makeResult({ status: 500, role: 'error', body: 'boom', responseRequestId: 'req_boom' }),
      ),
    ).rejects.toMatchObject({
      code: -1,
      msg: 'malformed_envelope',
      data: 'boom',
      status: 500,
      requestId: 'req_boom',
    })
  })

  it('prefers server-echoed X-Request-Id over outbound header', async () => {
    try {
      await callAdmin(
        makeResult({
          status: 200,
          role: 'data',
          body: { code: 3002, msg: 'invalid_oauth_provider', data: {} },
          responseRequestId: 'req_server',
          outboundRequestId: 'req_client',
        }),
      )
      throw new Error('expected throw')
    } catch (e) {
      expect(e).toBeInstanceOf(RouterApiError)
      expect((e as RouterApiError).requestId).toBe('req_server')
    }
  })

  it('falls back to outbound X-Request-Id when server omits the header', async () => {
    try {
      await callAdmin(
        makeResult({
          status: 500,
          role: 'error',
          body: { code: 1900, msg: 'db_unavailable', data: {} },
          outboundRequestId: 'req_outbound',
        }),
      )
      throw new Error('expected throw')
    } catch (e) {
      expect(e).toBeInstanceOf(RouterApiError)
      expect((e as RouterApiError).requestId).toBe('req_outbound')
    }
  })

  it('yields requestId=null when neither side carries the header', async () => {
    try {
      await callAdmin(
        makeResult({
          status: 500,
          role: 'error',
          body: { code: 1900, msg: 'db_unavailable', data: {} },
        }),
      )
      throw new Error('expected throw')
    } catch (e) {
      expect(e).toBeInstanceOf(RouterApiError)
      expect((e as RouterApiError).requestId).toBeNull()
    }
  })

  it('converts a TypeError (network failure) into RouterApiError transport_error', async () => {
    // REGRESSION GUARD for 2026-04-21 Codex review finding #2:
    // the awaited SDK promise rejects (network down, CORS, abort)
    // and the raw exception MUST NOT bubble to the caller — it
    // must be normalised to RouterApiError so catch-sites'
    // `instanceof` checks still fire.
    const networkDown = Promise.reject(new TypeError('Failed to fetch'))
    await expect(callAdmin(networkDown)).rejects.toMatchObject({
      code: -1,
      msg: 'transport_error',
      status: 0,
      requestId: null,
    })
  })

  it('converts a SyntaxError (JSON parse failure) into RouterApiError transport_error', async () => {
    // The generated SDK's `parseAs: 'auto'` path uses
    // `response.json()` which throws SyntaxError on malformed
    // bytes. Same normalisation as network failures.
    const parseBoom = Promise.reject(new SyntaxError('Unexpected token < in JSON at position 0'))
    await expect(callAdmin(parseBoom)).rejects.toMatchObject({
      code: -1,
      msg: 'transport_error',
      status: 0,
    })
    // And the thrown error is a real RouterApiError (not a
    // structural mimic) so `instanceof` guards fire.
    try {
      await callAdmin(parseBoom)
    } catch (e) {
      expect(e).toBeInstanceOf(RouterApiError)
    }
  })

  it('handles SDK shims that omit the Response object (transport_error)', async () => {
    // Defensive against emitter regressions. Emulate a shim that
    // resolves with `response: undefined`.
    const malformed = Promise.resolve({
      data: { code: 0, msg: 'ok', data: {} },
      error: undefined,
      request: new Request('https://dummy'),
      response: undefined as unknown as Response,
    })
    await expect(
      callAdmin(
        malformed as unknown as Promise<
          Parameters<typeof callAdmin>[0] extends infer _ ? never : never
        >,
      ),
    ).rejects.toMatchObject({
      code: -1,
      msg: 'transport_error',
      status: 0,
    })
  })

  it('RouterApiError.message embeds code + msg for log-grep', () => {
    const err = new RouterApiError({
      code: 3006,
      msg: 'flow_expired',
      data: {},
      requestId: 'req_x',
      status: 200,
    })
    expect(err.message).toBe('flow_expired (code=3006)')
    expect(err.name).toBe('RouterApiError')
  })

  it('shares the RouterApiError class with api-client.ts (single instanceof check works)', () => {
    // REGRESSION GUARD for 2026-04-21 Codex review finding #3:
    // both wrappers must raise the SAME class — a UI catch-site
    // that imports RouterApiError from either module must catch
    // errors from both.
    const err = new RouterApiError({
      code: 1001,
      msg: 'account_not_found',
      data: {},
      requestId: null,
      status: 200,
    })
    expect(err).toBeInstanceOf(ApiClientRouterApiError)
    expect(isRouterApiError(err)).toBe(true)

    // Structural guard rejects foreign error types.
    expect(isRouterApiError(new Error('generic'))).toBe(false)
    expect(isRouterApiError(null)).toBe(false)
    expect(isRouterApiError({ code: 'not-a-number' })).toBe(false)
  })
})

describe('callAdminAttachment', () => {
  it('returns a Blob + filename when the response is an attachment', async () => {
    const result = await callAdminAttachment(
      makeResult({
        status: 200,
        role: 'data',
        body: '{"OPENAI_API_KEY":null}',
        contentType: 'application/json; charset=utf-8',
        contentDisposition: 'attachment; filename="auth.json"',
        responseRequestId: 'req_attachment',
      }),
    )

    expect(result.filename).toBe('auth.json')
    expect(result.blob).toBeInstanceOf(Blob)
    expect(result.blob.size).toBeGreaterThan(0)
    const payload = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader()
      reader.onerror = () => {
        reject(reader.error ?? new Error('read blob failed'))
      }
      reader.onload = () => {
        resolve(String(reader.result))
      }
      reader.readAsText(result.blob)
    })
    expect(payload).toBe('{"OPENAI_API_KEY":null}')
    expect(result.blob.type).toBe('application/json; charset=utf-8')
  })

  it('throws business errors from text bodies when attachment header is absent', async () => {
    await expect(
      callAdminAttachment(
        makeResult({
          status: 200,
          role: 'data',
          body: '{"code":3014,"msg":"not_oauth_account","data":{"auth_method":"api_key"}}',
          responseRequestId: 'req_not_oauth',
        }),
      ),
    ).rejects.toMatchObject({
      code: 3014,
      msg: 'not_oauth_account',
      status: 200,
      requestId: 'req_not_oauth',
    })
  })

  it('throws system errors from text bodies on HTTP 500', async () => {
    await expect(
      callAdminAttachment(
        makeResult({
          status: 500,
          role: 'error',
          body: '{"code":3902,"msg":"oauth_export_read_failed","data":{}}',
          responseRequestId: 'req_export_500',
        }),
      ),
    ).rejects.toMatchObject({
      code: 3902,
      msg: 'oauth_export_read_failed',
      status: 500,
      requestId: 'req_export_500',
    })
  })

  it('rejects non-envelope non-attachment bodies as malformed_envelope', async () => {
    await expect(
      callAdminAttachment(
        makeResult({
          status: 200,
          role: 'data',
          body: 'not-json',
        }),
      ),
    ).rejects.toMatchObject({
      code: -1,
      msg: 'malformed_envelope',
      status: 200,
    })
  })

  it('converts attachment transport failures into RouterApiError transport_error', async () => {
    const networkDown = Promise.reject(new TypeError('Failed to fetch'))
    await expect(callAdminAttachment(networkDown)).rejects.toMatchObject({
      code: -1,
      msg: 'transport_error',
      status: 0,
      requestId: null,
    })
  })

  it('treats attachment SDK results without a Response as transport_error', async () => {
    const malformed = Promise.resolve({
      data: '{"OPENAI_API_KEY":null}',
      error: undefined,
      request: new Request('https://dummy'),
      response: undefined as unknown as Response,
    })
    await expect(
      callAdminAttachment(
        malformed as unknown as Promise<
          Parameters<typeof callAdminAttachment>[0] extends infer _ ? never : never
        >,
      ),
    ).rejects.toMatchObject({
      code: -1,
      msg: 'transport_error',
      status: 0,
    })
  })
})
