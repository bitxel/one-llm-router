import { useQuery } from '@tanstack/react-query'
import type { ReactNode } from 'react'

import { TopBar, type TopBarTag } from '@/components/neo'
import { api } from '@/lib/api-client'

import { Sidebar } from './Sidebar'

interface AppShellProps {
  children: ReactNode
  /** Optional breadcrumb tail rendered after the brand (e.g. `settings`). */
  breadcrumb?: string
  /** Optional slot rendered right of the tags, left of the theme toggle. */
  actions?: ReactNode
}

interface HealthSummary {
  status?: 'healthy' | 'degraded' | 'unhealthy'
  active_accounts?: number
}

/**
 * AppShell — persistent portal chrome for every `/admin/*` route.
 *
 * v9 Neo-Retro layout (mirrors `v9-neoretro-grafana.html`):
 *
 *   ┌────────────────────────────────────────────────────────────┐
 *   │ TopBar (brand bars · wordmark · breadcrumb · tags · toggle)│  48 px
 *   ├──────────┬─────────────────────────────────────────────────┤
 *   │          │                                                 │
 *   │ Sidebar  │   Page content (owns its own <Canvas/> layout)  │
 *   │ 232 px   │                                                 │
 *   │          │                                                 │
 *   └──────────┴─────────────────────────────────────────────────┘
 *
 * The shell polls `/api/admin/health` every 30 s so the TopBar can
 * surface the live `healthy / degraded / unhealthy` LED and account
 * counts. Polling is throttled with `staleTime` so the admin index's
 * own 15 s poll remains authoritative for the V-001 banner.
 */
export function AppShell({ children, breadcrumb, actions }: AppShellProps) {
  const { data: health } = useQuery({
    queryKey: ['admin', 'health', 'shell'],
    queryFn: () => api.get<HealthSummary>('/api/admin/health'),
    staleTime: 15_000,
    refetchInterval: 30_000,
  })

  const statusTag: TopBarTag = health?.status
    ? health.status === 'healthy'
      ? { kind: 'ok', label: 'healthy' }
      : health.status === 'degraded'
        ? { kind: 'warn', label: 'degraded' }
        : { kind: 'warn', label: 'unhealthy' }
    : { kind: 'info', label: '…' }

  const accountsTag: TopBarTag | null =
    typeof health?.active_accounts === 'number'
      ? {
          kind: 'plain',
          label: `${health.active_accounts} active account${
            health.active_accounts === 1 ? '' : 's'
          }`,
        }
      : null

  const tags: TopBarTag[] = []
  tags.push(statusTag)
  if (accountsTag) tags.push(accountsTag)

  return (
    <div className="flex h-full w-full flex-col bg-[var(--bg)] text-[var(--text)]">
      <TopBar
        slash={`/ admin${breadcrumb ? ` · ${breadcrumb}` : ''}`}
        tags={tags}
        actions={actions}
      />
      <div className="flex min-h-0 flex-1 flex-col md:flex-row">
        <Sidebar />
        <main id="main-content" tabIndex={-1} className="min-w-0 flex-1 overflow-y-auto">
          {children}
        </main>
      </div>
    </div>
  )
}
