import { type ChildProcess, execFileSync, execSync, spawn } from 'node:child_process'
import { copyFileSync, createWriteStream, existsSync, mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { test as base, expect, type Page, type Response } from '@playwright/test'

import { CodexBackendMockServer } from './codex-backend-mock'
import { OAuthMockServer } from './oauth-mock'

/**
 * E2E fixture for 002.
 *
 * The setup gate latches open in-process on first observation of
 * `config.json` (FR-007), so each test gets a dedicated runtime
 * directory containing its own `config.json` and `router.db`. The
 * fixture spawns a dedicated `./one-llm-router-e2e` process there, drives it, and
 * shuts it down on teardown. This mirrors the production invariant that
 * a wizard-latched gate never re-opens within a single process lifetime
 * without touching the developer's repo-root runtime files.
 *
 * Prerequisites:
 *   - The SPA must already be staged into `internal/spa/dist`.
 *     `make build` handles that before Playwright runs.
 *   - The fixture builds a temporary `cmd/one-llm-router-e2e` binary with the
 *     `e2e` build tag so mock Codex backend injection never exists on
 *     the production `cmd/one-llm-router` entrypoint.
 *   - The E2E port must be free. By default this is 18080, deliberately
 *     separate from the manual-dev default of 8080.
 */
type Fixtures = {
  cleanInstall: undefined
  codexBackendMock: CodexBackendMockServer
  oauthMock: OAuthMockServer
  routerLogPath: string
}

function copyRedactedRouterDB(source: string, target: string): void {
  copyFileSync(source, target)
  try {
    execFileSync(
      'sqlite3',
      [
        target,
        [
          "UPDATE upstream_accounts SET api_key='[redacted]' WHERE api_key IS NOT NULL;",
          'UPDATE upstream_accounts SET access_token=NULL, refresh_token=NULL, id_token=NULL;',
        ].join('\n'),
      ],
      { stdio: 'ignore' },
    )
  } catch (error) {
    rmSync(target, { force: true })
    throw error
  }
}

const __filename = fileURLToPath(import.meta.url)
const __dirname = dirname(__filename)
const REPO_ROOT = resolve(__dirname, '..', '..', '..')
const BASE_URL = process.env.E2E_BASE_URL ?? 'http://localhost:18080'
const BASE = new URL(BASE_URL)
const LISTEN_PORT = BASE.port || (BASE.protocol === 'https:' ? '443' : '80')
const LISTEN_ADDR = process.env.E2E_ROUTER_LISTEN_ADDR ?? `${BASE.hostname}:${LISTEN_PORT}`
const ROUTER_E2E_DB_PATH = 'ROUTER_E2E_DB_PATH'
const LIVE_UPSTREAM_SMOKE_ENABLED = process.env.LIVE_UPSTREAM_SMOKE === '1'
let routerE2EBinary: string | null = null
let routerE2EBinaryDir: string | null = null

function routerBinaryPath(): string {
  if (process.env.E2E_ROUTER_BIN) {
    return resolve(REPO_ROOT, process.env.E2E_ROUTER_BIN)
  }
  if (routerE2EBinary) {
    return routerE2EBinary
  }
  routerE2EBinaryDir = mkdtempSync(join(tmpdir(), 'one-llm-router-e2e-bin-'))
  routerE2EBinary = join(
    routerE2EBinaryDir,
    process.platform === 'win32' ? 'one-llm-router-e2e.exe' : 'one-llm-router-e2e',
  )
  execFileSync('go', ['build', '-tags', 'e2e', '-o', routerE2EBinary, './cmd/one-llm-router-e2e'], {
    cwd: REPO_ROOT,
    stdio: 'inherit',
  })
  process.once('exit', () => {
    if (routerE2EBinaryDir) {
      removePath(routerE2EBinaryDir)
    }
  })
  return routerE2EBinary
}

function removePath(path: string) {
  try {
    rmSync(path, { force: true, maxRetries: 3, recursive: true, retryDelay: 50 })
  } catch {
    /* best-effort */
  }
}

async function waitForReady(timeoutMs = 10_000): Promise<boolean> {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    try {
      const r = await fetch(`${BASE_URL}/api/setup/status`)
      if (r.status < 500) return true
    } catch {
      /* not yet listening */
    }
    await new Promise((r) => setTimeout(r, 200))
  }
  return false
}

function assertListenPortFree() {
  let output = ''
  try {
    output = execSync(`lsof -nP -iTCP:${LISTEN_PORT} -sTCP:LISTEN`, {
      stdio: ['ignore', 'pipe', 'ignore'],
    })
      .toString()
      .trim()
  } catch {
    return
  }
  if (output !== '') {
    throw new Error(
      `E2E listen port ${LISTEN_PORT} is busy; stop that process or set E2E_BASE_URL to a free port:\n${output}`,
    )
  }
}

function routerProcessEnv(
  oauthMock: OAuthMockServer,
  codexBackendMock: CodexBackendMockServer,
): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = { ...process.env }
  for (const key of Object.keys(env)) {
    if (key.startsWith('ROUTER_')) {
      delete env[key]
    }
  }
  return {
    ...env,
    ROUTER_LISTEN_ADDR: LISTEN_ADDR,
    ROUTER_OAUTH_AUTHORIZE_URL: oauthMock.url('/oauth/authorize'),
    ROUTER_OAUTH_TOKEN_URL: oauthMock.url('/oauth/token'),
    ROUTER_OAUTH_DEVICE_CODE_URL: oauthMock.url('/api/accounts/deviceauth/usercode'),
    ROUTER_OAUTH_DEVICE_TOKEN_URL: oauthMock.url('/api/accounts/deviceauth/token'),
    ROUTER_E2E_CODEX_BACKEND_BASE_URL: codexBackendMock.url,
  }
}

async function spawnRouter(
  runtimeDir: string,
  logPath: string,
  oauthMock: OAuthMockServer,
  codexBackendMock: CodexBackendMockServer,
): Promise<ChildProcess> {
  const routerBin = routerBinaryPath()
  const logStream = createWriteStream(logPath, { flags: 'a' })
  const proc = spawn(routerBin, ['-config', './config.json'], {
    cwd: runtimeDir,
    env: routerProcessEnv(oauthMock, codexBackendMock),
    stdio: ['ignore', 'pipe', 'pipe'],
    detached: false,
  })
  proc.stdout?.pipe(logStream)
  proc.stderr?.pipe(logStream)
  proc.once('exit', () => {
    logStream.end()
  })
  const ok = await waitForReady()
  if (!ok) {
    proc.kill('SIGKILL')
    throw new Error(
      `router at ${BASE_URL} did not become ready; check one-llm-router-e2e build logs`,
    )
  }
  return proc
}

async function stopRouter(proc: ChildProcess) {
  if (proc.exitCode !== null) return
  proc.kill('SIGTERM')
  await new Promise<void>((done) => {
    const fallback = setTimeout(() => {
      proc.kill('SIGKILL')
      done()
    }, 2000)
    proc.once('exit', () => {
      clearTimeout(fallback)
      done()
    })
  })
}

function isIgnorableConsoleError(message: {
  text(): string
  location(): { url: string }
  page(): { url(): string } | null
}): boolean {
  const text = message.text()
  const locationURL = message.location().url
  const pageURL = message.page()?.url() ?? ''
  if (text.startsWith('Failed to load resource:')) {
    for (const rawURL of [locationURL, pageURL]) {
      try {
        if (new URL(rawURL).pathname.startsWith('/api/')) {
          return true
        }
      } catch {
        /* keep checking the more specific allow-list below */
      }
    }
  }
  if (locationURL.includes('/favicon.ico') || pageURL.includes('/favicon.ico')) {
    return true
  }
  if (
    text.includes('ERR_CONNECTION_REFUSED') &&
    (locationURL.includes('/auth/callback') || pageURL.includes('/auth/callback'))
  ) {
    return true
  }
  return false
}

export const test = base.extend<Fixtures>({
  page: async ({ page, context }, use, testInfo) => {
    const failures: string[] = []
    const seenPages = new WeakSet<Page>()
    let deferredFailure: Error | null = null

    const attachPageGuard = (guardedPage: Page) => {
      if (seenPages.has(guardedPage)) {
        return
      }
      seenPages.add(guardedPage)
      guardedPage.on('pageerror', (error) => {
        failures.push(`pageerror on ${guardedPage.url() || 'about:blank'}: ${error.message}`)
      })
    }

    attachPageGuard(page)
    for (const existingPage of context.pages()) {
      attachPageGuard(existingPage)
    }

    const onPage = (guardedPage: Page) => {
      attachPageGuard(guardedPage)
    }
    const onConsole = (message: {
      type(): string
      text(): string
      location(): { url: string }
      page(): Page | null
    }) => {
      if (message.type() !== 'error' || isIgnorableConsoleError(message)) {
        return
      }
      const sourceURL = (message.page()?.url() ?? message.location().url) || 'unknown'
      failures.push(`console.error on ${sourceURL}: ${message.text()}`)
    }
    const onWebError = (webError: { page(): Page; error(): Error }) => {
      failures.push(
        `weberror on ${webError.page().url() || 'about:blank'}: ${webError.error().message}`,
      )
    }
    const onResponse = (response: Response) => {
      const status = response.status()
      if (status < 400) {
        return
      }
      let pathname = ''
      try {
        pathname = new URL(response.url()).pathname
      } catch {
        return
      }
      if (!pathname.startsWith('/api/')) {
        return
      }
      const headers = response.headers()
      if (headers['x-e2e-expected-error'] === 'true') {
        return
      }
      failures.push(`unexpected API response ${status} on ${response.url()}`)
    }

    context.on('page', onPage)
    context.on('console', onConsole)
    context.on('weberror', onWebError)
    page.on('response', onResponse)

    try {
      await use(page)
    } finally {
      context.off('page', onPage)
      context.off('console', onConsole)
      context.off('weberror', onWebError)
      page.off('response', onResponse)
      if (failures.length > 0) {
        deferredFailure = new Error(
          `unexpected browser errors in ${testInfo.title}:\n${failures.join('\n')}`,
        )
      }
    }
    if (deferredFailure) {
      throw deferredFailure
    }
  },
  oauthMock: [
    async ({ browserName: _browserName }, use) => {
      const server = new OAuthMockServer()
      await server.start()
      try {
        await use(server)
      } finally {
        await server.stop()
      }
    },
    { auto: true },
  ],
  codexBackendMock: [
    async ({ browserName: _browserName }, use) => {
      const server = new CodexBackendMockServer()
      await server.start()
      try {
        await use(server)
      } finally {
        await server.stop()
      }
    },
    { auto: true },
  ],
  routerLogPath: [
    async ({ browserName: _browserName }, use, testInfo) => {
      const path = testInfo.outputPath('router.log')
      removePath(path)
      await use(path)
    },
    { auto: true },
  ],
  cleanInstall: [
    async ({ oauthMock, codexBackendMock, routerLogPath }, use, testInfo) => {
      assertListenPortFree()
      const runtimeDir = mkdtempSync(join(tmpdir(), 'one-llm-router-e2e-'))
      const routerDBPath = join(runtimeDir, 'router.db')
      const previousDBPath = process.env[ROUTER_E2E_DB_PATH]
      process.env[ROUTER_E2E_DB_PATH] = routerDBPath

      let proc: ChildProcess | null = null
      try {
        proc = await spawnRouter(runtimeDir, routerLogPath, oauthMock, codexBackendMock)
        await use(undefined)
      } finally {
        if (proc) {
          await stopRouter(proc)
        }
        if (previousDBPath === undefined) {
          delete process.env[ROUTER_E2E_DB_PATH]
        } else {
          process.env[ROUTER_E2E_DB_PATH] = previousDBPath
        }
        if (!LIVE_UPSTREAM_SMOKE_ENABLED && existsSync(routerDBPath)) {
          copyRedactedRouterDB(routerDBPath, testInfo.outputPath('router.db'))
        }
        removePath(runtimeDir)
      }
    },
    { auto: true },
  ],
})

export { expect }
