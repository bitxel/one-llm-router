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

import { accountsExportAuthJson } from '@/generated/openapi'
import { api } from '@/lib/api-client'
import { callAdminAttachment, RouterApiError } from '@/lib/router-api'

import { AdminAccountDetail } from './detail'

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

vi.mock('@/generated/openapi', async () => {
  const actual = await vi.importActual<typeof import('@/generated/openapi')>('@/generated/openapi')
  return {
    ...actual,
    accountsExportAuthJson: vi.fn(),
  }
})

vi.mock('@/lib/router-api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/router-api')>('@/lib/router-api')
  return {
    ...actual,
    callAdmin: vi.fn(),
    callAdminAttachment: vi.fn(),
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
const accountsExportAuthJsonMock = vi.mocked(accountsExportAuthJson)
const callAdminAttachmentMock = vi.mocked(callAdminAttachment)
const toastSuccessMock = vi.mocked(toast.success)
const toastErrorMock = vi.mocked(toast.error)

async function renderDetail(path = '/admin/accounts/42') {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const rootRoute = createRootRoute({ component: () => <Outlet /> })
  const adminRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/admin',
    component: () => <Outlet />,
  })
  const detailRoute = createRoute({
    getParentRoute: () => adminRoute,
    path: '/accounts/$accountId',
    component: AdminAccountDetail,
  })
  const accountsListRoute = createRoute({
    getParentRoute: () => adminRoute,
    path: '/accounts',
    component: () => <div data-testid="accounts-list-route" />,
  })
  const routeTree = rootRoute.addChildren([
    adminRoute.addChildren([accountsListRoute, detailRoute]),
  ])
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [path] }),
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

describe('AdminAccountDetail', () => {
  beforeEach(() => {
    apiGetMock.mockReset()
    apiPostMock.mockReset()
    accountsExportAuthJsonMock.mockReset()
    callAdminAttachmentMock.mockReset()
    toastSuccessMock.mockReset()
    toastErrorMock.mockReset()
    vi.restoreAllMocks()
  })

  it('renders oauth metadata with plan label fallback and shows the export button', async () => {
    apiGetMock.mockResolvedValue({
      id: 42,
      name: 'primary-oauth',
      provider: 'openai',
      auth_method: 'oauth_browser',
      status: 'active',
      base_url: 'https://api.openai.com',
      created_at: '2026-04-22T08:00:00Z',
      updated_at: '2026-04-22T08:10:00Z',
      email: 'detail@example.com',
      plan_type: 'chatgpt-plus',
      primary_used_percent: 28,
      secondary_used_percent: null,
      primary_reset_at: '2026-04-22T14:30:00Z',
      primary_window_seconds: 7200,
      chatgpt_account_id: 'acct_detail_42',
      last_refresh: '2026-04-22T08:05:00Z',
      access_expires_at: '2026-04-22T09:05:00Z',
    })

    await renderDetail()

    expect(screen.queryByText('Release 003')).not.toBeInTheDocument()
    const stripeHeading = screen.getByRole('heading', { name: 'Account detail' })
    expect(stripeHeading).toBeInTheDocument()
    expect(stripeHeading.parentElement).toHaveTextContent('Accounts')
    expect(await screen.findByTestId('account-detail-view')).toBeInTheDocument()
    expect(screen.queryByText('live row')).not.toBeInTheDocument()
    expect(screen.queryByText('token-free')).not.toBeInTheDocument()
    expect(screen.queryByText('Export credentials')).not.toBeInTheDocument()
    expect(screen.queryByText('FR-014')).not.toBeInTheDocument()
    expect(screen.queryByText('soft delete')).not.toBeInTheDocument()
    expect(screen.getByTestId('account-summary-status')).toHaveTextContent('Active')
    expect(screen.getByTestId('account-summary-status')).toHaveClass('text-[var(--ok)]')
    expect(
      within(screen.getByTestId('account-summary-card')).queryByText('active'),
    ).not.toBeInTheDocument()
    const summary = screen.getByTestId('account-summary-card')
    expect(within(summary).getByText('detail@example.com')).toBeInTheDocument()
    expect(within(summary).getByText('openai')).toBeInTheDocument()
    expect(within(summary).getByText('OAuth browser')).toBeInTheDocument()
    const summaryText = summary.textContent ?? ''
    expect(summaryText.indexOf('detail@example.com')).toBeLessThan(summaryText.indexOf('openai'))
    expect(summaryText.indexOf('openai')).toBeLessThan(summaryText.indexOf('OAuth browser'))
    expect(screen.getByTestId('account-oauth-metadata')).toHaveTextContent('detail@example.com')
    expect(screen.getByTestId('account-oauth-metadata')).toHaveTextContent('ChatGPT Plus')
    expect(screen.getByTestId('account-oauth-metadata')).toHaveTextContent('28%')
    expect(screen.getByTestId('account-oauth-metadata')).toHaveTextContent('14:30:00 / 2h')
    expect(screen.getByRole('button', { name: /export auth\.json/i })).toBeInTheDocument()
  })

  it('hides export controls for api_key rows', async () => {
    apiGetMock.mockResolvedValue({
      id: 7,
      name: 'legacy-api-key',
      provider: 'openai',
      auth_method: 'api_key',
      status: 'active',
      base_url: null,
      created_at: '2026-04-22T08:00:00Z',
      updated_at: '2026-04-22T08:10:00Z',
    })

    await renderDetail('/admin/accounts/7')

    expect(await screen.findByTestId('account-detail-view')).toBeInTheDocument()
    expect(screen.queryByTestId('export-auth-json')).not.toBeInTheDocument()
    expect(screen.queryByTestId('account-oauth-metadata')).not.toBeInTheDocument()
    expect(screen.queryByText(/not exportable/i)).not.toBeInTheDocument()
  })

  it('renders non-active account summary status in the danger lane', async () => {
    apiGetMock.mockResolvedValue({
      id: 9,
      name: 'disabled-api-key',
      provider: 'openai',
      auth_method: 'api_key',
      status: 'disabled',
      base_url: null,
      created_at: '2026-04-22T08:00:00Z',
      updated_at: '2026-04-22T08:10:00Z',
    })

    await renderDetail('/admin/accounts/9')

    expect(await screen.findByTestId('account-detail-view')).toBeInTheDocument()
    expect(screen.queryByText('live row')).not.toBeInTheDocument()
    expect(screen.getByTestId('account-summary-status')).toHaveTextContent('Inactive')
    expect(screen.getByTestId('account-summary-status')).toHaveClass('text-[var(--err)]')
    expect(screen.queryByTestId('disable-account-button')).not.toBeInTheDocument()
    expect(screen.getByTestId('activate-account-button')).toHaveAccessibleName('Active account')
  })

  it('activates a non-active account and refreshes account queries', async () => {
    apiGetMock.mockResolvedValue({
      id: 9,
      name: 'activate-me',
      provider: 'openai',
      auth_method: 'api_key',
      status: 'disabled',
      base_url: null,
      created_at: '2026-04-22T08:00:00Z',
      updated_at: '2026-04-22T08:10:00Z',
    })
    apiPostMock.mockResolvedValue({ status: 'enabled' })

    await renderDetail('/admin/accounts/9')
    const user = userEvent.setup()

    await user.click(await screen.findByTestId('activate-account-button'))

    expect(apiPostMock).toHaveBeenCalledWith('/api/admin/accounts/9/enable', {})
    await waitFor(() => {
      expect(toastSuccessMock).toHaveBeenCalledWith('Account activated')
    })
  })

  it('disables an active account and refreshes account queries', async () => {
    apiGetMock.mockResolvedValue({
      id: 42,
      name: 'disable-me',
      provider: 'openai',
      auth_method: 'oauth_browser',
      status: 'active',
      base_url: 'https://api.openai.com',
      created_at: '2026-04-22T08:00:00Z',
      updated_at: '2026-04-22T08:10:00Z',
      email: 'disable@example.com',
      plan_type: 'chatgpt-plus',
      chatgpt_account_id: 'acct_disable_42',
      last_refresh: '2026-04-22T08:05:00Z',
      access_expires_at: '2026-04-22T09:05:00Z',
    })
    apiPostMock.mockResolvedValue({ status: 'disabled' })

    await renderDetail('/admin/accounts/42')
    const user = userEvent.setup()

    await user.click(await screen.findByTestId('disable-account-button'))

    expect(apiPostMock).toHaveBeenCalledWith('/api/admin/accounts/42/disable', {})
    await waitFor(() => {
      expect(toastSuccessMock).toHaveBeenCalledWith('Account disabled')
    })
  })

  it('downloads auth.json through a blob anchor flow', async () => {
    apiGetMock.mockResolvedValue({
      id: 42,
      name: 'downloadable-oauth',
      provider: 'openai',
      auth_method: 'oauth_browser',
      status: 'active',
      base_url: 'https://api.openai.com',
      created_at: '2026-04-22T08:00:00Z',
      updated_at: '2026-04-22T08:10:00Z',
      email: 'download@example.com',
      plan_type: 'chatgpt-team',
      chatgpt_account_id: 'acct_download_42',
      last_refresh: '2026-04-22T08:05:00Z',
      access_expires_at: '2026-04-22T09:05:00Z',
    })
    accountsExportAuthJsonMock.mockReturnValue(Promise.resolve({} as never))
    callAdminAttachmentMock.mockResolvedValue({
      blob: new Blob(['{"OPENAI_API_KEY":null}'], { type: 'application/json; charset=utf-8' }),
      filename: 'auth.json',
    })

    const createObjectURLSpy = vi.fn(() => 'blob:auth-json')
    const revokeObjectURLSpy = vi.fn()
    Object.defineProperty(URL, 'createObjectURL', {
      configurable: true,
      value: createObjectURLSpy,
    })
    Object.defineProperty(URL, 'revokeObjectURL', {
      configurable: true,
      value: revokeObjectURLSpy,
    })
    let clickedAnchor: HTMLAnchorElement | null = null
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      clickedAnchor = this
    })

    await renderDetail()
    const user = userEvent.setup()

    await user.click(await screen.findByTestId('export-auth-json'))

    expect(accountsExportAuthJsonMock).toHaveBeenCalledWith({
      path: { id: 42 },
      parseAs: 'text',
    })
    expect(callAdminAttachmentMock).toHaveBeenCalledTimes(1)
    expect(createObjectURLSpy).toHaveBeenCalledTimes(1)
    expect(clickSpy).toHaveBeenCalledTimes(1)
    expect(clickedAnchor).not.toBeNull()
    if (clickedAnchor === null) {
      throw new Error('expected a temporary download anchor')
    }
    const downloadAnchor = clickedAnchor as unknown as HTMLAnchorElement
    expect(downloadAnchor.href).toBe('blob:auth-json')
    expect(downloadAnchor.download).toBe('auth.json')

    await waitFor(() => {
      expect(revokeObjectURLSpy).toHaveBeenCalledWith('blob:auth-json')
    })
  })

  it('surfaces export failures as an error banner and toast', async () => {
    apiGetMock.mockResolvedValue({
      id: 42,
      name: 'failure-oauth',
      provider: 'openai',
      auth_method: 'oauth_browser',
      status: 'active',
      base_url: 'https://api.openai.com',
      created_at: '2026-04-22T08:00:00Z',
      updated_at: '2026-04-22T08:10:00Z',
      email: 'failure@example.com',
      plan_type: 'chatgpt-team',
      chatgpt_account_id: 'acct_failure_42',
      last_refresh: '2026-04-22T08:05:00Z',
      access_expires_at: '2026-04-22T09:05:00Z',
    })
    accountsExportAuthJsonMock.mockReturnValue(Promise.resolve({} as never))
    callAdminAttachmentMock.mockRejectedValue(
      new RouterApiError({
        code: 3902,
        msg: 'oauth_export_read_failed',
        data: {},
        requestId: 'req_export_fail',
        status: 500,
      }),
    )

    await renderDetail()
    const user = userEvent.setup()

    await user.click(await screen.findByTestId('export-auth-json'))

    expect(await screen.findByTestId('error-banner')).toHaveTextContent('oauth_export_read_failed')
    expect(screen.getByTestId('error-banner')).toHaveTextContent('req_export_fail')
    expect(toastErrorMock).toHaveBeenCalled()
    expect(screen.getByTestId('export-auth-json')).toBeEnabled()
    expect(screen.getByTestId('export-auth-json')).toHaveTextContent(/export auth\.json/i)
  })

  it('soft-deletes the account after an explicit confirmation click', async () => {
    apiGetMock.mockResolvedValue({
      id: 42,
      name: 'delete-me',
      provider: 'openai',
      auth_method: 'oauth_browser',
      status: 'active',
      base_url: 'https://api.openai.com',
      created_at: '2026-04-22T08:00:00Z',
      updated_at: '2026-04-22T08:10:00Z',
      email: 'delete@example.com',
      plan_type: 'chatgpt-plus',
      chatgpt_account_id: 'acct_delete_42',
      last_refresh: '2026-04-22T08:05:00Z',
      access_expires_at: '2026-04-22T09:05:00Z',
    })
    apiPostMock.mockResolvedValue({})

    await renderDetail('/admin/accounts/42')
    const user = userEvent.setup()

    const deleteButton = await screen.findByTestId('delete-account-button')
    expect(deleteButton).toHaveTextContent(/delete account/i)

    await user.click(deleteButton)
    expect(apiPostMock).not.toHaveBeenCalled()
    expect(deleteButton).toHaveTextContent(/confirm delete/i)

    await user.click(deleteButton)

    expect(apiPostMock).toHaveBeenCalledWith('/api/admin/accounts/42/delete', {})
    expect(toastSuccessMock).toHaveBeenCalledWith('Account deleted')
    expect(await screen.findByTestId('accounts-list-route')).toBeInTheDocument()
  })
})
