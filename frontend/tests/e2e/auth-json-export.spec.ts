import { readFileSync } from 'node:fs'
import type { Page } from '@playwright/test'

import { expect, test } from './fixtures'
import {
  buildAuthJSONFixture,
  completeSetupWizard,
  getAccountRow,
  readRouterLogs,
  sqliteTimestampToISOString,
} from './helpers'

async function importOAuthRow(page: Page) {
  await page.goto('/admin/accounts/new')
  await page.locator('[data-auth-method="oauth_import"]').click()
  await expect(page).toHaveURL(/\/admin\/accounts\/new-import$/)

  await page.getByTestId('import-auth-json-input').setInputFiles({
    name: 'auth.json',
    mimeType: 'application/json',
    buffer: Buffer.from(
      buildAuthJSONFixture({
        email: 'export@example.com',
        planType: 'chatgpt-plus',
        accountID: 'acct_export_1',
      }),
    ),
  })
  await page.getByTestId('import-auth-json-submit').click()
  await expect(page).toHaveURL(/\/admin\/accounts\/\d+$/)
  return Number(page.url().match(/\/admin\/accounts\/(\d+)$/)?.[1])
}

test.describe('auth.json export', () => {
  test.beforeEach(async ({ page }) => {
    await completeSetupWizard(page)
  })

  test('downloads auth.json, logs the export audit, round-trips through import, and hides export on api_key rows', async ({
    page,
    routerLogPath,
  }) => {
    const oauthID = await importOAuthRow(page)
    expect(oauthID).toBeGreaterThan(0)

    const downloadPromise = page.waitForEvent('download')
    await page.getByTestId('export-auth-json').click()
    const download = await downloadPromise
    expect(download.suggestedFilename()).toBe('auth.json')
    const downloadPath = await download.path()
    expect(downloadPath).toBeTruthy()

    const exported = JSON.parse(readFileSync(downloadPath ?? '', 'utf8')) as {
      OPENAI_API_KEY: null
      tokens: Record<string, unknown>
      last_refresh: string
    }
    expect(exported.OPENAI_API_KEY).toBeNull()
    expect(Object.keys(exported.tokens).sort()).toEqual([
      'access_token',
      'account_id',
      'id_token',
      'refresh_token',
    ])

    const exportLogs = readRouterLogs(routerLogPath).filter(
      (line) => line.msg === 'oauth_auth_json_exported' && line.account_id === oauthID,
    )
    expect(exportLogs).toHaveLength(1)

    await page.goto('/admin/accounts/new-import')
    await page.getByTestId('import-auth-json-input').setInputFiles(downloadPath)
    await page.getByTestId('import-auth-json-submit').click()
    await expect(page).toHaveURL(/\/admin\/accounts\/\d+$/)
    const importedID = Number(page.url().match(/\/admin\/accounts\/(\d+)$/)?.[1])
    expect(importedID).toBeGreaterThan(0)
    expect(importedID).not.toBe(oauthID)
    const imported = getAccountRow(importedID)
    expect(Buffer.from(imported.access_token ?? []).toString('utf8')).toBe(
      exported.tokens.access_token,
    )
    expect(Buffer.from(imported.refresh_token ?? []).toString('utf8')).toBe(
      exported.tokens.refresh_token,
    )
    expect(Buffer.from(imported.id_token ?? []).toString('utf8')).toBe(exported.tokens.id_token)
    expect(imported.chatgpt_account_id).toBe(exported.tokens.account_id)
    expect(sqliteTimestampToISOString(imported.last_refresh)).toBe(
      new Date(exported.last_refresh).toISOString(),
    )

    const importedDownloadPromise = page.waitForEvent('download')
    await page.getByTestId('export-auth-json').click()
    const importedDownload = await importedDownloadPromise
    const importedDownloadPath = await importedDownload.path()
    expect(importedDownloadPath).toBeTruthy()
    const reExported = JSON.parse(readFileSync(importedDownloadPath ?? '', 'utf8')) as {
      OPENAI_API_KEY: null
      tokens: Record<string, unknown>
      last_refresh: string
    }
    expect(reExported.OPENAI_API_KEY).toBeNull()
    expect(reExported.tokens).toEqual(exported.tokens)
    expect(new Date(reExported.last_refresh).toISOString()).toBe(
      new Date(exported.last_refresh).toISOString(),
    )

    await page.goto('/admin/accounts/1')
    await expect(page.getByTestId('account-detail-view')).toBeVisible()
    await expect(page.getByTestId('export-auth-json')).toHaveCount(0)

    const notOAuth = await page.request.post('/api/admin/accounts/1/export-auth-json')
    expect(notOAuth.status()).toBe(200)
    expect(await notOAuth.json()).toMatchObject({
      code: 3014,
      msg: 'not_oauth_account',
    })
  })
})
