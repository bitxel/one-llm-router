import { beforeEach, describe, expect, it } from 'vitest'

import { i18n, initI18n } from '@/i18n'

import { formatCount, formatDateTime, formatShortDate, intlLocale } from './intl'

initI18n()

describe('intlLocale', () => {
  beforeEach(() => {
    void i18n.changeLanguage('en')
  })

  it('maps the i18next language to the Intl locale from the registry', async () => {
    expect(intlLocale()).toBe('en-US')
    await i18n.changeLanguage('zh-CN')
    expect(intlLocale()).toBe('zh-CN')
    await i18n.changeLanguage('en')
  })
})

describe('formatCount', () => {
  beforeEach(() => {
    void i18n.changeLanguage('en')
  })

  it('formats grouped numbers for en', () => {
    expect(formatCount(1234)).toBe('1,234')
  })

  it('keeps grouping for zh-CN', () => {
    expect(formatCount(1234567)).toBe('1,234,567')
  })

  it('renders "-" for missing or NaN values', () => {
    expect(formatCount(undefined)).toBe('-')
    expect(formatCount(null)).toBe('-')
    expect(formatCount(Number.NaN)).toBe('-')
  })
})

describe('formatDateTime', () => {
  beforeEach(() => {
    void i18n.changeLanguage('en')
  })

  const ts = '2026-08-20T13:07:39Z'

  it('renders an English short date+time', () => {
    expect(formatDateTime(ts)).toMatch(/Aug 20/)
  })

  it('renders a Chinese short date+time', async () => {
    await i18n.changeLanguage('zh-CN')
    expect(formatDateTime(ts)).toMatch(/8月20日/)
    await i18n.changeLanguage('en')
  })

  it('returns the raw input when unparseable', () => {
    expect(formatDateTime('not-a-date')).toBe('not-a-date')
  })

  it('returns empty for empty input', () => {
    expect(formatDateTime('')).toBe('')
    expect(formatDateTime(null)).toBe('')
    expect(formatDateTime(undefined)).toBe('')
  })
})

describe('formatShortDate', () => {
  beforeEach(() => {
    void i18n.changeLanguage('en')
  })

  it('renders an English numeric year + month + day', () => {
    expect(formatShortDate('2026-08-20T00:00:00Z')).toMatch(/2026/)
  })

  it('renders Chinese 年/月/日 forms', async () => {
    await i18n.changeLanguage('zh-CN')
    expect(formatShortDate('2026-08-20T00:00:00Z')).toMatch(/2026年8月20日/)
    await i18n.changeLanguage('en')
  })
})
