import { expect, test } from './fixtures'

/**
 * setup-api.md v2.4 — operators may defer upstream-account seeding to
 * the admin portal. This test drives the same fresh-install bring-up
 * flow as `wizard-happy-path`, but exercises the Skip for now branch:
 *
 *   - Account step renders a "Skip for now" CTA
 *   - Clicking it advances straight to Plugins without filling the key
 *   - Commit lands on /admin/ and the "no healthy accounts" banner is
 *     visible (V-001 — /api/admin/health returns `degraded` when the
 *     upstream_accounts table is empty)
 *
 * Keeping the assertion on the dashboard banner (not on a separate
 * accounts page) pins the wizard-skip path to the observable operator
 * signal described in spec-002 §US-1 Edge-3.
 */
test.describe('wizard-skip-account', () => {
  test('skips upstream account and lands on /admin with degraded banner', async ({ page }) => {
    await page.goto('/')
    await expect(page).toHaveURL(/\/setup\//, { timeout: 5_000 })

    await page.getByTestId('wizard-next').click()
    await expect(page.getByRole('heading', { name: 'Database' })).toBeVisible()
    await page.getByTestId('wizard-next').click()

    await expect(page.getByRole('heading', { name: 'Upstream account', exact: true })).toBeVisible()
    const skip = page.getByTestId('wizard-skip-account')
    await expect(skip).toBeVisible()
    await skip.click()

    await expect(page.getByRole('heading', { name: 'Plugin intents', exact: true })).toBeVisible()
    await page.getByTestId('wizard-next').click()

    await expect(page.getByRole('heading', { name: 'Commit' })).toBeVisible()
    // Review must flag the deferred account decision so operators do
    // not commit thinking a seed key will be written.
    await expect(page.getByText(/deferred/i)).toBeVisible()
    await page.getByTestId('wizard-commit').click()

    await expect(page).toHaveURL(/\/admin/, { timeout: 10_000 })

    // V-001 — the admin dashboard surfaces the "no healthy accounts"
    // banner when /api/admin/health reports degraded (upstream_accounts
    // table is empty after a skipped commit).
    const banner = page.getByTestId('no-healthy-accounts-banner')
    await expect(banner).toBeVisible({ timeout: 10_000 })
    const bannerLink = banner.getByRole('link', { name: /Accounts (?:→|->) New/i })
    await expect(bannerLink).toHaveAttribute('href', '/admin/accounts/new')
    await bannerLink.click()
    await expect(page).toHaveURL(/\/admin\/accounts\/new$/)
    await expect(page.locator('[data-auth-method="api_key"]')).toHaveAttribute(
      'data-available',
      'true',
    )
  })

  // Undo path — an operator who skips, realises they do have a key
  // handy, goes Back to the Account step, and clicks "Register a key
  // instead" must be able to fill the form again. Without the undo
  // button the skipped view latches and the operator has to reload.
  test('undo skip re-opens the account form', async ({ page }) => {
    await page.goto('/')
    await expect(page).toHaveURL(/\/setup\//, { timeout: 5_000 })

    await page.getByTestId('wizard-next').click() // Welcome → Database
    await page.getByTestId('wizard-next').click() // Database → Account (sqlite, no probe)

    await expect(page.getByTestId('wizard-skip-account')).toBeVisible()
    await page.getByTestId('wizard-skip-account').click()

    // We are now on the Plugins step. Back should land us on the
    // "deferred" view, and the Undo button must return the form.
    await expect(page.getByRole('heading', { name: 'Plugin intents', exact: true })).toBeVisible()
    await page.getByTestId('wizard-back').click()

    const undo = page.getByTestId('wizard-unskip-account')
    await expect(undo).toBeVisible()
    await undo.click()

    // Re-opened account form fields.
    await expect(page.getByTestId('account.name')).toBeVisible()
    await expect(page.getByTestId('account.api_key')).toBeVisible()
    // Skip CTA is back.
    await expect(page.getByTestId('wizard-skip-account')).toBeVisible()
  })
})
