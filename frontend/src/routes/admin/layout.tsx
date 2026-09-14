import { Outlet, useLocation } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { AppShell } from '@/components/shared/AppShell'

/**
 * AdminLayout — authenticated portal chrome for every `/admin/*`
 * route. Feeds `AppShell` a location-aware breadcrumb so the TopBar
 * reads `/ admin · settings` on the Settings page and `/ admin` on the
 * landing one. `AppShell` owns the health polling + toggles —
 * this layout is intentionally thin.
 */
export function AdminLayout() {
  const location = useLocation()
  const { t } = useTranslation('common')
  const breadcrumb = location.pathname.startsWith('/admin/settings')
    ? t('breadcrumb.settings')
    : location.pathname.startsWith('/admin/playground')
      ? t('breadcrumb.playground')
      : location.pathname.startsWith('/admin/requests')
        ? t('breadcrumb.requests')
        : location.pathname.startsWith('/admin/accounts')
          ? t('breadcrumb.accounts')
          : location.pathname === '/admin' || location.pathname === '/admin/'
            ? t('breadcrumb.dashboard')
            : undefined
  return (
    <AppShell breadcrumb={breadcrumb}>
      <Outlet />
    </AppShell>
  )
}
