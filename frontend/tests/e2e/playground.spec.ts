import type { BrowserContext, Route } from '@playwright/test'

import { expect, test } from './fixtures'
import { completeSetupWizard } from './helpers'

type AccountListItem = {
  id: number
  name: string
  provider: string
  auth_method: 'api_key' | 'oauth_browser' | 'oauth_device' | 'oauth_import'
  status: 'active' | 'disabled'
  email?: string
  plan_type?: string
  plan_type_label?: string
}

function okEnvelope(data: unknown): string {
  return JSON.stringify({
    code: 0,
    msg: 'ok',
    data,
  })
}

function errorEnvelope(code: number, msg: string, data: Record<string, unknown> = {}): string {
  return JSON.stringify({
    code,
    msg,
    data,
  })
}

function activeAccounts(): AccountListItem[] {
  return [
    {
      id: 42,
      name: 'plus-account',
      provider: 'openai',
      auth_method: 'oauth_browser',
      status: 'active',
      email: 'plus@example.com',
      plan_type: 'chatgpt-plus',
      plan_type_label: 'ChatGPT Plus',
    },
    {
      id: 84,
      name: 'api-key-fallback',
      provider: 'openai',
      auth_method: 'api_key',
      status: 'active',
    },
  ]
}

async function mockAccounts(context: BrowserContext, accounts: AccountListItem[]) {
  await context.route('**/api/admin/accounts', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: okEnvelope({
        accounts,
        total: accounts.length,
      }),
    })
  })
}

async function mockPlaygroundRun(
  context: BrowserContext,
  handler: (route: Route) => Promise<void> | void,
) {
  await context.route('**/api/admin/playground/run', handler)
}

test.describe('playground', () => {
  test.beforeEach(async ({ page }) => {
    await completeSetupWizard(page)
  })

  test('automatic mode renders a successful probe result', async ({ page, context }) => {
    const requests: Array<Record<string, unknown>> = []
    await mockAccounts(context, activeAccounts())
    await mockPlaygroundRun(context, async (route) => {
      requests.push((route.request().postDataJSON() as Record<string, unknown>) ?? {})
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: okEnvelope({
          run: {
            selection_mode: 'auto',
            outcome: 'success',
            latency_ms: 123,
          },
          account: {
            id: 42,
            name: 'plus-account',
            provider: 'openai',
            auth_method: 'oauth_browser',
            status: 'active',
          },
          upstream: {
            status_code: 200,
            response_mode: 'json',
          },
          output: {
            text: 'Hello from playground.',
            text_available: true,
            raw_response_available: false,
          },
          usage: {
            input: 4,
            output: 3,
          },
        }),
      })
    })

    await page.goto('/admin/playground')
    await expect(page).toHaveURL(/\/admin\/playground$/)
    await expect(page.getByTestId('playground-model-input')).toHaveValue('gpt-5.4-mini')

    const responsePromise = page.waitForResponse('**/api/admin/playground/run')
    await page.getByTestId('playground-textarea').fill('hello')
    await page.getByTestId('playground-submit').click()
    const response = await responsePromise

    expect(response.status()).toBe(200)
    expect(requests).toEqual([
      {
        selection_mode: 'auto',
        model: 'gpt-5.4-mini',
        text: 'hello',
        max_output_tokens: 1024,
        include_raw_response: false,
      },
    ])
    await expect(page.getByTestId('playground-result')).toContainText('Hello from playground.')
    await expect(page.getByTestId('playground-result')).toContainText('plus-account')
    await expect(page.getByTestId('playground-result')).toContainText('123 ms')
  })

  test('account mode submits the selected active account without falling back', async ({
    page,
    context,
  }) => {
    const requests: Array<Record<string, unknown>> = []
    await mockAccounts(context, activeAccounts())
    await mockPlaygroundRun(context, async (route) => {
      requests.push((route.request().postDataJSON() as Record<string, unknown>) ?? {})
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: okEnvelope({
          run: {
            selection_mode: 'account',
            outcome: 'success',
            latency_ms: 98,
          },
          account: {
            id: 42,
            name: 'plus-account',
            provider: 'openai',
            auth_method: 'oauth_browser',
            status: 'active',
          },
          upstream: {
            status_code: 200,
            response_mode: 'json',
          },
          output: {
            text: 'Account-specific reply.',
            text_available: true,
            raw_response_available: false,
          },
          usage: {
            input: 8,
            output: 5,
          },
        }),
      })
    })

    await page.goto('/admin/playground')
    await page.getByRole('button', { name: 'Account' }).click()
    await page.getByTestId('playground-textarea').fill('probe this account')
    await page.getByTestId('playground-submit').click()

    await expect.poll(() => requests.length).toBe(1)
    expect(requests[0]).toMatchObject({
      selection_mode: 'account',
      account_id: 42,
      model: 'gpt-5.4-mini',
      text: 'probe this account',
    })
    await expect(page.getByTestId('playground-result')).toContainText('Account-specific reply.')
    await expect(page.getByTestId('playground-result')).toContainText('plus-account')
  })

  test('no active accounts shows the empty state and direct API returns 4002', async ({
    page,
    context,
  }) => {
    await mockAccounts(context, [])
    await mockPlaygroundRun(context, async (route) => {
      await route.fulfill({
        status: 200,
        headers: { 'X-Request-Id': 'req_playground_no_active' },
        contentType: 'application/json',
        body: errorEnvelope(4002, 'playground_no_active_account'),
      })
    })

    await page.goto('/admin/playground')
    await expect(page.getByTestId('playground-no-active')).toContainText(
      'No active accounts available.',
    )
    await expect(page.getByTestId('playground-new-account-link')).toHaveAttribute(
      'href',
      '/admin/accounts/new',
    )
    await expect(page.getByTestId('playground-submit')).toBeDisabled()

    const directRun = await page.evaluate(async () => {
      const response = await fetch('/api/admin/playground/run', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          selection_mode: 'auto',
          model: 'gpt-5.4-mini',
          text: 'hello',
        }),
      })
      return {
        status: response.status,
        body: await response.json(),
      }
    })

    expect(directRun).toMatchObject({
      status: 200,
      body: {
        code: 4002,
        msg: 'playground_no_active_account',
        data: {},
      },
    })
  })

  test('account mode surfaces 4003 when the selected account becomes unavailable', async ({
    page,
    context,
  }) => {
    await mockAccounts(context, activeAccounts())
    await mockPlaygroundRun(context, async (route) => {
      await route.fulfill({
        status: 200,
        headers: { 'X-Request-Id': 'req_playground_account_unavailable' },
        contentType: 'application/json',
        body: errorEnvelope(4003, 'playground_account_unavailable', {
          requested_account_id: 42,
          reason: 'disabled',
        }),
      })
    })

    await page.goto('/admin/playground')
    await page.getByRole('button', { name: 'Account' }).click()
    await page.getByTestId('playground-textarea').fill('probe this account')
    await page.getByTestId('playground-submit').click()

    await expect(page.getByTestId('error-banner')).toContainText('playground_account_unavailable')
    await expect(page.getByTestId('error-banner')).toContainText(
      'req_playground_account_unavailable',
    )
    await expect(page.getByTestId('playground-textarea')).toHaveValue('probe this account')
    await expect(page.getByRole('link', { name: 'Open account #42' })).toHaveAttribute(
      'href',
      '/admin/accounts/42',
    )
  })

  test('provider errors preserve prompt state, show correlation id, and link to account repair', async ({
    page,
    context,
  }) => {
    await mockAccounts(context, activeAccounts())
    await mockPlaygroundRun(context, async (route) => {
      await route.fulfill({
        status: 200,
        headers: { 'X-Request-Id': 'req_playground_upstream' },
        contentType: 'application/json',
        body: errorEnvelope(4004, 'playground_upstream_error', {
          account_id: 42,
          upstream_status: 401,
          provider_message: 'Bearer sk-secret-token',
        }),
      })
    })

    await page.goto('/admin/playground')
    await page.getByTestId('playground-textarea').fill('hello')
    await page.getByTestId('playground-submit').click()

    await expect(page.getByTestId('error-banner')).toContainText('playground_upstream_error')
    await expect(page.getByTestId('error-banner')).toContainText('req_playground_upstream')
    await expect(page.getByTestId('playground-textarea')).toHaveValue('hello')
    await expect(page.getByRole('link', { name: 'Open account #42' })).toHaveAttribute(
      'href',
      '/admin/accounts/42',
    )
    await expect(page.getByText('[redacted]')).toBeVisible()
    await expect(page.getByText(/sk-secret-token/)).toHaveCount(0)
  })

  test('successful no-text probes show the explicit no-text state and raw JSON', async ({
    page,
    context,
  }) => {
    const requests: Array<Record<string, unknown>> = []
    await mockAccounts(context, activeAccounts())
    await mockPlaygroundRun(context, async (route) => {
      requests.push((route.request().postDataJSON() as Record<string, unknown>) ?? {})
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: okEnvelope({
          run: {
            selection_mode: 'auto',
            outcome: 'no_extractable_text',
            latency_ms: 111,
          },
          account: {
            id: 42,
            name: 'plus-account',
            provider: 'openai',
            auth_method: 'oauth_browser',
            status: 'active',
          },
          upstream: {
            status_code: 200,
            response_mode: 'json',
          },
          output: {
            text: '',
            text_available: false,
            raw_response_available: true,
            raw_response: {
              id: 'resp_no_text',
            },
          },
          usage: {},
        }),
      })
    })

    await page.goto('/admin/playground')
    await page.getByTestId('playground-textarea').fill('hello')
    await page.getByTestId('playground-raw-switch').click()
    await page.getByTestId('playground-submit').click()

    await expect.poll(() => requests.length).toBe(1)
    expect(requests[0]).toMatchObject({
      include_raw_response: true,
    })
    await expect(page.getByTestId('playground-result')).toContainText(
      'No extractable text returned.',
    )
    await page.getByText('Raw response').click()
    await expect(page.getByText(/resp_no_text/)).toBeVisible()
  })
})
