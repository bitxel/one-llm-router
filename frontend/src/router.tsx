import type { QueryClient } from '@tanstack/react-query'
import {
  createRootRouteWithContext,
  createRoute,
  getRouteApi,
  lazyRouteComponent,
  Navigate,
  Outlet,
} from '@tanstack/react-router'
import { z } from 'zod'

/**
 * Route tree. We intentionally build the tree imperatively (rather
 * than via file-based routing) to keep the 002 scaffold small and
 * easily traceable during review. File-based routing can be adopted
 * incrementally starting with 003 as feature modules land.
 */

export interface RouterContext {
  queryClient: QueryClient
}

// Root error component. Per spec §US-3 Edge-2 an unhandled error in
// the portal MUST surface a visible banner without freezing navigation
// — TanStack Router calls this component whenever a route (or one of
// its descendants) throws during render or loading, which is the
// easiest React-idiomatic way to satisfy the contract without a
// bespoke error boundary in every leaf page.
function RouteErrorFallback({ error }: { error: Error }) {
  return (
    <div role="alert" data-testid="route-error-fallback" className="p-6">
      <div
        className="mx-auto flex max-w-2xl flex-col gap-3 border p-4 text-[13px] leading-[1.55]"
        style={{
          borderColor: 'var(--err)',
          background: 'color-mix(in oklch, var(--err) 12%, transparent)',
          color: 'var(--err)',
          borderRadius: 2,
        }}
      >
        <span className="font-medium uppercase tracking-[0.06em]">Something went wrong</span>
        <p className="text-[12.5px] text-[var(--text-dim)]">
          The admin portal hit an unexpected error while rendering this page. Navigation still works
          — pick another section from the left, or reload this page.
        </p>
        {error?.message ? (
          <pre
            className="max-h-40 overflow-auto border p-2 font-mono text-[11.5px] whitespace-pre-wrap"
            style={{
              background: 'var(--panel-hi)',
              borderColor: 'var(--line-2)',
              color: 'var(--text)',
              borderRadius: 2,
            }}
          >
            {error.message}
          </pre>
        ) : null}
      </div>
    </div>
  )
}

export const rootRoute = createRootRouteWithContext<RouterContext>()({
  component: () => <Outlet />,
  errorComponent: RouteErrorFallback,
})

const setupProvenanceSearchSchema = z.object({
  from: z.literal('setup').optional(),
})

const adminDashboardSearchSchema = z.object({
  range: z.enum(['1h', '1d', '7d', '30d']).optional().catch(undefined),
  account_id: z.coerce.number().int().positive().optional().catch(undefined),
})

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: () => <Navigate to="/admin" replace />,
})

const setupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/setup',
  component: lazyRouteComponent(() => import('./routes/setup/layout'), 'SetupLayout'),
})

const setupIndexRoute = createRoute({
  getParentRoute: () => setupRoute,
  path: '/',
  component: lazyRouteComponent(() => import('./routes/setup/wizard'), 'SetupWizard'),
})

const adminRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/admin',
  component: lazyRouteComponent(() => import('./routes/admin/layout'), 'AdminLayout'),
})

const adminIndexRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/',
  validateSearch: adminDashboardSearchSchema,
  component: lazyRouteComponent(() => import('./routes/admin/index'), 'AdminIndex'),
})

const adminSettingsRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/settings',
  component: lazyRouteComponent(() => import('./routes/admin/settings'), 'AdminSettings'),
})

const adminPlaygroundRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/playground',
  component: lazyRouteComponent(() => import('./routes/admin/playground'), 'AdminPlayground'),
})

const adminRequestsSearchSchema = z.object({
  search: z.string().optional(),
  start: z.string().optional(),
  end: z.string().optional(),
  account_id: z.string().optional(),
  outcome: z.string().optional(),
  model: z.string().optional(),
  response_mode: z.string().optional(),
  before: z.coerce.number().int().positive().optional().catch(undefined),
  page: z.coerce.number().int().positive().optional().catch(undefined),
  limit: z.coerce.number().int().min(1).max(200).optional().catch(undefined),
  detail: z.coerce.number().int().positive().optional().catch(undefined),
})

const adminRequestsRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/requests',
  validateSearch: adminRequestsSearchSchema,
  component: lazyRouteComponent(() => import('./routes/admin/requests'), 'AdminRequests'),
})

const adminAccountsRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/accounts',
  component: lazyRouteComponent(() => import('./routes/admin/accounts/list'), 'AdminAccountsList'),
})

const adminAccountsNewRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/accounts/new',
  component: lazyRouteComponent(
    () => import('./routes/admin/accounts/new-account-picker'),
    'AdminAccountsNewAccountPicker',
  ),
})

const adminAccountsNewAPIKeyRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/accounts/new-apikey',
  component: lazyRouteComponent(
    () => import('./routes/admin/accounts/new-apikey'),
    'AdminAccountsNewAPIKey',
  ),
})

const adminAccountsNewOAuthRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/accounts/new-oauth',
  validateSearch: setupProvenanceSearchSchema,
  component: lazyRouteComponent(
    () => import('./routes/admin/accounts/new-oauth'),
    'AdminAccountsNewOAuth',
  ),
})

const adminAccountsNewOAuthDeviceRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/accounts/new-oauth-device',
  validateSearch: setupProvenanceSearchSchema,
  component: lazyRouteComponent(
    () => import('./routes/admin/accounts/new-oauth-device'),
    'AdminAccountsNewOAuthDevice',
  ),
})

const adminAccountsNewImportRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/accounts/new-import',
  validateSearch: setupProvenanceSearchSchema,
  component: lazyRouteComponent(
    () => import('./routes/admin/accounts/new-import'),
    'AdminAccountsNewImport',
  ),
})

const adminAccountDetailRoute = createRoute({
  getParentRoute: () => adminRoute,
  path: '/accounts/$accountId',
  component: lazyRouteComponent(
    () => import('./routes/admin/accounts/detail'),
    'AdminAccountDetail',
  ),
})

export const routeTree = rootRoute.addChildren([
  indexRoute,
  setupRoute.addChildren([setupIndexRoute]),
  adminRoute.addChildren([
    adminIndexRoute,
    adminSettingsRoute,
    adminPlaygroundRoute,
    adminRequestsRoute,
    adminAccountsRoute,
    adminAccountsNewRoute,
    adminAccountsNewAPIKeyRoute,
    adminAccountsNewOAuthRoute,
    adminAccountsNewOAuthDeviceRoute,
    adminAccountsNewImportRoute,
    adminAccountDetailRoute,
  ]),
])

export const adminAccountsNewOAuthRouteApi = getRouteApi('/admin/accounts/new-oauth')
export const adminAccountsNewOAuthDeviceRouteApi = getRouteApi('/admin/accounts/new-oauth-device')
export const adminDashboardRouteApi = getRouteApi('/admin/')
export const adminRequestsRouteApi = getRouteApi('/admin/requests')
