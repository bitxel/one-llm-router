import { createServer, type IncomingMessage, type Server, type ServerResponse } from 'node:http'
import type { AddressInfo } from 'node:net'
import type { APIRequestContext, APIResponse } from '@playwright/test'

import { expect, test } from './fixtures'
import { buildAuthJSONFixture, commitAPIKeySetup } from './helpers'

test.use({ screenshot: 'off', trace: 'off', video: 'off' })

interface CapturedUpstreamRequest {
  method: string
  path: string
  headers: IncomingMessage['headers']
  rawBody: string
  body: Record<string, unknown>
}

interface RequestLogRecord {
  request_id: string
  method: string
  path: string
  status_code: number
  outcome: string
  error_code?: string | null
  model?: string | null
  model_params?: Record<string, unknown> | null
  router_metadata?: Record<string, unknown> | null
  response_mode: string
  token_usage?: Record<string, number> | null
  ttft_ms?: number | null
}

interface BridgeMetadata {
  op_id: string
  bridge_id: string
  client_contract: string
  upstream_contract: string
  credential_class: 'api_key' | 'oauth'
}

interface CompatCase {
  name: string
  responseMode: 'json' | 'sse'
  payload: Record<string, unknown>
  assertUpstream: (body: Record<string, unknown>) => void
}

const bridgeMetadataSecretPattern =
  /authorization|cookie|bearer|sk-|oauth-access|oauth-refresh|access_token|refresh_token|id_token|secret|request_body|response_body/i

const apiKeyResponsesBridge: BridgeMetadata = {
  op_id: 'op.openai.responses.create',
  bridge_id: 'bridge.openai.responses.direct',
  client_contract: 'contract.openai.v1.responses',
  upstream_contract: 'contract.openai.v1.responses',
  credential_class: 'api_key',
}

const apiKeyChatBridge: BridgeMetadata = {
  op_id: 'op.openai.chat_completions.create',
  bridge_id: 'bridge.openai.chat_completions.direct',
  client_contract: 'contract.openai.v1.chat_completions',
  upstream_contract: 'contract.openai.v1.chat_completions',
  credential_class: 'api_key',
}

const apiKeyModelsRetrieveBridge: BridgeMetadata = {
  op_id: 'op.openai.models.retrieve',
  bridge_id: 'bridge.openai.models.direct',
  client_contract: 'contract.openai.v1.models',
  upstream_contract: 'contract.openai.v1.models',
  credential_class: 'api_key',
}

const oauthResponsesBridge: BridgeMetadata = {
  op_id: 'op.openai.responses.create',
  bridge_id: 'bridge.openai.responses.to_codex',
  client_contract: 'contract.openai.v1.responses',
  upstream_contract: 'contract.chatgpt.backend_api.codex.responses',
  credential_class: 'oauth',
}

const oauthResponsesWebSocketBridge: BridgeMetadata = {
  op_id: 'op.openai.responses.websocket',
  bridge_id: 'bridge.openai.responses.websocket.to_codex',
  client_contract: 'contract.openai.v1.responses.websocket',
  upstream_contract: 'contract.chatgpt.backend_api.codex.responses.websocket',
  credential_class: 'oauth',
}

const oauthChatBridge: BridgeMetadata = {
  op_id: 'op.openai.chat_completions.create',
  bridge_id: 'bridge.openai.chat_completions.to_codex',
  client_contract: 'contract.openai.v1.chat_completions',
  upstream_contract: 'contract.chatgpt.backend_api.codex.responses',
  credential_class: 'oauth',
}

const codexNativeResponsesWebSocketBridge: BridgeMetadata = {
  op_id: 'op.codex_native.responses.websocket',
  bridge_id: 'bridge.codex_native.responses.websocket.direct',
  client_contract: 'contract.chatgpt.backend_api.codex.responses.websocket',
  upstream_contract: 'contract.chatgpt.backend_api.codex.responses.websocket',
  credential_class: 'oauth',
}

const codexNativeTranscribeBridge: BridgeMetadata = {
  op_id: 'op.codex_native.transcribe',
  bridge_id: 'bridge.codex_native.transcribe.direct',
  client_contract: 'contract.chatgpt.backend_api.transcribe',
  upstream_contract: 'contract.chatgpt.backend_api.transcribe',
  credential_class: 'oauth',
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

async function readRawBody(req: IncomingMessage): Promise<string> {
  const chunks: Buffer[] = []
  for await (const chunk of req) {
    chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk))
  }
  return Buffer.concat(chunks).toString('utf8')
}

function writeJSONResponse(res: ServerResponse, id: string): void {
  res.writeHead(200, { 'content-type': 'application/json' })
  res.end(
    JSON.stringify({
      id,
      object: 'response',
      status: 'completed',
      output_text: 'ok',
      usage: {
        input_tokens: 5,
        input_tokens_details: { cached_tokens: 2 },
        output_tokens: 3,
      },
    }),
  )
}

function writeUpstreamError(
  res: ServerResponse,
  statusCode: number,
  code: string,
  type: string,
): void {
  res.writeHead(statusCode, { 'content-type': 'application/json' })
  res.end(
    JSON.stringify({
      error: {
        code,
        message: `${code} from mock upstream`,
        type,
      },
    }),
  )
}

async function writeSSEResponse(res: ServerResponse, id: string): Promise<void> {
  res.writeHead(200, {
    'content-type': 'text/event-stream',
    'cache-control': 'no-cache',
    connection: 'keep-alive',
  })
  await delay(10)
  res.write(
    'event: response.output_text.delta\ndata: {"type":"response.output_text.delta","delta":"o"}\n\n',
  )
  await delay(5)
  res.end(
    `event: response.completed\ndata: {"type":"response.completed","response":{"id":"${id}","usage":{"input_tokens":5,"input_tokens_details":{"cached_tokens":2},"output_tokens":3}}}\n\n`,
  )
}

async function writeSlowSSEResponse(res: ServerResponse, id: string): Promise<void> {
  res.writeHead(200, {
    'content-type': 'text/event-stream',
    'cache-control': 'no-cache',
    connection: 'keep-alive',
  })
  await delay(10)
  res.write(
    'event: response.output_text.delta\ndata: {"type":"response.output_text.delta","delta":"early"}\n\n',
  )
  await delay(1000)
  res.end(
    `event: response.completed\ndata: {"type":"response.completed","response":{"id":"${id}","usage":{"input_tokens":1,"output_tokens":1}}}\n\n`,
  )
}

async function writeChatSSEResponse(res: ServerResponse): Promise<void> {
  res.writeHead(200, {
    'content-type': 'text/event-stream',
    'cache-control': 'no-cache',
    connection: 'keep-alive',
  })
  await delay(10)
  res.write(
    'data: {"id":"chatcmpl_mock","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}\n\n',
  )
  await delay(5)
  res.end(
    'data: {"id":"chatcmpl_mock","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":1}}}\n\n' +
      'data: [DONE]\n\n',
  )
}

async function startCompatUpstream(): Promise<{
  url: string
  captured: CapturedUpstreamRequest[]
  close: () => Promise<void>
}> {
  const captured: CapturedUpstreamRequest[] = []
  const server: Server = createServer(async (req, res) => {
    if (req.method === 'GET' && req.url === '/v1/models') {
      captured.push({
        method: req.method,
        path: req.url,
        headers: req.headers,
        rawBody: '',
        body: {},
      })
      res.writeHead(200, { 'content-type': 'application/json' })
      res.end(
        JSON.stringify({
          object: 'list',
          data: [{ id: 'gpt-5.4-mini', object: 'model', created: 1767225600 }],
        }),
      )
      return
    }
    if (req.method === 'GET' && req.url === '/v1/models/gpt-5.4-mini') {
      captured.push({
        method: req.method,
        path: req.url,
        headers: req.headers,
        rawBody: '',
        body: {},
      })
      res.writeHead(200, { 'content-type': 'application/json' })
      res.end(JSON.stringify({ id: 'gpt-5.4-mini', object: 'model', created: 1767225600 }))
      return
    }
    if (
      req.method !== 'POST' ||
      !['/v1/responses', '/v1/chat/completions'].includes(req.url ?? '')
    ) {
      res.writeHead(404, { 'content-type': 'application/json' })
      res.end(JSON.stringify({ error: { code: 'not_found' } }))
      return
    }

    const rawBody = await readRawBody(req)
    const body = JSON.parse(rawBody) as Record<string, unknown>
    captured.push({
      method: req.method,
      path: req.url,
      headers: req.headers,
      rawBody,
      body,
    })

    const metadata = body.metadata as Record<string, unknown> | undefined
    const caseName = typeof metadata?.compat_case === 'string' ? metadata.compat_case : 'unknown'
    if (caseName === 'E.upstream_400_json') {
      writeUpstreamError(res, 400, 'bad_request', 'invalid_request_error')
      return
    }
    if (caseName === 'E.upstream_500_json') {
      writeUpstreamError(res, 500, 'upstream_internal', 'server_error')
      return
    }

    const id = `resp_${caseName.replaceAll('.', '_')}`
    if (caseName === 'R.streaming_hold') {
      await writeSlowSSEResponse(res, id)
      return
    }
    if (req.url === '/v1/chat/completions') {
      if (body.stream === true) {
        await writeChatSSEResponse(res)
        return
      }
      res.writeHead(200, { 'content-type': 'application/json' })
      res.end(
        JSON.stringify({
          id: 'chatcmpl_mock',
          object: 'chat.completion',
          choices: [
            {
              index: 0,
              message: { role: 'assistant', content: 'ok' },
              finish_reason: 'stop',
            },
          ],
          usage: { prompt_tokens: 4, completion_tokens: 2, total_tokens: 6 },
        }),
      )
      return
    }
    if (body.stream === true) {
      await writeSSEResponse(res, id)
      return
    }
    writeJSONResponse(res, id)
  })

  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  const address = server.address() as AddressInfo
  return {
    url: `http://127.0.0.1:${address.port}`,
    captured,
    close: () => new Promise<void>((resolve) => server.close(() => resolve())),
  }
}

async function importOAuthAccount(request: APIRequestContext): Promise<void> {
  const response = await request.post('/api/admin/accounts/import-auth-json', {
    multipart: {
      auth_json: {
        name: 'auth.json',
        mimeType: 'application/json',
        buffer: Buffer.from(
          buildAuthJSONFixture({
            email: 'codex-compat@example.com',
            planType: 'chatgpt-plus',
            accountID: 'acct-codex-compat',
            accessToken: 'oauth-access-e2e',
            refreshToken: 'oauth-refresh-e2e',
            lastRefresh: new Date(Date.now() - 60_000).toISOString(),
          }),
        ),
      },
    },
  })
  const body = (await response.json()) as { code?: number; msg?: string }
  expect(response.status(), `import auth.json failed: ${JSON.stringify(body)}`).toBe(200)
  expect(body.code, `import auth.json failed: ${JSON.stringify(body)}`).toBe(0)
}

async function expectOKBody(
  response: APIResponse,
  caseName: string,
): Promise<{ text: string; requestID: string }> {
  const text = await response.text()
  expect(response.status(), `${caseName} failed with body: ${text.slice(0, 500)}`).toBe(200)
  const requestID = response.headers()['x-request-id']
  expect(requestID, `${caseName} missing x-request-id`).toBeTruthy()
  if (!requestID) {
    throw new Error(`${caseName} missing x-request-id`)
  }
  return { text, requestID }
}

async function expectErrorBody(
  response: APIResponse,
  caseName: string,
): Promise<{
  body: { error?: { code?: string; request_id?: string; type?: string } }
  requestID: string
}> {
  const text = await response.text()
  const requestID = response.headers()['x-request-id']
  expect(requestID, `${caseName} missing x-request-id`).toBeTruthy()
  if (!requestID) {
    throw new Error(`${caseName} missing x-request-id`)
  }
  const parsed = JSON.parse(text) as {
    code?: unknown
    msg?: unknown
    data?: unknown
    error?: { code?: string; request_id?: string; type?: string }
  }
  expect(parsed.code, `${caseName} must not use admin envelope code`).toBeUndefined()
  expect(parsed.msg, `${caseName} must not use admin envelope msg`).toBeUndefined()
  expect(parsed.data, `${caseName} must not use admin envelope data`).toBeUndefined()
  expect(parsed.error?.request_id, `${caseName} must keep request id header-only`).toBeUndefined()
  return {
    body: parsed,
    requestID,
  }
}

async function waitForRecordedRequest(
  request: APIRequestContext,
  requestID: string,
): Promise<RequestLogRecord> {
  const deadline = Date.now() + 10_000
  while (Date.now() < deadline) {
    const response = await request.get(
      `/api/admin/requests?search=${encodeURIComponent(requestID)}`,
    )
    const body = (await response.json()) as {
      code: number
      data?: { records?: RequestLogRecord[] }
    }
    if (body.code === 0) {
      const record = body.data?.records?.find((item) => item.request_id === requestID)
      if (record) {
        return record
      }
    }
    await delay(250)
  }
  throw new Error(`request log record not found for ${requestID}`)
}

async function waitForRecordedRequestByPath(
  request: APIRequestContext,
  path: string,
  responseMode: 'json' | 'sse' | 'websocket',
): Promise<RequestLogRecord> {
  const deadline = Date.now() + 10_000
  while (Date.now() < deadline) {
    const response = await request.get(
      `/api/admin/requests?response_mode=${encodeURIComponent(responseMode)}&limit=50`,
    )
    const body = (await response.json()) as {
      code: number
      data?: { records?: RequestLogRecord[] }
    }
    if (body.code === 0) {
      const record = body.data?.records?.find(
        (item) => item.path === path && item.response_mode === responseMode,
      )
      if (record) {
        return record
      }
    }
    await delay(250)
  }
  throw new Error(`request log record not found for ${responseMode} ${path}`)
}

function expectBridgeMetadata(record: RequestLogRecord, expected: BridgeMetadata): void {
  const rawBridge = record.router_metadata?.bridge
  expect(rawBridge, `${record.request_id} missing router_metadata.bridge`).toBeTruthy()
  expect(rawBridge, `${record.request_id} router_metadata.bridge`).toMatchObject(expected)

  const bridge = rawBridge as Record<string, unknown>
  for (const [key, value] of Object.entries(bridge)) {
    expect(key, `${record.request_id} unsafe bridge metadata key`).not.toMatch(
      bridgeMetadataSecretPattern,
    )
    expect(String(value), `${record.request_id} unsafe bridge metadata value`).not.toMatch(
      bridgeMetadataSecretPattern,
    )
  }
}

function expectNoBridgeMetadata(record: RequestLogRecord): void {
  expect(record.router_metadata?.bridge ?? undefined).toBeUndefined()
}

function expectModelsUnionMetadata(
  record: RequestLogRecord,
  expectedCredentialClasses: Array<'api_key' | 'oauth'>,
): void {
  expectNoBridgeMetadata(record)

  const rawModelsUnion = record.router_metadata?.models_union
  expect(rawModelsUnion, `${record.request_id} missing router_metadata.models_union`).toBeTruthy()
  expect(rawModelsUnion, `${record.request_id} router_metadata.models_union`).toMatchObject({
    account_count: expectedCredentialClasses.length,
    credential_classes: expectedCredentialClasses,
    source: 'active_eligible_accounts',
    partial_success: false,
    model_routing_bound: false,
  })
}

const compatCases: CompatCase[] = [
  {
    name: 'R.text_input_string',
    responseMode: 'json',
    payload: {
      model: 'gpt-5.4-mini',
      input: 'Return ok only.',
      max_output_tokens: 16,
      metadata: { compat_case: 'R.text_input_string' },
    },
    assertUpstream: (body) => {
      expect(body.input).toBe('Return ok only.')
      expect(body.max_output_tokens).toBe(16)
    },
  },
  {
    name: 'R.text_input_list',
    responseMode: 'json',
    payload: {
      model: 'gpt-5.4-mini',
      input: [
        {
          role: 'user',
          content: [{ type: 'input_text', text: 'Return ok only.' }],
        },
      ],
      metadata: { compat_case: 'R.text_input_list' },
    },
    assertUpstream: (body) => {
      expect(body.input).toEqual([
        {
          role: 'user',
          content: [{ type: 'input_text', text: 'Return ok only.' }],
        },
      ])
    },
  },
  {
    name: 'R.streaming',
    responseMode: 'sse',
    payload: {
      model: 'gpt-5.4-mini',
      input: 'Stream ok.',
      stream: true,
      metadata: { compat_case: 'R.streaming' },
    },
    assertUpstream: (body) => {
      expect(body.stream).toBe(true)
    },
  },
  {
    name: 'R.text_format_json_object',
    responseMode: 'json',
    payload: {
      model: 'gpt-5.4-mini',
      input: 'Return {"ok": true} as JSON.',
      text: { format: { type: 'json_object' } },
      metadata: { compat_case: 'R.text_format_json_object' },
    },
    assertUpstream: (body) => {
      expect(body.text).toEqual({ format: { type: 'json_object' } })
    },
  },
  {
    name: 'R.reasoning_effort_low',
    responseMode: 'json',
    payload: {
      model: 'gpt-5.4-mini',
      input: 'Solve 1+1.',
      reasoning: { effort: 'low' },
      metadata: { compat_case: 'R.reasoning_effort_low' },
    },
    assertUpstream: (body) => {
      expect(body.reasoning).toEqual({ effort: 'low' })
    },
  },
  {
    name: 'R.include_logprobs',
    responseMode: 'json',
    payload: {
      model: 'gpt-5.4-mini',
      input: 'Return ok.',
      include: ['message.output_text.logprobs'],
      metadata: { compat_case: 'R.include_logprobs' },
    },
    assertUpstream: (body) => {
      expect(body.include).toEqual(['message.output_text.logprobs'])
    },
  },
  {
    name: 'R.image_input_url',
    responseMode: 'json',
    payload: {
      model: 'gpt-5.4-mini',
      input: [
        {
          role: 'user',
          content: [
            { type: 'input_text', text: 'Describe the image.' },
            { type: 'input_image', image_url: 'https://example.com/image.png' },
          ],
        },
      ],
      metadata: { compat_case: 'R.image_input_url' },
    },
    assertUpstream: (body) => {
      expect(body.input).toEqual([
        {
          role: 'user',
          content: [
            { type: 'input_text', text: 'Describe the image.' },
            { type: 'input_image', image_url: 'https://example.com/image.png' },
          ],
        },
      ])
    },
  },
]

test.describe('data-plane compatibility', () => {
  test('preserves client-facing /v1/responses request shapes and records usage', async ({
    page,
  }) => {
    const upstream = await startCompatUpstream()
    try {
      await commitAPIKeySetup(page.request, {
        accountName: 'compat-local',
        apiKey: 'sk-local-compat',
        baseURL: upstream.url,
      })

      for (const compatCase of compatCases) {
        await test.step(compatCase.name, async () => {
          const response = await page.request.post('/v1/responses', {
            data: compatCase.payload,
          })
          const { requestID, text: bodyText } = await expectOKBody(response, compatCase.name)
          if (compatCase.responseMode === 'sse') {
            expect(response.headers()['content-type']).toContain('text/event-stream')
            expect(bodyText).toContain('data:')
            expect(bodyText).toContain('response.output_text.delta')
            expect(bodyText).toContain('response.completed')
          } else {
            expect(response.headers()['content-type']).toContain('application/json')
            const body = JSON.parse(bodyText) as { id?: string; usage?: unknown }
            expect(body.id).toBeTruthy()
            expect(body.usage).toBeTruthy()
          }

          const captured = upstream.captured.at(-1)
          expect(captured, `${compatCase.name} upstream capture`).toBeTruthy()
          expect(captured?.method).toBe('POST')
          expect(captured?.path).toBe('/v1/responses')
          expect(captured?.headers.authorization).toBe('Bearer sk-local-compat')
          compatCase.assertUpstream(captured?.body ?? {})

          const record = await waitForRecordedRequest(page.request, requestID)
          expect(record.status_code).toBe(200)
          expect(record.outcome).toBe('success')
          expect(record.model).toBe('gpt-5.4-mini')
          expect(record.response_mode).toBe(compatCase.responseMode)
          expect(record.token_usage).toMatchObject({
            input: 5,
            cached_input: 2,
            output: 3,
          })
          if (compatCase.responseMode === 'sse') {
            expect(record.ttft_ms).toEqual(expect.any(Number))
          }
          expectBridgeMetadata(record, apiKeyResponsesBridge)
        })
      }

      expect(upstream.captured).toHaveLength(compatCases.length)
    } finally {
      await upstream.close()
    }
  })

  test('streams first SSE chunk before upstream completion', async ({ page }) => {
    const upstream = await startCompatUpstream()
    try {
      await commitAPIKeySetup(page.request, {
        accountName: 'compat-streaming-reader',
        apiKey: 'sk-local-streaming-reader',
        baseURL: upstream.url,
      })
      await page.goto('/admin/')

      const result = await page.evaluate(async () => {
        const controller = new AbortController()
        const started = performance.now()
        const response = await fetch('/v1/responses', {
          method: 'POST',
          headers: { 'content-type': 'application/json' },
          body: JSON.stringify({
            model: 'gpt-5.4-mini',
            input: 'Stream early.',
            stream: true,
            metadata: { compat_case: 'R.streaming_hold' },
          }),
          signal: controller.signal,
        })
        const reader = response.body?.getReader()
        if (!reader) {
          throw new Error('missing response body reader')
        }
        const first = await reader.read()
        controller.abort()
        return {
          status: response.status,
          elapsedMs: performance.now() - started,
          chunk: new TextDecoder().decode(first.value ?? new Uint8Array()),
        }
      })

      expect(result.status).toBe(200)
      expect(result.elapsedMs).toBeLessThan(800)
      expect(result.chunk).toContain('response.output_text.delta')
      expect(result.chunk).not.toContain('response.completed')
    } finally {
      await upstream.close()
    }
  })

  test('preserves upstream error responses and records upstream_error', async ({ page }) => {
    const upstream = await startCompatUpstream()
    try {
      await commitAPIKeySetup(page.request, {
        accountName: 'compat-errors',
        apiKey: 'sk-local-compat-errors',
        baseURL: upstream.url,
      })

      const cases = [
        {
          name: 'E.upstream_400_json',
          statusCode: 400,
          errorCode: 'bad_request',
        },
        {
          name: 'E.upstream_500_json',
          statusCode: 500,
          errorCode: 'upstream_internal',
        },
      ]

      for (const errorCase of cases) {
        await test.step(errorCase.name, async () => {
          const response = await page.request.post('/v1/responses', {
            data: {
              model: 'gpt-5.4-mini',
              input: 'Trigger upstream error.',
              metadata: { compat_case: errorCase.name },
            },
          })

          expect(response.status()).toBe(errorCase.statusCode)
          const { body, requestID } = await expectErrorBody(response, errorCase.name)
          expect(body.error?.code).toBe(errorCase.errorCode)
          expect(body.error?.type).not.toBe('router_error')

          const record = await waitForRecordedRequest(page.request, requestID)
          expect(record.status_code).toBe(errorCase.statusCode)
          expect(record.outcome).toBe('upstream_error')
          expect(record.error_code).toBe(errorCase.errorCode)
          expect(record.response_mode).toBe('json')
          expectBridgeMetadata(record, apiKeyResponsesBridge)
        })
      }
    } finally {
      await upstream.close()
    }
  })

  test('returns router-native data-plane error when upstream cannot be reached', async ({
    page,
  }) => {
    await commitAPIKeySetup(page.request, {
      accountName: 'compat-connect-failed',
      apiKey: 'sk-local-compat-connect-failed',
      baseURL: 'http://127.0.0.1:1',
    })

    const response = await page.request.post('/v1/responses', {
      data: {
        model: 'gpt-5.4-mini',
        input: 'Trigger connect failure.',
      },
    })

    expect(response.status()).toBe(502)
    const { body, requestID } = await expectErrorBody(response, 'E.upstream_connect_failed')
    expect(body.error).toMatchObject({
      code: 'upstream_connect_failed',
      type: 'router_error',
    })
    expect(requestID).toBeTruthy()

    const record = await waitForRecordedRequest(page.request, requestID)
    expect(record.status_code).toBe(502)
    expect(record.outcome).toBe('router_error')
    expect(record.error_code).toBe('upstream_connect_failed')
    expect(record.response_mode).toBe('json')
    expectBridgeMetadata(record, apiKeyResponsesBridge)
  })

  test('covers API-key chat, models, admin usage, and unsupported routes', async ({
    page,
    codexBackendMock,
  }) => {
    codexBackendMock.reset()
    const upstream = await startCompatUpstream()
    try {
      await commitAPIKeySetup(page.request, {
        accountName: 'compat-openai-platform',
        apiKey: 'sk-local-platform',
        baseURL: upstream.url,
      })

      const chatJSON = await page.request.post('/v1/chat/completions', {
        data: {
          model: 'gpt-5.4-mini',
          messages: [{ role: 'user', content: 'Return ok.' }],
          metadata: { compat_case: 'C.chat_json' },
        },
      })
      const { requestID: chatJSONRequestID, text: chatJSONText } = await expectOKBody(
        chatJSON,
        'C.chat_json',
      )
      expect(JSON.parse(chatJSONText).object).toBe('chat.completion')
      expect(upstream.captured.at(-1)?.path).toBe('/v1/chat/completions')
      const chatJSONRecord = await waitForRecordedRequest(page.request, chatJSONRequestID)
      expect(chatJSONRecord.response_mode).toBe('json')
      expectBridgeMetadata(chatJSONRecord, apiKeyChatBridge)

      const chatSSE = await page.request.post('/v1/chat/completions', {
        data: {
          model: 'gpt-5.4-mini',
          messages: [{ role: 'user', content: 'Stream ok.' }],
          stream: true,
          stream_options: { include_usage: true },
          metadata: { compat_case: 'C.chat_sse' },
        },
      })
      const { requestID: chatSSERequestID, text: chatSSEText } = await expectOKBody(
        chatSSE,
        'C.chat_sse',
      )
      expect(chatSSE.headers()['content-type']).toContain('text/event-stream')
      expect(chatSSEText).toContain('chat.completion.chunk')
      const chatRecord = await waitForRecordedRequest(page.request, chatSSERequestID)
      expect(chatRecord.response_mode).toBe('sse')
      expect(chatRecord.token_usage).toMatchObject({ input: 4, cached_input: 1, output: 2 })
      expectBridgeMetadata(chatRecord, apiKeyChatBridge)

      const models = await page.request.get('/v1/models')
      const { requestID: modelsRequestID, text: modelsBodyText } = await expectOKBody(
        models,
        'M.models_list',
      )
      expect(JSON.parse(modelsBodyText).data[0].id).toBe('gpt-5.4-mini')
      expect(upstream.captured.at(-1)?.path).toBe('/v1/models')
      const modelsRecord = await waitForRecordedRequest(page.request, modelsRequestID)
      expectModelsUnionMetadata(modelsRecord, ['api_key'])

      const model = await page.request.get('/v1/models/gpt-5.4-mini')
      const { requestID: modelRequestID, text: modelBodyText } = await expectOKBody(
        model,
        'M.model_retrieve',
      )
      expect(JSON.parse(modelBodyText).id).toBe('gpt-5.4-mini')
      expect(upstream.captured.at(-1)?.path).toBe('/v1/models/gpt-5.4-mini')
      const modelRecord = await waitForRecordedRequest(page.request, modelRequestID)
      expectBridgeMetadata(modelRecord, apiKeyModelsRetrieveBridge)

      const usage = await page.request.get('/api/admin/usage')
      const usageBodyText = (await expectOKBody(usage, 'U.admin_usage')).text
      expect(JSON.parse(usageBodyText)).toMatchObject({
        code: 0,
        msg: 'ok',
        data: {
          request_count: expect.any(Number),
          total_tokens: expect.any(Number),
          cached_input_tokens: expect.any(Number),
          total_cost_usd: 0,
          limits: [],
        },
      })

      const beforeUnsupported = upstream.captured.length
      const unsupported = await page.request.post('/v1/audio/transcriptions', {
        multipart: {
          file: {
            name: 'sample.wav',
            mimeType: 'audio/wav',
            buffer: Buffer.from('not-a-real-audio-file'),
          },
        },
      })
      expect(unsupported.status()).toBe(404)
      const { body, requestID: unsupportedRequestID } = await expectErrorBody(
        unsupported,
        'U.audio_transcriptions',
      )
      expect(body.error).toMatchObject({
        code: 'unsupported_endpoint',
        type: 'router_error',
      })
      const unsupportedRecord = await waitForRecordedRequest(page.request, unsupportedRequestID)
      expectNoBridgeMetadata(unsupportedRecord)
      expect(upstream.captured).toHaveLength(beforeUnsupported)
      expect(codexBackendMock.requests).toHaveLength(0)

      for (const path of ['/backend-api/not-supported']) {
        const rejected = await page.request.get(path)
        expect(rejected.status()).toBe(404)
        expect(rejected.headers()['x-request-id']).toBeTruthy()
        const rejectedBody = (await rejected.json()) as {
          error?: { code?: string; type?: string; request_id?: string }
        }
        expect(rejectedBody.error).toMatchObject({
          code: 'unsupported_endpoint',
          type: 'router_error',
        })
        expect(rejectedBody.error?.request_id).toBeUndefined()
      }
      expect(upstream.captured).toHaveLength(beforeUnsupported)
      expect(codexBackendMock.requests).toHaveLength(0)
    } finally {
      await upstream.close()
    }
  })

  test('covers OAuth Codex models, websocket, transcribe, and admin usage locally', async ({
    page,
    request,
    codexBackendMock,
  }) => {
    codexBackendMock.reset()
    const upstream = await startCompatUpstream()
    try {
      await commitAPIKeySetup(request, {
        accountName: 'compat-oauth-seed',
        apiKey: 'sk-local-seed',
        baseURL: upstream.url,
      })
      await importOAuthAccount(request)
      const disabled = await request.post('/api/admin/accounts/1/disable')
      const disabledBody = (await disabled.json()) as { code?: number }
      expect(disabledBody.code).toBe(0)

      const models = await request.get('/v1/models')
      const { requestID: oauthModelsRequestID, text: modelsBodyText } = await expectOKBody(
        models,
        'O.models_facade',
      )
      expect(JSON.parse(modelsBodyText)).toMatchObject({
        object: 'list',
        data: [expect.objectContaining({ id: 'gpt-5.4-mini', object: 'model' })],
      })
      expect(codexBackendMock.requests.at(-1)?.path).toBe('/codex/models')
      const oauthModelsRecord = await waitForRecordedRequest(request, oauthModelsRequestID)
      expectModelsUnionMetadata(oauthModelsRecord, ['oauth'])

      const oauthResponseJSON = await request.post('/v1/responses', {
        data: {
          model: 'gpt-5.4-mini',
          input: 'Return ok.',
          store: false,
          stream: false,
          temperature: 0.8,
        },
      })
      const { requestID: oauthResponseJSONRequestID, text: oauthResponseJSONText } =
        await expectOKBody(oauthResponseJSON, 'O.responses_json_facade')
      expect(JSON.parse(oauthResponseJSONText).id).toBe('resp_codex_mock')
      const oauthResponseJSONUpstream = codexBackendMock.requests.at(-1)
      expect(oauthResponseJSONUpstream?.path).toBe('/codex/responses')
      const oauthResponseJSONBody = JSON.parse(
        oauthResponseJSONUpstream?.rawBody ?? '{}',
      ) as Record<string, unknown>
      expect(oauthResponseJSONBody.stream).toBe(true)
      expect(oauthResponseJSONBody.store).toBe(false)
      expect(oauthResponseJSONBody.temperature).toBeUndefined()
      const oauthResponseJSONRecord = await waitForRecordedRequest(
        request,
        oauthResponseJSONRequestID,
      )
      expect(oauthResponseJSONRecord.response_mode).toBe('json')
      expectBridgeMetadata(oauthResponseJSONRecord, oauthResponsesBridge)

      const oauthResponseSSE = await request.post('/v1/responses', {
        data: {
          model: 'gpt-5.4-mini',
          input: 'Stream ok.',
          store: false,
          stream: true,
        },
      })
      const { requestID: oauthResponseSSERequestID, text: oauthResponseSSEText } =
        await expectOKBody(oauthResponseSSE, 'O.responses_sse_facade')
      expect(oauthResponseSSE.headers()['content-type']).toContain('text/event-stream')
      expect(oauthResponseSSEText).toContain('response.completed')
      expect(codexBackendMock.requests.at(-1)?.path).toBe('/codex/responses')
      const oauthResponseSSERecord = await waitForRecordedRequest(
        request,
        oauthResponseSSERequestID,
      )
      expect(oauthResponseSSERecord.response_mode).toBe('sse')
      expectBridgeMetadata(oauthResponseSSERecord, oauthResponsesBridge)

      const oauthChatJSON = await request.post('/v1/chat/completions', {
        data: {
          model: 'gpt-5.4-mini',
          messages: [{ role: 'user', content: 'Return ok.' }],
          stream: false,
        },
      })
      const { requestID: oauthChatJSONRequestID, text: oauthChatJSONText } = await expectOKBody(
        oauthChatJSON,
        'O.chat_json_facade',
      )
      expect(JSON.parse(oauthChatJSONText)).toMatchObject({
        object: 'chat.completion',
        model: 'gpt-5.4-mini',
      })
      const oauthChatJSONUpstream = codexBackendMock.requests.at(-1)
      expect(oauthChatJSONUpstream?.path).toBe('/codex/responses')
      const oauthChatJSONBody = JSON.parse(oauthChatJSONUpstream?.rawBody ?? '{}') as Record<
        string,
        unknown
      >
      expect(oauthChatJSONBody.stream).toBe(true)
      expect(oauthChatJSONBody.store).toBe(false)
      const oauthChatJSONRecord = await waitForRecordedRequest(request, oauthChatJSONRequestID)
      expect(oauthChatJSONRecord.response_mode).toBe('json')
      expectBridgeMetadata(oauthChatJSONRecord, oauthChatBridge)

      const oauthChatSSE = await request.post('/v1/chat/completions', {
        data: {
          model: 'gpt-5.4-mini',
          messages: [{ role: 'user', content: 'Stream ok.' }],
          stream: true,
          stream_options: { include_usage: true },
        },
      })
      const { requestID: oauthChatSSERequestID, text: oauthChatSSEText } = await expectOKBody(
        oauthChatSSE,
        'O.chat_sse_facade',
      )
      expect(oauthChatSSE.headers()['content-type']).toContain('text/event-stream')
      expect(oauthChatSSEText).toContain('chat.completion.chunk')
      expect(codexBackendMock.requests.at(-1)?.path).toBe('/codex/responses')
      const oauthChatSSERecord = await waitForRecordedRequest(request, oauthChatSSERequestID)
      expect(oauthChatSSERecord.response_mode).toBe('sse')
      expectBridgeMetadata(oauthChatSSERecord, oauthChatBridge)

      await page.goto('/admin/')
      const openWebSocket = async (path: string) =>
        await page.evaluate(async (targetPath) => {
          const url = `${location.origin.replace(/^http/, 'ws')}${targetPath}`
          return await new Promise<{ message?: string; error?: string }>((resolve) => {
            let settled = false
            let message = ''
            const finish = (result: { message?: string; error?: string }) => {
              if (settled) return
              settled = true
              clearTimeout(timeout)
              resolve(result)
            }
            const timeout = setTimeout(() => finish({ error: 'websocket timeout' }), 5000)
            const ws = new WebSocket(url)
            ws.addEventListener('open', () => {
              ws.send(JSON.stringify({ type: 'response.create', response: { input: 'hi' } }))
            })
            ws.addEventListener('message', (event) => {
              message = String(event.data)
              ws.close()
            })
            ws.addEventListener('close', () => finish({ message }))
            ws.addEventListener('error', () => finish({ error: 'websocket error' }))
          })
        }, path)

      const responsesWSResult = await openWebSocket('/v1/responses')
      expect(responsesWSResult.error).toBeUndefined()
      expect(responsesWSResult.message).toBe('codex websocket reply')
      expect(codexBackendMock.websockets.at(-1)?.headers['openai-beta']).toContain(
        'responses_websockets=2026-02-06',
      )
      expect(codexBackendMock.websockets.at(-1)?.frames[0]).toContain('response.create')
      const responsesWSRecord = await waitForRecordedRequestByPath(
        request,
        '/v1/responses',
        'websocket',
      )
      expectBridgeMetadata(responsesWSRecord, oauthResponsesWebSocketBridge)

      const nativeWSResult = await openWebSocket('/backend-api/codex/responses')
      expect(nativeWSResult.error).toBeUndefined()
      expect(nativeWSResult.message).toBe('codex websocket reply')
      expect(codexBackendMock.websockets.at(-1)?.headers['openai-beta']).toContain(
        'responses_websockets=2026-02-06',
      )
      expect(codexBackendMock.websockets.at(-1)?.frames[0]).toContain('response.create')
      const nativeWSRecord = await waitForRecordedRequestByPath(
        request,
        '/backend-api/codex/responses',
        'websocket',
      )
      expectBridgeMetadata(nativeWSRecord, codexNativeResponsesWebSocketBridge)

      const transcribe = await request.post('/backend-api/transcribe', {
        multipart: {
          file: {
            name: 'sample.wav',
            mimeType: 'audio/wav',
            buffer: Buffer.from('not-a-real-audio-file'),
          },
          prompt: 'short prompt',
        },
      })
      const { requestID: transcribeRequestID, text: transcribeText } = await expectOKBody(
        transcribe,
        'O.transcribe',
      )
      expect(JSON.parse(transcribeText).text).toBe('transcribed locally')
      const transcribeUpstream = codexBackendMock.requests.at(-1)
      expect(transcribeUpstream?.path).toBe('/transcribe')
      expect(transcribeUpstream?.headers['x-request-id']).toBeUndefined()
      expect(transcribeUpstream?.headers.accept).toBeUndefined()
      const transcribeRecord = await waitForRecordedRequest(request, transcribeRequestID)
      expect(transcribeRecord.status_code).toBe(200)
      expect(transcribeRecord.response_mode).toBe('json')
      expect(transcribeRecord.token_usage ?? null).toBeNull()
      expectBridgeMetadata(transcribeRecord, codexNativeTranscribeBridge)

      const codexUsage = await request.get('/api/admin/usage')
      const usageText = (await expectOKBody(codexUsage, 'O.admin_usage')).text
      expect(JSON.parse(usageText)).toMatchObject({
        code: 0,
        msg: 'ok',
        data: {
          codex: {
            plan_type: 'chatgpt-plus',
            rate_limit: null,
            credits: null,
            additional_rate_limits: [],
          },
        },
      })
    } finally {
      await upstream.close()
    }
  })
})
