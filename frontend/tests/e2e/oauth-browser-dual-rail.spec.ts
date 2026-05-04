import type { Page } from '@playwright/test'

import { expect, test } from './fixtures'
import {
  completeSetupWizard,
  listAccountRows,
  readRouterLogs,
  waitForHealthAccountActive,
  waitForLogEvent,
} from './helpers'

async function openBrowserOAuth(page: Page) {
  await page.goto('/admin/accounts/new')
  await page.locator('[data-auth-method="oauth_browser"]').click()
  await expect(page).toHaveURL(/\/admin\/accounts\/new-oauth$/)
}

function accountIDFromURL(url: string): number {
  const match = url.match(/\/admin\/accounts\/(\d+)$/)
  if (!match) {
    throw new Error(`account id not found in url ${url}`)
  }
  return Number(match[1])
}

async function readBrowserStartListenerBound(response: {
  json(): Promise<unknown>
}): Promise<boolean> {
  const body = (await response.json()) as { data?: { listener_bound?: boolean } }
  return body.data?.listener_bound === true
}

test.describe('oauth browser dual rail', () => {
  test.beforeEach(async ({ page }) => {
    await completeSetupWizard(page)
  })

  test('loopback rail wins and health reports active within 5s', async ({
    page,
    routerLogPath,
  }) => {
    await openBrowserOAuth(page)

    const requestPromise = page.waitForRequest('**/api/admin/oauth/browser/start')
    const responsePromise = page.waitForResponse('**/api/admin/oauth/browser/start')
    const popupPromise = page.waitForEvent('popup')
    await page.getByTestId('oauth-start-button').click()

    const startRequest = await requestPromise
    expect(startRequest.postDataJSON()).toEqual({ provider: 'openai' })
    const listenerBound = await readBrowserStartListenerBound(await responsePromise)

    const popup = await popupPromise
    await popup.waitForLoadState('domcontentloaded')
    await expect(popup.getByTestId('mock-oauth-title')).toBeVisible()
    test.skip(!listenerBound, 'OAuth loopback listener unavailable on this host')

    await popup.getByTestId('approve-loopback').click()
    await expect(page).toHaveURL(/\/admin\/accounts\/\d+$/, { timeout: 15_000 })
    await expect(page.getByTestId('account-detail-view')).toBeVisible()

    const accountID = accountIDFromURL(page.url())
    const completion = await waitForLogEvent(
      routerLogPath,
      (line) => line.msg === 'oauth_flow_completed' && line.rail === 'loopback',
      10_000,
    )
    await waitForHealthAccountActive(page.request, accountID, String(completion.time))
  })

  test('manual paste rail wins after state mismatch retry', async ({ page, routerLogPath }) => {
    await openBrowserOAuth(page)

    const popupPromise = page.waitForEvent('popup')
    await page.getByTestId('oauth-start-button').click()
    const popup = await popupPromise
    await popup.waitForLoadState('domcontentloaded')

    const callbackURL = (await popup.getByTestId('callback-url').textContent()) ?? ''
    const mismatchURL = (await popup.getByTestId('callback-url-mismatch').textContent()) ?? ''
    expect(callbackURL).toContain('state=')
    expect(mismatchURL).toContain('state=')

    const callbackAbortPattern = /http:\/\/localhost:\d+\/auth\/callback.*/
    await page.context().route(callbackAbortPattern, (route) => route.abort('connectionrefused'))
    await popup.getByTestId('approve-manual').click()

    await expect(page.getByTestId('oauth-callback-input')).toBeVisible()

    const mismatchResponsePromise = page.waitForResponse(
      '**/api/admin/oauth/browser/manual-callback',
    )
    await page.getByTestId('oauth-callback-input').fill(mismatchURL)
    await page.getByRole('button', { name: /submit pasted url/i }).click()
    const mismatchResponse = await mismatchResponsePromise
    expect(mismatchResponse.status()).toBe(200)
    expect(await mismatchResponse.json()).toMatchObject({
      code: 3003,
      msg: 'oauth_state_mismatch',
    })
    await expect(page.getByTestId('oauth-inline-error')).toContainText('State mismatch')

    const successResponsePromise = page.waitForResponse(
      '**/api/admin/oauth/browser/manual-callback',
    )
    await page.getByTestId('oauth-callback-input').fill(callbackURL)
    await page.getByRole('button', { name: /submit pasted url/i }).click()
    const successResponse = await successResponsePromise
    expect(successResponse.status()).toBe(200)
    expect(await successResponse.json()).toMatchObject({
      code: 0,
      msg: 'ok',
      data: {
        account: {
          auth_method: 'oauth_browser',
        },
        rail: 'manual_paste',
        status: 'success',
      },
    })

    await expect(page).toHaveURL(/\/admin\/accounts\/\d+$/, { timeout: 15_000 })
    const accountID = accountIDFromURL(page.url())

    const completion = await waitForLogEvent(
      routerLogPath,
      (line) => line.msg === 'oauth_flow_completed' && line.rail === 'manual_paste',
      10_000,
    )
    await waitForHealthAccountActive(page.request, accountID, String(completion.time))
    await page.context().unroute(callbackAbortPattern)
  })

  test('CAS race between loopback and manual paste creates exactly one row and one completion log', async ({
    page,
    routerLogPath,
  }) => {
    await openBrowserOAuth(page)

    const popupPromise = page.waitForEvent('popup')
    await page.getByTestId('oauth-start-button').click()
    const popup = await popupPromise
    await popup.waitForLoadState('domcontentloaded')

    const callbackURL = (await popup.getByTestId('callback-url').textContent()) ?? ''
    await page.getByTestId('oauth-callback-input').fill(callbackURL)

    const manualResponsePromise = page.waitForResponse('**/api/admin/oauth/browser/manual-callback')
    await Promise.allSettled([
      popup.getByTestId('approve-loopback').click(),
      page.getByRole('button', { name: /submit pasted url/i }).click(),
    ])
    const manualResponse = await manualResponsePromise
    expect(manualResponse.status()).toBe(200)

    await expect(page).toHaveURL(/\/admin\/accounts\/\d+$/, { timeout: 15_000 })

    const oauthRows = listAccountRows().filter(
      (row) => row.auth_method === 'oauth_browser' && row.status !== 'deleted',
    )
    expect(oauthRows).toHaveLength(1)
    const completions = readRouterLogs(routerLogPath).filter(
      (line) => line.msg === 'oauth_flow_completed',
    )
    expect(completions).toHaveLength(1)
  })
})
