/**
 * Locale registry — the single source of truth for every supported
 * UI language. Adding a language means:
 *
 *   1. Copy `src/locales/en/` → `src/locales/<code>/` and translate.
 *   2. Append one entry here (code, Intl locale, native name, tag prefixes).
 *   3. Done — detection, the switcher, `<html lang>` sync, the catalog
 *      parity test, and typed t() keys pick the entry up automatically.
 *
 * `prefixes` map browser language tags to this locale: the first
 * prefix that a `navigator.language(s)` tag starts with wins (e.g.
 * `zh-TW` → `zh-CN`, `en-GB` → `en`). `code` itself is always matched
 * exactly first.
 */
export interface LocaleDef {
  /** i18next language code — mirrors the folder name under `src/locales/`. */
  readonly code: string
  /** BCP 47 tag handed to `Intl.*` formatters. */
  readonly intl: string
  /** Native name shown inside the language switcher. */
  readonly nativeName: string
  /** Compact switcher segment label. */
  readonly short: string
  /** Browser tags mapping to this locale, most specific first. */
  readonly prefixes: readonly string[]
}

export const SUPPORTED_LOCALES: readonly LocaleDef[] = [
  {
    code: 'en',
    intl: 'en-US',
    nativeName: 'English',
    short: 'EN',
    prefixes: ['en'],
  },
  {
    code: 'zh-CN',
    intl: 'zh-CN',
    nativeName: '中文',
    short: '中文',
    prefixes: ['zh'],
  },
] as const satisfies readonly LocaleDef[]

export const DEFAULT_LOCALE = 'en'

export const LOCALE_CODES = SUPPORTED_LOCALES.map((locale) => locale.code)

export type LocaleCode = (typeof SUPPORTED_LOCALES)[number]['code'] | (string & {})

export function isSupportedLocale(code: string): code is LocaleCode {
  return LOCALE_CODES.includes(code)
}

export function localeDef(code: string): LocaleDef | undefined {
  return SUPPORTED_LOCALES.find((locale) => locale.code === code)
}

/**
 * Resolve an arbitrary BCP 47 tag (from localStorage or the browser)
 * to a supported locale. Exact code match first, then prefix match.
 */
export function normalizeLocaleTag(tag: string | null | undefined): LocaleCode | null {
  if (!tag) return null
  const lower = tag.trim().toLowerCase()
  if (!lower) return null
  const exact = SUPPORTED_LOCALES.find((locale) => locale.code.toLowerCase() === lower)
  if (exact) return exact.code
  const prefixed = SUPPORTED_LOCALES.find((locale) =>
    locale.prefixes.some((prefix) => lower === prefix || lower.startsWith(`${prefix}-`)),
  )
  return prefixed ? prefixed.code : null
}
