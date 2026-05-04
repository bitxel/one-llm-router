import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'

import { SIDEBAR_ITEMS, Sidebar } from './Sidebar'

const SIDEBAR_COLLAPSED_STORAGE_KEY = 'one-llm-router.sidebar.collapsed'

function renderWithRouter(initialPath = '/admin') {
  const rootRoute = createRootRoute({ component: () => <Sidebar /> })
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/admin',
    component: () => <div>dash</div>,
  })
  const settingsRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/admin/settings',
    component: () => <div>settings</div>,
  })
  const accountsRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/admin/accounts',
    component: () => <div>accounts</div>,
  })
  const playgroundRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/admin/playground',
    component: () => <div>playground</div>,
  })
  const requestsRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/admin/requests',
    component: () => <div>requests</div>,
  })
  const tree = rootRoute.addChildren([
    indexRoute,
    settingsRoute,
    accountsRoute,
    playgroundRoute,
    requestsRoute,
  ])
  const router = createRouter({
    routeTree: tree,
    history: createMemoryHistory({ initialEntries: [initialPath] }),
  })
  const qc = new QueryClient()
  return render(
    <QueryClientProvider client={qc}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}

describe('Sidebar', () => {
  beforeEach(() => {
    window.localStorage.removeItem(SIDEBAR_COLLAPSED_STORAGE_KEY)
  })

  it('renders every registered nav entry', async () => {
    renderWithRouter()
    for (const item of SIDEBAR_ITEMS) {
      expect(await screen.findByText(item.label)).toBeInTheDocument()
    }
  })

  it('renders Dashboard, Accounts, Playground, Requests, and Settings as live links', async () => {
    renderWithRouter()
    expect(await screen.findByTestId('sidebar-dashboard')).toHaveAttribute('href', '/admin')
    expect(await screen.findByTestId('sidebar-accounts')).toHaveAttribute('href', '/admin/accounts')
    expect(await screen.findByTestId('sidebar-playground')).toHaveAttribute(
      'href',
      '/admin/playground',
    )
    expect(await screen.findByTestId('sidebar-requests')).toHaveAttribute('href', '/admin/requests')
    expect(await screen.findByTestId('sidebar-settings')).toHaveAttribute('href', '/admin/settings')
  })

  it('disables future entries with a stable planning badge', async () => {
    renderWithRouter()
    for (const item of SIDEBAR_ITEMS.filter((i) => !i.enabled)) {
      const el = await screen.findByTestId(
        `sidebar-${item.label.toLowerCase().replaceAll(' ', '-')}`,
      )
      expect(el).toHaveAttribute('aria-disabled', 'true')
      expect(el).toHaveTextContent(item.badge ?? '')
      expect(el.tagName).toBe('SPAN')
      expect(el).not.toHaveAttribute('href')
    }
  })

  it('marks active entry for the current location', async () => {
    renderWithRouter('/admin/settings')
    const settings = await screen.findByTestId('sidebar-settings')
    // v9 sidebar: active row is flagged by `data-active="true"` so the
    // test hook survives future restyles (the accent stripe is a CSS
    // concern, not an accessibility one).
    expect(settings).toHaveAttribute('data-active', 'true')
  })

  it('keeps Dashboard active on the trailing-slash dashboard route', async () => {
    renderWithRouter('/admin/')
    const dashboard = await screen.findByTestId('sidebar-dashboard')
    expect(dashboard).toHaveAttribute('data-active', 'true')
  })

  it('collapses the desktop sidebar and persists the operator preference', async () => {
    const user = userEvent.setup()
    renderWithRouter('/admin/')

    const nav = await screen.findByRole('navigation', { name: 'Admin navigation' })
    const toggle = await screen.findByTestId('sidebar-collapse-toggle')

    expect(nav).not.toHaveAttribute('data-collapsed')
    expect(toggle).toHaveAttribute('aria-label', 'Collapse sidebar')

    await user.click(toggle)

    expect(nav).toHaveAttribute('data-collapsed', 'true')
    expect(toggle).toHaveAttribute('aria-label', 'Expand sidebar')
    expect(window.localStorage.getItem(SIDEBAR_COLLAPSED_STORAGE_KEY)).toBe('true')
  })

  it('links the lower-left utility slot to GitHub', async () => {
    renderWithRouter('/admin/')

    expect(await screen.findByRole('link', { name: 'github' })).toHaveAttribute(
      'href',
      'https://github.com/bitxel/one-llm-router',
    )
  })
})
