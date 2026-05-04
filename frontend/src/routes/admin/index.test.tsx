import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { type DashboardData, dashboardGet } from '@/generated/openapi'
import { api } from '@/lib/api-client'
import { callAdmin } from '@/lib/router-api'
import { routeTree } from '@/router'

vi.mock('recharts', () => {
  const Chart = ({ children }: { children?: React.ReactNode }) => (
    <div data-testid="mock-chart">{children}</div>
  )
  const Primitive = () => null
  return {
    Area: Primitive,
    AreaChart: Chart,
    CartesianGrid: Primitive,
    Line: Primitive,
    LineChart: Chart,
    ResponsiveContainer: ({ children }: { children?: React.ReactNode }) => (
      <div data-testid="mock-responsive-chart">{children}</div>
    ),
    Tooltip: Primitive,
    XAxis: Primitive,
    YAxis: Primitive,
  }
})

vi.mock('@/generated/openapi', async () => {
  const actual = await vi.importActual<typeof import('@/generated/openapi')>('@/generated/openapi')
  return {
    ...actual,
    dashboardGet: vi.fn((options: unknown) => Promise.resolve({ op: 'dashboard', options })),
  }
})

vi.mock('@/lib/router-api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/router-api')>('@/lib/router-api')
  return {
    ...actual,
    callAdmin: vi.fn(),
  }
})

vi.mock('@/lib/api-client', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api-client')>('@/lib/api-client')
  return {
    ...actual,
    api: {
      ...actual.api,
      get: vi.fn(),
    },
  }
})

interface SdkMarker {
  op: 'dashboard'
  options: unknown
}

const dashboardGetMock = vi.mocked(dashboardGet)
const callAdminMock = vi.mocked(callAdmin)
const apiGetMock = vi.mocked(api.get)

const dashboardPayload: DashboardData = {
  range: '7d',
  window_start: '2026-04-18T00:00:00Z',
  window_end: '2026-04-25T00:00:00Z',
  bucket_seconds: 21600,
  account_options: [
    {
      id: 7,
      label: 'plus-account',
      account: {
        id: 7,
        name: 'plus-account',
        provider: 'openai',
        auth_method: 'oauth_browser',
        status: 'active',
        created_at: '2026-04-18T00:00:00Z',
        updated_at: '2026-04-25T00:00:00Z',
        email: 'plus@example.com',
        plan_type_label: 'ChatGPT Plus',
      },
    },
  ],
  cards: {
    active_accounts: { value: 2 },
    requests: {
      total: 128,
      series: [{ timestamp: '2026-04-25T00:00:00Z', count: 128 }],
    },
    tokens: {
      totals: { input_cached: 400, input_non_cached: 900, output: 300 },
      series: [
        {
          timestamp: '2026-04-25T00:00:00Z',
          input_cached: 400,
          input_non_cached: 900,
          output: 300,
        },
      ],
    },
    error_rate: {
      value: 0.125,
      total: 128,
      errors: 16,
      series: [{ timestamp: '2026-04-25T00:00:00Z', total: 128, errors: 16, rate: 0.125 }],
    },
    ttft: {
      p95_ms: 420,
      sample_count: 12,
      series: [{ timestamp: '2026-04-25T00:00:00Z', p95_ms: 420, sample_count: 12 }],
    },
  },
}

async function renderDashboard(path = '/admin') {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
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

describe('AdminIndex dashboard', () => {
  beforeEach(() => {
    window.localStorage.clear()
    dashboardGetMock.mockClear()
    apiGetMock.mockResolvedValue({
      status: 'healthy',
      active_accounts: 2,
      disabled_accounts: 0,
    } as never)
    callAdminMock.mockImplementation(async (call) => {
      const marker = (await call) as unknown as SdkMarker
      if (marker.op === 'dashboard') return dashboardPayload as never
      throw new Error('unexpected sdk call')
    })
  })

  it('renders live metric cards from the dashboard endpoint', async () => {
    await renderDashboard()

    expect(await screen.findByRole('heading', { name: 'Dashboard' })).toBeInTheDocument()
    expect(screen.getByTestId('dashboard-card-active_accounts')).toHaveTextContent('2')
    expect(screen.getByTestId('dashboard-card-requests')).toHaveTextContent('128')
    expect(screen.getByTestId('dashboard-card-tokens')).toHaveTextContent('1,600')
    expect(screen.getByTestId('dashboard-card-error_rate')).toHaveTextContent('12.5%')
    expect(screen.getByTestId('dashboard-card-ttft')).toHaveTextContent('420 ms')
    expect(screen.getAllByTestId('mock-responsive-chart').length).toBeGreaterThanOrEqual(4)
    expect(screen.getByRole('button', { name: 'Refresh' })).not.toHaveTextContent('Refresh')
    expect(screen.getByRole('button', { name: 'Edit dashboard' })).not.toHaveTextContent(
      'Edit dashboard',
    )
    expect(dashboardGetMock).toHaveBeenCalledWith({ query: { range: '7d' } })
  })

  it('keeps the no-active-accounts operator banner on the dashboard route', async () => {
    callAdminMock.mockImplementation(async (call) => {
      const marker = (await call) as unknown as SdkMarker
      if (marker.op === 'dashboard') {
        return {
          ...dashboardPayload,
          cards: {
            ...dashboardPayload.cards,
            active_accounts: { value: 0 },
          },
        } as never
      }
      throw new Error('unexpected sdk call')
    })

    await renderDashboard()

    expect(await screen.findByTestId('no-healthy-accounts-banner')).toHaveTextContent(
      'No active upstream accounts',
    )
  })

  it('keeps range and account filters in URL-backed query params', async () => {
    const user = userEvent.setup()
    await renderDashboard()

    await user.click(await screen.findByRole('button', { name: '1h' }))
    await waitFor(() => {
      expect(dashboardGetMock).toHaveBeenLastCalledWith({ query: { range: '1h' } })
    })

    await user.selectOptions(screen.getByLabelText('Account'), '7')
    await waitFor(() => {
      expect(dashboardGetMock).toHaveBeenLastCalledWith({
        query: { range: '1h', account_id: 7 },
      })
    })
  })

  it('lets the operator hide cards inline and reorder cards by dragging', async () => {
    const user = userEvent.setup()
    await renderDashboard()

    await user.click(await screen.findByRole('button', { name: 'Edit dashboard' }))
    expect(screen.queryByTestId('dashboard-settings')).not.toBeInTheDocument()

    await user.click(
      within(screen.getByTestId('dashboard-card-ttft')).getByRole('button', { name: 'Hide TTFT' }),
    )

    await waitFor(() => {
      expect(screen.queryByTestId('dashboard-card-ttft')).not.toBeInTheDocument()
    })
    expect(screen.getByTestId('dashboard-hidden-cards')).toBeInTheDocument()
    expect(screen.getByTestId('dashboard-hidden-card-ttft')).toHaveTextContent('TTFT')

    fireEvent.dragStart(screen.getByTestId('dashboard-card-tokens'), {
      dataTransfer: createDataTransfer('tokens'),
    })
    fireEvent.dragOver(screen.getByTestId('dashboard-card-requests'), {
      dataTransfer: createDataTransfer('tokens'),
    })
    fireEvent.drop(screen.getByTestId('dashboard-card-requests'), {
      dataTransfer: createDataTransfer('tokens'),
    })
    const visibleCards = screen.getAllByTestId(/^dashboard-card-/)
    expect(visibleCards.map((card) => card.getAttribute('data-card-id'))).toEqual([
      'active_accounts',
      'tokens',
      'requests',
      'error_rate',
    ])
  })

  it('moves the first dashboard card after the next card when dragging forward', async () => {
    const user = userEvent.setup()
    await renderDashboard()

    await user.click(await screen.findByRole('button', { name: 'Edit dashboard' }))
    fireEvent.dragStart(screen.getByTestId('dashboard-card-active_accounts'), {
      dataTransfer: createDataTransfer('active_accounts'),
    })
    fireEvent.dragOver(screen.getByTestId('dashboard-card-requests'), {
      dataTransfer: createDataTransfer('active_accounts'),
    })
    fireEvent.drop(screen.getByTestId('dashboard-card-requests'), {
      dataTransfer: createDataTransfer('active_accounts'),
    })

    await waitFor(() => {
      const visibleCards = screen.getAllByTestId(/^dashboard-card-/)
      expect(visibleCards.map((card) => card.getAttribute('data-card-id'))).toEqual([
        'requests',
        'active_accounts',
        'tokens',
        'error_rate',
        'ttft',
      ])
    })
  })

  it('lets operators reorder dashboard cards without drag gestures', async () => {
    const user = userEvent.setup()
    await renderDashboard()

    await user.click(await screen.findByRole('button', { name: 'Edit dashboard' }))

    await user.click(
      within(screen.getByTestId('dashboard-card-active_accounts')).getByRole('button', {
        name: 'Move Active Accounts down',
      }),
    )

    await waitFor(() => {
      const visibleCards = screen.getAllByTestId(/^dashboard-card-/)
      expect(visibleCards.map((card) => card.getAttribute('data-card-id'))).toEqual([
        'requests',
        'active_accounts',
        'tokens',
        'error_rate',
        'ttft',
      ])
    })

    await user.click(
      within(screen.getByTestId('dashboard-card-active_accounts')).getByRole('button', {
        name: 'Move Active Accounts up',
      }),
    )

    await waitFor(() => {
      const visibleCards = screen.getAllByTestId(/^dashboard-card-/)
      expect(visibleCards.map((card) => card.getAttribute('data-card-id'))).toEqual([
        'active_accounts',
        'requests',
        'tokens',
        'error_rate',
        'ttft',
      ])
    })
  })
})

function createDataTransfer(cardID: string) {
  const store = new Map<string, string>([['text/plain', cardID]])
  return {
    dropEffect: 'move',
    effectAllowed: 'move',
    getData: vi.fn((type: string) => store.get(type) ?? ''),
    setData: vi.fn((type: string, value: string) => {
      store.set(type, value)
    }),
  }
}
