import { Outlet, useLocation } from '@tanstack/react-router'

import { AppShell } from '@/components/shared/AppShell'

/**
 * AdminLayout — authenticated portal chrome for every `/admin/*`
 * route. Feeds `AppShell` a location-aware breadcrumb so the TopBar
 * reads `/ admin · settings` on the Settings page and `/ admin` on the
 * landing one. `AppShell` owns the health polling + theme toggle —
 * this layout is intentionally thin.
 */
export function AdminLayout() {
  const location = useLocation()
  const breadcrumb = location.pathname.startsWith('/admin/settings')
    ? 'settings'
    : location.pathname.startsWith('/admin/playground')
      ? 'playground'
      : location.pathname.startsWith('/admin/requests')
        ? 'requests'
        : location.pathname.startsWith('/admin/accounts')
          ? 'accounts'
          : location.pathname === '/admin' || location.pathname === '/admin/'
            ? 'dashboard'
            : undefined
  return (
    <AppShell breadcrumb={breadcrumb}>
      <Outlet />
    </AppShell>
  )
}
