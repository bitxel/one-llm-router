import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
  useLocation,
  useParams,
} from '@tanstack/react-router'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createContext, type ReactNode, useContext } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { Err003DeviceAuthUnavailable, Err003OAuthFlowInProgress } from '@/lib/errcode'
import type { OAuthFlowSnapshot, UseOAuthFlowResult } from '@/lib/oauth-flow'
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
    oauthDeviceStart: vi.fn(),
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

import { oauthCancel, oauthDeviceStart } from '@/generated/openapi'
import { useOAuthFlow } from '@/lib/oauth-flow'
import { callAdmin } from '@/lib/router-api'

import { AdminAccountsNewOAuthDevice } from './new-oauth-device'

const callAdminMock = vi.mocked(callAdmin)
const oauthDeviceStartMock = vi.mocked(oauthDeviceStart)
const oauthCancelMock = vi.mocked(oauthCancel)
const useOAuthFlowMock = vi.mocked(useOAuthFlow)

const RouteRenderTickContext = createContext(0)
const idleSnapshot: OAuthFlowSnapshot = { status: 'idle' }
const pendingDeviceSnapshot: OAuthFlowSnapshot = {
  status: 'pending',
  method: 'device',
  flow_id: 'fl_device_pending_1234567890',
  user_code: 'ABCD-1234',
  verification_url: 'https://auth.openai.com/codex/device',
  created_at: '2026-04-15T10:00:00Z',
  expires_at: '2026-04-15T10:15:00Z',
}
const successSnapshot: OAuthFlowSnapshot = {
  status: 'success',
  method: 'device',
  flow_id: 'fl_device_pending_1234567890',
  account: {
    id: 81,
    name: 'device@example.com',
    provider: 'openai',
    auth_method: 'oauth_device',
    status: 'active',
  },
}
const expiredSnapshot: OAuthFlowSnapshot = {
  status: 'error',
  method: 'device',
  flow_id: 'fl_device_expired_1234567890',
  error: {
    code: 'expired_token',
    message: 'Device code expired',
  },
}

let currentFlowResult: UseOAuthFlowResult
let refetchMock: ReturnType<typeof vi.fn>

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

function DetailRouteProbe() {
  const { accountId } = useParams({ strict: false })
  return <div data-testid="detail-route">{accountId}</div>
}

function BrowserRouteProbe() {
  const location = useLocation()
  return <div data-testid="browser-route">{location.pathname}</div>
}

function DeviceRouteProbe() {
  useContext(RouteRenderTickContext)
  return <AdminAccountsNewOAuthDevice />
}

async function renderWithRouter() {
  const rootRoute = createRootRoute({ component: () => <Outlet /> })
  const adminRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/admin',
    component: () => <Outlet />,
  })
  const deviceRoute = createRoute({
    getParentRoute: () => adminRoute,
    path: '/accounts/new-oauth-device',
    component: DeviceRouteProbe,
  })
  const browserRoute = createRoute({
    getParentRoute: () => adminRoute,
    path: '/accounts/new-oauth',
    component: BrowserRouteProbe,
  })
  const detailRoute = createRoute({
    getParentRoute: () => adminRoute,
    path: '/accounts/$accountId',
    component: DetailRouteProbe,
  })
  const routeTree = rootRoute.addChildren([
    adminRoute.addChildren([deviceRoute, browserRoute, detailRoute]),
  ])
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: ['/admin/accounts/new-oauth-device'] }),
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
  refetchMock = vi.fn().mockResolvedValue({ data: idleSnapshot } as never)
  setFlowResult({})
  useOAuthFlowMock.mockImplementation(() => currentFlowResult)
  oauthDeviceStartMock.mockResolvedValue({} as never)
  oauthCancelMock.mockResolvedValue({} as never)
  callAdminMock.mockReset()
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: {
      writeText: vi.fn().mockResolvedValue(undefined),
    },
  })
})

afterEach(() => {
  vi.useRealTimers()
})

describe('AdminAccountsNewOAuthDevice', () => {
  it('starts the device flow, renders landmarks, and navigates on success', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-04-15T10:00:00Z'))
    setFlowResult({
      status: 'pending',
      flow: pendingDeviceSnapshot,
      isPolling: true,
    })
    callAdminMock.mockResolvedValueOnce({
      flow_id: pendingDeviceSnapshot.flow_id,
      user_code: 'ABCD-1234',
      verification_url: 'https://auth.openai.com/codex/device',
      interval_seconds: 5,
      expires_at: '2026-04-15T10:15:00Z',
      method: 'device',
    } as never)
    refetchMock.mockResolvedValue({ data: pendingDeviceSnapshot } as never)

    const view = await renderWithRouter()
    await act(async () => {
      await Promise.resolve()
    })

    expect(screen.getByTestId('device-user-code')).toHaveTextContent('ABCD-1234')
    expect(screen.getByTestId('device-countdown')).toHaveTextContent('900s')
    const verificationLink = screen.getByTestId('device-verification-url')
    expect(verificationLink).toHaveAttribute('href', 'https://auth.openai.com/codex/device')
    expect(verificationLink).toHaveAttribute('target', '_blank')
    expect(verificationLink).toHaveAttribute('rel', 'noopener noreferrer')
    expect(screen.getByTestId('device-user-code')).toHaveAttribute('aria-label', 'Device code')
    expect(screen.getByRole('button', { name: 'Copy device code' })).toBeInTheDocument()

    vi.useRealTimers()
    setFlowResult({
      status: 'success',
      flow: successSnapshot,
      isPolling: false,
    })
    await view.rerenderRoute()
    await act(async () => {
      await Promise.resolve()
    })

    await waitFor(() => {
      expect(screen.getByTestId('detail-route')).toHaveTextContent('81')
    })
  })

  it('ignores stale success snapshots that do not belong to the current device page flow', async () => {
    setFlowResult({
      status: 'success',
      flow: {
        ...successSnapshot,
        flow_id: 'fl_stale_success_1234567890',
      },
      isPolling: false,
    })
    callAdminMock.mockResolvedValueOnce({
      flow_id: 'fl_device_start_1234567890',
      user_code: 'WXYZ-9876',
      verification_url: 'https://auth.openai.com/codex/device',
      interval_seconds: 5,
      expires_at: '2026-04-15T10:15:00Z',
      method: 'device',
    } as never)
    refetchMock.mockResolvedValue({ data: pendingDeviceSnapshot } as never)

    await renderWithRouter()
    await act(async () => {
      await Promise.resolve()
    })

    expect(screen.queryByTestId('detail-route')).not.toBeInTheDocument()
    expect(screen.getByTestId('device-user-code')).toHaveTextContent('WXYZ-9876')
  })

  it('shows the in-progress conflict panel, cancels the pending flow, and retries start', async () => {
    callAdminMock
      .mockRejectedValueOnce(
        new RouterApiError({
          code: Err003OAuthFlowInProgress,
          msg: 'oauth_flow_in_progress',
          data: {
            method: 'device',
            flow_id: 'fl_device_conflict_1234567890',
            expires_at: '2026-04-15T10:15:00Z',
            created_at: '2026-04-15T10:00:00Z',
          },
          requestId: 'req-conflict',
          status: 200,
        }),
      )
      .mockResolvedValueOnce({ status: 'idle' } as never)
      .mockResolvedValueOnce({
        flow_id: 'fl_device_restart_1234567890',
        user_code: 'WXYZ-9876',
        verification_url: 'https://auth.openai.com/codex/device',
        interval_seconds: 5,
        expires_at: '2026-04-15T10:25:00Z',
        method: 'device',
      } as never)
    refetchMock.mockResolvedValue({ data: pendingDeviceSnapshot } as never)

    await renderWithRouter()
    const user = userEvent.setup()

    expect(await screen.findByTestId('device-conflict-panel')).toHaveTextContent(
      'A flow is already pending',
    )

    await user.click(screen.getByRole('button', { name: 'Cancel pending flow' }))
    await act(async () => {
      await Promise.resolve()
    })

    expect(oauthCancelMock).toHaveBeenCalledWith({
      body: { flow_id: 'fl_device_conflict_1234567890' },
    })
    await waitFor(() => {
      expect(oauthDeviceStartMock).toHaveBeenCalledTimes(2)
    })
    expect(await screen.findByTestId('device-user-code')).toHaveTextContent('WXYZ-9876')
  })

  it('surfaces device_auth_unavailable with a browser-flow fallback link', async () => {
    callAdminMock.mockRejectedValueOnce(
      new RouterApiError({
        code: Err003DeviceAuthUnavailable,
        msg: 'device_auth_unavailable',
        data: { provider: 'openai' },
        requestId: 'req-unavailable',
        status: 200,
      }),
    )

    await renderWithRouter()
    await act(async () => {
      await Promise.resolve()
    })

    expect(screen.getByTestId('device-unavailable-panel')).toHaveTextContent(
      'This account cannot use the device flow',
    )
    expect(screen.getByRole('link', { name: 'Try browser sign-in instead' })).toHaveAttribute(
      'href',
      '/admin/accounts/new-oauth',
    )
  })

  it('restarts an active pending device flow by cancelling it first', async () => {
    setFlowResult({
      status: 'pending',
      flow: pendingDeviceSnapshot,
      isPolling: true,
    })
    callAdminMock
      .mockResolvedValueOnce({
        flow_id: 'fl_device_start_1234567890',
        user_code: 'ABCD-1234',
        verification_url: 'https://auth.openai.com/codex/device',
        interval_seconds: 5,
        expires_at: '2026-04-15T10:15:00Z',
        method: 'device',
      } as never)
      .mockResolvedValueOnce({ status: 'idle' } as never)
      .mockResolvedValueOnce({
        flow_id: 'fl_device_restart_1234567890',
        user_code: 'WXYZ-9876',
        verification_url: 'https://auth.openai.com/codex/device',
        interval_seconds: 5,
        expires_at: '2026-04-15T10:30:00Z',
        method: 'device',
      } as never)
    refetchMock.mockResolvedValue({ data: pendingDeviceSnapshot } as never)

    await renderWithRouter()
    await act(async () => {
      await Promise.resolve()
    })

    await act(async () => {
      fireEvent.click(screen.getByTestId('device-restart-button'))
    })
    await act(async () => {
      await Promise.resolve()
    })

    expect(oauthCancelMock).toHaveBeenCalledWith({
      body: { flow_id: 'fl_device_pending_1234567890' },
    })
    await waitFor(() => {
      expect(oauthDeviceStartMock).toHaveBeenCalledTimes(2)
    })
  })

  it('waits for the server to declare expiry before showing the expired terminal state', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-04-15T10:14:58Z'))
    setFlowResult({
      status: 'pending',
      flow: {
        ...pendingDeviceSnapshot,
        expires_at: '2026-04-15T10:15:00Z',
      },
      isPolling: true,
    })
    callAdminMock
      .mockResolvedValueOnce({
        flow_id: 'fl_device_start_1234567890',
        user_code: 'ABCD-1234',
        verification_url: 'https://auth.openai.com/codex/device',
        interval_seconds: 5,
        expires_at: '2026-04-15T10:15:00Z',
        method: 'device',
      } as never)
      .mockResolvedValueOnce({
        flow_id: 'fl_device_restart_1234567890',
        user_code: 'ABCD-1234',
        verification_url: 'https://auth.openai.com/codex/device',
        interval_seconds: 5,
        expires_at: '2026-04-15T10:30:00Z',
        method: 'device',
      } as never)
    refetchMock.mockResolvedValue({ data: pendingDeviceSnapshot } as never)

    const view = await renderWithRouter()
    await act(async () => {
      await Promise.resolve()
    })

    await act(async () => {
      vi.advanceTimersByTime(2_500)
    })
    expect(screen.getByTestId('device-countdown')).toHaveTextContent('Awaiting server…')

    setFlowResult({
      status: 'error',
      flow: expiredSnapshot,
      isPolling: false,
    })
    await view.rerenderRoute()

    expect(screen.getByTestId('device-status-pill')).toHaveTextContent('Flow expired')

    const startCallsBeforeRestart = oauthDeviceStartMock.mock.calls.length

    await act(async () => {
      fireEvent.click(screen.getByTestId('device-restart-button'))
    })
    await act(async () => {
      await Promise.resolve()
    })

    expect(oauthDeviceStartMock).toHaveBeenCalledTimes(startCallsBeforeRestart + 1)
  })

  it('copies the device code and verification URL, then resets the buttons after 2 seconds', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-04-15T10:00:00Z'))
    setFlowResult({
      status: 'pending',
      flow: pendingDeviceSnapshot,
      isPolling: true,
    })
    callAdminMock.mockResolvedValueOnce({
      flow_id: 'fl_device_start_1234567890',
      user_code: 'ABCD-1234',
      verification_url: 'https://auth.openai.com/codex/device',
      interval_seconds: 5,
      expires_at: '2026-04-15T10:15:00Z',
      method: 'device',
    } as never)
    refetchMock.mockResolvedValue({ data: pendingDeviceSnapshot } as never)

    await renderWithRouter()
    await act(async () => {
      await Promise.resolve()
    })
    const clipboard = vi.mocked(navigator.clipboard.writeText)

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Copy device code' }))
      fireEvent.click(screen.getByRole('button', { name: 'Copy verification URL' }))
    })

    expect(clipboard).toHaveBeenNthCalledWith(1, 'ABCD-1234')
    expect(clipboard).toHaveBeenNthCalledWith(2, 'https://auth.openai.com/codex/device')
    expect(screen.getAllByText('Copied')).toHaveLength(2)

    await act(async () => {
      vi.advanceTimersByTime(2_000)
    })

    expect(screen.getByText('Copy device code')).toBeInTheDocument()
    expect(screen.getByText('Copy verification URL')).toBeInTheDocument()
  })
})
