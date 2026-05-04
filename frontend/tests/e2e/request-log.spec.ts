import { createServer } from 'node:http'
import type { AddressInfo } from 'node:net'
import type { BrowserContext, Route } from '@playwright/test'

import { expect, test } from './fixtures'
import { completeSetupWizard } from './helpers'

function okEnvelope(data: unknown): string {
  return JSON.stringify({
    code: 0,
    msg: 'ok',
    data,
  })
}

async function mockRequestLog(context: BrowserContext, onList?: (route: Route) => void) {
  await context.route('**/api/admin/requests/options**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: okEnvelope({
        accounts: [
          {
            id: 7,
            label: 'plus-account',
            account: {
              id: 7,
              name: 'plus-account',
              provider: 'openai',
              auth_method: 'oauth_browser',
              status: 'active',
              created_at: '2026-04-25T01:00:00Z',
              updated_at: '2026-04-25T01:00:00Z',
            },
          },
        ],
        outcomes: ['success', 'upstream_error'],
        models: ['gpt-5.4-mini'],
        response_modes: ['json', 'sse'],
      }),
    })
  })
  await context.route('**/api/admin/requests/13', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: okEnvelope({
        record: {
          id: 13,
          request_id: 'req_e2e_13',
          created_at: '2026-04-25T02:00:00Z',
          client_ip: '203.0.113.13',
          upstream_account_id: 7,
          account: {
            id: 7,
            name: 'plus-account',
            provider: 'openai',
            auth_method: 'oauth_browser',
            status: 'active',
            created_at: '2026-04-25T01:00:00Z',
            updated_at: '2026-04-25T01:00:00Z',
          },
          session_key: 'sess-e2e',
          method: 'POST',
          path: '/v1/responses',
          status_code: 200,
          latency_ms: 45,
          outcome: 'success',
          model: 'gpt-5.4-mini',
          model_params: { service_tier: 'default' },
          router_metadata: {
            bridge: {
              op_id: 'op.openai.responses.create',
              bridge_id: 'bridge.openai.responses.to_codex',
            },
          },
          response_mode: 'json',
          token_usage: { total_tokens: 17 },
          client_request_body: '{"prompt":"hello from e2e"}',
          upstream_request_body: '{"input":"hello from e2e"}',
          upstream_response_body: '{"text":"world from e2e"}',
        },
      }),
    })
  })
  await context.route(/\/api\/admin\/requests(?:\?.*)?$/, async (route) => {
    onList?.(route)
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: okEnvelope({
        records: [
          {
            id: 13,
            request_id: 'req_e2e_13',
            created_at: '2026-04-25T02:00:00Z',
            client_ip: '203.0.113.13',
            upstream_account_id: 7,
            account: {
              id: 7,
              name: 'plus-account',
              provider: 'openai',
              auth_method: 'oauth_browser',
              status: 'active',
              created_at: '2026-04-25T01:00:00Z',
              updated_at: '2026-04-25T01:00:00Z',
            },
            session_key: 'sess-e2e',
            method: 'POST',
            path: '/v1/responses',
            status_code: 200,
            latency_ms: 45,
            outcome: 'success',
            model: 'gpt-5.4-mini',
            model_params: { service_tier: 'default' },
            router_metadata: {
              bridge: {
                op_id: 'op.openai.responses.create',
                bridge_id: 'bridge.openai.responses.to_codex',
              },
            },
            response_mode: 'json',
            token_usage: { total_tokens: 17 },
          },
        ],
        has_more: false,
      }),
    })
  })
}

async function startRequestLogUpstream(): Promise<{
  url: string
  close: () => Promise<void>
}> {
  const server = createServer((req, res) => {
    if (req.method !== 'POST' || req.url !== '/v1/responses') {
      res.statusCode = 404
      res.end('not found')
      return
    }
    req.resume()
    res.setHeader('Content-Type', 'application/json')
    res.end(
      JSON.stringify({
        id: 'resp_request_log_real',
        access_token: 'secret-access-token',
        output_text: 'ok',
        usage: { input_tokens: 1, output_tokens: 2 },
      }),
    )
  })
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  const address = server.address() as AddressInfo
  return {
    url: `http://127.0.0.1:${address.port}`,
    close: () => new Promise<void>((resolve) => server.close(() => resolve())),
  }
}

test.describe('request log', () => {
  test('filters rows and opens captured request/response bodies', async ({ page, context }) => {
    await completeSetupWizard(page)
    const listURLs: string[] = []
    await mockRequestLog(context, (route) => {
      listURLs.push(route.request().url())
    })

    await page.getByTestId('sidebar-requests').click()
    await expect(page).toHaveURL(/\/admin\/requests$/)
    await expect(page.getByTestId('requests-table')).toContainText('req_e2e_13')
    await expect(page.getByTestId('requests-table')).toContainText('203.0.113.13')
    await expect(page.getByTestId('requests-table')).toContainText('plus-account')

    await page.getByTestId('requests-advanced-filter-toggle').click()
    await page.getByTestId('requests-search-input').fill('req_e2e')
    await page.getByTestId('requests-search-apply').click()

    await expect.poll(() => listURLs.some((url) => url.includes('search=req_e2e'))).toBe(true)

    await page.getByTestId('request-open-13').click()
    await expect(page.getByTestId('request-detail-layer')).toBeVisible()
    await expect(page.getByTestId('request-detail-layer')).toContainText('req_e2e_13')
    await expect(page.getByTestId('request-detail-view')).toContainText('hello from e2e')
    await expect(page.getByTestId('request-detail-view')).toContainText('203.0.113.13')
    await expect(page.getByTestId('request-detail-view')).toContainText('input')
    await expect(page.getByTestId('request-detail-view')).toContainText('world from e2e')
    await expect(page.getByTestId('request-detail-view')).toContainText('Router metadata')
    await expect(page.getByTestId('request-detail-view')).toContainText(
      'op.openai.responses.create',
    )
  })

  test('records a real proxy request and redacts captured bodies', async ({ page }) => {
    const upstream = await startRequestLogUpstream()
    try {
      await completeSetupWizard(page, { baseURL: upstream.url })

      const settingsResponse = await page.request.post('/api/admin/settings/update', {
        data: {
          runtime: {
            log_client_request_body: true,
            log_upstream_request_body: true,
            log_upstream_response_body: true,
          },
        },
      })
      expect(settingsResponse.status()).toBe(200)
      expect((await settingsResponse.json()).code).toBe(0)

      const proxyResponse = await page.request.post('/v1/responses', {
        headers: {
          'X-Forwarded-For': '198.51.100.99',
        },
        data: {
          model: 'gpt-4o-mini',
          input: 'hello sk-request-secret-123456',
        },
      })
      expect(proxyResponse.status()).toBe(200)
      const requestID = proxyResponse.headers()['x-request-id']
      expect(requestID).toBeTruthy()

      await expect
        .poll(async () => {
          const response = await page.request.get(
            `/api/admin/requests?search=${encodeURIComponent(requestID)}`,
          )
          const body = (await response.json()) as {
            code: number
            data?: { records?: Array<{ request_id: string; client_ip: string }> }
          }
          if (body.code !== 0) return 0
          const record = (body.data?.records ?? []).find((item) => item.request_id === requestID)
          if (!record || record.client_ip === '' || record.client_ip === '198.51.100.99') return 0
          return 1
        })
        .toBe(1)

      await page.goto(`/admin/requests?search=${encodeURIComponent(requestID)}`)
      await expect(page.getByTitle(requestID)).toBeVisible()
      await expect(page.getByTestId('requests-table')).not.toContainText('198.51.100.99')
      await expect(page.getByTestId('requests-table')).not.toContainText('sk-request-secret')

      await page.getByRole('button', { name: `Open request detail ${requestID}` }).click()
      await expect(page.getByTestId('request-detail-layer')).toBeVisible()
      const detail = page.getByTestId('request-detail-view')
      await expect(detail).toContainText('[redacted]')
      await expect(detail).toContainText('Router metadata')
      await expect(detail).not.toContainText('sk-request-secret')
      await expect(detail).not.toContainText('secret-access-token')
    } finally {
      await upstream.close()
    }
  })
})
