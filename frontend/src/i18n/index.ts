import i18next, { type i18n as I18nInstance } from 'i18next'
import { initReactI18next } from 'react-i18next'

import { detectLanguage } from './detect'
import { localeDef } from './locales'
import { resources } from './resources'

export { detectLanguage, LANGUAGE_STORAGE_KEY, persistLanguage } from './detect'
export {
  DEFAULT_LOCALE,
  isSupportedLocale,
  type LocaleCode,
  localeDef,
  normalizeLocaleTag,
  SUPPORTED_LOCALES,
} from './locales'

/**
 * Namespace inventory. One namespace per feature area + `common`
 * (shared chrome) + `errors` (envelope error-code copy keyed by the
 * symbols in `lib/errcode.ts`).
 */
export const NAMESPACES = [
  'common',
  'dashboard',
  'accounts',
  'requests',
  'playground',
  'settings',
  'setup',
  'errors',
] as const

export type Namespace = (typeof NAMESPACES)[number]

/**
 * i18next is initialized synchronously: catalogs are statically
 * bundled and no backend is registered, so there is no async loading
 * phase — no key-name flash on first paint and no suspense plumbing
 * in routes or tests.
 */
let initialized = false

export function initI18n(): I18nInstance {
  if (initialized) return i18n
  initialized = true

  void i18n.use(initReactI18next).init({
    resources,
    lng: detectLanguage(),
    fallbackLng: 'en',
    supportedLngs: Object.keys(resources),
    interpolation: {
      // React escapes rendered text; i18next must not double-escape.
      escapeValue: false,
    },
    returnNull: false,
    react: {
      useSuspense: false,
    },
  })

  i18n.on('languageChanged', syncDocumentLanguage)
  syncDocumentLanguage(i18n.language)
  return i18n
}

/** Keep `<html lang>` in step with the active UI language. */
function syncDocumentLanguage(lng: string): void {
  if (typeof document === 'undefined') return
  document.documentElement.setAttribute('lang', localeDef(lng)?.intl ?? lng)
}

export const i18n = i18next.createInstance()
