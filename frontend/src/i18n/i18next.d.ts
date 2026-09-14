import type { CatalogResources } from './resources'
import 'i18next'

/**
 * Bind the English catalogs (the key-structure source of truth) to
 * i18next's typed t() so every `t('…')` call is checked at compile
 * time — a typo'd or missing key is a `tsc --noEmit` failure, not a
 * runtime escape.
 */
declare module 'i18next' {
  interface CustomTypeOptions {
    defaultNS: 'common'
    resources: CatalogResources['en']
  }
}
