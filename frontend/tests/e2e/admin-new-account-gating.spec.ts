import type { Page } from '@playwright/test'

import { expect, test } from './fixtures'

async function completeSetupWizard(page: Page) {
  await page.goto('/setup/')
  await page.getByTestId('wizard-next').click()
  await expect(page.getByTestId('wizard-probe')).toHaveCount(0)
  await page.getByTestId('wizard-next').click()
  await page.getByTestId('account.api_key').fill('sk-e2e-placeholder-key')
  await page.getByTestId('wizard-next').click()
  await page.getByTestId('wizard-next').click()
  await page.getByTestId('wizard-commit').click()
  await expect(page).toHaveURL(/\/admin/, { timeout: 10_000 })
}

test.describe('admin new-account onboarding', () => {
  test.beforeEach(async ({ page }) => {
    await completeSetupWizard(page)
  })

  test('shows all four account onboarding cards as live', async ({ page }) => {
    await page.goto('/admin/accounts/new')

    await expect(page.locator('[data-auth-method="api_key"]')).toHaveAttribute(
      'aria-disabled',
      'false',
    )
    await expect(page.locator('[data-auth-method="oauth_browser"]')).toHaveAttribute(
      'aria-disabled',
      'false',
    )
    await expect(page.locator('[data-auth-method="oauth_device"]')).toHaveAttribute(
      'aria-disabled',
      'false',
    )
    await expect(page.locator('[data-auth-method="oauth_import"]')).toHaveAttribute(
      'aria-disabled',
      'false',
    )
    await expect(page).toHaveURL(/\/admin\/accounts\/new$/)
  })

  test('keeps the API-key deep link live once the route ships', async ({ page }) => {
    await page.goto('/admin/accounts/new-apikey')

    await expect(page).toHaveURL(/\/admin\/accounts\/new-apikey$/)
    await expect(page.getByTestId('apikey-create-form')).toBeVisible()
    await expect(page.getByTestId('apikey-api-key-input')).toBeVisible()
  })

  test('keeps import deep links live once the route ships', async ({ page }) => {
    await page.goto('/admin/accounts/new-import')
    await expect(page).toHaveURL(/\/admin\/accounts\/new-import$/)
    await expect(page.getByTestId('import-auth-json-input')).toBeVisible()
  })
})
