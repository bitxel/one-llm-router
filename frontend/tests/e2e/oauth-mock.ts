import { createServer, type IncomingMessage, type ServerResponse } from 'node:http'
import type { AddressInfo } from 'node:net'

interface TokenRecord {
  email: string
  planType: string
  chatgptAccountID: string
  accessToken: string
  refreshToken: string
  idToken: string
  expiresIn: number
}

interface DeviceSession {
  deviceAuthID: string
  userCode: string
  verificationURL: string
  status: 'pending' | 'approved' | 'denied' | 'expired'
  authorizationCode: string
  codeVerifier: string
  record: TokenRecord
}

function makeJWT(payload: Record<string, unknown>): string {
  const header = Buffer.from(JSON.stringify({ alg: 'none', typ: 'JWT' })).toString('base64url')
  const body = Buffer.from(JSON.stringify(payload)).toString('base64url')
  return `${header}.${body}.sig`
}

function issueTokenRecord(prefix: string, index: number): TokenRecord {
  const email = `${prefix}${index}@example.com`
  const planType = prefix.includes('device') ? 'chatgpt-team' : 'chatgpt-plus'
  const chatgptAccountID = `acct_${prefix}_${index}`
  const now = Math.floor(Date.now() / 1000)
  return {
    email,
    planType,
    chatgptAccountID,
    accessToken: `fixture_${prefix}_access_${index}`,
    refreshToken: `fixture_${prefix}_refresh_${index}`,
    idToken: makeJWT({
      email,
      exp: now + 3600,
      auth: {
        plan_type: planType,
        chatgpt_account_id: chatgptAccountID,
      },
    }),
    expiresIn: 3600,
  }
}

async function readBody(req: IncomingMessage): Promise<string> {
  const chunks: Buffer[] = []
  for await (const chunk of req) {
    chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk))
  }
  return Buffer.concat(chunks).toString('utf8')
}

function html(body: string): string {
  return `<!doctype html><html><body style="font-family: system-ui; padding: 24px">${body}</body></html>`
}

export class OAuthMockServer {
  private readonly browserCodes = new Map<string, TokenRecord>()
  private readonly refreshTokens = new Map<string, TokenRecord>()
  private readonly deviceSessions = new Map<string, DeviceSession>()
  private browserSeq = 1
  private deviceSeq = 1
  private server = createServer(async (req, res) => this.route(req, res))
  baseURL = ''

  async start(): Promise<void> {
    await new Promise<void>((resolve, reject) => {
      this.server.once('error', reject)
      this.server.listen(0, '127.0.0.1', () => resolve())
    })
    const addr = this.server.address() as AddressInfo
    this.baseURL = `http://127.0.0.1:${addr.port}`
  }

  async stop(): Promise<void> {
    await new Promise<void>((resolve) => {
      this.server.close(() => resolve())
    })
  }

  url(path: string): string {
    return new URL(path, `${this.baseURL}/`).toString()
  }

  private async route(req: IncomingMessage, res: ServerResponse): Promise<void> {
    const url = new URL(req.url ?? '/', `${this.baseURL}/`)
    if (req.method === 'GET' && url.pathname === '/oauth/authorize') {
      this.handleAuthorize(url, res)
      return
    }
    if (req.method === 'POST' && url.pathname === '/oauth/token') {
      await this.handleToken(req, res)
      return
    }
    if (req.method === 'POST' && url.pathname === '/api/accounts/deviceauth/usercode') {
      this.handleDeviceCode(res)
      return
    }
    if (req.method === 'POST' && url.pathname === '/api/accounts/deviceauth/token') {
      await this.handleDevicePoll(req, res)
      return
    }
    if (req.method === 'GET' && url.pathname === '/codex/device') {
      this.handleDevicePage(url, res)
      return
    }
    if (req.method === 'GET' && url.pathname === '/codex/device/approve') {
      this.updateDeviceStatus(url.searchParams.get('device_auth_id'), 'approved', res)
      return
    }
    if (req.method === 'GET' && url.pathname === '/codex/device/deny') {
      this.updateDeviceStatus(url.searchParams.get('device_auth_id'), 'denied', res)
      return
    }
    if (req.method === 'GET' && url.pathname === '/codex/device/expire') {
      this.updateDeviceStatus(url.searchParams.get('device_auth_id'), 'expired', res)
      return
    }
    res.statusCode = 404
    res.end('not found')
  }

  private handleAuthorize(url: URL, res: ServerResponse): void {
    const state = url.searchParams.get('state') ?? ''
    const redirectURI =
      url.searchParams.get('redirect_uri') ?? 'http://localhost:1455/auth/callback'
    const index = this.browserSeq++
    const code = `mock_browser_code_${index}`
    const record = issueTokenRecord('browser', index)
    this.browserCodes.set(code, record)
    this.refreshTokens.set(record.refreshToken, issueTokenRecord('browser_refresh', index))

    const callback = new URL(redirectURI)
    callback.searchParams.set('code', code)
    callback.searchParams.set('state', state)

    const mismatch = new URL(redirectURI)
    mismatch.searchParams.set('code', code)
    mismatch.searchParams.set('state', `${state}_mismatch`)

    const callbackURL = callback.toString()
    const mismatchURL = mismatch.toString()

    res.setHeader('Content-Type', 'text/html; charset=utf-8')
    res.end(
      html(`
        <h1 data-testid="mock-oauth-title">Mock OAuth approval</h1>
        <p data-testid="callback-url">${callbackURL}</p>
        <p data-testid="callback-url-mismatch">${mismatchURL}</p>
        <button data-testid="approve-loopback" onclick="window.location.href='${callbackURL}'">Approve loopback</button>
        <button data-testid="approve-manual" onclick="window.location.href='${callbackURL}'">Approve manual</button>
        <button data-testid="approve-mismatch" onclick="window.location.href='${mismatchURL}'">Approve mismatch</button>
      `),
    )
  }

  private async handleToken(req: IncomingMessage, res: ServerResponse): Promise<void> {
    const body = await readBody(req)
    const form = new URLSearchParams(body)
    const grantType = form.get('grant_type')

    if (grantType === 'authorization_code') {
      const code = form.get('code') ?? ''
      const record = this.browserCodes.get(code) ?? this.findDeviceRecordByCode(code)
      if (!record) {
        this.writeJSON(res, 400, {
          error: 'invalid_grant',
          error_description: 'unknown authorization code',
        })
        return
      }
      this.writeJSON(res, 200, {
        access_token: record.accessToken,
        refresh_token: record.refreshToken,
        id_token: record.idToken,
        expires_in: record.expiresIn,
      })
      return
    }

    if (grantType === 'refresh_token') {
      const refreshToken = form.get('refresh_token') ?? ''
      const record = this.refreshTokens.get(refreshToken)
      if (!record) {
        this.writeJSON(res, 400, {
          error: 'invalid_grant',
          error_description: 'unknown refresh token',
        })
        return
      }
      this.writeJSON(res, 200, {
        access_token: record.accessToken,
        refresh_token: record.refreshToken,
        id_token: record.idToken,
        expires_in: record.expiresIn,
      })
      return
    }

    this.writeJSON(res, 400, {
      error: 'unsupported_grant_type',
      error_description: `unsupported grant_type ${grantType ?? '<empty>'}`,
    })
  }

  private handleDeviceCode(res: ServerResponse): void {
    const index = this.deviceSeq++
    const deviceAuthID = `device-auth-${index}`
    const userCode = `ABCD-${String(index).padStart(4, '0')}`
    const authorizationCode = `mock_device_code_${index}`
    const codeVerifier = `mock_device_verifier_${index}`
    const verificationURL = this.url(
      `/codex/device?device_auth_id=${encodeURIComponent(deviceAuthID)}&user_code=${encodeURIComponent(userCode)}`,
    )
    const session: DeviceSession = {
      deviceAuthID,
      userCode,
      verificationURL,
      status: 'pending',
      authorizationCode,
      codeVerifier,
      record: issueTokenRecord('device', index),
    }
    this.deviceSessions.set(deviceAuthID, session)
    this.refreshTokens.set(session.record.refreshToken, issueTokenRecord('device_refresh', index))

    this.writeJSON(res, 200, {
      device_auth_id: deviceAuthID,
      user_code: userCode,
      verification_uri: verificationURL,
      interval: 2,
      expires_in: 900,
    })
  }

  private async handleDevicePoll(req: IncomingMessage, res: ServerResponse): Promise<void> {
    const raw = await readBody(req)
    const parsed = JSON.parse(raw) as { device_auth_id?: string; user_code?: string }
    const session = parsed.device_auth_id
      ? this.deviceSessions.get(parsed.device_auth_id)
      : undefined
    if (!session || session.userCode !== parsed.user_code) {
      this.writeJSON(res, 404, { error: 'device_not_found' })
      return
    }

    switch (session.status) {
      case 'pending':
        this.writeJSON(res, 200, { status: 'authorization_pending' })
        return
      case 'denied':
        this.writeJSON(res, 403, {
          error: 'access_denied',
          error_description: 'denied by operator',
        })
        return
      case 'expired':
        this.writeJSON(res, 403, {
          status: 'expired_token',
          error_description: 'device code expired',
        })
        return
      case 'approved':
        this.writeJSON(res, 200, {
          authorization_code: session.authorizationCode,
          code_verifier: session.codeVerifier,
        })
        return
    }
  }

  private handleDevicePage(url: URL, res: ServerResponse): void {
    const deviceAuthID = url.searchParams.get('device_auth_id') ?? ''
    const userCode = url.searchParams.get('user_code') ?? ''
    const approveURL = this.url(
      `/codex/device/approve?device_auth_id=${encodeURIComponent(deviceAuthID)}`,
    )
    const denyURL = this.url(
      `/codex/device/deny?device_auth_id=${encodeURIComponent(deviceAuthID)}`,
    )
    const expireURL = this.url(
      `/codex/device/expire?device_auth_id=${encodeURIComponent(deviceAuthID)}`,
    )

    res.setHeader('Content-Type', 'text/html; charset=utf-8')
    res.end(
      html(`
        <h1 data-testid="device-approval-title">Approve device code</h1>
        <p data-testid="device-approval-code">${userCode}</p>
        <a data-testid="device-approve-link" href="${approveURL}">Approve</a>
        <a data-testid="device-deny-link" href="${denyURL}">Deny</a>
        <a data-testid="device-expire-link" href="${expireURL}">Expire</a>
      `),
    )
  }

  private updateDeviceStatus(
    deviceAuthID: string | null,
    status: DeviceSession['status'],
    res: ServerResponse,
  ): void {
    if (!deviceAuthID) {
      res.statusCode = 400
      res.end('missing device_auth_id')
      return
    }
    const session = this.deviceSessions.get(deviceAuthID)
    if (!session) {
      res.statusCode = 404
      res.end('unknown device_auth_id')
      return
    }
    session.status = status
    this.deviceSessions.set(deviceAuthID, session)
    res.setHeader('Content-Type', 'text/html; charset=utf-8')
    res.end(
      html(`
        <h1 data-testid="device-approval-result">${status}</h1>
        <p data-testid="device-approval-code">${session.userCode}</p>
      `),
    )
  }

  private findDeviceRecordByCode(code: string): TokenRecord | undefined {
    for (const session of this.deviceSessions.values()) {
      if (session.authorizationCode === code) {
        return session.record
      }
    }
    return undefined
  }

  private writeJSON(res: ServerResponse, status: number, body: unknown): void {
    res.statusCode = status
    res.setHeader('Content-Type', 'application/json; charset=utf-8')
    res.end(JSON.stringify(body))
  }
}
