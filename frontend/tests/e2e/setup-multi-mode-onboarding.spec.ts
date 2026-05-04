import type { Page } from '@playwright/test'

import { expect, test } from './fixtures'

async function advanceToUpstreamStep(page: Page) {
  await page.goto('/setup/')
  await page.getByTestId('wizard-next').click()
  await page.getByTestId('wizard-next').click()
  await expect(page.getByRole('heading', { name: 'Upstream account', exact: true })).toBeVisible()
}

async function advanceToCommit(page: Page) {
  await page.getByTestId('wizard-next').click()
  await expect(page.getByRole('heading', { name: 'Plugin intents', exact: true })).toBeVisible()
  await page.getByTestId('wizard-next').click()
  await expect(page.getByRole('heading', { name: 'Commit' })).toBeVisible()
}

async function expectNoOnboardingIntentStorage(page: Page) {
  await expect
    .poll(async () =>
      page.evaluate(() => {
        const intentKeyRE = /(setup|wizard|onboard|auth[_-]?method|oauth|first[_-]?account)/i
        return {
          local: Object.keys(localStorage).filter((key) => intentKeyRE.test(key)),
          session: Object.keys(sessionStorage).filter((key) => intentKeyRE.test(key)),
        }
      }),
    )
    .toEqual({ local: [], session: [] })
}

test.describe('setup multi-mode onboarding', () => {
  test('cold install exposes the same four auth methods on the setup upstream step', async ({
    page,
  }) => {
    await advanceToUpstreamStep(page)

    const methods = page.locator('[data-testid^="wizard-auth-method-"]')
    await expect(methods).toHaveCount(4)
    await expect(methods.nth(0)).toHaveAttribute('data-auth-method', 'api_key')
    await expect(methods.nth(1)).toHaveAttribute('data-auth-method', 'oauth_browser')
    await expect(methods.nth(2)).toHaveAttribute('data-auth-method', 'oauth_device')
    await expect(methods.nth(3)).toHaveAttribute('data-auth-method', 'oauth_import')
    await expect(methods.nth(0).getByRole('radio')).toBeChecked()
    await expect(methods.nth(1).getByRole('radio')).not.toBeChecked()
  })

  test('API-key setup path seeds the first account inline', async ({ page }) => {
    await advanceToUpstreamStep(page)
    await page.getByTestId('account.api_key').fill('sk-setup-seed')

    await advanceToCommit(page)
    await page.getByTestId('wizard-commit').click()

    await expect(page).toHaveURL(/\/admin\/?$/, { timeout: 10_000 })
    const response = await page.request.get('/api/admin/accounts')
    expect(response.ok()).toBeTruthy()
    const body = (await response.json()) as {
      code: number
      data: {
        accounts: Array<{
          name: string
          provider: string
          auth_method: string
          status: string
        }>
      }
    }
    expect(body.code).toBe(0)
    expect(body.data.accounts).toHaveLength(1)
    expect(body.data.accounts[0]).toMatchObject({
      name: 'primary',
      provider: 'openai',
      auth_method: 'api_key',
      status: 'active',
    })
    await expectNoOnboardingIntentStorage(page)
  })

  test('browser OAuth selection commits setup and hands off into the browser onboarding route', async ({
    page,
  }) => {
    await advanceToUpstreamStep(page)
    await page.getByTestId('wizard-auth-method-oauth_browser').click()

    await advanceToCommit(page)
    await page.getByTestId('wizard-commit').click()

    await expect(page).toHaveURL(/\/admin\/accounts\/new-oauth\?from=setup$/, { timeout: 10_000 })
    await expect(page.getByTestId('oauth-start-button')).toBeVisible()
    await expectNoOnboardingIntentStorage(page)
  })

  test('device OAuth selection commits setup and hands off into the device onboarding route', async ({
    page,
  }) => {
    await advanceToUpstreamStep(page)
    await page.getByTestId('wizard-auth-method-oauth_device').click()

    await advanceToCommit(page)
    await page.getByTestId('wizard-commit').click()

    await expect(page).toHaveURL(/\/admin\/accounts\/new-oauth-device\?from=setup$/, {
      timeout: 10_000,
    })
    await expect(page.getByTestId('device-flow-panel')).toBeVisible()
    await expectNoOnboardingIntentStorage(page)
  })

  test('auth.json import selection commits setup and hands off into the import route', async ({
    page,
  }) => {
    await advanceToUpstreamStep(page)
    await page.getByTestId('wizard-auth-method-oauth_import').click()

    await advanceToCommit(page)
    await page.getByTestId('wizard-commit').click()

    await expect(page).toHaveURL(/\/admin\/accounts\/new-import\?from=setup$/, { timeout: 10_000 })
    await expect(page.getByTestId('import-auth-json-input')).toBeVisible()
    await expect(page.getByTestId('import-auth-json-submit')).toBeVisible()
    await expectNoOnboardingIntentStorage(page)
  })

  test('skip-for-now commits a degraded install without seeding an account', async ({ page }) => {
    await advanceToUpstreamStep(page)
    await page.getByTestId('wizard-skip-account').click()

    await expect(page.getByRole('heading', { name: 'Plugin intents', exact: true })).toBeVisible()
    await page.getByTestId('wizard-next').click()
    await page.getByTestId('wizard-commit').click()

    await expect(page).toHaveURL(/\/admin\/?$/, { timeout: 10_000 })
    await expect(page.getByTestId('no-healthy-accounts-banner')).toBeVisible()
    const response = await page.request.get('/api/admin/accounts')
    expect(response.ok()).toBeTruthy()
    const body = (await response.json()) as {
      code: number
      data: { accounts: unknown[] }
    }
    expect(body.code).toBe(0)
    expect(body.data.accounts).toHaveLength(0)
    await expectNoOnboardingIntentStorage(page)
  })
})
