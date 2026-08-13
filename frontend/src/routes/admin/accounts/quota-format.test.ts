import { describe, expect, it } from 'vitest'

import { formatDeadline, formatQuotaDetail, formatWindowSeconds } from './quota-format'

const fixedNow = new Date('2026-08-13T10:00:00Z')

describe('formatDeadline', () => {
  it('renders time-only when the reset is within the next 24 hours', () => {
    expect(formatDeadline('2026-08-13T14:30:00Z', fixedNow)).toBe('14:30:00')
  })

  it('renders date + time when the reset is beyond 24 hours', () => {
    expect(formatDeadline('2026-08-15T02:00:00Z', fixedNow)).toBe('Aug 15 02:00:00')
  })

  it('renders empty for missing values', () => {
    expect(formatDeadline(null, fixedNow)).toBe('')
    expect(formatDeadline(undefined, fixedNow)).toBe('')
  })

  it('falls back to the raw value for unparseable input', () => {
    expect(formatDeadline('not-a-date', fixedNow)).toBe('not-a-date')
  })
})

describe('formatWindowSeconds', () => {
  it('formats minute and hour durations', () => {
    expect(formatWindowSeconds(7200)).toBe('2h')
    expect(formatWindowSeconds(7500)).toBe('2h 5m')
    expect(formatWindowSeconds(300)).toBe('5m')
  })

  it('formats day durations at or beyond 24 hours', () => {
    expect(formatWindowSeconds(86400)).toBe('1d')
    expect(formatWindowSeconds(604800)).toBe('7d')
    expect(formatWindowSeconds(25 * 3600)).toBe('1d')
  })

  it('renders empty for missing or invalid durations', () => {
    expect(formatWindowSeconds(null)).toBe('')
    expect(formatWindowSeconds(undefined)).toBe('')
    expect(formatWindowSeconds(0)).toBe('')
    expect(formatWindowSeconds(-5)).toBe('')
  })
})

describe('formatQuotaDetail', () => {
  it('combines reset time and window duration', () => {
    expect(formatQuotaDetail('2026-08-13T14:30:00Z', 7200, fixedNow)).toBe('14:30:00 / 2h')
  })

  it('omits the window prefix when only a reset time is present', () => {
    expect(formatQuotaDetail('2026-08-13T14:30:00Z', null, fixedNow)).toBe('14:30:00')
  })

  it('shows only the window duration when no reset time is present', () => {
    expect(formatQuotaDetail(null, 7200, fixedNow)).toBe('2h')
  })

  it('renders empty when neither is present', () => {
    expect(formatQuotaDetail(null, null, fixedNow)).toBe('')
    expect(formatQuotaDetail(undefined, undefined, fixedNow)).toBe('')
  })
})
