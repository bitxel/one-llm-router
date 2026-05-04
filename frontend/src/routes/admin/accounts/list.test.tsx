import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { toast } from 'sonner'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api-client'

import { AdminAccountsList } from './list'

vi.mock('@/lib/api-client', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api-client')>('@/lib/api-client')
  return {
    ...actual,
    api: {
      ...actual.api,
      get: vi.fn(),
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

const apiGetMock = vi.mocked(api.get)
const apiPostMock = vi.mocked(api.post)
const toastSuccessMock = vi.mocked(toast.success)

async function renderList() {
  const rootRoute = createRootRoute({ component: () => <Outlet /> })
  const adminRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/admin',
    component: () => <Outlet />,
  })
  const listRoute = createRoute({
    getParentRoute: () => adminRoute,
    path: '/accounts',
    component: AdminAccountsList,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([adminRoute.addChildren([listRoute])]),
    history: createMemoryHistory({ initialEntries: ['/admin/accounts'] }),
  })
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
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

describe('AdminAccountsList', () => {
  beforeEach(() => {
    apiGetMock.mockReset()
    apiPostMock.mockReset()
    toastSuccessMock.mockReset()
  })

  it('renders oauth plan labels identically and omits oauth-only metadata on api_key rows', async () => {
    apiGetMock.mockResolvedValue({
      total: 4,
      accounts: [
        {
          id: 1,
          name: 'legacy-key',
          provider: 'openai',
          auth_method: 'api_key',
          status: 'active',
          base_url: null,
          created_at: '2026-04-22T08:00:00Z',
          updated_at: '2026-04-22T08:10:00Z',
        },
        {
          id: 2,
          name: 'plus-account',
          provider: 'openai',
          auth_method: 'oauth_browser',
          status: 'active',
          base_url: 'https://api.openai.com',
          created_at: '2026-04-22T08:00:00Z',
          updated_at: '2026-04-22T08:10:00Z',
          email: 'plus@example.com',
          plan_type: 'chatgpt-plus',
          primary_remaining_quota: '18 messages',
          secondary_remaining_quota: '42 messages',
        },
        {
          id: 3,
          name: 'team-account',
          provider: 'openai',
          auth_method: 'oauth_device',
          status: 'active',
          base_url: 'https://api.openai.com',
          created_at: '2026-04-22T08:00:00Z',
          updated_at: '2026-04-22T08:10:00Z',
          email: 'team@example.com',
          plan_type: 'chatgpt-team',
          quota: {
            primary: { remaining: 12 },
            secondary: { remaining: 34 },
          },
        },
        {
          id: 4,
          name: 'enterprise-account',
          provider: 'openai',
          auth_method: 'oauth_import',
          status: 'active',
          base_url: 'https://api.openai.com',
          created_at: '2026-04-22T08:00:00Z',
          updated_at: '2026-04-22T08:10:00Z',
          email: 'enterprise@example.com',
          plan_type: 'custom-enterprise',
        },
      ],
    } as never)

    await renderList()

    expect(screen.queryByText('Release 003')).not.toBeInTheDocument()
    expect(screen.queryByText('token-free projection')).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Accounts overview' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'All Accounts' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Overview' })).not.toBeInTheDocument()
    expect(await screen.findByTestId('accounts-list')).toHaveClass(
      'md:grid-cols-2',
      'xl:grid-cols-3',
    )
    expect(await screen.findByTestId('account-row-1')).toBeInTheDocument()
    expect(screen.getByTestId('account-row-1')).not.toHaveTextContent('Email')
    expect(screen.getByTestId('account-row-1')).toHaveTextContent('Base URL')

    const plusRow = within(screen.getByTestId('account-row-2'))
    expect(plusRow.getByText('plus@example.com')).toBeInTheDocument()
    expect(plusRow.getByText('ChatGPT Plus')).toBeInTheDocument()
    expect(plusRow.getByText('Primary quota')).toBeInTheDocument()
    expect(plusRow.getByText('Secondary quota')).toBeInTheDocument()
    expect(plusRow.getByText('18 messages')).toBeInTheDocument()
    expect(plusRow.getByText('42 messages')).toBeInTheDocument()
    expect(plusRow.getByText('Active')).toBeInTheDocument()
    expect(plusRow.getByTestId('account-status-active')).toHaveClass('text-[var(--ok)]')
    expect(
      plusRow.getByTestId('account-detail-link-2').querySelector('.lucide-settings'),
    ).not.toBeNull()
    expect(screen.getByTestId('account-row-2')).not.toHaveTextContent('ChatGPT account')
    expect(screen.getByTestId('account-row-2')).not.toHaveTextContent('Last refresh')
    expect(screen.getByTestId('account-row-2')).not.toHaveTextContent('Access expires at')
    expect(screen.getByTestId('account-row-2')).not.toHaveTextContent('Base URL')
    expect(screen.getByTestId('account-row-2')).not.toHaveTextContent('active')

    expect(screen.getByTestId('account-row-3')).toHaveTextContent('ChatGPT Team')
    expect(screen.getByTestId('account-row-3')).toHaveTextContent('12')
    expect(screen.getByTestId('account-row-3')).toHaveTextContent('34')
    expect(screen.getByTestId('account-row-4')).toHaveTextContent('custom-enterprise')
  })

  it('disables an active account from the card action', async () => {
    apiGetMock.mockResolvedValue({
      total: 1,
      accounts: [
        {
          id: 2,
          name: 'plus-account',
          provider: 'openai',
          auth_method: 'oauth_browser',
          status: 'active',
          base_url: 'https://api.openai.com',
          created_at: '2026-04-22T08:00:00Z',
          updated_at: '2026-04-22T08:10:00Z',
          email: 'plus@example.com',
          plan_type: 'chatgpt-plus',
        },
      ],
    } as never)
    apiPostMock.mockResolvedValue({ status: 'disabled' })

    await renderList()
    const user = userEvent.setup()

    await user.click(await screen.findByTestId('disable-account-2'))

    expect(apiPostMock).toHaveBeenCalledWith('/api/admin/accounts/2/disable', {})
    await waitFor(() => {
      expect(toastSuccessMock).toHaveBeenCalledWith('Account disabled')
    })
  })

  it('activates an inactive account from the card action', async () => {
    apiGetMock.mockResolvedValue({
      total: 1,
      accounts: [
        {
          id: 9,
          name: 'paused-account',
          provider: 'openai',
          auth_method: 'api_key',
          status: 'disabled',
          base_url: 'https://api.openai.com',
          created_at: '2026-04-22T08:00:00Z',
          updated_at: '2026-04-22T08:10:00Z',
        },
      ],
    } as never)
    apiPostMock.mockResolvedValue({ status: 'enabled' })

    await renderList()
    const user = userEvent.setup()

    expect(await screen.findByTestId('activate-account-9')).toHaveAccessibleName('Active')
    expect(screen.queryByTestId('disable-account-9')).not.toBeInTheDocument()

    await user.click(screen.getByTestId('activate-account-9'))

    expect(apiPostMock).toHaveBeenCalledWith('/api/admin/accounts/9/enable', {})
    await waitFor(() => {
      expect(toastSuccessMock).toHaveBeenCalledWith('Account activated')
    })
  })
})
