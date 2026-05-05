import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
  useParams,
} from '@tanstack/react-router'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createContext, type ReactNode, useContext } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import {
  Err003AlreadyConsumed,
  Err003OAuthFlowInProgress,
  Err003OAuthStateMismatch,
  Err003OAuthUpstreamError,
} from '@/lib/errcode'
import type { UseOAuthFlowResult } from '@/lib/oauth-flow'
import { RouterApiError } from '@/lib/router-api-error'

vi.mock('sonner', () => ({
  toast: {
    error: vi.fn(),
  },
}))

vi.mock('@/generated/openapi', async () => {
  const actual = await vi.importActual<typeof import('@/generated/openapi')>('@/generated/openapi')
  return {
    ...actual,
    oauthBrowserStart: vi.fn(),
    oauthBrowserManualCallback: vi.fn(),
    oauthCancel: vi.fn(),
  }
})

vi.mock('@/lib/router-api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/router-api')>('@/lib/router-api')
  return {
    ...actual,
    callAdmin: vi.fn(),
  }
})

vi.mock('@/lib/oauth-flow', async () => {
  const actual = await vi.importActual<typeof import('@/lib/oauth-flow')>('@/lib/oauth-flow')
  return {
    ...actual,
    useOAuthFlow: vi.fn(),
  }
})

import { oauthBrowserManualCallback, oauthBrowserStart, oauthCancel } from '@/generated/openapi'
import { type OAuthFlowSnapshot, useOAuthFlow } from '@/lib/oauth-flow'
import { callAdmin } from '@/lib/router-api'

import { AdminAccountsNewOAuth } from './new-oauth'

const callAdminMock = vi.mocked(callAdmin)
const oauthBrowserStartMock = vi.mocked(oauthBrowserStart)
const oauthBrowserManualCallbackMock = vi.mocked(oauthBrowserManualCallback)
const oauthCancelMock = vi.mocked(oauthCancel)
const useOAuthFlowMock = vi.mocked(useOAuthFlow)

const browserStartFlowID = 'fl_browser_start_1234567890'
const RouteRenderTickContext = createContext(0)
const idleSnapshot: OAuthFlowSnapshot = { status: 'idle' }
const pendingBrowserSnapshot: OAuthFlowSnapshot = {
  status: 'pending',
  method: 'browser',
  flow_id: browserStartFlowID,
  listener_bound: true,
  created_at: '2026-04-21T12:00:00Z',
  expires_at: '2026-04-21T12:05:00Z',
}
const successSnapshot: OAuthFlowSnapshot = {
  status: 'success',
  method: 'browser',
  flow_id: browserStartFlowID,
  rail: 'loopback',
  account: {
    id: 77,
    name: 'op@example.com',
    provider: 'openai',
    auth_method: 'oauth_browser',
    status: 'active',
  },
}

let currentFlowResult: UseOAuthFlowResult
let refetchMock: ReturnType<typeof vi.fn>
let openedTabs: Array<{
  location: { href: string }
  opener: unknown
  close: ReturnType<typeof vi.fn>
}>

function setFlowResult(next: Partial<UseOAuthFlowResult>) {
  currentFlowResult = {
    status: 'idle',
    flow: null,
    isPolling: false,
    error: null,
    isError: false,
    isFetching: false,
    isLoading: false,
    refetch: refetchMock,
    ...next,
  }
}

function FlowDetailProbe() {
  const { accountId } = useParams({ strict: false })
  return <div data-testid="detail-route">{accountId}</div>
}

function NewOAuthRouteProbe() {
  useContext(RouteRenderTickContext)
  return <AdminAccountsNewOAuth />
}

async function renderWithRouter() {
  const rootRoute = createRootRoute({ component: () => <Outlet /> })
  const adminRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/admin',
    component: () => <Outlet />,
  })
  const newOAuthRoute = createRoute({
    getParentRoute: () => adminRoute,
    path: '/accounts/new-oauth',
    component: NewOAuthRouteProbe,
  })
  const detailRoute = createRoute({
    getParentRoute: () => adminRoute,
    path: '/accounts/$accountId',
    component: FlowDetailProbe,
  })
  const routeTree = rootRoute.addChildren([adminRoute.addChildren([newOAuthRoute, detailRoute])])
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: ['/admin/accounts/new-oauth'] }),
  })
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
  let renderVersion = 0
  const renderTree = (version: number): ReactNode => (
    <QueryClientProvider client={queryClient}>
      <RouteRenderTickContext.Provider value={version}>
        <RouterProvider router={router} />
      </RouteRenderTickContext.Provider>
    </QueryClientProvider>
  )
  const view = render(renderTree(renderVersion))
  await act(async () => {
    await router.load()
  })
  return {
    ...view,
    async rerenderRoute() {
      renderVersion += 1
      await act(async () => {
        view.rerender(renderTree(renderVersion))
      })
    },
  }
}

beforeEach(() => {
  openedTabs = []
  refetchMock = vi.fn().mockResolvedValue({ data: idleSnapshot } as never)
  setFlowResult({})
  useOAuthFlowMock.mockImplementation(() => currentFlowResult)
  oauthBrowserStartMock.mockResolvedValue({} as never)
  oauthBrowserManualCallbackMock.mockResolvedValue({} as never)
  oauthCancelMock.mockResolvedValue({} as never)
  callAdminMock.mockReset()
  vi.spyOn(window, 'open').mockImplementation((url) => {
    const tab = {
      location: { href: typeof url === 'string' ? url : '' },
      opener: window,
      close: vi.fn(),
    }
    openedTabs.push(tab)
    return tab as unknown as Window
  })
})

describe('AdminAccountsNewOAuth', () => {
  it('renders the large-title start screen with one intro and one action', async () => {
    await renderWithRouter()

    const intro =
      'Open a ChatGPT sign-in tab. If the browser cannot return to this router, paste the localhost callback URL below.'
    expect(screen.getByRole('heading', { name: /add chatgpt oauth account/i })).toBeInTheDocument()
    expect(screen.getByText('Browser OAuth')).toBeInTheDocument()
    expect(screen.getAllByText(intro)).toHaveLength(1)
    expect(screen.getByText(intro)).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        name: /browser oauth/i,
        level: 3,
      }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        name: /flow status/i,
      }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        name: /pending flow/i,
      }),
    ).not.toBeInTheDocument()
    expect(screen.getAllByRole('button')).toHaveLength(1)
    expect(screen.getByTestId('oauth-start-button')).toHaveTextContent('Start browser sign-in')
  })

  it('starts the browser flow, keeps the paste callback input mounted, and navigates on poll success', async () => {
    callAdminMock.mockResolvedValueOnce({
      flow_id: browserStartFlowID,
      authorize_url: 'https://auth.openai.com/oauth/authorize?state=s_abc',
      callback_url: 'http://localhost:1455/auth/callback',
      listener_bound: true,
      expires_at: '2026-04-21T12:05:00Z',
      method: 'browser',
    } as never)
    refetchMock.mockResolvedValue({ data: pendingBrowserSnapshot } as never)

    const view = await renderWithRouter()
    const user = userEvent.setup()

    await user.click(screen.getByTestId('oauth-start-button'))

    expect(window.open).toHaveBeenCalledWith('about:blank', '_blank')
    expect(openedTabs[0]?.opener).toBeNull()
    expect(openedTabs[0]?.location.href).toBe('https://auth.openai.com/oauth/authorize?state=s_abc')
    expect(refetchMock).toHaveBeenCalledTimes(1)
    expect(await screen.findByLabelText('Paste callback URL')).toBeInTheDocument()
    expect(screen.getByTestId('oauth-flow-status')).toHaveTextContent(
      'Expires at Apr 21, 2026, 12:05 UTC',
    )
    expect(screen.getByTestId('oauth-flow-status')).not.toHaveTextContent('pending')
    expect(screen.getByTestId('oauth-flow-status')).not.toHaveTextContent('Listener active')

    setFlowResult({
      status: 'success',
      flow: successSnapshot,
      isPolling: false,
    })
    await view.rerenderRoute()

    await waitFor(() => {
      expect(screen.getByTestId('detail-route')).toHaveTextContent('77')
    })
  })

  it('ignores stale success snapshots that do not belong to the current page flow', async () => {
    setFlowResult({
      status: 'success',
      flow: {
        ...successSnapshot,
        flow_id: 'fl_stale_success_1234567890',
      },
      isPolling: false,
    })

    await renderWithRouter()

    expect(screen.getByTestId('oauth-start-button')).toBeInTheDocument()
    expect(screen.queryByTestId('detail-route')).not.toBeInTheDocument()
  })

  it('keeps the callback URL editable on oauth_state_mismatch', async () => {
    setFlowResult({
      status: 'pending',
      flow: pendingBrowserSnapshot,
      isPolling: true,
    })
    callAdminMock.mockRejectedValueOnce(
      new RouterApiError({
        code: Err003OAuthStateMismatch,
        msg: 'oauth_state_mismatch',
        data: {},
        requestId: 'req-state',
        status: 200,
      }),
    )

    await renderWithRouter()
    const user = userEvent.setup()

    const input = await screen.findByLabelText('Paste callback URL')
    await user.type(input, 'http://localhost:1455/auth/callback?code=bad&state=wrong')
    await user.click(screen.getByRole('button', { name: 'Submit callback URL' }))

    expect(await screen.findByTestId('oauth-inline-error')).toHaveTextContent('State mismatch')
    expect(input).toHaveValue('http://localhost:1455/auth/callback?code=bad&state=wrong')
  })

  it('shows provider_error inline and keeps the callback URL editable on oauth_upstream_error', async () => {
    setFlowResult({
      status: 'pending',
      flow: pendingBrowserSnapshot,
      isPolling: true,
    })
    callAdminMock.mockRejectedValueOnce(
      new RouterApiError({
        code: Err003OAuthUpstreamError,
        msg: 'oauth_upstream_error',
        data: { provider_error: 'server_error', provider_message: 'upstream busy' },
        requestId: 'req-upstream',
        status: 200,
      }),
    )

    await renderWithRouter()
    const user = userEvent.setup()

    const input = await screen.findByLabelText('Paste callback URL')
    await user.type(input, 'http://localhost:1455/auth/callback?error=server_error&state=ok')
    await user.click(screen.getByRole('button', { name: 'Submit callback URL' }))

    const inline = await screen.findByTestId('oauth-inline-error')
    expect(inline).toHaveTextContent('OpenAI rejected the callback')
    expect(inline).toHaveTextContent('server_error')
    expect(input).toHaveValue('http://localhost:1455/auth/callback?error=server_error&state=ok')
  })

  it('conceals the pasted callback URL and shows only a safe summary', async () => {
    setFlowResult({
      status: 'pending',
      flow: pendingBrowserSnapshot,
      isPolling: true,
    })

    await renderWithRouter()
    const user = userEvent.setup()

    const input = await screen.findByLabelText('Paste callback URL')
    expect(input).toHaveAttribute('type', 'password')

    await user.type(
      input,
      'http://localhost:1455/auth/callback?code=sensitive-code&state=sensitive-state',
    )

    const summary = screen.getByTestId('oauth-callback-summary')
    expect(summary).toHaveTextContent('localhost:1455/auth/callback')
    expect(summary).toHaveTextContent('code present')
    expect(summary).toHaveTextContent('state present')
    expect(summary).not.toHaveTextContent('sensitive-code')
    expect(summary).not.toHaveTextContent('sensitive-state')
    expect(input).toHaveAttribute('type', 'password')
    expect(screen.queryByRole('button', { name: 'Show raw URL' })).not.toBeInTheDocument()
  })

  it('renders the paste-only badge when the loopback listener is unavailable', async () => {
    callAdminMock.mockResolvedValueOnce({
      flow_id: 'fl_browser_start_1234567890',
      authorize_url: 'https://auth.openai.com/oauth/authorize?state=s_abc',
      callback_url: 'http://localhost:1455/auth/callback',
      listener_bound: false,
      expires_at: '2026-04-21T12:05:00Z',
      method: 'browser',
    } as never)
    refetchMock.mockResolvedValue({
      data: {
        status: 'pending',
        method: 'device',
        flow_id: 'fl_conflict_1234567890',
        user_code: 'ABCD-1234',
        verification_url: 'https://auth.openai.com/codex/device',
        expires_at: '2026-04-21T12:05:00Z',
        created_at: '2026-04-21T12:00:00Z',
      },
    } as never)

    await renderWithRouter()
    const user = userEvent.setup()

    await user.click(screen.getByTestId('oauth-start-button'))

    expect(await screen.findByTestId('oauth-paste-only-badge')).toHaveTextContent('Paste-only mode')
    expect(screen.getByLabelText('Paste callback URL')).toBeInTheDocument()
  })

  it('treats already_consumed as success by refetching flow status and navigating', async () => {
    setFlowResult({
      status: 'pending',
      flow: pendingBrowserSnapshot,
      isPolling: true,
    })
    callAdminMock.mockRejectedValueOnce(
      new RouterApiError({
        code: Err003AlreadyConsumed,
        msg: 'already_consumed',
        data: { rail_won: 'loopback' },
        requestId: 'req-cas',
        status: 200,
      }),
    )
    refetchMock.mockResolvedValueOnce({ data: successSnapshot } as never)

    await renderWithRouter()
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText('Paste callback URL'),
      'http://localhost:1455/auth/callback?code=winner&state=s_ok',
    )
    await user.click(screen.getByRole('button', { name: 'Submit callback URL' }))

    await waitFor(() => {
      expect(screen.getByTestId('detail-route')).toHaveTextContent('77')
    })
  })

  it('returns to a clean start screen when the pasted callback reports cancelled', async () => {
    setFlowResult({
      status: 'pending',
      flow: pendingBrowserSnapshot,
      isPolling: true,
    })
    callAdminMock.mockResolvedValueOnce({
      status: 'cancelled',
      rail: 'manual_paste' as const,
    } as never)

    const view = await renderWithRouter()
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText('Paste callback URL'),
      'http://localhost:1455/auth/callback?error=access_denied&state=s_ok',
    )
    await user.click(screen.getByRole('button', { name: 'Submit callback URL' }))

    setFlowResult({})
    await view.rerenderRoute()
    await waitFor(() => {
      expect(screen.getByTestId('oauth-start-button')).toBeInTheDocument()
    })
    expect(screen.queryByLabelText('Paste callback URL')).not.toBeInTheDocument()
  })

  it('shows the conflict banner and cancels the pending flow before retrying start', async () => {
    callAdminMock
      .mockRejectedValueOnce(
        new RouterApiError({
          code: Err003OAuthFlowInProgress,
          msg: 'oauth_flow_in_progress',
          data: {
            method: 'device',
            flow_id: 'fl_conflict_1234567890',
            verification_url: 'https://auth.openai.com/codex/device',
            expires_at: '2026-04-21T12:05:00Z',
            created_at: '2026-04-21T12:00:00Z',
          },
          requestId: 'req-conflict',
          status: 200,
        }),
      )
      .mockResolvedValueOnce({ status: 'idle' } as never)
      .mockResolvedValueOnce({
        flow_id: 'fl_browser_start_1234567890',
        authorize_url: 'https://auth.openai.com/oauth/authorize?state=s_abc',
        callback_url: 'http://localhost:1455/auth/callback',
        listener_bound: true,
        expires_at: '2026-04-21T12:05:00Z',
        method: 'browser',
      } as never)
    refetchMock.mockResolvedValue({ data: idleSnapshot } as never)

    await renderWithRouter()
    const user = userEvent.setup()

    await user.click(screen.getByTestId('oauth-start-button'))

    expect(await screen.findByTestId('oauth-conflict-banner')).toHaveTextContent(
      'A flow is already pending',
    )

    await user.click(screen.getByTestId('oauth-open-pending'))
    await waitFor(() => {
      expect(window.open).toHaveBeenCalledWith(
        'https://auth.openai.com/codex/device',
        '_blank',
        'noopener,noreferrer',
      )
    })

    await user.click(screen.getByTestId('oauth-cancel-pending'))

    expect(callAdminMock).toHaveBeenCalledTimes(3)
    expect(await screen.findByLabelText('Paste callback URL')).toBeInTheDocument()
  })
})
