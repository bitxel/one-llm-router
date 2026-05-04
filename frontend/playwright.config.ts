import { defineConfig, devices } from '@playwright/test'

/**
 * Playwright config for the 002 admin portal + setup wizard E2E suite.
 *
 * The suite runs against the REAL Go router bound to :18080 by default — the
 * frontend bundle is built from `frontend/dist/`, staged into
 * `internal/spa/dist/`, and then served by the same binary (single
 * port). Each test orchestrates its own clean state in a per-test temp
 * directory; see the quickstart.md Path A flow for the happy path.
 *
 * `webServer` is left empty because the Go binary is started by the
 * fixture outside Playwright's webServer lifecycle.
 */
export default defineConfig({
  testDir: './tests/e2e',
  timeout: 120_000,
  expect: { timeout: 10_000 },
  // E2E tests spawn one Go router at a time. Runtime files are isolated
  // per test, but the suite still serialises at the worker level because
  // the OAuth loopback rail and router HTTP port are process-global.
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: 1,
  reporter: process.env.CI ? [['github'], ['html', { open: 'never' }]] : [['list']],
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:18080',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
})
