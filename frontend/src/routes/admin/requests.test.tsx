import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { toast } from 'sonner'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import {
  type RequestDetailData,
  type RequestsListData,
  type RequestsOptionsData,
  requestsGet,
  requestsList,
  requestsOptions,
} from '@/generated/openapi'
import { api } from '@/lib/api-client'
import { callAdmin } from '@/lib/router-api'
import { routeTree } from '@/router'

vi.mock('sonner', () => ({
  toast: {
    success: vi.fn(),
    error: vi.fn(),
  },
}))

vi.mock('@/generated/openapi', async () => {
  const actual = await vi.importActual<typeof import('@/generated/openapi')>('@/generated/openapi')
  return {
    ...actual,
    requestsList: vi.fn((options: unknown) => Promise.resolve({ op: 'list', options })),
    requestsOptions: vi.fn((options: unknown) => Promise.resolve({ op: 'options', options })),
    requestsGet: vi.fn((options: unknown) => Promise.resolve({ op: 'get', options })),
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
  op: 'list' | 'options' | 'get'
  options: unknown
}

const requestsListMock = vi.mocked(requestsList)
const requestsOptionsMock = vi.mocked(requestsOptions)
const requestsGetMock = vi.mocked(requestsGet)
const callAdminMock = vi.mocked(callAdmin)
const apiGetMock = vi.mocked(api.get)
const toastSuccessMock = vi.mocked(toast.success)
const clipboardWriteTextMock = vi.fn(() => Promise.resolve())
const longRequestID = 'req_live_13abcdef1234567890'

const listPayload: RequestsListData = {
  has_more: true,
  next_before_id: 12,
  records: [
    {
      id: 13,
      request_id: longRequestID,
      created_at: '2026-04-25T02:00:00Z',
      client_ip: '203.0.113.10',
      upstream_account_id: 7,
      account: {
        id: 7,
        name: 'plus-account',
        provider: 'openai',
        base_url: 'https://api.openai.test/v1',
        auth_method: 'oauth_browser',
        status: 'active',
        created_at: '2026-04-25T01:00:00Z',
        updated_at: '2026-04-25T01:00:00Z',
        email: 'plus@example.com',
        plan_type_label: 'ChatGPT Plus',
      },
      session_key: 'sess-a',
      method: 'POST',
      path: '/v1/responses',
      status_code: 200,
      latency_ms: 45,
      ttft_ms: 17,
      outcome: 'success',
      model: 'gpt-5.4-mini',
      model_params: { service_tier: 'priority' },
      router_metadata: {
        bridge: {
          op_id: 'op.openai.responses.create',
          bridge_id: 'bridge.openai.responses.to_codex',
          upstream_endpoint: '/codex/responses',
        },
      },
      response_mode: 'websocket',
      token_usage: { input: 123, cached_input: 100, output: 234, reasoning: 123 },
    },
  ],
}

function listPayloadForIDs(ids: number[], nextBeforeID?: number): RequestsListData {
  return {
    has_more: typeof nextBeforeID === 'number',
    next_before_id: nextBeforeID,
    records: ids.map((id) => ({
      ...listPayload.records[0],
      id,
      request_id: `req_live_${id}`,
    })),
  }
}

const optionsPayload: RequestsOptionsData = {
  accounts: [
    {
      id: 7,
      label: 'plus-account',
      account: listPayload.records[0].account,
    },
  ],
  outcomes: ['success', 'upstream_error'],
  models: ['gpt-5.4-mini'],
  response_modes: ['json', 'sse', 'websocket'],
}

const detailPayload: RequestDetailData = {
  record: {
    ...listPayload.records[0],
    client_request_body: '{"prompt":"hello"}',
    upstream_request_body: '{"input":"hello"}',
    upstream_response_body: '{"text":"hello\\n\\nworld"}',
  },
}

async function renderRequests(path = '/admin/requests') {
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

describe('AdminRequests', () => {
  beforeEach(() => {
    window.localStorage.clear()
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: clipboardWriteTextMock },
    })
    clipboardWriteTextMock.mockClear()
    requestsListMock.mockClear()
    requestsOptionsMock.mockClear()
    requestsGetMock.mockClear()
    apiGetMock.mockResolvedValue({
      status: 'healthy',
      active_accounts: 1,
      disabled_accounts: 0,
    } as never)
    toastSuccessMock.mockReset()
    callAdminMock.mockImplementation(async (call) => {
      const marker = (await call) as unknown as SdkMarker
      if (marker.op === 'list') return listPayload as never
      if (marker.op === 'options') return optionsPayload as never
      return detailPayload as never
    })
  })

  it('renders request rows without leaking captured bodies into the list', async () => {
    await renderRequests()

    expect(screen.queryByText('Release 004')).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Request log' })).toBeInTheDocument()
    const table = await screen.findByTestId('requests-table')
    const expectedDate = new Intl.DateTimeFormat(undefined, {
      day: '2-digit',
      month: '2-digit',
      year: 'numeric',
    }).format(new Date(listPayload.records[0].created_at))
    const expectedTime = new Intl.DateTimeFormat(undefined, {
      hour: '2-digit',
      hour12: false,
      minute: '2-digit',
      second: '2-digit',
      timeZoneName: 'short',
    }).format(new Date(listPayload.records[0].created_at))

    expect(within(table).getByText(expectedDate)).toBeInTheDocument()
    expect(within(table).getByText(expectedTime)).toBeInTheDocument()
    expect(within(table).getByText('req_live_...567890')).toBeInTheDocument()
    expect(within(table).getByText('203.0.113.10')).toBeInTheDocument()
    expect(within(table).queryByText(longRequestID)).not.toBeInTheDocument()
    expect(within(table).getByTitle(longRequestID)).toBeInTheDocument()
    await userEvent.click(
      within(table).getByRole('button', { name: `Copy request ID ${longRequestID}` }),
    )
    expect(clipboardWriteTextMock).toHaveBeenCalledWith(longRequestID)
    expect(toastSuccessMock).toHaveBeenCalledWith('Copied')
    const routeCell = within(table).getByText('/v1/responses').closest('td')
    expect(routeCell).not.toHaveTextContent('POST')
    expect(within(table).getByText('plus-account')).toBeInTheDocument()
    expect(within(table).getByText(/input:123/)).toBeInTheDocument()
    expect(screen.queryByText(/hello/)).not.toBeInTheDocument()
    expect(requestsListMock).toHaveBeenCalledWith({
      query: expect.objectContaining({ limit: 50 }),
    })
  })

  it('keeps filters in URL-backed query params', async () => {
    const user = userEvent.setup()
    await renderRequests()

    expect(await screen.findByRole('button', { name: 'From' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'To' })).toBeInTheDocument()
    expect(document.querySelector('input[type="datetime-local"]')).not.toBeInTheDocument()

    await user.click(screen.getByTestId('requests-advanced-filter-toggle'))

    await user.type(await screen.findByTestId('requests-search-input'), 'req_live')
    await user.click(screen.getByTestId('requests-search-apply'))

    await waitFor(() => {
      expect(requestsListMock).toHaveBeenLastCalledWith({
        query: expect.objectContaining({ search: 'req_live' }),
      })
    })

    await user.click(screen.getByRole('button', { name: 'success' }))

    await waitFor(() => {
      expect(requestsListMock).toHaveBeenLastCalledWith({
        query: expect.objectContaining({ outcome: ['success'], search: 'req_live' }),
      })
    })

    const lastOptionsCall = requestsOptionsMock.mock.calls.at(-1)?.[0] as
      | { query?: Record<string, unknown> }
      | undefined
    expect(lastOptionsCall?.query).toEqual(expect.objectContaining({ search: 'req_live' }))
    expect(lastOptionsCall?.query).not.toHaveProperty('outcome')
    expect(screen.getByRole('button', { name: 'upstream_error' })).toHaveAttribute(
      'aria-pressed',
      'false',
    )
  })

  it('accepts websocket response mode filters from the URL', async () => {
    await renderRequests('/admin/requests?response_mode=websocket')

    await waitFor(() => {
      expect(requestsListMock).toHaveBeenLastCalledWith({
        query: expect.objectContaining({ response_mode: ['websocket'] }),
      })
    })
    expect(await screen.findByRole('button', { name: 'websocket' })).toHaveAttribute(
      'aria-pressed',
      'true',
    )
    expect(screen.getByTestId('requests-table')).toHaveTextContent('websocket')
  })

  it('uses calendar pickers for absolute from and to time filters', async () => {
    const user = userEvent.setup()
    await renderRequests('/admin/requests?start=2026-04-25T02%3A00%3A00.000Z')

    await user.click(await screen.findByRole('button', { name: 'From' }))

    const calendar = await screen.findByRole('dialog', { name: 'From calendar' })
    expect(calendar).toBeInTheDocument()
    const fromTime = within(calendar).getByLabelText('From time')
    fireEvent.change(fromTime, { target: { value: '09:15' } })
    await user.click(within(calendar).getByRole('button', { name: 'Apply' }))

    await waitFor(() => {
      expect(requestsListMock).toHaveBeenLastCalledWith({
        query: expect.objectContaining({
          start: new Date(2026, 3, 25, 9, 15, 0).toISOString(),
        }),
      })
    })
  })

  it('keeps facet options in their original order when one is selected', async () => {
    await renderRequests('/admin/requests?outcome=upstream_error')

    await waitFor(() => {
      expect(requestsListMock).toHaveBeenLastCalledWith({
        query: expect.objectContaining({ outcome: ['upstream_error'] }),
      })
    })

    const outcomeButtons = screen
      .getAllByRole('button')
      .filter(
        (button) => button.textContent === 'success' || button.textContent === 'upstream_error',
      )
    expect(outcomeButtons.map((button) => button.textContent)).toEqual([
      'success',
      'upstream_error',
    ])
    expect(screen.getByRole('button', { name: 'upstream_error' })).toHaveAttribute(
      'aria-pressed',
      'true',
    )
  })

  it('uses cursor pagination controls without exposing cursor ids', async () => {
    const user = userEvent.setup()
    await renderRequests()
    await screen.findByTestId('request-open-13')

    expect(screen.queryByText(/before #/)).not.toBeInTheDocument()
    expect(screen.queryByText('latest')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Go to page/ })).not.toBeInTheDocument()
    expect(screen.getByLabelText('Current page 1')).toHaveTextContent('Page 1')
    expect(screen.getByRole('button', { name: 'Previous page' })).toBeDisabled()
    const nextPageButton = screen.getByRole('button', { name: 'Next page' })
    const pageSizeSelect = screen.getByTestId('requests-limit-select')

    expect(screen.getByText('Page size')).toBeInTheDocument()
    expect(nextPageButton.compareDocumentPosition(pageSizeSelect)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    )

    await user.click(nextPageButton)

    await waitFor(() => {
      expect(
        requestsListMock.mock.calls.some((call) => {
          const options = call[0] as { query?: { before?: number; limit?: number } }
          return options.query?.before === 12 && options.query.limit === 50
        }),
      ).toBe(true)
    })
    expect(await screen.findByLabelText('Current page 2')).toHaveTextContent('Page 2')
    expect(screen.getByRole('button', { name: 'Previous page' })).not.toBeDisabled()
  })

  it('normalizes mismatched cursor deep links before moving backward', async () => {
    const user = userEvent.setup()
    callAdminMock.mockImplementation(async (call) => {
      const marker = (await call) as unknown as SdkMarker
      if (marker.op === 'options') return optionsPayload as never
      if (marker.op === 'get') return detailPayload as never
      const query = (marker.options as { query?: { before?: number } }).query
      if (query?.before === 27) return listPayloadForIDs([26, 25, 24], 17) as never
      if (query?.before === 17) return listPayloadForIDs([16, 15, 14], 7) as never
      if (query?.before === 7) return listPayloadForIDs([6, 5, 4]) as never
      return listPayloadForIDs([36, 35, 34], 27) as never
    })

    await renderRequests('/admin/requests?before=7&page=3&limit=10')

    expect(await screen.findByLabelText('Current page 4')).toHaveTextContent('Page 4')
    expect(screen.getByRole('button', { name: 'Previous page' })).not.toBeDisabled()

    await user.click(screen.getByRole('button', { name: 'Previous page' }))

    await waitFor(() => {
      expect(screen.getByLabelText('Current page 3')).toHaveTextContent('Page 3')
    })
    expect(
      requestsListMock.mock.calls.some((call) => {
        const options = call[0] as { query?: { before?: number; limit?: number } }
        return options.query?.before === 17 && options.query.limit === 10
      }),
    ).toBe(true)
  })

  it('clears stale cursor deep links that cannot be resolved', async () => {
    callAdminMock.mockImplementation(async (call) => {
      const marker = (await call) as unknown as SdkMarker
      if (marker.op === 'options') return optionsPayload as never
      if (marker.op === 'get') return detailPayload as never
      const query = (marker.options as { query?: { before?: number } }).query
      if (query?.before === 27) return listPayloadForIDs([26, 25, 24], 17) as never
      if (query?.before === 17) return listPayloadForIDs([16, 15, 14]) as never
      if (query?.before === 999) return listPayloadForIDs([998, 997, 996]) as never
      return listPayloadForIDs([36, 35, 34], 27) as never
    })

    await renderRequests('/admin/requests?before=999&page=3&limit=10')

    await waitFor(() => {
      expect(screen.getByLabelText('Current page 1')).toHaveTextContent('Page 1')
    })
    expect(screen.getByRole('button', { name: 'Previous page' })).toBeDisabled()
    expect(
      requestsListMock.mock.calls.some((call) => {
        const options = call[0] as { query?: { before?: number; limit?: number } }
        return options.query?.before === 999 && options.query?.limit === 10
      }),
    ).toBe(true)
    expect(
      requestsListMock.mock.calls.some((call) => {
        const options = call[0] as { query?: { before?: number; limit?: number } }
        return options.query?.before === undefined && options.query?.limit === 10
      }),
    ).toBe(true)
  })

  it('places refresh to the right of columns and refetches request data', async () => {
    const user = userEvent.setup()
    await renderRequests()

    const columnsButton = await screen.findByTestId('requests-columns-button')
    const refreshButton = screen.getByTestId('requests-refresh')

    expect(columnsButton.compareDocumentPosition(refreshButton)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    )
    const listCalls = requestsListMock.mock.calls.length
    const optionsCalls = requestsOptionsMock.mock.calls.length

    await user.click(refreshButton)

    await waitFor(() => {
      expect(requestsListMock.mock.calls.length).toBeGreaterThan(listCalls)
      expect(requestsOptionsMock.mock.calls.length).toBeGreaterThan(optionsCalls)
    })
  })

  it('lets operators hide request table columns without removing the open action', async () => {
    const user = userEvent.setup()
    await renderRequests()

    const table = await screen.findByTestId('requests-table')
    expect(within(table).getByText('Route')).toBeInTheDocument()
    expect(within(table).getByText('/v1/responses')).toBeInTheDocument()

    await user.click(screen.getByTestId('requests-columns-button'))
    await user.click(await screen.findByRole('button', { name: 'Route' }))

    expect(within(table).queryByText('Route')).not.toBeInTheDocument()
    expect(within(table).queryByText('/v1/responses')).not.toBeInTheDocument()
    const detailButton = within(table).getByTestId('request-open-13')
    expect(detailButton).toBeInTheDocument()
    expect(detailButton).toHaveClass('focus-visible:[box-shadow:none]')
    expect(within(table).getByText('Actions').closest('th')).toHaveClass('sticky', 'right-0')
    expect(detailButton.closest('td')).toHaveClass('sticky', 'right-0')
  })

  it('migrates v1 column preferences and keeps client IP visible', async () => {
    window.localStorage.setItem(
      'one-llm-router.requests.columns.v1',
      JSON.stringify(['created_at', 'request_id']),
    )

    await renderRequests()

    const table = await screen.findByTestId('requests-table')
    expect(within(table).getByText('Client IP')).toBeInTheDocument()
    expect(within(table).getByText('203.0.113.10')).toBeInTheDocument()
    expect(within(table).queryByText('Route')).not.toBeInTheDocument()
    expect(window.localStorage.getItem('one-llm-router.requests.columns.v1')).toBeNull()
    expect(window.localStorage.getItem('one-llm-router.requests.columns.v2')).toContain('client_ip')
  })

  it('loads detail bodies on demand and pretty-prints JSON', async () => {
    const user = userEvent.setup()
    await renderRequests()

    await user.click(await screen.findByTestId('request-open-13'))

    expect(await screen.findByTestId('request-detail-layer')).toBeInTheDocument()
    const dialog = screen.getByRole('dialog', { name: 'Request Log Detail' })
    expect(dialog).toBeInTheDocument()
    expect(within(dialog).queryByText('loaded')).not.toBeInTheDocument()
    expect(within(dialog).getAllByText('req_live_...567890').length).toBeGreaterThan(0)
    expect(within(dialog).queryByText('#13 · req_live_...567890')).not.toBeInTheDocument()
    expect(within(dialog).getByText(longRequestID)).toBeInTheDocument()
    expect(within(dialog).getAllByTitle(longRequestID).length).toBeGreaterThan(0)
    expect(
      within(dialog).getByRole('button', { name: `Copy request ID ${longRequestID}` }),
    ).toBeInTheDocument()
    const detailTimestamp = within(dialog).getByTestId('request-detail-created-at')
    const detailDate = new Intl.DateTimeFormat(undefined, {
      day: '2-digit',
      month: '2-digit',
      year: 'numeric',
    }).format(new Date(detailPayload.record.created_at))
    const detailTime = new Intl.DateTimeFormat(undefined, {
      hour: '2-digit',
      hour12: false,
      minute: '2-digit',
      second: '2-digit',
      timeZoneName: 'short',
    }).format(new Date(detailPayload.record.created_at))
    expect(detailTimestamp).toHaveTextContent(`${detailDate} ${detailTime}`)
    expect(detailTimestamp).toHaveAttribute('title', detailPayload.record.created_at)
    expect(await screen.findByTestId('request-detail-view')).toBeInTheDocument()
    const summary = within(dialog).getByTestId('request-detail-summary')
    expect(summary).not.toHaveTextContent('Route')
    expect(summary).not.toHaveTextContent('/v1/responses')
    expect(summary).toHaveTextContent('Outcome')
    expect(summary).toHaveTextContent('success')
    expect(summary).toHaveTextContent('Token Usage')
    expect(within(summary).getByRole('img', { name: 'Token usage legend' })).toBeInTheDocument()
    expect(
      within(summary).getByTestId('request-detail-token-usage-help-tooltip'),
    ).toHaveTextContent('Total: 357 Input: 123 Cached input: 100 Output: 234 Reasoning: 123')
    const tokenUsage = within(summary).getByTestId('request-detail-token-usage')
    expect(tokenUsage).toHaveTextContent('123 / 100 / 234 / 123')
    expect(within(summary).getByTestId('request-detail-token-usage-tooltip')).toHaveTextContent(
      'Total: 357 Input: 123 Cached input: 100 Output: 234 Reasoning: 123',
    )
    expect(within(dialog).getByTestId('request-flow-map')).toHaveTextContent('Client')
    expect(within(dialog).getByTestId('request-flow-map')).toHaveTextContent('203.0.113.10')
    expect(within(dialog).getAllByText('Router').length).toBeGreaterThanOrEqual(2)
    expect(within(dialog).getByTestId('request-flow-map')).toHaveTextContent('LLM Server')
    expect(within(dialog).getByText('response returned to router')).toBeInTheDocument()
    expect(within(dialog).getAllByText('203.0.113.10').length).toBeGreaterThanOrEqual(2)
    expect(within(dialog).getByText('Client → Router')).toBeInTheDocument()
    expect(within(dialog).getByText('Router → LLM Server')).toBeInTheDocument()
    expect(within(dialog).getByText('LLM Server → Router')).toBeInTheDocument()
    expect(within(dialog).getAllByText('https://api.openai.test/v1').length).toBeGreaterThan(0)
    const upstreamSection = within(dialog).getByText('Router → LLM Server').closest('section')
    expect(upstreamSection).not.toBeNull()
    expect(within(upstreamSection as HTMLElement).getByText('Endpoint')).toBeInTheDocument()
    expect(within(upstreamSection as HTMLElement).getByText('/codex/responses')).toBeInTheDocument()
    expect(requestsGetMock).toHaveBeenCalledWith({ path: { id: 13 } })
    expect(within(dialog).getByText('"prompt"')).toBeInTheDocument()
    expect(within(dialog).getByText('"input"')).toBeInTheDocument()
    expect(within(dialog).getByText('"text"')).toBeInTheDocument()
    expect(within(dialog).getAllByText('hello').length).toBeGreaterThan(0)
    expect(within(dialog).getAllByTestId('json-preview').length).toBeGreaterThanOrEqual(3)
    expect(dialog.querySelectorAll('[data-json-token="key"]').length).toBeGreaterThan(0)
    expect(dialog.querySelectorAll('[data-json-token="string"]').length).toBeGreaterThan(0)
    expect(within(dialog).getAllByTestId('json-string-newline')).toHaveLength(2)
    expect(
      Array.from(dialog.querySelectorAll('[data-json-token="string"]')).some((node) =>
        node.textContent?.includes('world'),
      ),
    ).toBe(true)
    expect(screen.queryByText('Model params')).not.toBeInTheDocument()
    expect(screen.queryByText('Router metadata')).not.toBeInTheDocument()
    expect(within(dialog).getByText('Token Usage')).toBeInTheDocument()
    expect(within(dialog).queryByText('"op_id"')).not.toBeInTheDocument()
    expect(within(dialog).queryByText('op.openai.responses.create')).not.toBeInTheDocument()
    expect(screen.queryByText(/"service_tier": "priority"/)).not.toBeInTheDocument()
    expect(within(dialog).getAllByText('TTFT 17 ms / Total 45 ms').length).toBeGreaterThan(0)

    const responseSection = within(dialog).getByText('LLM Server → Router').closest('section')
    expect(responseSection).not.toBeNull()
    const responseCopyButton = within(responseSection as HTMLElement).getByRole('button', {
      name: 'Copy LLM Server → Router',
    })
    expect(responseCopyButton).toBeEnabled()
  })

  it('shows playground upstream endpoint from top-level router metadata', async () => {
    const user = userEvent.setup()
    callAdminMock.mockImplementation(async (call) => {
      const marker = (await call) as unknown as SdkMarker
      if (marker.op === 'list') return listPayload as never
      if (marker.op === 'options') return optionsPayload as never
      const data: RequestDetailData = {
        record: {
          ...detailPayload.record,
          path: '/api/admin/playground/run',
          router_metadata: { upstream_endpoint: '/codex/responses' },
        },
      }
      return data as never
    })

    await renderRequests()
    await user.click(await screen.findByTestId('request-open-13'))

    const dialog = await screen.findByRole('dialog', { name: 'Request Log Detail' })
    const upstreamSection = within(dialog).getByText('Router → LLM Server').closest('section')
    expect(upstreamSection).not.toBeNull()
    expect(within(upstreamSection as HTMLElement).getByText('Endpoint')).toBeInTheDocument()
    expect(within(upstreamSection as HTMLElement).getByText('/codex/responses')).toBeInTheDocument()
  })

  it('shows only total latency when detail has no TTFT', async () => {
    const recordWithoutTTFT = { ...detailPayload.record }
    delete recordWithoutTTFT.ttft_ms
    callAdminMock.mockImplementation(async (call) => {
      const marker = (await call) as unknown as SdkMarker
      if (marker.op === 'list') return listPayload as never
      if (marker.op === 'options') return optionsPayload as never
      return { record: recordWithoutTTFT } as never
    })

    const user = userEvent.setup()
    await renderRequests()

    await user.click(await screen.findByTestId('request-open-13'))

    const dialog = await screen.findByRole('dialog', { name: 'Request Log Detail' })
    expect(within(dialog).getAllByText('Total 45 ms').length).toBeGreaterThan(0)
    expect(within(dialog).queryByText(/TTFT/)).not.toBeInTheDocument()
  })

  it('clears an open detail when list filters change', async () => {
    const user = userEvent.setup()
    await renderRequests()

    await user.click(screen.getByTestId('requests-advanced-filter-toggle'))
    await user.click(await screen.findByTestId('request-open-13'))
    expect(await screen.findByTestId('request-detail-view')).toBeInTheDocument()

    await user.type(screen.getByTestId('requests-search-input'), 'req_live')
    await user.click(screen.getByTestId('requests-search-apply'))

    await waitFor(() => {
      expect(screen.queryByTestId('request-detail-layer')).not.toBeInTheDocument()
    })
  })

  it('closes the detail layer without changing list filters', async () => {
    const user = userEvent.setup()
    await renderRequests()

    await user.click(await screen.findByTestId('request-open-13'))
    const layer = await screen.findByTestId('request-detail-layer')
    await user.click(
      within(screen.getByRole('dialog', { name: 'Request Log Detail' })).getByRole('button', {
        name: 'Close',
      }),
    )

    await waitFor(() => {
      expect(layer).not.toBeInTheDocument()
    })
    expect(screen.getByTestId('requests-table')).toHaveTextContent('req_live_...567890')
  })

  it('closes the detail layer with Escape', async () => {
    const user = userEvent.setup()
    await renderRequests()

    await user.click(await screen.findByTestId('request-open-13'))
    expect(await screen.findByTestId('request-detail-layer')).toBeInTheDocument()

    await user.keyboard('{Escape}')

    await waitFor(() => {
      expect(screen.queryByTestId('request-detail-layer')).not.toBeInTheDocument()
    })
  })

  it('renders list errors without also showing an empty state', async () => {
    callAdminMock.mockImplementation(async (call) => {
      const marker = (await call) as unknown as SdkMarker
      if (marker.op === 'list') throw new Error('db unavailable')
      if (marker.op === 'options') return optionsPayload as never
      return detailPayload as never
    })

    await renderRequests()

    expect(await screen.findByText('Request log unavailable')).toBeInTheDocument()
    expect(screen.queryByTestId('requests-empty')).not.toBeInTheDocument()
  })

  it('renders detail errors without also showing the idle prompt', async () => {
    const user = userEvent.setup()
    callAdminMock.mockImplementation(async (call) => {
      const marker = (await call) as unknown as SdkMarker
      if (marker.op === 'list') return listPayload as never
      if (marker.op === 'options') return optionsPayload as never
      throw new Error('detail missing')
    })

    await renderRequests()
    await user.click(await screen.findByTestId('request-open-13'))

    expect(await screen.findByTestId('request-detail-layer')).toBeInTheDocument()
    expect(await screen.findByText('Request detail unavailable')).toBeInTheDocument()
    expect(screen.queryByTestId('request-detail-empty')).not.toBeInTheDocument()
  })

  it('renders empty state from an empty successful page', async () => {
    callAdminMock.mockImplementation(async (call) => {
      const marker = (await call) as unknown as SdkMarker
      if (marker.op === 'options') return optionsPayload as never
      return { records: [], has_more: false } as never
    })

    await renderRequests()

    expect(await screen.findByTestId('requests-empty')).toHaveTextContent(
      'No request records match the current filters.',
    )
  })
})
