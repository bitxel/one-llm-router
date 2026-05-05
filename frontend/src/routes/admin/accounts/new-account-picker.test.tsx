import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
  useLocation,
} from '@tanstack/react-router'
import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { ACCOUNT_AUTH_METHOD_IDS } from '@/lib/account-auth-methods'
import { AdminAccountsNewAccountPicker } from './new-account-picker'
import { strings } from './new-account-picker.strings'

function RouteProbe() {
  const location = useLocation()
  return <div data-testid="route-probe">{location.pathname}</div>
}

async function renderWithRouter() {
  const rootRoute = createRootRoute({ component: () => <Outlet /> })
  const adminRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/admin',
    component: () => <Outlet />,
  })
  const pickerRoute = createRoute({
    getParentRoute: () => adminRoute,
    path: '/accounts/new',
    component: AdminAccountsNewAccountPicker,
  })
  const apiKeyRoute = createRoute({
    getParentRoute: () => adminRoute,
    path: '/accounts/new-apikey',
    component: RouteProbe,
  })
  const browserRoute = createRoute({
    getParentRoute: () => adminRoute,
    path: '/accounts/new-oauth',
    component: RouteProbe,
  })
  const deviceRoute = createRoute({
    getParentRoute: () => adminRoute,
    path: '/accounts/new-oauth-device',
    component: RouteProbe,
  })
  const importRoute = createRoute({
    getParentRoute: () => adminRoute,
    path: '/accounts/new-import',
    component: RouteProbe,
  })
  const routeTree = rootRoute.addChildren([
    adminRoute.addChildren([pickerRoute, apiKeyRoute, browserRoute, deviceRoute, importRoute]),
  ])
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: ['/admin/accounts/new'] }),
  })

  await act(async () => {
    render(<RouterProvider router={router} />)
    await router.load()
  })
}

describe('AdminAccountsNewAccountPicker', () => {
  function getCard(authMethod: string) {
    const card = document.querySelector(`[data-auth-method="${authMethod}"]`)
    if (!(card instanceof HTMLButtonElement)) {
      throw new Error(`missing auth method card: ${authMethod}`)
    }
    return card
  }

  it('renders exactly four live auth-method cards in the required order', async () => {
    await renderWithRouter()

    const cards = screen.getAllByRole('button')

    expect(cards).toHaveLength(4)
    expect(cards.map((card) => card.getAttribute('data-auth-method'))).toEqual(
      ACCOUNT_AUTH_METHOD_IDS,
    )
    expect(screen.getByText(strings.cards.apiKey.title)).toBeInTheDocument()
    expect(screen.getByText(strings.cards.oauthBrowser.title)).toBeInTheDocument()
    expect(screen.getByText(strings.cards.oauthDevice.title)).toBeInTheDocument()
    expect(screen.getByText(strings.cards.oauthImport.title)).toBeInTheDocument()
    expect(screen.queryByText('Paste an OpenAI API key')).not.toBeInTheDocument()
    expect(screen.queryByText('Sign in with ChatGPT in your browser')).not.toBeInTheDocument()
    expect(screen.queryByText('Headless? Use a device code')).not.toBeInTheDocument()
    expect(screen.queryByText('Upload your local ~/.codex/auth.json')).not.toBeInTheDocument()
    expect(cards.map((card) => card.getAttribute('data-available'))).toEqual(
      ACCOUNT_AUTH_METHOD_IDS.map(() => 'true'),
    )
    expect(cards[0]).toHaveAttribute('aria-disabled', 'false')
    expect(cards[1]).toHaveAttribute('aria-disabled', 'false')
    expect(cards[2]).toHaveAttribute('aria-disabled', 'false')
    expect(cards[3]).toHaveAttribute('aria-disabled', 'false')
  })

  it('navigates to the API-key route on click', async () => {
    await renderWithRouter()
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: /api key/i }))

    expect(await screen.findByTestId('route-probe')).toHaveTextContent('/admin/accounts/new-apikey')
  })

  it('navigates to the browser OAuth route on click', async () => {
    await renderWithRouter()
    const user = userEvent.setup()

    await user.click(getCard('oauth_browser'))

    expect(await screen.findByTestId('route-probe')).toHaveTextContent('/admin/accounts/new-oauth')
  })

  it('navigates to the device OAuth route on click', async () => {
    await renderWithRouter()
    const user = userEvent.setup()

    await user.click(getCard('oauth_device'))

    expect(await screen.findByTestId('route-probe')).toHaveTextContent(
      '/admin/accounts/new-oauth-device',
    )
  })

  it('navigates to the import route on click', async () => {
    await renderWithRouter()
    const user = userEvent.setup()

    await user.click(getCard('oauth_import'))

    expect(await screen.findByTestId('route-probe')).toHaveTextContent('/admin/accounts/new-import')
  })

  it('keeps all four cards keyboard-reachable in DOM order', async () => {
    await renderWithRouter()
    const user = userEvent.setup()

    const cards = screen.getAllByRole('button')

    await user.tab()
    expect(cards[0]).toHaveFocus()
    await user.tab()
    expect(cards[1]).toHaveFocus()
    await user.tab()
    expect(cards[2]).toHaveFocus()
    await user.tab()
    expect(cards[3]).toHaveFocus()
  })

  it('navigates on Enter for the focused API-key card', async () => {
    await renderWithRouter()
    const user = userEvent.setup()

    await user.tab()
    expect(screen.getAllByRole('button')[0]).toHaveFocus()

    await user.keyboard('{Enter}')

    expect(await screen.findByTestId('route-probe')).toHaveTextContent('/admin/accounts/new-apikey')
  })

  it('navigates on Space for the focused import card', async () => {
    await renderWithRouter()
    const user = userEvent.setup()

    await user.tab()
    await user.tab()
    await user.tab()
    await user.tab()
    expect(screen.getAllByRole('button')[3]).toHaveFocus()

    await user.keyboard(' ')

    expect(await screen.findByTestId('route-probe')).toHaveTextContent('/admin/accounts/new-import')
  })
})
