import accountsEn from '@/locales/en/accounts.json'
import commonEn from '@/locales/en/common.json'
import dashboardEn from '@/locales/en/dashboard.json'
import errorsEn from '@/locales/en/errors.json'
import playgroundEn from '@/locales/en/playground.json'
import requestsEn from '@/locales/en/requests.json'
import settingsEn from '@/locales/en/settings.json'
import setupEn from '@/locales/en/setup.json'
import accountsZh from '@/locales/zh-CN/accounts.json'
import commonZh from '@/locales/zh-CN/common.json'
import dashboardZh from '@/locales/zh-CN/dashboard.json'
import errorsZh from '@/locales/zh-CN/errors.json'
import playgroundZh from '@/locales/zh-CN/playground.json'
import requestsZh from '@/locales/zh-CN/requests.json'
import settingsZh from '@/locales/zh-CN/settings.json'
import setupZh from '@/locales/zh-CN/setup.json'

/**
 * Static catalog assembly. Every namespace is bundled (no runtime
 * fetching) so the SPA works identically from `go:embed` production
 * builds and `pnpm dev`; total payload is tens of kilobytes. If the
 * locale count grows, this is the single place to switch to dynamic
 * `import()` per language.
 */
export const resources = {
  en: {
    common: commonEn,
    dashboard: dashboardEn,
    accounts: accountsEn,
    requests: requestsEn,
    playground: playgroundEn,
    settings: settingsEn,
    setup: setupEn,
    errors: errorsEn,
  },
  'zh-CN': {
    common: commonZh,
    dashboard: dashboardZh,
    accounts: accountsZh,
    requests: requestsZh,
    playground: playgroundZh,
    settings: settingsZh,
    setup: setupZh,
    errors: errorsZh,
  },
} as const

export type CatalogResources = typeof resources
