import type { APIResponse } from '@playwright/test'

import { expect, test } from './fixtures'
import { commitAPIKeySetup } from './helpers'

test.use({ screenshot: 'off', trace: 'off', video: 'off' })

const requiredEnv = [
  'LIVE_UPSTREAM_SMOKE',
  'LIVE_OPENAI_API_KEY',
  'LIVE_OPENAI_BASE_URL',
  'LIVE_OPENAI_MODEL',
] as const

interface LiveCompatCase {
  name: string
  assertion: 'id' | 'json_output' | 'text'
  optional?: boolean
  stream?: boolean
  payload: (model: string) => Record<string, unknown>
}

function liveEnvReady(): boolean {
  return (
    process.env.LIVE_UPSTREAM_SMOKE === '1' &&
    requiredEnv.every((key) => Boolean(process.env[key]?.trim()))
  )
}

function liveValue(key: (typeof requiredEnv)[number]): string {
  const value = process.env[key]?.trim()
  if (!value) {
    throw new Error(`${key} is required`)
  }
  return value
}

function optionalLiveValue(key: string): string | undefined {
  const value = process.env[key]?.trim()
  return value ? value : undefined
}

function liveChatModel(): string {
  return optionalLiveValue('LIVE_OPENAI_CHAT_MODEL') ?? liveValue('LIVE_OPENAI_MODEL')
}

function liveCompatCases(): LiveCompatCase[] {
  const cases: LiveCompatCase[] = [
    {
      name: 'R.text_input_string',
      assertion: 'text',
      payload: (model) => ({
        model,
        input: 'Reply with the single word pong.',
        max_output_tokens: 16,
      }),
    },
    {
      name: 'R.text_input_list',
      assertion: 'text',
      payload: (model) => ({
        model,
        input: [
          {
            role: 'user',
            content: [{ type: 'input_text', text: 'Reply with the single word pong.' }],
          },
        ],
        max_output_tokens: 16,
      }),
    },
    {
      name: 'R.streaming',
      assertion: 'id',
      stream: true,
      payload: (model) => ({
        model,
        input: 'Reply with the single word pong.',
        max_output_tokens: 16,
        stream: true,
      }),
    },
    {
      name: 'R.text_format_json_object',
      assertion: 'json_output',
      payload: (model) => ({
        model,
        input: 'Return exactly {"ok":true} as JSON.',
        max_output_tokens: 64,
        text: { format: { type: 'json_object' } },
      }),
    },
    {
      name: 'R.reasoning_effort_low',
      assertion: 'id',
      payload: (model) => ({
        model,
        input: 'Solve 1+1. Reply with only the number.',
        max_output_tokens: 32,
        reasoning: { effort: 'low' },
      }),
    },
  ]

  const imageURL = optionalLiveValue('LIVE_OPENAI_IMAGE_URL')
  if (imageURL) {
    cases.push({
      name: 'R.image_input_url',
      assertion: 'text',
      optional: true,
      payload: (model) => ({
        model,
        input: [
          {
            role: 'user',
            content: [
              { type: 'input_text', text: 'Describe this image in one short phrase.' },
              { type: 'input_image', image_url: imageURL },
            ],
          },
        ],
        max_output_tokens: 64,
      }),
    })
  }

  if (process.env.LIVE_OPENAI_ENABLE_WEB_SEARCH === '1') {
    cases.push({
      name: 'T.web_search',
      assertion: 'text',
      optional: true,
      payload: (model) => ({
        model,
        input: 'Search for one current headline and summarize it in one sentence.',
        max_output_tokens: 96,
        tools: [{ type: 'web_search' }],
      }),
    })
  }

  return cases
}

function extractChatCompletionText(body: unknown): string {
  if (!body || typeof body !== 'object') {
    return ''
  }
  const response = body as {
    choices?: Array<{ message?: { content?: unknown } }>
  }
  return (
    response.choices
      ?.map((choice) => (typeof choice.message?.content === 'string' ? choice.message.content : ''))
      .join('')
      .trim() ?? ''
  )
}

function extractOutputText(body: unknown): string {
  if (!body || typeof body !== 'object') {
    return ''
  }
  const response = body as {
    output_text?: unknown
    output?: Array<{ content?: Array<{ text?: unknown; type?: unknown }> }>
  }
  if (typeof response.output_text === 'string') {
    return response.output_text
  }
  const parts: string[] = []
  for (const item of response.output ?? []) {
    for (const content of item.content ?? []) {
      if (typeof content.text === 'string') {
        parts.push(content.text)
      }
    }
  }
  return parts.join('')
}

async function expectLiveOK(response: APIResponse, name: string) {
  const text = await response.text()
  expect(response.status(), `${name} failed with body: ${text.slice(0, 1000)}`).toBe(200)
  const requestID = response.headers()['x-request-id']
  expect(requestID, `${name} missing x-request-id`).toBeTruthy()
  if (!requestID) {
    throw new Error(`${name} missing x-request-id`)
  }
  return { requestID, text }
}

function isOptionalUnsupported(status: number, text: string): boolean {
  if (![400, 404, 422].includes(status)) {
    return false
  }
  try {
    const body = JSON.parse(text) as { error?: { type?: string } }
    return body.error?.type !== 'router_error'
  } catch {
    return true
  }
}

function assertLiveJSONBody(body: unknown, liveCase: LiveCompatCase) {
  expect(body, `${liveCase.name} response must be an object`).toEqual(expect.any(Object))
  const response = body as { id?: unknown }
  expect(response.id, `${liveCase.name} missing response id`).toBeTruthy()

  if (liveCase.assertion === 'id') {
    return
  }
  const outputText = extractOutputText(body).trim()
  expect(outputText, `${liveCase.name} missing output text`).not.toBe('')
  if (liveCase.assertion === 'json_output') {
    expect(() => JSON.parse(outputText), `${liveCase.name} output is not valid JSON`).not.toThrow()
  }
}

test.describe('live upstream compat smoke', () => {
  test.skip(!liveEnvReady(), 'live upstream smoke is opt-in')

  test('runs a real API-key /v1 compatibility matrix', async ({ page }) => {
    const baseURL = liveValue('LIVE_OPENAI_BASE_URL')
    const model = liveValue('LIVE_OPENAI_MODEL')

    await commitAPIKeySetup(page.request, {
      accountName: 'live-compat',
      apiKey: liveValue('LIVE_OPENAI_API_KEY'),
      baseURL,
    })

    let completedCaseCount = 0
    await test.step('M.models_list', async () => {
      const response = await page.request.get('/v1/models')
      const { text } = await expectLiveOK(response, 'M.models_list')
      const body = JSON.parse(text) as { object?: unknown; data?: unknown }
      expect(body.object).toBe('list')
      expect(Array.isArray(body.data), 'M.models_list data must be an array').toBe(true)
      completedCaseCount += 1
    })

    const chatModel = liveChatModel()
    await test.step('C.chat_completions_json', async () => {
      const response = await page.request.post('/v1/chat/completions', {
        data: {
          model: chatModel,
          messages: [{ role: 'user', content: 'Reply with the single word pong.' }],
          max_completion_tokens: 16,
        },
      })
      const { text } = await expectLiveOK(response, 'C.chat_completions_json')
      const body = JSON.parse(text) as { id?: unknown; object?: unknown; choices?: unknown }
      expect(body.id).toBeTruthy()
      expect(body.object).toBe('chat.completion')
      expect(Array.isArray(body.choices), 'C.chat_completions_json choices must be an array').toBe(
        true,
      )
      expect(extractChatCompletionText(body), 'C.chat_completions_json missing content').not.toBe(
        '',
      )
      completedCaseCount += 1
    })

    await test.step('C.chat_completions_streaming', async () => {
      const response = await page.request.post('/v1/chat/completions', {
        data: {
          model: chatModel,
          messages: [{ role: 'user', content: 'Reply with the single word pong.' }],
          max_completion_tokens: 16,
          stream: true,
        },
      })
      const { text } = await expectLiveOK(response, 'C.chat_completions_streaming')
      expect(response.headers()['content-type']).toContain('text/event-stream')
      expect(text).toContain('data:')
      expect(text).toContain('chat.completion.chunk')
      expect(text).toContain('[DONE]')
      completedCaseCount += 1
    })

    let streamRequestID: string | null = null
    const cases = liveCompatCases()
    for (const liveCase of cases) {
      await test.step(liveCase.name, async () => {
        const response = await page.request.post('/v1/responses', {
          data: liveCase.payload(model),
        })
        if (liveCase.optional && response.status() >= 400) {
          const text = await response.text()
          if (isOptionalUnsupported(response.status(), text)) {
            test.info().annotations.push({
              type: 'optional-skip',
              description: `${liveCase.name} returned ${response.status()}: ${text.slice(0, 240)}`,
            })
            return
          }
          expect(
            response.status(),
            `${liveCase.name} failed with body: ${text.slice(0, 1000)}`,
          ).toBe(200)
        }
        const { requestID, text } = await expectLiveOK(response, liveCase.name)
        completedCaseCount += 1
        if (liveCase.stream) {
          expect(response.headers()['content-type']).toContain('text/event-stream')
          expect(text).toContain('data:')
          expect(text).toMatch(/response\.(completed|output_text\.delta)/)
          streamRequestID = requestID
          return
        }
        assertLiveJSONBody(JSON.parse(text), liveCase)
      })
    }

    await test.step('U.audio_transcriptions_deferred', async () => {
      const response = await page.request.post('/v1/audio/transcriptions', {
        multipart: {
          file: {
            name: 'sample.wav',
            mimeType: 'audio/wav',
            buffer: Buffer.from('not-a-real-audio-file'),
          },
        },
      })
      const text = await response.text()
      expect(response.status(), text.slice(0, 1000)).toBe(404)
      expect(
        response.headers()['x-request-id'],
        'unsupported audio missing x-request-id',
      ).toBeTruthy()
      const body = JSON.parse(text) as { error?: { code?: unknown; type?: unknown } }
      expect(body.error).toMatchObject({
        code: 'unsupported_endpoint',
        type: 'router_error',
      })
    })

    await test.step('U.admin_usage_local', async () => {
      await expect
        .poll(async () => {
          const response = await page.request.get('/api/admin/usage')
          if (response.status() !== 200) return -1
          const body = (await response.json()) as {
            code?: number
            data?: { request_count?: number }
          }
          if (body.code !== 0) return -1
          return body.data?.request_count ?? 0
        })
        .toBeGreaterThanOrEqual(completedCaseCount)
    })

    expect(streamRequestID).toBeTruthy()
    if (!streamRequestID) {
      throw new Error('R.streaming did not return x-request-id')
    }

    await expect
      .poll(async () => {
        const response = await page.request.get(
          `/api/admin/requests?search=${encodeURIComponent(streamRequestID)}`,
        )
        const body = (await response.json()) as {
          code: number
          data?: {
            records?: Array<{
              request_id: string
              response_mode: string
              ttft_ms: number | null
            }>
          }
        }
        if (body.code !== 0) return null
        return body.data?.records?.find((record) => record.request_id === streamRequestID) ?? null
      })
      .toMatchObject({
        request_id: streamRequestID,
        response_mode: 'sse',
        ttft_ms: expect.any(Number),
      })

    const dashboardResponse = await page.request.get('/api/admin/dashboard?range=1h')
    expect(dashboardResponse.status()).toBe(200)
    const dashboard = (await dashboardResponse.json()) as {
      code: number
      data?: { cards?: { requests?: { total?: number }; ttft?: { sample_count?: number } } }
    }
    expect(dashboard.code).toBe(0)
    expect(dashboard.data?.cards?.requests?.total).toBeGreaterThanOrEqual(completedCaseCount)
    expect(dashboard.data?.cards?.ttft?.sample_count).toBeGreaterThanOrEqual(1)
  })
})
