import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { DatabaseSync } from 'node:sqlite'

import type { APIRequestContext, Page } from '@playwright/test'

export const REPO_ROOT = resolve(import.meta.dirname, '..', '..', '..')
export const ROUTER_DB_ENV = 'ROUTER_E2E_DB_PATH'

export function routerDBPath(): string {
  const path = process.env[ROUTER_DB_ENV]
  if (!path) {
    throw new Error(
      `${ROUTER_DB_ENV} must be set by the E2E fixture before reading router DB state`,
    )
  }
  return path
}

function makeJWT(payload: Record<string, unknown>): string {
  const header = Buffer.from(JSON.stringify({ alg: 'none', typ: 'JWT' })).toString('base64url')
  const body = Buffer.from(JSON.stringify(payload)).toString('base64url')
  return `${header}.${body}.sig`
}

export function buildAuthJSONFixture({
  email,
  planType,
  accountID,
  accessToken,
  refreshToken,
  lastRefresh = '2026-04-22T08:00:00Z',
}: {
  email: string
  planType: string
  accountID: string
  accessToken?: string
  refreshToken?: string
  lastRefresh?: string
}): string {
  return JSON.stringify({
    OPENAI_API_KEY: null,
    tokens: {
      access_token: accessToken ?? `fixture_import_access_${accountID}`,
      refresh_token: refreshToken ?? `fixture_import_refresh_${accountID}`,
      id_token: makeJWT({
        email,
        exp: Math.floor(Date.now() / 1000) + 3600,
        auth: {
          plan_type: planType,
          chatgpt_account_id: accountID,
        },
      }),
      account_id: accountID,
    },
    last_refresh: lastRefresh,
  })
}

export async function completeSetupWizard(
  page: Page,
  options: { baseURL?: string } = {},
): Promise<void> {
  await page.goto('/setup/')
  await page.getByTestId('wizard-next').click()
  await page.getByTestId('wizard-next').click()
  await page.getByTestId('account.api_key').fill('sk-e2e-placeholder-key')
  if (options.baseURL) {
    await page.getByTestId('account.base_url').fill(options.baseURL)
  }
  await page.getByTestId('wizard-next').click()
  await page.getByTestId('wizard-next').click()
  await page.getByTestId('wizard-commit').click()
  await page.waitForURL(/\/admin/, { timeout: 15_000 })
}

export async function commitAPIKeySetup(
  request: APIRequestContext,
  options: {
    baseURL: string
    apiKey?: string
    accountName?: string
    logBodies?: boolean
  },
): Promise<void> {
  const logBodies = options.logBodies ?? false
  const response = await request.post('/api/setup/commit', {
    data: {
      db: { driver: 'sqlite3', url: 'router.db' },
      plugins: {
        admin_auth: { enabled: false },
        client_keys: { enabled: false },
      },
      first_account: {
        name: options.accountName ?? 'e2e-api-key',
        provider: 'openai',
        api_key: options.apiKey ?? 'sk-e2e-placeholder-key',
        base_url: options.baseURL,
      },
      runtime: {
        log_client_request_body: logBodies,
        log_upstream_request_body: logBodies,
        log_upstream_response_body: logBodies,
        log_retention_days: 30,
        log_level: 'info',
      },
    },
  })
  const body = (await response.json()) as { code?: number; msg?: string }
  if (response.status() !== 200 || body.code !== 0) {
    throw new Error(`setup commit failed: status=${response.status()} body=${JSON.stringify(body)}`)
  }
}

export interface RouterLogLine {
  time?: string
  level?: string
  msg?: string
  [key: string]: unknown
}

export function readRouterLogs(logPath: string): RouterLogLine[] {
  const raw = readFileSync(logPath, 'utf8')
  return raw
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .flatMap((line) => {
      try {
        return [JSON.parse(line) as RouterLogLine]
      } catch {
        return []
      }
    })
}

export async function waitForLogEvent(
  logPath: string,
  predicate: (line: RouterLogLine) => boolean,
  timeoutMs = 10_000,
): Promise<RouterLogLine> {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    const match = readRouterLogs(logPath).find(predicate)
    if (match) {
      return match
    }
    await new Promise((resolve) => setTimeout(resolve, 250))
  }
  throw new Error(`router log event not found within ${timeoutMs}ms`)
}

export async function waitForHealthAccountActive(
  request: APIRequestContext,
  accountID: number,
  completedAt: string,
): Promise<void> {
  const completedAtMs = Date.parse(completedAt)
  if (Number.isNaN(completedAtMs)) {
    throw new Error(`invalid completion timestamp: ${completedAt}`)
  }
  const deadline = Date.now() + 5_000
  while (Date.now() < deadline) {
    const response = await request.get(`/api/admin/health?account_id=${accountID}`)
    const body = (await response.json()) as {
      code: number
      data: {
        accounts?: Record<string, { status?: string } | number>
      }
    }
    const probed = body.data.accounts?.[String(accountID)]
    if (
      response.ok() &&
      body.code === 0 &&
      probed &&
      typeof probed === 'object' &&
      probed.status === 'active'
    ) {
      const dateHeader = response.headers().date
      if (!dateHeader) {
        throw new Error('health response missing Date header')
      }
      const deltaMs = Date.parse(dateHeader) - completedAtMs
      if (Number.isNaN(deltaMs) || deltaMs > 5_000) {
        throw new Error(`health active delta ${deltaMs}ms exceeds 5000ms`)
      }
      return
    }
    await new Promise((resolve) => setTimeout(resolve, 250))
  }
  throw new Error(`account ${accountID} did not become active within 5s`)
}

export interface AccountRow {
  id: number
  name: string
  api_key: Uint8Array | null
  auth_method: string
  status: string
  email: string | null
  plan_type: string | null
  chatgpt_account_id: string | null
  created_at: string
  updated_at: string
  last_refresh: string | null
  access_expires_at: string | null
  access_token: Uint8Array | null
  refresh_token: Uint8Array | null
  id_token: Uint8Array | null
}

export function listAccountRows(): AccountRow[] {
  const db = new DatabaseSync(routerDBPath())
  try {
    const stmt = db.prepare(`
      SELECT
        id,
        name,
        api_key,
        auth_method,
        status,
        email,
        plan_type,
        chatgpt_account_id,
        created_at,
        updated_at,
        last_refresh,
        access_expires_at,
        access_token,
        refresh_token,
        id_token
      FROM upstream_accounts
      ORDER BY id ASC
    `)
    return stmt.all() as AccountRow[]
  } finally {
    db.close()
  }
}

export function getAccountRow(id: number): AccountRow {
  const db = new DatabaseSync(routerDBPath())
  try {
    const stmt = db.prepare(`
      SELECT
        id,
        name,
        api_key,
        auth_method,
        status,
        email,
        plan_type,
        chatgpt_account_id,
        created_at,
        updated_at,
        last_refresh,
        access_expires_at,
        access_token,
        refresh_token,
        id_token
      FROM upstream_accounts
      WHERE id = ?
    `)
    const row = stmt.get(id) as AccountRow | undefined
    if (!row) {
      throw new Error(`account ${id} not found in ${routerDBPath()}`)
    }
    return row
  } finally {
    db.close()
  }
}

const sqliteTimestampPattern = /^(\d{4})-(\d{2})-(\d{2}) (\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?$/

export function sqliteTimestampToISOString(value: string | null): string | null {
  if (value == null) {
    return null
  }

  const sqliteMatch = sqliteTimestampPattern.exec(value)
  if (sqliteMatch) {
    const [, year, month, day, hour, minute, second, fractional = '0'] = sqliteMatch
    const millis = Number(`${fractional}000`.slice(0, 3))
    return new Date(
      Date.UTC(
        Number(year),
        Number(month) - 1,
        Number(day),
        Number(hour),
        Number(minute),
        Number(second),
        millis,
      ),
    ).toISOString()
  }

  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) {
    throw new Error(`invalid timestamp: ${value}`)
  }
  return parsed.toISOString()
}
