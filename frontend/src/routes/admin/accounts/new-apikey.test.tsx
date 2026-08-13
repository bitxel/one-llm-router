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
import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { toast } from 'sonner'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { api, RouterApiError } from '@/lib/api-client'
import { Err001AccountNameConflict } from '@/lib/errcode'
import { AdminAccountsNewAPIKey } from './new-apikey'

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

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}))

const apiPostMock = vi.mocked(api.post)
const toastSuccessMock = vi.mocked(toast.success)
const toastErrorMock = vi.mocked(toast.error)

function DetailProbe() {
  const { accountId } = useParams({ strict: false })
  return <div data-testid="detail-route">{accountId}</div>
}

async function renderAPIKeyRoute() {
  const rootRoute = createRootRoute({ component: () => <Outlet /> })
  const adminRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/admin',
    component: () => <Outlet />,
  })
  const apiKeyRoute = createRoute({
    getParentRoute: () => adminRoute,
    path: '/accounts/new-apikey',
    component: AdminAccountsNewAPIKey,
  })
  const detailRoute = createRoute({
    getParentRoute: () => adminRoute,
    path: '/accounts/$accountId',
    component: DetailProbe,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([adminRoute.addChildren([apiKeyRoute, detailRoute])]),
    history: createMemoryHistory({ initialEntries: ['/admin/accounts/new-apikey'] }),
  })
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })

  await act(async () => {
    render(
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )
    await router.load()
  })
}

describe('AdminAccountsNewAPIKey', () => {
  beforeEach(() => {
    apiPostMock.mockReset()
    toastSuccessMock.mockReset()
    toastErrorMock.mockReset()
  })

  it('creates an api_key account through the legacy admin endpoint and navigates to detail', async () => {
    apiPostMock.mockResolvedValue({
      id: 88,
      name: 'primary',
      provider: 'openai',
      status: 'active',
      created_at: '2026-04-23T00:00:00Z',
      updated_at: '2026-04-23T00:00:00Z',
    } as never)
    const user = userEvent.setup()
    await renderAPIKeyRoute()

    await user.type(screen.getByTestId('apikey-name-input'), 'primary')
    await user.type(screen.getByTestId('apikey-api-key-input'), 'sk-admin-key')
    await user.click(screen.getByTestId('apikey-submit'))

    expect(apiPostMock).toHaveBeenCalledWith('/api/admin/accounts', {
      name: 'primary',
      provider: 'openai',
      auth_method: 'api_key',
      api_key: 'sk-admin-key',
      base_url: undefined,
      capabilities: undefined,
    })
    expect(await screen.findByTestId('detail-route')).toHaveTextContent('88')
    expect(toastSuccessMock).toHaveBeenCalledWith('API-key account created')
  })

  it('accepts a multi-byte name client-side', async () => {
    apiPostMock.mockResolvedValue({
      id: 90,
      name: '中文账户',
      provider: 'openai',
      status: 'active',
      created_at: '2026-04-23T00:00:00Z',
      updated_at: '2026-04-23T00:00:00Z',
    } as never)
    const user = userEvent.setup()
    await renderAPIKeyRoute()

    await user.type(screen.getByTestId('apikey-name-input'), '中文账户')
    await user.type(screen.getByTestId('apikey-api-key-input'), 'sk-admin-key')
    await user.click(screen.getByTestId('apikey-submit'))

    expect(apiPostMock).toHaveBeenCalledWith('/api/admin/accounts', {
      name: '中文账户',
      provider: 'openai',
      auth_method: 'api_key',
      api_key: 'sk-admin-key',
      base_url: undefined,
      capabilities: undefined,
    })
  })

  it('passes an optional base_url only when provided', async () => {
    apiPostMock.mockResolvedValue({
      id: 89,
      name: 'proxy',
      provider: 'openai',
      base_url: 'https://api.openai.com',
      status: 'active',
      created_at: '2026-04-23T00:00:00Z',
      updated_at: '2026-04-23T00:00:00Z',
    } as never)
    const user = userEvent.setup()
    await renderAPIKeyRoute()

    await user.type(screen.getByTestId('apikey-name-input'), 'proxy')
    await user.type(screen.getByTestId('apikey-api-key-input'), 'sk-proxy-key')
    await user.type(screen.getByTestId('apikey-base-url-input'), 'https://api.openai.com')
    await user.click(screen.getByTestId('apikey-submit'))

    expect(apiPostMock).toHaveBeenCalledWith('/api/admin/accounts', {
      name: 'proxy',
      provider: 'openai',
      auth_method: 'api_key',
      api_key: 'sk-proxy-key',
      base_url: 'https://api.openai.com',
      capabilities: undefined,
    })
  })

  it('shows server business errors without navigating', async () => {
    apiPostMock.mockRejectedValue(
      new RouterApiError({
        code: Err001AccountNameConflict,
        msg: 'account_name_conflict',
        data: { detail: 'account name conflicts with existing active account' },
        requestId: 'req-conflict',
        status: 200,
      }),
    )
    const user = userEvent.setup()
    await renderAPIKeyRoute()

    await user.type(screen.getByTestId('apikey-name-input'), 'primary')
    await user.type(screen.getByTestId('apikey-api-key-input'), 'sk-admin-key')
    await user.click(screen.getByTestId('apikey-submit'))

    expect(await screen.findByTestId('error-banner')).toHaveTextContent('account_name_conflict')
    expect(screen.queryByTestId('detail-route')).not.toBeInTheDocument()
    expect(toastErrorMock).toHaveBeenCalledWith('Failed to create API-key account')
  })

  it('blocks invalid base_url before POST', async () => {
    const user = userEvent.setup()
    await renderAPIKeyRoute()

    await user.type(screen.getByTestId('apikey-name-input'), 'badurl')
    await user.type(screen.getByTestId('apikey-api-key-input'), 'sk-admin-key')
    await user.type(screen.getByTestId('apikey-base-url-input'), 'https://api.openai.com?query=1')
    await user.click(screen.getByTestId('apikey-submit'))

    expect(await screen.findByText(/base_url must be an absolute/i)).toBeInTheDocument()
    expect(apiPostMock).not.toHaveBeenCalled()
  })
})
