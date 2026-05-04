import { describe, expect, it } from 'vitest'

import {
  CodeSymbols,
  Err001AccountNotFound,
  Err001LegacyOK,
  Err002ConfigWriteFailed,
  Err002InvalidBaseURL,
  Err002MigrateFailed,
  Err002SetupRequired,
  Err003AlreadyConsumed,
  Err003InvalidAuthJSON,
  Err003OAuthExportReadFailed,
  Err003OAuthFlowInProgress,
  Err003OAuthInternalError,
  Err003OAuthStoreFailed,
  Err003OAuthUpstreamError,
  Err004InvalidPlaygroundRequest,
  Err004PlaygroundInternalError,
  Err004PlaygroundNoActiveAccount,
  Err004PlaygroundResponseTooLarge,
  PlatformOK,
  symbolFor,
} from './errcode'

describe('errcode registry', () => {
  it('mirrors the authoritative integer values', () => {
    expect(PlatformOK).toBe(0)
    expect(Err001AccountNotFound).toBe(1001)
    expect(Err001LegacyOK).toBe(1000)
    expect(Err002SetupRequired).toBe(2011)
    expect(Err002InvalidBaseURL).toBe(2016)
    expect(Err002MigrateFailed).toBe(2901)
    expect(Err002ConfigWriteFailed).toBe(2903)
    expect(Err003OAuthFlowInProgress).toBe(3001)
    expect(Err003AlreadyConsumed).toBe(3005)
    expect(Err003InvalidAuthJSON).toBe(3011)
    expect(Err003OAuthUpstreamError).toBe(3016)
    expect(Err003OAuthInternalError).toBe(3900)
    expect(Err003OAuthStoreFailed).toBe(3901)
    expect(Err003OAuthExportReadFailed).toBe(3902)
    expect(Err004InvalidPlaygroundRequest).toBe(4001)
    expect(Err004PlaygroundNoActiveAccount).toBe(4002)
    expect(Err004PlaygroundResponseTooLarge).toBe(4007)
    expect(Err004PlaygroundInternalError).toBe(4900)
  })

  it('exposes symbolFor for debug display', () => {
    expect(symbolFor(0)).toBe('ok')
    expect(symbolFor(2011)).toBe('setup_required')
    expect(symbolFor(3001)).toBe('oauth_flow_in_progress')
    expect(symbolFor(3005)).toBe('already_consumed')
    expect(symbolFor(3902)).toBe('oauth_export_read_failed')
    expect(symbolFor(Err004InvalidPlaygroundRequest)).toBe('invalid_playground_request')
    expect(symbolFor(Err004PlaygroundResponseTooLarge)).toBe('playground_response_too_large')
    expect(symbolFor(Err004PlaygroundInternalError)).toBe('playground_internal_error')
    expect(symbolFor(3017)).toBe('code_3017')
    expect(symbolFor(9999)).toBe('code_9999')
  })

  it('does not double-register a code', () => {
    const codes = Object.keys(CodeSymbols)
    expect(new Set(codes).size).toBe(codes.length)
  })

  // The legacy 001 alias (code 1000) and the canonical success code
  // (code 0) both map to the symbol "ok" per docs/error-codes.md.
  // Every OTHER symbol must still be unique so a server-side code
  // cannot accidentally be routed to the wrong UI string.
  it('reuses only the canonical ok symbol across codes', () => {
    const counts = new Map<string, number>()
    for (const sym of Object.values(CodeSymbols)) {
      counts.set(sym, (counts.get(sym) ?? 0) + 1)
    }
    for (const [sym, n] of counts) {
      if (sym === 'ok') {
        expect(n).toBe(2)
      } else {
        expect(n).toBe(1)
      }
    }
  })
})
