import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook } from '@testing-library/react'
import type { PropsWithChildren } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import * as openapi from '@/generated/openapi'

import { type OAuthFlowSnapshot, oauthFlowQueryOptions, useOAuthFlow } from './oauth-flow'
import * as routerApi from './router-api'

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  })

  return function Wrapper({ children }: PropsWithChildren) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  }
}

function mockFlowSequence(...snapshots: OAuthFlowSnapshot[]) {
  const oauthFlowStatusMock = vi.spyOn(openapi, 'oauthFlowStatus')
  oauthFlowStatusMock.mockResolvedValue({} as never)

  let index = 0
  const callAdminMock = vi.spyOn(routerApi, 'callAdmin')
  callAdminMock.mockImplementation(
    async () => snapshots[Math.min(index++, snapshots.length - 1)] as never,
  )

  return { oauthFlowStatusMock, callAdminMock }
}

async function advancePollingClock(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms)
  })
}

async function flushQueryResult() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

describe('useOAuthFlow', () => {
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('returns idle state without polling after an idle snapshot', async () => {
    vi.useFakeTimers()
    const { oauthFlowStatusMock } = mockFlowSequence({ status: 'idle' })

    const { result } = renderHook(() => useOAuthFlow(), {
      wrapper: createWrapper(),
    })

    await advancePollingClock(0)
    await flushQueryResult()

    expect(result.current.status).toBe('idle')
    expect(result.current.flow).toBeNull()
    expect(result.current.isPolling).toBe(false)
    expect(oauthFlowStatusMock).toHaveBeenCalledTimes(1)

    await advancePollingClock(3_000)

    expect(oauthFlowStatusMock).toHaveBeenCalledTimes(1)
  })

  it('polls once per second while pending', async () => {
    vi.useFakeTimers()
    const pending: OAuthFlowSnapshot = {
      status: 'pending',
      method: 'browser',
      flow_id: 'fl_browser_pending_1234567890',
      listener_bound: true,
      created_at: '2026-04-21T12:00:00Z',
      expires_at: '2026-04-21T12:05:00Z',
    }
    const { oauthFlowStatusMock } = mockFlowSequence(pending, pending)

    const { result } = renderHook(() => useOAuthFlow(), {
      wrapper: createWrapper(),
    })

    await advancePollingClock(0)
    await flushQueryResult()

    expect(result.current.flow).toEqual(pending)
    expect(result.current.isPolling).toBe(true)
    expect(oauthFlowStatusMock).toHaveBeenCalledTimes(1)

    await advancePollingClock(1_000)
    await flushQueryResult()

    expect(oauthFlowStatusMock).toHaveBeenCalledTimes(2)
    expect(result.current.status).toBe('pending')
    expect(result.current.isPolling).toBe(true)
  })

  it('uses a caller-supplied pending poll interval', () => {
    const { refetchInterval } = oauthFlowQueryOptions({ pendingIntervalMs: 5_000 })
    expect(refetchInterval).toBeTypeOf('function')

    const getInterval = refetchInterval as (query: {
      state: { data?: OAuthFlowSnapshot }
    }) => number | false

    expect(
      getInterval({
        state: {
          data: {
            status: 'pending',
            method: 'device',
            flow_id: 'fl_pending_device_1234567890',
            user_code: 'ABCD-1234',
            verification_url: 'https://auth.openai.com/codex/device',
            created_at: '2026-04-21T12:00:00Z',
            expires_at: '2026-04-21T12:15:00Z',
          },
        },
      }),
    ).toBe(5_000)
  })

  it('stops polling when the current snapshot is terminal or idle', () => {
    const { refetchInterval } = oauthFlowQueryOptions()
    expect(refetchInterval).toBeTypeOf('function')

    const getInterval = refetchInterval as (query: {
      state: { data?: OAuthFlowSnapshot }
    }) => number | false

    expect(getInterval({ state: { data: { status: 'idle' } } })).toBe(false)
    expect(
      getInterval({
        state: {
          data: {
            status: 'success',
            method: 'browser',
            flow_id: 'fl_success_1234567890',
            rail: 'loopback',
            account: {
              id: 77,
              name: 'op@example.com',
              provider: 'openai',
              auth_method: 'oauth_browser',
              status: 'active',
            },
          },
        },
      }),
    ).toBe(false)
    expect(
      getInterval({
        state: {
          data: {
            status: 'error',
            method: 'device',
            flow_id: 'fl_error_1234567890',
            error: {
              code: 'expired_token',
              message: 'Device code expired',
            },
          },
        },
      }),
    ).toBe(false)
    expect(
      getInterval({
        state: {
          data: {
            status: 'pending',
            method: 'device',
            flow_id: 'fl_pending_1234567890',
            user_code: 'ABCD-1234',
            verification_url: 'https://auth.openai.com/codex/device',
            created_at: '2026-04-21T12:00:00Z',
            expires_at: '2026-04-21T12:15:00Z',
          },
        },
      }),
    ).toBe(1_000)
  })

  it('fails fast when the flow payload is malformed', async () => {
    vi.useFakeTimers()
    const oauthFlowStatusMock = vi.spyOn(openapi, 'oauthFlowStatus')
    oauthFlowStatusMock.mockResolvedValue({} as never)
    vi.spyOn(routerApi, 'callAdmin').mockResolvedValue({ malformed: true } as never)

    const { result } = renderHook(() => useOAuthFlow(), {
      wrapper: createWrapper(),
    })

    await advancePollingClock(0)
    await flushQueryResult()

    expect(result.current.isError).toBe(true)
    expect(result.current.error).toBeInstanceOf(routerApi.RouterApiError)
    expect(result.current.error?.msg).toBe('malformed_flow_status')
    expect(result.current.status).toBe('idle')
    expect(result.current.flow).toBeNull()
  })
})
