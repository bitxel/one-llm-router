import { i18n, localeDef } from '@/i18n'

/**
 * Locale-aware Intl formatting helpers.
 *
 * All formatters derive their locale from the active i18next language,
 * so numbers and dates re-render (via react-i18next subscriptions) when
 * the operator switches language.
 *
 * IMPORTANT: `Intl.*` formatter instances must NOT be cached at module
 * scope — a module-level `new Intl.DateTimeFormat('en-US')` freezes the
 * locale for the lifetime of the page. Here instances are memoized per
 * locale tag instead, so a language switch simply picks another cache
 * bucket.
 */

/** BCP 47 tag for Intl.* matching the active UI language. */
export function intlLocale(): string {
  return localeDef(i18n.language)?.intl ?? i18n.language
}

const numberFormatCache = new Map<string, Intl.NumberFormat>()

function cachedNumberFormat(locale: string, options?: Intl.NumberFormatOptions) {
  const key = `${locale}|${JSON.stringify(options ?? null)}`
  let fmt = numberFormatCache.get(key)
  if (!fmt) {
    fmt = new Intl.NumberFormat(locale, options)
    numberFormatCache.set(key, fmt)
  }
  return fmt
}

const dateTimeFormatCache = new Map<string, Intl.DateTimeFormat>()

function cachedDateTimeFormat(locale: string, options?: Intl.DateTimeFormatOptions) {
  const key = `${locale}|${JSON.stringify(options ?? null)}`
  let fmt = dateTimeFormatCache.get(key)
  if (!fmt) {
    fmt = new Intl.DateTimeFormat(locale, options)
    dateTimeFormatCache.set(key, fmt)
  }
  return fmt
}

/** Grouped decimal rendering of a finite number; '-' for missing values. */
export function formatCount(
  value: number | null | undefined,
  options?: Intl.NumberFormatOptions,
): string {
  if (typeof value !== 'number' || Number.isNaN(value)) return '-'
  return cachedNumberFormat(intlLocale(), options).format(value)
}

const DATETIME_OPTIONS: Intl.DateTimeFormatOptions = {
  month: 'short',
  day: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
}

/** "Aug 20 13:07" (en) / "8月20日 13:07" (zh) for a timestamp; raw input when unparseable. */
export function formatDateTime(value: string | Date | null | undefined): string {
  if (value === null || value === undefined || value === '') return ''
  const date = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(date.getTime())) return String(value)
  return cachedDateTimeFormat(intlLocale(), DATETIME_OPTIONS).format(date)
}

const SHORT_DATE_OPTIONS: Intl.DateTimeFormatOptions = {
  year: 'numeric',
  month: 'short',
  day: 'numeric',
}

/** "Aug 20, 2026" (en) / "2026年8月20日" (zh); raw input when unparseable. */
export function formatShortDate(value: string | Date | null | undefined): string {
  if (value === null || value === undefined || value === '') return ''
  const date = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(date.getTime())) return String(value)
  return cachedDateTimeFormat(intlLocale(), SHORT_DATE_OPTIONS).format(date)
}
