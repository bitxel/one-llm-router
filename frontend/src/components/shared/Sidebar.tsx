import { Link, useLocation } from '@tanstack/react-router'
import {
  BarChart3,
  FlaskConical,
  Key,
  LayoutDashboard,
  List,
  PanelLeftClose,
  PanelLeftOpen,
  Settings,
  Users,
} from 'lucide-react'
import { type ComponentType, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'

/**
 * Sidebar keeps the admin IA stable across releases.
 * `Dashboard`, `Accounts`, `Playground`, `Requests`, and `Settings` are
 * live in the current build. The remaining entries stay visible-but-disabled
 * so the shell shape does not jump as future features land.
 *
 * Labels are `common` catalog keys (translated at render time). The
 * `data-testid` slug is derived from the route path — never from the
 * translated label — so the test contract survives language switches.
 *
 * Styling follows v9 Neo-Retro (`specs/002-.../mocks/v9-neoretro-grafana.html`):
 * each row is a dense text line with an accent top-stripe on the active
 * entry, a mono index column on the left, and a compact status badge
 * trailing the disabled entries. No rounded corners; colours come from
 * CSS tokens so the whole column flips cleanly under `data-theme`.
 *
 * Test contract:
 *   - Each row has `data-testid="sidebar-<slug>"`.
 *   - Disabled rows have `aria-disabled="true"` and render
 *     a stable badge string.
 *   - Active row carries `data-active="true"` (new — replaces the
 *     previous `bg-(--color-surface-card)` assertion hook).
 */

/** Stable DOM slug derived from the route path (language-independent). */
export function sidebarSlug(to: string): string {
  const tail = to.replace(/^\/admin\/?/, '')
  return tail === '' ? 'dashboard' : tail
}

export interface SidebarNavItem {
  to: string
  /** `common` namespace key for the translated label. */
  labelKey:
    | 'nav.dashboard'
    | 'nav.accounts'
    | 'nav.playground'
    | 'nav.requests'
    | 'nav.clientKeys'
    | 'nav.observability'
    | 'nav.settings'
  icon: ComponentType<{ className?: string }>
  enabled: boolean
  badge?: 'planned'
  idx: string
}

export const SIDEBAR_ITEMS: readonly SidebarNavItem[] = [
  { to: '/admin', labelKey: 'nav.dashboard', icon: LayoutDashboard, enabled: true, idx: '01' },
  {
    to: '/admin/accounts',
    labelKey: 'nav.accounts',
    icon: Users,
    enabled: true,
    idx: '02',
  },
  {
    to: '/admin/playground',
    labelKey: 'nav.playground',
    icon: FlaskConical,
    enabled: true,
    idx: '03',
  },
  {
    to: '/admin/requests',
    labelKey: 'nav.requests',
    icon: List,
    enabled: true,
    idx: '04',
  },
  {
    to: '/admin/client-keys',
    labelKey: 'nav.clientKeys',
    icon: Key,
    enabled: false,
    badge: 'planned',
    idx: '05',
  },
  {
    to: '/admin/observability',
    labelKey: 'nav.observability',
    icon: BarChart3,
    enabled: false,
    badge: 'planned',
    idx: '06',
  },
  { to: '/admin/settings', labelKey: 'nav.settings', icon: Settings, enabled: true, idx: '07' },
]

const SIDEBAR_COLLAPSED_STORAGE_KEY = 'one-llm-router.sidebar.collapsed'

function readInitialCollapsed(): boolean {
  if (typeof window === 'undefined') return false
  try {
    return window.localStorage.getItem(SIDEBAR_COLLAPSED_STORAGE_KEY) === 'true'
  } catch {
    return false
  }
}

export function Sidebar() {
  const location = useLocation()
  const { t } = useTranslation('common')
  const [collapsed, setCollapsed] = useState(readInitialCollapsed)
  const ToggleIcon = collapsed ? PanelLeftOpen : PanelLeftClose
  const toggleLabel = collapsed ? t('sidebar.expand') : t('sidebar.collapse')

  useEffect(() => {
    try {
      window.localStorage.setItem(SIDEBAR_COLLAPSED_STORAGE_KEY, String(collapsed))
    } catch {
      /* storage can be unavailable in private mode; sidebar still works for the session */
    }
  }, [collapsed])

  return (
    <nav
      aria-label={t('sidebar.label')}
      data-collapsed={collapsed || undefined}
      className={cn(
        'flex w-full shrink-0 flex-col border-b border-[var(--line)] bg-[var(--panel)] md:h-full md:border-r md:border-b-0',
        collapsed ? 'md:w-[68px]' : 'md:w-[232px]',
      )}
    >
      <div
        className={cn(
          'hidden border-b border-[var(--line)] bg-[var(--panel-head)] px-4 py-3 md:flex md:items-center md:justify-between md:gap-3',
          collapsed && 'md:justify-center md:px-2',
        )}
      >
        <div className={cn('min-w-0', collapsed && 'md:sr-only')}>
          <span className="block font-mono text-[10.5px] font-medium uppercase tracking-[0.14em] text-[var(--text-muted)]">
            {t('sidebar.adminPortal')}
          </span>
        </div>
        <button
          type="button"
          aria-label={toggleLabel}
          aria-expanded={!collapsed}
          data-testid="sidebar-collapse-toggle"
          onClick={() => setCollapsed((value) => !value)}
          className="inline-flex h-8 w-8 shrink-0 cursor-pointer items-center justify-center bg-transparent text-[var(--text-muted)] transition-colors hover:bg-[var(--panel-hi)] hover:text-[var(--text)] focus-visible:outline-none focus-visible:[box-shadow:var(--focus)]"
          style={{ borderRadius: 2 }}
        >
          <ToggleIcon className="h-[15px] w-[15px]" strokeWidth={1.9} />
        </button>
      </div>

      <ul className="flex overflow-x-auto md:flex-col md:overflow-visible">
        {SIDEBAR_ITEMS.map((item) => {
          const active =
            item.enabled &&
            ((item.to === '/admin' &&
              (location.pathname === '/admin' || location.pathname === '/admin/')) ||
              location.pathname === item.to ||
              (item.to !== '/admin' && location.pathname.startsWith(`${item.to}/`)))
          const Icon = item.icon
          const label = t(item.labelKey)
          const slug = sidebarSlug(item.to)
          if (!item.enabled) {
            return (
              <li key={item.to} className="shrink-0 md:shrink">
                <span
                  aria-disabled="true"
                  data-testid={`sidebar-${slug}`}
                  title={collapsed ? label : undefined}
                  className={cn(
                    'flex min-h-11 min-w-[118px] cursor-not-allowed items-center justify-center gap-2 border-r border-b-2 border-r-[var(--line)] border-b-transparent px-3 py-2 text-[12.5px] text-[var(--text-faint)]',
                    'md:min-h-0 md:min-w-0 md:justify-start md:gap-[10px] md:border-r-0 md:border-b md:border-l-2 md:border-l-transparent md:border-b-[var(--line)] md:px-4 md:py-[10px]',
                    collapsed && 'md:justify-center md:gap-0 md:px-0',
                  )}
                >
                  <span
                    className={cn(
                      'hidden w-[22px] font-mono text-[10.5px] text-[var(--text-muted)]',
                      !collapsed && 'md:inline',
                    )}
                  >
                    {item.idx}
                  </span>
                  <Icon className="h-[14px] w-[14px] opacity-60" />
                  <span className={cn('truncate', collapsed && 'md:sr-only')}>{label}</span>
                  {item.badge ? (
                    <span
                      className={cn(
                        'ml-auto hidden border px-[6px] py-[1px] font-mono text-[10.5px] uppercase tracking-[0.08em]',
                        !collapsed && 'md:inline',
                      )}
                      style={{
                        color: 'var(--text-muted)',
                        borderColor: 'var(--line-2)',
                        borderRadius: 2,
                      }}
                    >
                      {t('nav.planned')}
                    </span>
                  ) : null}
                </span>
              </li>
            )
          }
          return (
            <li key={item.to} className="shrink-0 md:shrink">
              <Link
                to={item.to}
                data-testid={`sidebar-${slug}`}
                data-active={active || undefined}
                title={collapsed ? label : undefined}
                className={cn(
                  'flex min-h-11 min-w-[118px] items-center justify-center gap-2 border-r border-b-2 border-r-[var(--line)] px-3 py-2 text-[12.5px] transition-colors',
                  'md:min-h-0 md:min-w-0 md:justify-start md:gap-[10px] md:border-r-0 md:border-b md:border-l-2 md:border-b-[var(--line)] md:px-4 md:py-[10px]',
                  collapsed && 'md:justify-center md:gap-0 md:px-0',
                  active
                    ? 'border-b-[var(--accent)] bg-[var(--panel-hi)] text-[var(--text)] md:border-l-[var(--accent)] md:border-b-[var(--line)]'
                    : 'border-b-transparent text-[var(--text-dim)] hover:bg-[var(--panel-hi)] hover:text-[var(--text)] md:border-l-transparent',
                )}
              >
                <span
                  className={cn(
                    'hidden w-[22px] font-mono text-[10.5px]',
                    !collapsed && 'md:inline',
                  )}
                  style={{
                    color: active ? 'var(--accent)' : 'var(--text-muted)',
                    fontFeatureSettings: '"tnum"',
                  }}
                >
                  {item.idx}
                </span>
                <Icon className="h-[14px] w-[14px]" />
                <span className={cn('truncate', collapsed && 'md:sr-only')}>{label}</span>
              </Link>
            </li>
          )
        })}
      </ul>

      <div
        className={cn(
          'mt-auto hidden border-t border-[var(--line)] bg-[var(--panel-head)] px-4 py-3 md:block',
          collapsed && 'md:hidden',
        )}
      >
        <span className="block font-mono text-[10.5px] uppercase tracking-[0.14em] text-[var(--text-muted)]">
          {t('sidebar.docs')}
        </span>
        <a
          href="https://github.com/bitxel/one-llm-router"
          target="_blank"
          rel="noreferrer"
          className="mt-[4px] block font-mono text-[11.5px] text-[var(--text-dim)] underline-offset-4 hover:text-[var(--accent)] hover:underline"
        >
          github
        </a>
      </div>
    </nav>
  )
}
