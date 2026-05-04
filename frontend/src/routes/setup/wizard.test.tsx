import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
  useLocation,
} from '@tanstack/react-router'
import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ACCOUNT_AUTH_METHOD_IDS } from '@/lib/account-auth-methods'
import { api } from '@/lib/api-client'
import { SetupWizard } from './wizard'

vi.mock('@/lib/api-client', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api-client')>('@/lib/api-client')
  return {
    ...actual,
    api: {
      ...actual.api,
      post: vi.fn(),
    },
  }
})

const apiPostMock = vi.mocked(api.post)

function RouteProbe() {
  const location = useLocation()
  const search = new URLSearchParams(
    Object.entries(location.search).flatMap(([key, value]) =>
      value == null ? [] : [[key, String(value)]],
    ),
  ).toString()
  return <div data-testid="route-probe">{`${location.pathname}${search ? `?${search}` : ''}`}</div>
}

async function renderWizard(initialEntry = '/setup/') {
  const rootRoute = createRootRoute({ component: () => <Outlet /> })
  const setupRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/setup',
    component: () => <Outlet />,
  })
  const setupIndexRoute = createRoute({
    getParentRoute: () => setupRoute,
    path: '/',
    component: SetupWizard,
  })
  const adminRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/admin',
    component: RouteProbe,
  })
  const browserRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/admin/accounts/new-oauth',
    component: RouteProbe,
  })
  const deviceRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/admin/accounts/new-oauth-device',
    component: RouteProbe,
  })
  const importRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/admin/accounts/new-import',
    component: RouteProbe,
  })

  const router = createRouter({
    routeTree: rootRoute.addChildren([
      setupRoute.addChildren([setupIndexRoute]),
      adminRoute,
      browserRoute,
      deviceRoute,
      importRoute,
    ]),
    history: createMemoryHistory({ initialEntries: [initialEntry] }),
  })

  await act(async () => {
    render(<RouterProvider router={router} />)
    await router.load()
  })
}

async function advanceToAccountStep(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByTestId('wizard-next'))
  await user.click(screen.getByTestId('wizard-next'))
  expect(await screen.findByRole('heading', { name: /^Upstream account$/ })).toBeInTheDocument()
}

async function advanceToReviewStep(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByTestId('wizard-next'))
  await user.click(screen.getByTestId('wizard-next'))
}

describe('SetupWizard multi-mode onboarding', () => {
  beforeEach(() => {
    apiPostMock.mockReset()
    apiPostMock.mockResolvedValue({ redirect: '/admin' } as never)
  })

  it('renders the same four auth methods as the admin picker and defaults to API key', async () => {
    const user = userEvent.setup()
    await renderWizard()

    await advanceToAccountStep(user)

    const cards = screen.getAllByTestId(/^wizard-auth-method-/)
    expect(cards).toHaveLength(4)
    expect(cards.map((card) => card.getAttribute('data-auth-method'))).toEqual(
      ACCOUNT_AUTH_METHOD_IDS,
    )
    expect(
      within(screen.getByTestId('wizard-auth-method-api_key')).getByRole('radio'),
    ).toBeChecked()
    expect(
      within(screen.getByTestId('wizard-auth-method-oauth_browser')).getByRole('radio'),
    ).not.toBeChecked()
    expect(screen.getByTestId('account.name')).toBeInTheDocument()
    expect(screen.getByTestId('account.api_key')).toBeInTheDocument()
  })

  it('preserves inline API-key seeding and sends first_account in setup commit', async () => {
    const user = userEvent.setup()
    await renderWizard()

    await advanceToAccountStep(user)
    await user.clear(screen.getByTestId('account.api_key'))
    await user.type(screen.getByTestId('account.api_key'), 'sk-inline-seed')
    await advanceToReviewStep(user)
    await user.click(screen.getByTestId('wizard-commit'))

    expect(apiPostMock).toHaveBeenLastCalledWith('/api/setup/commit', {
      db: { driver: 'sqlite3', url: 'router.db' },
      first_account: {
        name: 'primary',
        provider: 'openai',
        api_key: 'sk-inline-seed',
        base_url: undefined,
      },
      plugins: {
        admin_auth: { enabled: false },
        client_keys: { enabled: false },
      },
    })
    expect(await screen.findByTestId('route-probe')).toHaveTextContent('/admin')
  })

  it.each([
    ['oauth_browser', '/admin/accounts/new-oauth?from=setup'],
    ['oauth_device', '/admin/accounts/new-oauth-device?from=setup'],
    ['oauth_import', '/admin/accounts/new-import?from=setup'],
  ] as const)('omits first_account and hands off %s mode to the matching onboarding route', async (method, expectedPath) => {
    const user = userEvent.setup()
    await renderWizard()

    await advanceToAccountStep(user)
    await user.click(screen.getByTestId(`wizard-auth-method-${method}`))

    expect(screen.queryByTestId('account.api_key')).not.toBeInTheDocument()

    await advanceToReviewStep(user)
    await user.click(screen.getByTestId('wizard-commit'))

    expect(apiPostMock).toHaveBeenLastCalledWith('/api/setup/commit', {
      db: { driver: 'sqlite3', url: 'router.db' },
      plugins: {
        admin_auth: { enabled: false },
        client_keys: { enabled: false },
      },
    })
    expect(await screen.findByTestId('route-probe')).toHaveTextContent(expectedPath)
  })

  it('supports keyboard commit for deferred modes without re-triggering hidden API-key validation', async () => {
    const user = userEvent.setup()
    await renderWizard()

    await advanceToAccountStep(user)
    await user.click(screen.getByTestId('wizard-auth-method-oauth_browser'))
    await advanceToReviewStep(user)
    await user.keyboard('{Meta>}{Enter}{/Meta}')

    expect(apiPostMock).toHaveBeenCalledTimes(1)
    expect(await screen.findByTestId('route-probe')).toHaveTextContent(
      '/admin/accounts/new-oauth?from=setup',
    )
  })

  it('keeps skip-for-now valid and still omits first_account on commit', async () => {
    const user = userEvent.setup()
    await renderWizard()

    await advanceToAccountStep(user)
    await user.click(screen.getByTestId('wizard-skip-account'))
    await user.click(screen.getByTestId('wizard-next'))
    await user.click(screen.getByTestId('wizard-commit'))

    expect(apiPostMock).toHaveBeenLastCalledWith('/api/setup/commit', {
      db: { driver: 'sqlite3', url: 'router.db' },
      plugins: {
        admin_auth: { enabled: false },
        client_keys: { enabled: false },
      },
    })
    expect(await screen.findByTestId('route-probe')).toHaveTextContent('/admin')
  })

  it('surfaces commit failures for deferred modes and does not hand off prematurely', async () => {
    apiPostMock.mockRejectedValueOnce(new Error('disk full'))
    const user = userEvent.setup()
    await renderWizard()

    await advanceToAccountStep(user)
    await user.click(screen.getByTestId('wizard-auth-method-oauth_browser'))
    await advanceToReviewStep(user)
    await user.click(screen.getByTestId('wizard-commit'))

    expect(await screen.findByTestId('error-banner')).toHaveTextContent('disk full')
    expect(screen.getByRole('heading', { name: /^Commit$/ })).toBeInTheDocument()
    expect(screen.queryByTestId('route-probe')).not.toBeInTheDocument()
  })
})
