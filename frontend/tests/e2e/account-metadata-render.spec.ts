import type { Page } from '@playwright/test'

import { expect, test } from './fixtures'
import { buildAuthJSONFixture, completeSetupWizard, listAccountRows } from './helpers'

async function importAccount(page: Page, fixture: string) {
  await page.goto('/admin/accounts/new-import')
  await page.getByTestId('import-auth-json-input').setInputFiles({
    name: 'auth.json',
    mimeType: 'application/json',
    buffer: Buffer.from(fixture),
  })
  await page.getByTestId('import-auth-json-submit').click()
  await expect(page).toHaveURL(/\/admin\/accounts\/\d+$/)
}

test.describe('account metadata render', () => {
  test.beforeEach(async ({ page }) => {
    await completeSetupWizard(page)
  })

  test('renders mapped and fallback plan labels identically on list and detail, while api_key rows omit oauth metadata', async ({
    page,
  }) => {
    const fixtures = [
      {
        email: 'plus@example.com',
        planType: 'chatgpt-plus',
        accountID: 'acct_plus',
        label: 'ChatGPT Plus',
      },
      {
        email: 'team@example.com',
        planType: 'chatgpt-team',
        accountID: 'acct_team',
        label: 'ChatGPT Team',
      },
      {
        email: 'enterprise@example.com',
        planType: 'chatgpt-enterprise',
        accountID: 'acct_enterprise',
        label: 'ChatGPT Enterprise',
      },
      {
        email: 'custom@example.com',
        planType: 'chatgpt-custom',
        accountID: 'acct_custom',
        label: 'chatgpt-custom',
      },
    ]

    for (const fixture of fixtures) {
      await importAccount(
        page,
        buildAuthJSONFixture({
          email: fixture.email,
          planType: fixture.planType,
          accountID: fixture.accountID,
        }),
      )
    }

    const rows = listAccountRows()
    const idsByEmail = new Map(rows.map((row) => [row.email, row.id]))

    await page.goto('/admin/accounts')
    await expect(page.getByTestId('accounts-list')).toBeVisible()
    await expect(page.getByTestId('account-row-1')).not.toContainText('Email')

    for (const fixture of fixtures) {
      const accountID = idsByEmail.get(fixture.email)
      expect(accountID).toBeTruthy()
      await expect(page.getByTestId(`account-row-${accountID}`)).toContainText(fixture.label)

      await page.getByTestId(`account-detail-link-${accountID}`).click()
      await expect(page).toHaveURL(new RegExp(`/admin/accounts/${accountID}$`))
      await expect(page.getByTestId('account-oauth-metadata')).toContainText(fixture.label)
      await page.goto('/admin/accounts')
    }

    await page.getByTestId('account-detail-link-1').click()
    await expect(page).toHaveURL(/\/admin\/accounts\/1$/)
    await expect(page.getByTestId('account-detail-view')).toBeVisible()
    await expect(page.getByTestId('account-oauth-metadata')).toHaveCount(0)
  })
})
