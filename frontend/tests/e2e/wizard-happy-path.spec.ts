import { expect, test } from './fixtures'

/**
 * T-114 — Wizard happy-path E2E.
 *
 * Drives the REAL Go router on :8080 through the full 5-step bring-up
 * flow. The fixture wipes `config.json` + `router.db` before each run
 * so the setup gate is armed. Upstream account uses a deliberately
 * placeholder key — the commit path does NOT reach OpenAI during the
 * wizard (no warm-up ping is sent; the smoke happens in quickstart
 * Path A step 6 which is out of scope for this spec).
 */
test.describe('wizard-happy-path', () => {
  test('completes the 5-step flow and lands on /admin/', async ({ page }) => {
    await page.goto('/')

    // index redirects to /admin which the setup gate rewrites to /setup/
    await expect(page).toHaveURL(/\/setup\//, { timeout: 5_000 })
    await expect(page.getByText('Welcome to one-llm-router')).toBeVisible()

    // Step 0 → Database
    await page.getByTestId('wizard-next').click()
    await expect(page.getByRole('heading', { name: 'Database' })).toBeVisible()

    // Defaults are pre-filled (US-5 AC-1). For sqlite we skip the probe
    // step entirely — migrate up on commit is the real acceptance test
    // and the DSN is a local file path. The Test button must therefore
    // NOT render, and a single Continue advances straight to the
    // account step.
    await expect(page.getByTestId('wizard-probe')).toHaveCount(0)
    await page.getByTestId('wizard-next').click()

    // Step 2 → Upstream account
    //
    // The Stripe section marker renders an <h2> with exactly the step
    // label; the step body headline uses an <h1> that also mentions
    // "upstream account" ("Register your first upstream account."), so
    // we anchor strictly on the Stripe via `exact: true`.
    await expect(page.getByRole('heading', { name: 'Upstream account', exact: true })).toBeVisible()
    await page.getByTestId('account.api_key').fill('sk-e2e-placeholder-key')
    await page.getByTestId('wizard-next').click()

    // Step 3 → Plugins (defaults OFF)
    await expect(page.getByRole('heading', { name: 'Plugin intents', exact: true })).toBeVisible()
    await page.getByTestId('wizard-next').click()

    // Step 4 → Commit
    await expect(page.getByRole('heading', { name: 'Commit' })).toBeVisible()
    await page.getByTestId('wizard-commit').click()

    await expect(page).toHaveURL(/\/admin/, { timeout: 10_000 })
    await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible()
    await expect(page.getByText('Monitor live account capacity')).toBeVisible()
  })
})
