import { beforeEach, describe, expect, it } from 'vitest'

import { i18n, initI18n } from '@/i18n'
import { Err001AccountNotFound, Err001InternalError } from '@/lib/errcode'
import { RouterApiError } from '@/lib/router-api-error'

import { localizedErrorMessage, tDynamic } from './error-message'

initI18n()

function apiError(code: number, msg: string): RouterApiError {
  return new RouterApiError({ code, msg, data: null, requestId: 'req_test', status: 200 })
}

describe('localizedErrorMessage', () => {
  beforeEach(() => {
    void i18n.changeLanguage('en')
  })

  it('renders the errors-catalog entry for a known symbol (en)', () => {
    expect(localizedErrorMessage(apiError(Err001AccountNotFound, 'account not found'))).toBe(
      'Account not found.',
    )
  })

  it('renders the localized entry for zh-CN', async () => {
    await i18n.changeLanguage('zh-CN')
    expect(localizedErrorMessage(apiError(Err001InternalError, 'internal error'))).toBe(
      '路由器发生内部错误。',
    )
    await i18n.changeLanguage('en')
  })

  it('falls back to the canonical server msg for uncataloged symbols', () => {
    // 424242 has no CodeSymbols entry → symbolFor yields code_424242,
    // which has no catalog key → the envelope msg must surface verbatim.
    expect(localizedErrorMessage(apiError(424242, 'uncataloged failure'))).toBe(
      'uncataloged failure',
    )
  })

  it('passes plain Error messages through untouched', () => {
    expect(localizedErrorMessage(new Error('boom'))).toBe('boom')
  })

  it('stringifies non-Error throwables', () => {
    expect(localizedErrorMessage('plain string')).toBe('plain string')
  })
})

describe('tDynamic', () => {
  beforeEach(() => {
    void i18n.changeLanguage('en')
  })

  it('resolves validation-message keys through the catalog', () => {
    expect(tDynamic('setup:validation.nameRequired')).toBe('name is required')
  })

  it('passes non-key strings (zod library defaults) through untouched', () => {
    expect(tDynamic('Number must be greater than or equal to 1')).toBe(
      'Number must be greater than or equal to 1',
    )
  })

  it('maps the zh catalog entry when the UI language is Chinese', async () => {
    await i18n.changeLanguage('zh-CN')
    expect(tDynamic('setup:validation.nameRequired')).toBe('昵称不能为空')
    await i18n.changeLanguage('en')
  })

  it('returns an empty string for empty input', () => {
    expect(tDynamic(undefined)).toBe('')
    expect(tDynamic('')).toBe('')
  })
})
