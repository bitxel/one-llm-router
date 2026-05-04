import { expect, test } from './fixtures'
import { completeSetupWizard, getAccountRow } from './helpers'

test.describe('oauth device happy path', () => {
  test.beforeEach(async ({ page }) => {
    await completeSetupWizard(page)
  })

  test('converges within 2x provider interval after approval', async ({ page, context }) => {
    await page.goto('/admin/accounts/new')
    await page.locator('[data-auth-method="oauth_device"]').click()
    await expect(page).toHaveURL(/\/admin\/accounts\/new-oauth-device$/)

    await expect(page.getByTestId('device-user-code')).toBeVisible()
    const verificationURL = await page.getByTestId('device-verification-url').getAttribute('href')
    expect(verificationURL).toBeTruthy()
    const intervalBadge = await page.getByText(/poll interval \d+s/i).textContent()
    const intervalSeconds = Number(intervalBadge?.match(/(\d+)s/i)?.[1])
    expect(intervalSeconds).toBeGreaterThan(0)

    const approvalPage = await context.newPage()
    await approvalPage.goto(verificationURL ?? '')
    await approvalPage.getByTestId('device-approve-link').click()
    await expect(approvalPage.getByTestId('device-approval-result')).toContainText('approved')

    const approvedAt = Date.now()
    await expect(page).toHaveURL(/\/admin\/accounts\/\d+$/, { timeout: 6_000 })
    expect(Date.now() - approvedAt).toBeLessThanOrEqual(intervalSeconds * 2_000)
    const accountID = Number(page.url().match(/\/admin\/accounts\/(\d+)$/)?.[1])
    expect(accountID).toBeGreaterThan(0)

    const row = getAccountRow(accountID)
    expect(row.auth_method).toBe('oauth_device')
    expect(row.status).toBe('active')
    expect(row.last_refresh).toBeTruthy()
    expect(row.access_expires_at).toBeTruthy()
  })
})
