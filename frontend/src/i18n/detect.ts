import { DEFAULT_LOCALE, type LocaleCode, normalizeLocaleTag } from './locales'

/**
 * Language persistence + browser detection.
 *
 * Detection order (industry best practice for SPAs):
 *   1. An explicitly chosen language persisted in localStorage wins —
 *      the operator's manual choice must always beat inference.
 *   2. Otherwise the browser's preferred languages (`navigator.languages`,
 *      falling back to `navigator.language`) are matched against the
 *      locale registry — a Chinese browser defaults to Chinese, any
 *      other browser defaults to English.
 *   3. Everything else falls back to `en`.
 *
 * The inferred (step 2) result is intentionally NOT persisted: the
 * detection reruns on the next visit until the operator makes an
 * explicit choice, so clearing the stored value restores
 * browser-language behaviour. Only `persistLanguage` (called by the
 * language switcher) writes to localStorage.
 */

export const LANGUAGE_STORAGE_KEY = 'one-llm-router.lang'

function readStoredLocale(): LocaleCode | null {
  if (typeof window === 'undefined') return null
  try {
    return normalizeLocaleTag(window.localStorage.getItem(LANGUAGE_STORAGE_KEY))
  } catch {
    // Storage can be unavailable (private mode); detection continues.
    return null
  }
}

function readBrowserLocale(): LocaleCode | null {
  if (typeof navigator === 'undefined') return null
  const candidates =
    typeof navigator.languages !== 'undefined' && navigator.languages.length > 0
      ? navigator.languages
      : [navigator.language]
  for (const tag of candidates) {
    const locale = normalizeLocaleTag(tag)
    if (locale) return locale
  }
  return null
}

/** Resolve the language the UI should boot with (no side effects). */
export function detectLanguage(): LocaleCode {
  return readStoredLocale() ?? readBrowserLocale() ?? DEFAULT_LOCALE
}

/** Persist an explicit operator choice. Inference never calls this. */
export function persistLanguage(code: LocaleCode): void {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, code)
  } catch {
    // Storage unavailable — the choice applies to this session only.
  }
}
