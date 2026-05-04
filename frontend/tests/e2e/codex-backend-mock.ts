import { createHash } from 'node:crypto'
import { createServer, type IncomingMessage, type Server, type ServerResponse } from 'node:http'
import type { AddressInfo, Socket } from 'node:net'

const websocketGUID = '258EAFA5-E914-47DA-95CA-C5AB0DC85B11'

export interface CapturedCodexRequest {
  method: string
  path: string
  headers: IncomingMessage['headers']
  rawBody: string
}

export interface CapturedCodexWebSocket {
  path: string
  headers: IncomingMessage['headers']
  frames: string[]
}

function textFrame(payload: string): Buffer {
  const body = Buffer.from(payload, 'utf8')
  if (body.length < 126) {
    return Buffer.concat([Buffer.from([0x81, body.length]), body])
  }
  if (body.length <= 0xffff) {
    const header = Buffer.alloc(4)
    header[0] = 0x81
    header[1] = 126
    header.writeUInt16BE(body.length, 2)
    return Buffer.concat([header, body])
  }
  const header = Buffer.alloc(10)
  header[0] = 0x81
  header[1] = 127
  header.writeBigUInt64BE(BigInt(body.length), 2)
  return Buffer.concat([header, body])
}

function closeFrame(): Buffer {
  return Buffer.from([0x88, 0x00])
}

function decodeClientFrame(buffer: Buffer): { opcode: number; payload: Buffer } | null {
  if (buffer.length < 2) return null

  const opcode = buffer[0] & 0x0f
  const masked = (buffer[1] & 0x80) !== 0
  let length = buffer[1] & 0x7f
  let offset = 2

  if (length === 126) {
    if (buffer.length < offset + 2) return null
    length = buffer.readUInt16BE(offset)
    offset += 2
  } else if (length === 127) {
    if (buffer.length < offset + 8) return null
    const wide = buffer.readBigUInt64BE(offset)
    if (wide > BigInt(Number.MAX_SAFE_INTEGER)) return null
    length = Number(wide)
    offset += 8
  }

  let mask: Buffer | null = null
  if (masked) {
    if (buffer.length < offset + 4) return null
    mask = buffer.subarray(offset, offset + 4)
    offset += 4
  }
  if (buffer.length < offset + length) return null

  const payload = Buffer.from(buffer.subarray(offset, offset + length))
  if (mask) {
    for (let i = 0; i < payload.length; i += 1) {
      payload[i] ^= mask[i % 4]
    }
  }
  return { opcode, payload }
}

async function readRawBody(req: IncomingMessage): Promise<string> {
  const chunks: Buffer[] = []
  for await (const chunk of req) {
    chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk))
  }
  return Buffer.concat(chunks).toString('utf8')
}

export class CodexBackendMockServer {
  private server: Server
  private sockets = new Set<Socket>()
  private urlValue = ''
  readonly requests: CapturedCodexRequest[] = []
  readonly websockets: CapturedCodexWebSocket[] = []

  constructor() {
    this.server = createServer((req, res) => {
      this.handleHTTP(req, res).catch((error) => {
        res.writeHead(500, { 'content-type': 'application/json' })
        res.end(JSON.stringify({ error: { code: 'mock_error', message: String(error) } }))
      })
    })
    this.server.on('upgrade', (req, socket) => this.handleUpgrade(req, socket))
  }

  get url(): string {
    if (!this.urlValue) {
      throw new Error('CodexBackendMockServer has not been started')
    }
    return this.urlValue
  }

  async start(): Promise<void> {
    await new Promise<void>((resolve) => this.server.listen(0, '127.0.0.1', resolve))
    const address = this.server.address() as AddressInfo
    this.urlValue = `http://127.0.0.1:${address.port}`
  }

  async stop(): Promise<void> {
    for (const socket of this.sockets) {
      socket.destroy()
    }
    await new Promise<void>((resolve) => this.server.close(() => resolve()))
  }

  reset(): void {
    this.requests.splice(0)
    this.websockets.splice(0)
  }

  private async handleHTTP(req: IncomingMessage, res: ServerResponse): Promise<void> {
    const rawBody = await readRawBody(req)
    this.requests.push({
      method: req.method ?? '',
      path: req.url ?? '',
      headers: req.headers,
      rawBody,
    })

    if (req.method === 'GET' && req.url === '/codex/models') {
      res.writeHead(200, { 'content-type': 'application/json' })
      res.end(
        JSON.stringify({
          models: [
            {
              slug: 'gpt-5.4-mini',
              display_name: 'GPT 5.4 Mini',
              description: 'Local mock model',
              context_window: 128000,
              supported_in_api: true,
              owned_by: 'openai',
              created: 1767225600,
            },
          ],
        }),
      )
      return
    }

    if (req.method === 'POST' && req.url === '/transcribe') {
      res.writeHead(200, { 'content-type': 'application/json' })
      res.end(JSON.stringify({ text: 'transcribed locally' }))
      return
    }

    if (
      req.method === 'POST' &&
      (req.url === '/codex/responses' || req.url === '/codex/responses/compact')
    ) {
      res.writeHead(200, { 'content-type': 'text/event-stream' })
      res.end(
        'event: response.completed\n' +
          'data: {"type":"response.completed","response":{"id":"resp_codex_mock","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":1}}}\n\n',
      )
      return
    }

    res.writeHead(404, { 'content-type': 'application/json' })
    res.end(JSON.stringify({ error: { code: 'not_found' } }))
  }

  private handleUpgrade(req: IncomingMessage, socket: Socket): void {
    this.sockets.add(socket)
    socket.once('close', () => this.sockets.delete(socket))
    socket.on('error', () => {
      // Router-side CloseNow can reset the TCP socket after a valid close path.
    })

    if (req.url !== '/codex/responses') {
      socket.write('HTTP/1.1 404 Not Found\r\n\r\n')
      socket.destroy()
      return
    }

    const key = req.headers['sec-websocket-key']
    if (typeof key !== 'string') {
      socket.write('HTTP/1.1 400 Bad Request\r\n\r\n')
      socket.destroy()
      return
    }

    const accept = createHash('sha1').update(`${key}${websocketGUID}`).digest('base64')
    socket.write(
      'HTTP/1.1 101 Switching Protocols\r\n' +
        'Upgrade: websocket\r\n' +
        'Connection: Upgrade\r\n' +
        `Sec-WebSocket-Accept: ${accept}\r\n\r\n`,
    )

    const capture: CapturedCodexWebSocket = {
      path: req.url,
      headers: req.headers,
      frames: [],
    }
    this.websockets.push(capture)

    socket.on('data', (chunk) => {
      const frame = decodeClientFrame(Buffer.from(chunk))
      if (!frame) return
      if (frame.opcode === 0x8) {
        socket.write(closeFrame())
        socket.end()
        return
      }
      if (frame.opcode === 0x1) {
        capture.frames.push(frame.payload.toString('utf8'))
        socket.write(textFrame('codex websocket reply'))
      }
    })
  }
}
