import { expect, test } from './fixtures'

/**
 * T-312 — Admin portal shell E2E (US-3 AC-1 + AC-2).
 *
 * The test first completes the wizard (reuses the happy-path flow in
 * abbreviated form), then asserts the shell is stable + renders the
 * current release mix of live and future nav entries. A network-fault
 * simulation asserts that a 500 response on `/api/admin/settings`
 * produces a visible error state that carries the X-Request-Id
 * correlation identifier.
 */
test.describe('portal-shell', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/setup/')
    await page.getByTestId('wizard-next').click() // Welcome
    // sqlite3 default: no Test button, Continue advances straight.
    await expect(page.getByTestId('wizard-probe')).toHaveCount(0)
    await page.getByTestId('wizard-next').click() // Database
    await page.getByTestId('account.api_key').fill('sk-shell-placeholder')
    await page.getByTestId('wizard-next').click() // advance from account
    await page.getByTestId('wizard-next').click() // advance from plugins
    await page.getByTestId('wizard-commit').click()
    await expect(page).toHaveURL(/\/admin/, { timeout: 10_000 })
  })

  test('AC-3.1 shell renders with enabled + disabled nav entries', async ({ page }) => {
    await expect(page.getByTestId('sidebar-dashboard')).toBeVisible()
    await expect(page.getByTestId('sidebar-accounts')).toBeVisible()
    await expect(page.getByTestId('sidebar-accounts')).not.toHaveAttribute('aria-disabled', 'true')
    await expect(page.getByTestId('sidebar-settings')).toBeVisible()

    await expect(page.getByTestId('sidebar-requests')).toBeVisible()
    await expect(page.getByTestId('sidebar-requests')).not.toHaveAttribute('aria-disabled', 'true')

    for (const label of ['sidebar-client-keys', 'sidebar-observability'] as const) {
      const el = page.getByTestId(label)
      await expect(el).toHaveAttribute('aria-disabled', 'true')
      await expect(el).toContainText('planned')
    }

    await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible()
    await expect(page.getByText('Monitor live account capacity')).toBeVisible()

    await page.getByTestId('sidebar-accounts').click()
    await expect(page).toHaveURL(/\/admin\/accounts$/)
    await expect(page.getByRole('link', { name: /Add account/i })).toBeVisible()

    await page.getByTestId('sidebar-dashboard').click()
    await expect(page).toHaveURL(/\/admin$/)

    await page.getByTestId('sidebar-accounts').click()
    await expect(page).toHaveURL(/\/admin\/accounts$/)
    await expect(page.getByTestId('sidebar-accounts')).toHaveAttribute('data-active', 'true')

    await page.getByTestId('sidebar-requests').click()
    await expect(page).toHaveURL(/\/admin\/requests$/)

    await page.getByTestId('sidebar-accounts').click()
    await expect(page).toHaveURL(/\/admin\/accounts$/)

    for (const label of ['sidebar-client-keys', 'sidebar-observability'] as const) {
      await page.getByTestId(label).click({ force: true })
      await expect(page).toHaveURL(/\/admin\/accounts$/)
    }
  })

  test('AC-3.1b settings success path renders current plugin-intent copy', async ({ page }) => {
    await page.getByTestId('sidebar-settings').click()
    await expect(page).toHaveURL(/\/admin\/settings$/)
    await expect(page.getByText('intent only').first()).toBeVisible()
    await expect(page.getByText(/Plugin not yet installed/i).first()).toBeVisible()
    await expect(page.getByTestId('plugin-switch-admin_auth')).toHaveAttribute(
      'aria-checked',
      'false',
    )
    await expect(page.getByTestId('plugin-switch-client_keys')).toHaveAttribute(
      'aria-checked',
      'false',
    )
  })

  test('AC-3.2 server 500 surfaces an error with X-Request-Id', async ({ page, context }) => {
    // Intercept the settings fetch and respond with a simulated 500
    // that still carries an envelope + X-Request-Id header so the
    // client can extract the correlation id for operator reporting.
    await context.route('**/api/admin/settings', (route) => {
      return route.fulfill({
        status: 500,
        headers: { 'X-Request-Id': 'req_e2e_forced_error', 'X-E2E-Expected-Error': 'true' },
        contentType: 'application/json',
        body: JSON.stringify({ code: -1, msg: 'unknown_error', data: {} }),
      })
    })

    await page.getByTestId('sidebar-settings').click()
    const banner = page.getByTestId('error-banner')
    await expect(banner).toBeVisible({ timeout: 5_000 })
    await expect(banner).toContainText('req_e2e_forced_error')
  })
})
