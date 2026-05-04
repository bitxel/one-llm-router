import { expect, test } from './fixtures'

/**
 * T-402 — Wizard defaults E2E (US-5 AC-1 + AC-2).
 *
 * Asserts that the fresh-install wizard pre-fills the safe defaults
 * listed in spec §FR-014: sqlite3 driver, `router.db` DSN, provider
 * `openai`, plugin toggles OFF. Then times the accept-defaults +
 * commit path — must complete under 10s P95 on a local SQLite.
 */
test.describe('wizard-defaults', () => {
  test('defaults are pre-selected and commit is fast', async ({ page }) => {
    await page.goto('/setup/')
    await page.getByTestId('wizard-next').click()

    // Database step — defaults pre-filled. The driver picker is a
    // radiogroup of EngineCards (SQLite / PostgreSQL / MySQL); assert
    // the sqlite card is the checked radio and the DSN is seeded.
    await expect(page.getByRole('radio', { name: /SQLite/ }).first()).toHaveAttribute(
      'aria-checked',
      'true',
    )
    await expect(page.getByTestId('dsn')).toHaveValue('router.db')

    // sqlite3 is a local file; the Test button is not rendered and
    // Continue advances straight through (spec-002 §FR-014, relaxed
    // 2026-04-15). No probe-result should appear for the default path.
    await expect(page.getByTestId('wizard-probe')).toHaveCount(0)
    await page.getByTestId('wizard-next').click()

    // Upstream step — provider locked to openai. The provider input's
    // DOM id is the literal string "account.provider", so the CSS must
    // escape the dot or `.provider` is interpreted as a class name.
    await expect(page.getByRole('heading', { name: 'Upstream account', exact: true })).toBeVisible()
    await expect(page.getByTestId('wizard-auth-method-api_key').getByRole('radio')).toBeChecked()
    await expect(page.getByTestId('wizard-auth-method-oauth_browser')).toHaveAttribute(
      'data-auth-method',
      'oauth_browser',
    )
    await expect(page.getByTestId('wizard-auth-method-oauth_device')).toHaveAttribute(
      'data-auth-method',
      'oauth_device',
    )
    await expect(page.getByTestId('wizard-auth-method-oauth_import')).toHaveAttribute(
      'data-auth-method',
      'oauth_import',
    )
    await expect(page.locator('input[id="account.provider"]')).toHaveValue('openai')
    await page.getByTestId('account.api_key').fill('sk-defaults-placeholder')
    await page.getByTestId('wizard-next').click()

    // Plugin intents — both switches OFF by default (aria-checked)
    const adminSwitch = page.getByTestId('wizard-plugin-admin_auth')
    const keysSwitch = page.getByTestId('wizard-plugin-client_keys')
    await expect(adminSwitch).toHaveAttribute('aria-checked', 'false')
    await expect(keysSwitch).toHaveAttribute('aria-checked', 'false')
    await page.getByTestId('wizard-next').click()

    // Commit — measure wall-clock until /admin/ appears.
    const commitBtn = page.getByTestId('wizard-commit')
    const started = Date.now()
    await commitBtn.click()
    await expect(page).toHaveURL(/\/admin/, { timeout: 10_000 })
    const elapsed = Date.now() - started
    expect(elapsed).toBeLessThan(10_000)
  })

  test('enabled plugin intents are reflected in the review summary', async ({ page }) => {
    await page.goto('/setup/')
    await page.getByTestId('wizard-next').click()
    await page.getByTestId('wizard-next').click()

    await expect(page.getByRole('heading', { name: 'Upstream account', exact: true })).toBeVisible()
    await page.getByTestId('account.api_key').fill('sk-defaults-placeholder')
    await page.getByTestId('wizard-next').click()

    const adminSwitch = page.getByTestId('wizard-plugin-admin_auth')
    const keysSwitch = page.getByTestId('wizard-plugin-client_keys')
    await adminSwitch.click()
    await keysSwitch.click()
    await expect(adminSwitch).toHaveAttribute('aria-checked', 'true')
    await expect(keysSwitch).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByText(/intent only/i).first()).toBeVisible()

    await page.getByTestId('wizard-next').click()

    await expect(page.getByText('on (intent only — plugin not installed)')).toHaveCount(2)
  })
})
