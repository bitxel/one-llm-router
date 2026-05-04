import { type UseQueryOptions, type UseQueryResult, useQuery } from '@tanstack/react-query'

import { type FlowStatusEnvelope, oauthFlowStatus } from '@/generated/openapi'

import { callAdmin, RouterApiError, type RouterApiError as RouterApiErrorShape } from './router-api'

export type OAuthFlowSnapshot = NonNullable<FlowStatusEnvelope['data']>
export type OAuthFlowStatus = OAuthFlowSnapshot['status']
export type OAuthFlowPending = Extract<OAuthFlowSnapshot, { status: 'pending' }>
export type OAuthFlowTerminal = Extract<OAuthFlowSnapshot, { status: 'success' | 'error' }>
export type OAuthFlowActive = Exclude<OAuthFlowSnapshot, { status: 'idle' }>

export interface UseOAuthFlowResult
  extends Pick<
    UseQueryResult<OAuthFlowSnapshot, RouterApiErrorShape>,
    'error' | 'isError' | 'isFetching' | 'isLoading' | 'refetch'
  > {
  status: OAuthFlowStatus
  flow: OAuthFlowActive | null
  isPolling: boolean
}

export const oauthFlowQueryKey = ['admin', 'oauth', 'flow'] as const

export interface OAuthFlowQueryOptionsInput {
  pendingIntervalMs?: number
}

async function fetchOAuthFlow(): Promise<OAuthFlowSnapshot> {
  return decodeOAuthFlowSnapshot(await callAdmin(oauthFlowStatus()))
}

export const oauthFlowQueryOptions = (
  options?: OAuthFlowQueryOptionsInput,
): UseQueryOptions<
  OAuthFlowSnapshot,
  RouterApiErrorShape,
  OAuthFlowSnapshot,
  typeof oauthFlowQueryKey
> => ({
  queryKey: oauthFlowQueryKey,
  queryFn: fetchOAuthFlow,
  staleTime: 0,
  gcTime: 0,
  refetchOnMount: 'always' as const,
  refetchOnWindowFocus: false,
  refetchInterval: (query) =>
    query.state.data?.status === 'pending' ? (options?.pendingIntervalMs ?? 1_000) : false,
})

export function useOAuthFlow(options?: OAuthFlowQueryOptionsInput): UseOAuthFlowResult {
  const query = useQuery(oauthFlowQueryOptions(options))
  const snapshot = query.data
  const status = snapshot?.status ?? 'idle'
  const flow: OAuthFlowActive | null =
    snapshot && snapshot.status !== 'idle' ? (snapshot as OAuthFlowActive) : null

  return {
    status,
    flow,
    isPolling: status === 'pending',
    error: query.error,
    isError: query.isError,
    isFetching: query.isFetching,
    isLoading: query.isLoading,
    refetch: query.refetch,
  }
}

function decodeOAuthFlowSnapshot(value: unknown): OAuthFlowSnapshot {
  if (isOAuthFlowSnapshot(value)) {
    return value
  }
  throw new RouterApiError({
    code: -1,
    msg: 'malformed_flow_status',
    data: value,
    requestId: null,
    status: 200,
  })
}

function isOAuthFlowSnapshot(value: unknown): value is OAuthFlowSnapshot {
  if (typeof value !== 'object' || value === null) {
    return false
  }
  const candidate = value as Record<string, unknown>
  if (candidate.status === 'idle') {
    return true
  }
  if (candidate.status === 'pending') {
    return isPendingSnapshot(candidate)
  }
  if (candidate.status === 'success') {
    return isSuccessSnapshot(candidate)
  }
  if (candidate.status === 'error') {
    return isErrorSnapshot(candidate)
  }
  return false
}

function isPendingSnapshot(candidate: Record<string, unknown>): candidate is OAuthFlowPending {
  if (
    typeof candidate.flow_id !== 'string' ||
    typeof candidate.created_at !== 'string' ||
    typeof candidate.expires_at !== 'string'
  ) {
    return false
  }
  if (candidate.method === 'browser') {
    return typeof candidate.listener_bound === 'boolean'
  }
  if (candidate.method === 'device') {
    return typeof candidate.user_code === 'string' && typeof candidate.verification_url === 'string'
  }
  return false
}

function isSuccessSnapshot(candidate: Record<string, unknown>): candidate is OAuthFlowTerminal {
  if (
    typeof candidate.flow_id !== 'string' ||
    (candidate.method !== 'browser' && candidate.method !== 'device')
  ) {
    return false
  }
  return typeof candidate.account === 'object' && candidate.account !== null
}

function isErrorSnapshot(candidate: Record<string, unknown>): candidate is OAuthFlowTerminal {
  if (
    typeof candidate.flow_id !== 'string' ||
    (candidate.method !== 'browser' && candidate.method !== 'device') ||
    typeof candidate.error !== 'object' ||
    candidate.error === null
  ) {
    return false
  }
  const error = candidate.error as Record<string, unknown>
  return typeof error.code === 'string' && typeof error.message === 'string'
}
