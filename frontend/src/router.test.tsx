import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import {
  accountsExportAuthJson,
  type DashboardData,
  dashboardGet,
  oauthCancel,
  oauthDeviceStart,
  requestsList,
  requestsOptions,
  settingsGet,
  settingsUpdate,
} from '@/generated/openapi'
import { api } from '@/lib/api-client'
import { useOAuthFlow } from '@/lib/oauth-flow'
import { callAdmin, callAdminAttachment } from '@/lib/router-api'
import { routeTree } from './router'

vi.mock('recharts', () => {
  const Chart = ({ children }: { children?: React.ReactNode }) => (
    <div data-testid="mock-route-chart">{children}</div>
  )
  const Primitive = () => null
  return {
    Area: Primitive,
    AreaChart: Chart,
    CartesianGrid: Primitive,
    Line: Primitive,
    LineChart: Chart,
    ResponsiveContainer: ({ children }: { children?: React.ReactNode }) => (
      <div data-testid="mock-route-responsive-chart">{children}</div>
    ),
    Tooltip: Primitive,
    XAxis: Primitive,
    YAxis: Primitive,
  }
})

vi.mock('@/lib/api-client', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api-client')>('@/lib/api-client')
  return {
    ...actual,
    api: {
      ...actual.api,
      get: vi.fn().mockResolvedValue({
        status: 'healthy',
        active_accounts: 1,
        disabled_accounts: 0,
      }),
      post: vi.fn(),
    },
  }
})

vi.mock('@/generated/openapi', async () => {
  const actual = await vi.importActual<typeof import('@/generated/openapi')>('@/generated/openapi')
  return {
    ...actual,
    accountsExportAuthJson: vi.fn(),
    dashboardGet: vi.fn(() => Promise.resolve({ op: 'dashboard' })),
    oauthDeviceStart: vi.fn(),
    oauthCancel: vi.fn(),
    requestsList: vi.fn(() => Promise.resolve({ op: 'list' })),
    requestsOptions: vi.fn(() => Promise.resolve({ op: 'options' })),
    settingsGet: vi.fn(() => Promise.resolve({ op: 'settings-get' })),
    settingsUpdate: vi.fn((options: unknown) =>
      Promise.resolve({ op: 'settings-update', options }),
    ),
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

vi.mock('@/lib/oauth-flow', async () => {
  const actual = await vi.importActual<typeof import('@/lib/oauth-flow')>('@/lib/oauth-flow')
  return {
    ...actual,
    useOAuthFlow: vi.fn(),
  }
})

const apiGetMock = vi.mocked(api.get)
const apiPostMock = vi.mocked(api.post)
const accountsExportAuthJsonMock = vi.mocked(accountsExportAuthJson)
const dashboardGetMock = vi.mocked(dashboardGet)
const callAdminMock = vi.mocked(callAdmin)
const callAdminAttachmentMock = vi.mocked(callAdminAttachment)
const useOAuthFlowMock = vi.mocked(useOAuthFlow)
const oauthDeviceStartMock = vi.mocked(oauthDeviceStart)
const oauthCancelMock = vi.mocked(oauthCancel)
const requestsListMock = vi.mocked(requestsList)
const requestsOptionsMock = vi.mocked(requestsOptions)
const settingsGetMock = vi.mocked(settingsGet)
const settingsUpdateMock = vi.mocked(settingsUpdate)

const routerDashboardPayload: DashboardData = {
  range: '7d',
  window_start: '2026-04-18T00:00:00Z',
  window_end: '2026-04-25T00:00:00Z',
  bucket_seconds: 21600,
  account_options: [],
  cards: {
    active_accounts: { value: 1 },
    requests: { total: 0, series: [] },
    tokens: {
      totals: { input_cached: 0, input_non_cached: 0, output: 0 },
      series: [],
    },
    error_rate: { value: 0, total: 0, errors: 0, series: [] },
    ttft: { p95_ms: null, sample_count: 0, series: [] },
  },
}

async function renderRouterAt(path: string) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  })
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [path] }),
    context: { queryClient },
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

describe('routeTree new-account gating', () => {
  beforeEach(() => {
    apiGetMock.mockResolvedValue({
      status: 'healthy',
      active_accounts: 1,
      disabled_accounts: 0,
    })
    apiPostMock.mockReset()
    accountsExportAuthJsonMock.mockReset()
    dashboardGetMock.mockClear()
    oauthDeviceStartMock.mockResolvedValue({} as never)
    oauthCancelMock.mockResolvedValue({} as never)
    requestsListMock.mockClear()
    requestsOptionsMock.mockClear()
    settingsGetMock.mockClear()
    settingsUpdateMock.mockClear()
    callAdminMock.mockReset()
    callAdminMock.mockImplementation(async (call) => {
      const marker = (await call) as unknown as { op?: string }
      if (marker.op === 'dashboard') return routerDashboardPayload as never
      throw new Error(`unexpected admin sdk call: ${marker.op ?? 'unknown'}`)
    })
    callAdminAttachmentMock.mockReset()
    useOAuthFlowMock.mockReturnValue({
      status: 'pending',
      flow: {
        status: 'pending',
        method: 'device',
        flow_id: 'fl_device_pending_1234567890',
        user_code: 'ABCD-1234',
        verification_url: 'https://auth.openai.com/codex/device',
        created_at: '2026-04-15T10:00:00Z',
        expires_at: '2026-04-15T10:15:00Z',
      },
      isPolling: true,
      error: null,
      isError: false,
      isFetching: false,
      isLoading: false,
      refetch: vi.fn().mockResolvedValue({ data: { status: 'idle' } }),
    })
  })

  it('deep-links the real dashboard route and renders the dashboard title', async () => {
    await renderRouterAt('/admin')

    expect(await screen.findByRole('heading', { name: 'Dashboard' })).toBeInTheDocument()
  })

  it('deep-links the real API-key route and renders the account form', async () => {
    await renderRouterAt('/admin/accounts/new-apikey')

    expect(await screen.findByTestId('apikey-create-form')).toBeInTheDocument()
    expect(screen.getByTestId('apikey-api-key-input')).toBeInTheDocument()
  })

  it('deep-links the real import route and renders the upload affordance', async () => {
    await renderRouterAt('/admin/accounts/new-import')

    expect(await screen.findByTestId('import-auth-json-input')).toBeInTheDocument()
    expect(screen.getByTestId('import-auth-json-submit')).toBeInTheDocument()
  })

  it('deep-links the real device route and renders the live device screen', async () => {
    callAdminMock.mockResolvedValueOnce({
      flow_id: 'fl_device_start_1234567890',
      user_code: 'ABCD-1234',
      verification_url: 'https://auth.openai.com/codex/device',
      interval_seconds: 5,
      expires_at: '2026-04-15T10:15:00Z',
      method: 'device',
    } as never)

    await renderRouterAt('/admin/accounts/new-oauth-device')

    expect(await screen.findByTestId('device-flow-panel')).toBeInTheDocument()
    expect(screen.getByTestId('device-user-code')).toHaveTextContent('ABCD-1234')
    expect(screen.getByTestId('device-verification-url')).toHaveAttribute(
      'href',
      'https://auth.openai.com/codex/device',
    )
    expect(screen.getByTestId('device-countdown')).toBeInTheDocument()
    expect(oauthDeviceStartMock).toHaveBeenCalledTimes(1)
  })

  it('deep-links the real settings route and renders persisted plugin intents', async () => {
    callAdminMock.mockImplementation(async (call) => {
      const marker = (await call) as unknown as { op: 'settings-get' | 'settings-update' }
      if (marker.op === 'settings-get') {
        return {
          runtime: {
            log_client_request_body: false,
            log_upstream_request_body: false,
            log_upstream_response_body: false,
            log_retention_days: 14,
            log_level: 'info',
            model_renames: [],
          },
          db: {
            driver: 'sqlite3',
            host: '',
            database_name: 'router.db',
          },
          plugins: [],
          plugin_intents: [
            {
              id: 'admin_auth',
              label: 'Admin authentication',
              enabled: true,
              status: 'status from api',
            },
            {
              id: 'client_keys',
              label: 'Client API keys',
              enabled: false,
              status: 'second status from api',
            },
          ],
          system: {
            router_version: '0.3.0',
            router_git_sha: 'deadbeef',
            router_built_at: '2026-04-23T00:00:00Z',
          },
        } as never
      }
      return {
        runtime: {
          log_client_request_body: false,
          log_upstream_request_body: false,
          log_upstream_response_body: false,
          log_retention_days: 14,
          log_level: 'info',
          model_renames: [],
        },
        db: {
          driver: 'sqlite3',
          host: '',
          database_name: 'router.db',
        },
        plugins: [],
        plugin_intents: [
          {
            id: 'admin_auth',
            label: 'Admin authentication',
            enabled: false,
            status: 'status from api',
          },
          {
            id: 'client_keys',
            label: 'Client API keys',
            enabled: false,
            status: 'second status from api',
          },
        ],
        system: {
          router_version: '0.3.0',
          router_git_sha: 'deadbeef',
          router_built_at: '2026-04-23T00:00:00Z',
        },
      } as never
    })

    await renderRouterAt('/admin/settings')

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Settings' })).toBeInTheDocument()
    })
    expect(await screen.findByText('status from api')).toBeInTheDocument()
    expect(screen.getByText('second status from api')).toBeInTheDocument()
    expect(screen.getAllByText(/Plugin not yet installed/i)).toHaveLength(2)
    expect(screen.getByTestId('plugin-switch-admin_auth')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('plugin-switch-client_keys')).toHaveAttribute('aria-checked', 'false')

    const user = userEvent.setup()
    await user.click(screen.getByTestId('plugin-switch-client_keys'))

    await waitFor(() => {
      expect(settingsUpdateMock).toHaveBeenCalledWith({
        body: { plugins: { client_keys: { enabled: true } } },
      })
    })
  })

  it('deep-links the real playground route and renders the run form', async () => {
    await renderRouterAt('/admin/playground')

    expect(await screen.findByTestId('playground-form')).toBeInTheDocument()
    expect(screen.getByDisplayValue('gpt-5.4-mini')).toBeInTheDocument()
  })

  it('deep-links the real requests route and renders the request log shell', async () => {
    callAdminMock.mockImplementation(async (call) => {
      const marker = (await call) as unknown as { op: 'list' | 'options' }
      if (marker.op === 'options') {
        return { accounts: [], outcomes: [], models: [], response_modes: [] } as never
      }
      return { records: [], has_more: false } as never
    })

    await renderRouterAt('/admin/requests')

    expect(await screen.findByTestId('requests-empty')).toBeInTheDocument()
    expect(requestsListMock).toHaveBeenCalledTimes(1)
    expect(requestsOptionsMock).toHaveBeenCalledTimes(1)
  })

  it('fails visibly when settings payload omits plugin_intents instead of fabricating intent values', async () => {
    callAdminMock.mockImplementation(async () => {
      return {
        runtime: {
          log_client_request_body: false,
          log_upstream_request_body: false,
          log_upstream_response_body: false,
          log_retention_days: 14,
          log_level: 'info',
        },
        db: {
          driver: 'sqlite3',
          host: '',
          database_name: 'router.db',
        },
        plugins: [],
        system: {
          router_version: '0.3.0',
          router_git_sha: 'deadbeef',
          router_built_at: '2026-04-23T00:00:00Z',
        },
      } as never
    })

    await renderRouterAt('/admin/settings')

    expect(await screen.findByTestId('error-banner')).toHaveTextContent(
      'plugin_intents must be an array',
    )
    expect(screen.queryByTestId('plugin-switch-admin_auth')).not.toBeInTheDocument()
  })

  it('deep-links the real account detail route and renders the export affordance for oauth rows', async () => {
    apiGetMock.mockImplementation(async (path) => {
      if (path === '/api/admin/accounts/42') {
        return {
          id: 42,
          name: 'route-detail-oauth',
          provider: 'openai',
          auth_method: 'oauth_import',
          status: 'active',
          base_url: 'https://api.openai.com',
          created_at: '2026-04-22T08:00:00Z',
          updated_at: '2026-04-22T08:10:00Z',
          email: 'route@example.com',
          plan_type: 'chatgpt-plus',
          chatgpt_account_id: 'acct_route_42',
          last_refresh: '2026-04-22T08:05:00Z',
          access_expires_at: '2026-04-22T09:05:00Z',
        } as never
      }
      return {
        status: 'healthy',
        active_accounts: 1,
        disabled_accounts: 0,
      } as never
    })

    await renderRouterAt('/admin/accounts/42')

    expect(await screen.findByTestId('account-detail-view')).toBeInTheDocument()
    expect(screen.getAllByText('route@example.com')).toHaveLength(2)
    expect(screen.getByRole('button', { name: /export auth\.json/i })).toBeInTheDocument()
  })

  it('runs the export download flow through the real routeTree detail route', async () => {
    apiGetMock.mockImplementation(async (path) => {
      if (path === '/api/admin/accounts/42') {
        return {
          id: 42,
          name: 'route-detail-export',
          provider: 'openai',
          auth_method: 'oauth_browser',
          status: 'active',
          base_url: 'https://api.openai.com',
          created_at: '2026-04-22T08:00:00Z',
          updated_at: '2026-04-22T08:10:00Z',
          email: 'route-export@example.com',
          plan_type: 'chatgpt-plus',
          chatgpt_account_id: 'acct_route_export_42',
          last_refresh: '2026-04-22T08:05:00Z',
          access_expires_at: '2026-04-22T09:05:00Z',
        } as never
      }
      return {
        status: 'healthy',
        active_accounts: 1,
        disabled_accounts: 0,
      } as never
    })
    accountsExportAuthJsonMock.mockReturnValue(Promise.resolve({} as never))
    callAdminAttachmentMock.mockResolvedValue({
      blob: new Blob(['{"OPENAI_API_KEY":null}'], { type: 'application/json; charset=utf-8' }),
      filename: 'auth.json',
    })

    const createObjectURLSpy = vi.fn(() => 'blob:route-tree-auth-json')
    const revokeObjectURLSpy = vi.fn()
    Object.defineProperty(URL, 'createObjectURL', {
      configurable: true,
      value: createObjectURLSpy,
    })
    Object.defineProperty(URL, 'revokeObjectURL', {
      configurable: true,
      value: revokeObjectURLSpy,
    })
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

    await renderRouterAt('/admin/accounts/42')

    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /export auth\.json/i }))

    expect(accountsExportAuthJsonMock).toHaveBeenCalledWith({
      path: { id: 42 },
      parseAs: 'text',
    })
    expect(callAdminAttachmentMock).toHaveBeenCalledTimes(1)
    expect(createObjectURLSpy).toHaveBeenCalledTimes(1)
    expect(clickSpy).toHaveBeenCalledTimes(1)
    expect(revokeObjectURLSpy).toHaveBeenCalledWith('blob:route-tree-auth-json')
  })
})
