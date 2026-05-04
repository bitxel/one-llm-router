import { expect, test } from './fixtures'
import { buildAuthJSONFixture, completeSetupWizard, listAccountRows } from './helpers'

test.describe('auth.json import', () => {
  test.beforeEach(async ({ page }) => {
    await completeSetupWizard(page)
    await page.goto('/admin/accounts/new')
    await page.locator('[data-auth-method="oauth_import"]').click()
    await expect(page).toHaveURL(/\/admin\/accounts\/new-import$/)
  })

  test('imports valid auth.json, ignores unknown keys, and rejects malformed payloads with envelope codes', async ({
    page,
  }) => {
    const initialCount = listAccountRows().length
    const validPayload = JSON.parse(
      buildAuthJSONFixture({
        email: 'import-plus@example.com',
        planType: 'chatgpt-plus',
        accountID: 'acct_import_plus',
      }),
    ) as Record<string, unknown>
    validPayload.extra_top_level = { ignored: true }

    const validResponsePromise = page.waitForResponse('**/api/admin/accounts/import-auth-json')
    await page.getByTestId('import-auth-json-input').setInputFiles({
      name: 'auth.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(validPayload)),
    })
    await page.getByTestId('import-auth-json-submit').click()
    const validResponse = await validResponsePromise
    expect(validResponse.status()).toBe(200)
    const validBody = (await validResponse.json()) as {
      code: number
      data: { account: { id: number; auth_method: string } }
    }
    expect(validBody).toMatchObject({
      code: 0,
      msg: 'ok',
      data: {
        account: {
          auth_method: 'oauth_import',
        },
      },
    })
    await expect(page).toHaveURL(new RegExp(`/admin/accounts/${validBody.data.account.id}$`))
    await expect(page.getByTestId('account-detail-view')).toBeVisible()
    await expect(page.getByTestId('account-oauth-metadata')).toContainText('ChatGPT Plus')
    expect(listAccountRows()).toHaveLength(initialCount + 1)

    const stableCount = listAccountRows().length

    await page.goto('/admin/accounts/new-import')

    const missingRefresh = JSON.parse(
      buildAuthJSONFixture({
        email: 'import-invalid@example.com',
        planType: 'chatgpt-team',
        accountID: 'acct_import_invalid',
      }),
    ) as { tokens: Record<string, unknown> }
    delete missingRefresh.tokens.refresh_token

    const invalidResponsePromise = page.waitForResponse('**/api/admin/accounts/import-auth-json')
    await page.getByTestId('import-auth-json-input').setInputFiles({
      name: 'missing-refresh.json',
      mimeType: 'application/json',
      buffer: Buffer.from(JSON.stringify(missingRefresh)),
    })
    await page.getByTestId('import-auth-json-submit').click()
    const invalidResponse = await invalidResponsePromise
    expect(invalidResponse.status()).toBe(200)
    expect(await invalidResponse.json()).toMatchObject({
      code: 3011,
      msg: 'invalid_auth_json',
      data: {
        missing_fields: ['tokens.refresh_token'],
      },
    })
    await expect(page.getByTestId('error-banner')).toContainText('invalid_auth_json')
    expect(listAccountRows()).toHaveLength(stableCount)

    const oversizedPart = buildAuthJSONFixture({
      email: 'import-too-big@example.com',
      planType: 'chatgpt-plus',
      accountID: 'acct_import_big',
      accessToken: `fixture_${'x'.repeat(20_000)}`,
    })
    const partResponsePromise = page.waitForResponse('**/api/admin/accounts/import-auth-json')
    await page.getByTestId('import-auth-json-input').setInputFiles({
      name: 'oversized-part.json',
      mimeType: 'application/json',
      buffer: Buffer.from(oversizedPart),
    })
    await page.getByTestId('import-auth-json-submit').click()
    const partResponse = await partResponsePromise
    expect(partResponse.status()).toBe(200)
    expect(await partResponse.json()).toMatchObject({
      code: 2009,
      msg: 'request_body_too_large',
      data: {
        scope: 'part',
        limit_bytes: 16384,
      },
    })
    await expect(page.getByTestId('error-banner')).toContainText('request_body_too_large')
    expect(listAccountRows()).toHaveLength(stableCount)

    const envelopeResponse = await page.evaluate(async () => {
      const boundary = '----router-envelope-test'
      const validPart = JSON.stringify({
        OPENAI_API_KEY: null,
        tokens: {
          access_token: 'fixture_envelope_access',
          refresh_token: 'fixture_envelope_refresh',
          id_token: 'fixture_envelope_id',
          account_id: 'acct_envelope',
        },
        last_refresh: '2026-04-22T08:00:00Z',
      })
      const junk = 'x'.repeat(70_000)
      const body =
        `${junk}\r\n--${boundary}\r\n` +
        'Content-Disposition: form-data; name="auth_json"; filename="auth.json"\r\n' +
        'Content-Type: application/json\r\n\r\n' +
        `${validPart}\r\n--${boundary}--\r\n`

      const response = await fetch('/api/admin/accounts/import-auth-json', {
        method: 'POST',
        headers: {
          'Content-Type': `multipart/form-data; boundary=${boundary}`,
        },
        body,
      })
      return {
        status: response.status,
        body: await response.json(),
      }
    })
    expect(envelopeResponse.status).toBe(200)
    expect(envelopeResponse.body).toMatchObject({
      code: 2009,
      msg: 'request_body_too_large',
      data: {
        scope: 'envelope',
        limit_bytes: 65536,
      },
    })
    expect(listAccountRows()).toHaveLength(stableCount)
  })
})
