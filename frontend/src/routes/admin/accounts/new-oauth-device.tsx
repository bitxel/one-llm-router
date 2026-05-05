import { useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { Copy, ExternalLink, RotateCcw } from 'lucide-react'
import { type MutableRefObject, useEffect, useEffectEvent, useMemo, useRef, useState } from 'react'
import { toast } from 'sonner'

import { Canvas, Field, PanelCard, Stripe } from '@/components/neo'
import { ErrorBanner } from '@/components/shared/ErrorBanner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { type DeviceStartEnvelope, oauthCancel, oauthDeviceStart } from '@/generated/openapi'
import {
  Err003DeviceAuthUnavailable,
  Err003FlowExpired,
  Err003FlowIDMismatch,
  Err003InvalidOAuthProvider,
  Err003OAuthFlowInProgress,
  Err003OAuthStoreFailed,
  Err003OAuthUpstreamError,
  PlatformUnknown,
} from '@/lib/errcode'
import { type OAuthFlowPending, oauthFlowQueryKey, useOAuthFlow } from '@/lib/oauth-flow'
import { callAdmin } from '@/lib/router-api'
import { RouterApiError } from '@/lib/router-api-error'
import { strings } from './new-oauth-device.strings'
import { invalidateAdminAccountQueries } from './query-keys'

type DeviceStartSuccess = NonNullable<DeviceStartEnvelope['data']>
type DevicePendingFlow = Extract<OAuthFlowPending, { method: 'device' }>

interface StartedDeviceFlow {
  flow_id: string
  user_code: string
  verification_url: string
  interval_seconds: number
  expires_at: string
}

interface PendingConflict {
  flow_id: string
  method: 'browser' | 'device'
  expires_at: string
  created_at: string
}

interface CopyState {
  code: boolean
  url: boolean
}

const copyResetDelayMs = 2_000

export function AdminAccountsNewOAuthDevice() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const copyResetTimers = useRef<{ code: number | null; url: number | null }>({
    code: null,
    url: null,
  })
  const observedDeviceFlowIDRef = useRef<string | null>(null)
  const autoStartAttemptedRef = useRef(false)
  const [startedFlow, setStartedFlow] = useState<StartedDeviceFlow | null>(null)
  const [conflictFlow, setConflictFlow] = useState<PendingConflict | null>(null)
  const [screenError, setScreenError] = useState<unknown>(null)
  const [isStarting, setIsStarting] = useState(false)
  const [isCancelling, setIsCancelling] = useState(false)
  const [countdownLabel, setCountdownLabel] = useState<string>(strings.countdownExpired)
  const [copyState, setCopyState] = useState<CopyState>({ code: false, url: false })
  const oauthFlow = useOAuthFlow({
    pendingIntervalMs: startedFlow ? startedFlow.interval_seconds * 1_000 : 5_000,
  })

  const devicePendingFlow = isPendingDeviceFlow(oauthFlow.flow) ? oauthFlow.flow : null
  const foreignPendingFlow =
    oauthFlow.flow && oauthFlow.flow.status === 'pending' && oauthFlow.flow.method !== 'device'
      ? {
          flow_id: oauthFlow.flow.flow_id,
          method: oauthFlow.flow.method,
          expires_at: oauthFlow.flow.expires_at,
          created_at: oauthFlow.flow.created_at,
        }
      : null
  const pendingConflict = conflictFlow ?? foreignPendingFlow
  const activeDeviceFlow = devicePendingFlow
    ? {
        flow_id: devicePendingFlow.flow_id,
        user_code: devicePendingFlow.user_code,
        verification_url: devicePendingFlow.verification_url,
        interval_seconds: startedFlow?.interval_seconds ?? 5,
        expires_at: devicePendingFlow.expires_at,
      }
    : startedFlow

  const terminalError = oauthFlow.flow?.status === 'error' ? oauthFlow.flow.error : null
  const terminalStatusLabel = useMemo(
    () => renderTerminalStatus(terminalError?.code),
    [terminalError?.code],
  )
  const currentError = oauthFlow.isError ? oauthFlow.error : screenError
  const isBusy = isStarting || isCancelling

  useEffect(() => {
    if (!devicePendingFlow) {
      return
    }
    setConflictFlow(null)
    setScreenError(null)
  }, [devicePendingFlow])

  useEffect(() => {
    if (activeDeviceFlow?.flow_id) {
      observedDeviceFlowIDRef.current = activeDeviceFlow.flow_id
    }
  }, [activeDeviceFlow?.flow_id])

  useEffect(() => {
    if (oauthFlow.flow?.status !== 'success') {
      return
    }
    if (oauthFlow.flow.flow_id !== observedDeviceFlowIDRef.current) {
      return
    }
    void navigateToAccount(oauthFlow.flow.account.id, navigate, queryClient)
  }, [navigate, oauthFlow.flow, queryClient])

  useEffect(() => {
    if (!activeDeviceFlow) {
      setCountdownLabel(strings.countdownExpired)
      return
    }

    const updateCountdown = () => {
      setCountdownLabel(formatCountdown(activeDeviceFlow.expires_at))
    }

    updateCountdown()
    const intervalId = window.setInterval(updateCountdown, 1_000)
    return () => {
      window.clearInterval(intervalId)
    }
  }, [activeDeviceFlow])

  useEffect(() => {
    return () => {
      clearCopyTimer('code', copyResetTimers)
      clearCopyTimer('url', copyResetTimers)
    }
  }, [])

  const startDeviceFlow = useEffectEvent(async () => {
    setIsStarting(true)
    setScreenError(null)
    setConflictFlow(null)
    try {
      const started = (await callAdmin(
        oauthDeviceStart({
          body: { provider: 'openai' },
        }),
      )) as unknown as DeviceStartSuccess
      setStartedFlow({
        flow_id: started.flow_id,
        user_code: started.user_code,
        verification_url: started.verification_url,
        interval_seconds: started.interval_seconds,
        expires_at: started.expires_at,
      })
      await oauthFlow.refetch()
    } catch (err) {
      if (err instanceof RouterApiError && err.code === Err003OAuthFlowInProgress) {
        setStartedFlow(null)
        setConflictFlow(readPendingConflict(err))
        setScreenError(null)
        void oauthFlow.refetch()
        return
      }
      if (err instanceof RouterApiError && err.code === Err003DeviceAuthUnavailable) {
        setStartedFlow(null)
        setScreenError(err)
        return
      }
      if (err instanceof RouterApiError && err.code === Err003InvalidOAuthProvider) {
        setStartedFlow(null)
        setScreenError(err)
        return
      }
      setScreenError(err)
      setStartedFlow(null)
    } finally {
      setIsStarting(false)
    }
  })

  useEffect(() => {
    if (autoStartAttemptedRef.current) {
      return
    }
    autoStartAttemptedRef.current = true
    void startDeviceFlow()
  }, [])

  async function handleCancelPending() {
    const flowId = pendingConflict?.flow_id ?? activeDeviceFlow?.flow_id
    if (!flowId) {
      return
    }

    setIsCancelling(true)
    setScreenError(null)
    try {
      await callAdmin(
        oauthCancel({
          body: { flow_id: flowId },
        }),
      )
      resetToStart(queryClient, setConflictFlow, setScreenError, setStartedFlow)
      await startDeviceFlow()
    } catch (err) {
      if (err instanceof RouterApiError && err.code === Err003FlowIDMismatch) {
        resetToStart(queryClient, setConflictFlow, setScreenError, setStartedFlow)
        await oauthFlow.refetch()
      } else {
        setScreenError(err)
      }
    } finally {
      setIsCancelling(false)
    }
  }

  async function handleRestart() {
    if (oauthFlow.status === 'pending') {
      await handleCancelPending()
      return
    }
    resetToStart(queryClient, setConflictFlow, setScreenError, setStartedFlow)
    await startDeviceFlow()
  }

  async function handleCopy(kind: keyof CopyState, value: string) {
    try {
      await navigator.clipboard.writeText(value)
      setCopyState((prev) => ({ ...prev, [kind]: true }))
      clearCopyTimer(kind, copyResetTimers)
      copyResetTimers.current[kind] = window.setTimeout(() => {
        setCopyState((prev) => ({ ...prev, [kind]: false }))
        copyResetTimers.current[kind] = null
      }, copyResetDelayMs)
    } catch {
      toast.error(strings.toasts.copyFailed)
    }
  }

  const showConflictPanel = Boolean(pendingConflict)
  const showUnavailablePanel =
    currentError instanceof RouterApiError && currentError.code === Err003DeviceAuthUnavailable
  const showInvalidProviderPanel =
    currentError instanceof RouterApiError && currentError.code === Err003InvalidOAuthProvider
  const showErrorBanner =
    currentError &&
    !showConflictPanel &&
    !showUnavailablePanel &&
    !showInvalidProviderPanel &&
    !(terminalError && activeDeviceFlow)
  const statusLabel =
    oauthFlow.status === 'success'
      ? strings.status.success
      : oauthFlow.status === 'pending'
        ? strings.status.pending
        : oauthFlow.status === 'error'
          ? terminalStatusLabel.label
          : strings.status.starting

  return (
    <Canvas variant="wide">
      <Stripe eyebrow={strings.eyebrow}>{strings.stripe}</Stripe>
      <div className="mb-8 max-w-[70ch]">
        <h1 className="mb-3 max-w-[22ch] text-balance">
          {strings.titleLead} <strong>{strings.titleStrong}</strong>
        </h1>
        <p className="max-w-[66ch] text-[14px] leading-[1.7] text-[var(--text-dim)]">
          {strings.intro}
        </p>
      </div>

      {showErrorBanner ? (
        <ErrorBanner error={currentError} title={describeStartError(currentError).title} />
      ) : null}

      {showConflictPanel ? (
        <PanelCard title={strings.panel.blocked} meta="pending" data-testid="device-conflict-panel">
          <div className="space-y-4">
            <p className="text-[13px] leading-[1.65] text-[var(--text-dim)]">
              {strings.err.oauth_flow_in_progress}
            </p>
            <div className="grid gap-3 text-[12.5px] text-[var(--text-dim)] md:grid-cols-3">
              <div>
                <span className="block font-mono text-[10.5px] uppercase tracking-[0.12em] text-[var(--text-muted)]">
                  {strings.labels.conflictMethod}
                </span>
                <span>{pendingConflict?.method}</span>
              </div>
              <div>
                <span className="block font-mono text-[10.5px] uppercase tracking-[0.12em] text-[var(--text-muted)]">
                  {strings.labels.conflictFlow}
                </span>
                <span className="font-mono">{pendingConflict?.flow_id}</span>
              </div>
              <div>
                <span className="block font-mono text-[10.5px] uppercase tracking-[0.12em] text-[var(--text-muted)]">
                  {strings.labels.conflictExpires}
                </span>
                <span>{pendingConflict?.expires_at}</span>
              </div>
            </div>
            <Button onClick={handleCancelPending} disabled={isBusy}>
              {strings.actions.cancelPending}
            </Button>
          </div>
        </PanelCard>
      ) : null}

      {showUnavailablePanel ? (
        <PanelCard
          title={strings.panel.unavailable}
          meta="3015"
          data-testid="device-unavailable-panel"
        >
          <div className="space-y-4">
            <p className="text-[13px] leading-[1.65] text-[var(--text-dim)]">
              {strings.err.device_auth_unavailable}
            </p>
            <Button asChild>
              <Link to="/admin/accounts/new-oauth">{strings.actions.tryBrowser}</Link>
            </Button>
          </div>
        </PanelCard>
      ) : null}

      {showInvalidProviderPanel ? (
        <PanelCard
          title={strings.panel.blocked}
          meta="3002"
          data-testid="device-invalid-provider-panel"
        >
          <div className="space-y-3 text-[13px] leading-[1.65] text-[var(--text-dim)]">
            <p>{strings.err.invalid_oauth_provider}</p>
            <code className="inline-block border border-[var(--line-2)] bg-[var(--panel-2)] px-2 py-1 font-mono text-[11.5px] text-[var(--text)]">
              {JSON.stringify((currentError as RouterApiError).data)}
            </code>
          </div>
        </PanelCard>
      ) : null}

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_320px]">
        <div>
          <PanelCard
            title={
              activeDeviceFlow
                ? strings.panel.pending
                : terminalError
                  ? strings.panel.failed
                  : strings.panel.code
            }
            meta={renderPanelMeta(activeDeviceFlow, terminalError)}
            data-testid="device-flow-panel"
          >
            {activeDeviceFlow ? (
              <>
                <Field label={strings.fields.userCode.label} hint={strings.fields.userCode.hint}>
                  <div className="flex flex-wrap items-start gap-3">
                    <output
                      data-testid="device-user-code"
                      aria-label={strings.labels.userCode}
                      className="min-w-[180px] border border-[var(--line-2)] bg-[var(--panel-2)] px-4 py-3 font-mono text-[20px] tracking-[0.18em] text-[var(--text)]"
                      style={{ borderRadius: 2 }}
                    >
                      {activeDeviceFlow.user_code}
                    </output>
                    <Button
                      type="button"
                      variant="outline"
                      aria-label={strings.labels.copyCode}
                      onClick={() => handleCopy('code', activeDeviceFlow.user_code)}
                    >
                      <Copy />
                      {copyState.code ? strings.actions.copied : strings.actions.copyCode}
                    </Button>
                  </div>
                </Field>

                <Field
                  label={strings.fields.verificationURL.label}
                  hint={strings.fields.verificationURL.hint}
                >
                  <div className="flex flex-wrap items-start gap-3">
                    <a
                      data-testid="device-verification-url"
                      href={activeDeviceFlow.verification_url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex items-center gap-2 text-[13px] leading-[1.6] text-[var(--accent)] underline-offset-4 hover:underline"
                    >
                      <span className="break-all">{activeDeviceFlow.verification_url}</span>
                      <ExternalLink className="h-4 w-4" />
                    </a>
                    <Button
                      type="button"
                      variant="outline"
                      aria-label={strings.labels.copyURL}
                      onClick={() => handleCopy('url', activeDeviceFlow.verification_url)}
                    >
                      <Copy />
                      {copyState.url ? strings.actions.copied : strings.actions.copyURL}
                    </Button>
                  </div>
                </Field>

                <Field label={strings.fields.countdown.label} hint={strings.fields.countdown.hint}>
                  <div className="flex flex-wrap items-center gap-3">
                    <div
                      data-testid="device-countdown"
                      className="border border-[var(--line-2)] bg-[var(--panel-2)] px-3 py-2 font-mono text-[13px] text-[var(--text)]"
                      style={{ borderRadius: 2 }}
                    >
                      {countdownLabel}
                    </div>
                    <Badge variant="outline">
                      {`${strings.fields.countdown.intervalBadge} ${activeDeviceFlow.interval_seconds}s`}
                    </Badge>
                  </div>
                </Field>
              </>
            ) : (
              <div className="text-[13px] leading-[1.65] text-[var(--text-dim)]">
                {terminalError ? terminalStatusLabel.detail : strings.intro}
              </div>
            )}
          </PanelCard>
        </div>

        <div>
          <PanelCard
            title={strings.panel.status}
            meta={<span data-testid="device-status-pill">{statusLabel}</span>}
            metaMuted={oauthFlow.status !== 'pending'}
          >
            <div className="space-y-4 text-[13px] leading-[1.65] text-[var(--text-dim)]">
              <div>{renderStatusDetail(oauthFlow.status, terminalStatusLabel.detail)}</div>
              {activeDeviceFlow && oauthFlow.status === 'pending' ? (
                <Button
                  type="button"
                  variant="outline"
                  onClick={handleRestart}
                  disabled={isBusy}
                  data-testid="device-restart-button"
                >
                  <RotateCcw />
                  {strings.actions.restart}
                </Button>
              ) : null}
              {!activeDeviceFlow || terminalError ? (
                <Button
                  type="button"
                  variant="outline"
                  onClick={handleRestart}
                  disabled={isBusy}
                  data-testid="device-restart-button"
                >
                  <RotateCcw />
                  {strings.actions.restart}
                </Button>
              ) : null}
            </div>
          </PanelCard>
        </div>
      </div>
    </Canvas>
  )
}

function isPendingDeviceFlow(
  flow: ReturnType<typeof useOAuthFlow>['flow'],
): flow is DevicePendingFlow {
  return Boolean(flow && flow.status === 'pending' && flow.method === 'device')
}

function formatCountdown(expiresAt: string): string {
  const remainingMs = Date.parse(expiresAt) - Date.now()
  if (!Number.isFinite(remainingMs) || remainingMs <= 0) {
    return strings.countdownAwaitingServer
  }
  return `${Math.ceil(remainingMs / 1000)}s`
}

function renderPanelMeta(
  activeDeviceFlow: StartedDeviceFlow | null,
  terminalError: { code: string; message: string } | null,
) {
  if (activeDeviceFlow) {
    return strings.panel.metaPending
  }
  if (terminalError) {
    return terminalError.code
  }
  return strings.panel.metaStarting
}

function renderTerminalStatus(code?: string) {
  if (code === 'expired_token' || code === 'flow_expired') {
    return {
      label: strings.status.expired,
      detail: strings.toasts.flowExpired,
    }
  }
  if (code === 'cancelled') {
    return {
      label: strings.status.cancelled,
      detail: strings.toasts.flowCancelled,
    }
  }
  if (code === 'access_denied') {
    return {
      label: strings.status.denied,
      detail: strings.toasts.flowCancelled,
    }
  }
  return {
    label: strings.status.failed,
    detail: strings.err.default,
  }
}

function renderStatusDetail(
  status: ReturnType<typeof useOAuthFlow>['status'],
  terminalDetail: string,
) {
  if (status === 'pending') {
    return strings.status.pending
  }
  if (status === 'success') {
    return strings.status.success
  }
  if (status === 'error') {
    return terminalDetail
  }
  return strings.intro
}

function describeStartError(error: unknown): { title: string; detail: string } {
  if (!(error instanceof RouterApiError)) {
    return { title: strings.err.default, detail: '' }
  }
  if (error.code === Err003OAuthUpstreamError) {
    return {
      title: strings.err.oauth_upstream_error,
      detail: joinParts([
        readStringField(error.data, 'provider_error'),
        readStringField(error.data, 'provider_message'),
      ]),
    }
  }
  if (error.code === Err003OAuthStoreFailed) {
    return { title: strings.err.oauth_store_failed, detail: '' }
  }
  if (error.code === Err003FlowExpired) {
    return { title: strings.status.expired, detail: strings.toasts.flowExpired }
  }
  if (error.code === PlatformUnknown) {
    return { title: strings.err.transport_error, detail: '' }
  }
  return {
    title: strings.err.default,
    detail: typeof error.msg === 'string' ? error.msg : '',
  }
}

function resetToStart(
  queryClient: ReturnType<typeof useQueryClient>,
  setConflictFlow: (value: PendingConflict | null) => void,
  setScreenError: (value: unknown) => void,
  setStartedFlow: (value: StartedDeviceFlow | null) => void,
) {
  setConflictFlow(null)
  setScreenError(null)
  setStartedFlow(null)
  queryClient.setQueryData(oauthFlowQueryKey, { status: 'idle' })
}

async function navigateToAccount(
  accountId: number,
  navigate: ReturnType<typeof useNavigate>,
  queryClient: ReturnType<typeof useQueryClient>,
) {
  await invalidateAdminAccountQueries(queryClient, accountId)
  queryClient.setQueryData(oauthFlowQueryKey, { status: 'idle' })
  await navigate({
    to: '/admin/accounts/$accountId',
    params: { accountId: String(accountId) },
  })
}

function readPendingConflict(error: RouterApiError): PendingConflict | null {
  if (typeof error.data !== 'object' || error.data === null) {
    return null
  }
  const data = error.data as Record<string, unknown>
  if (
    (data.method !== 'browser' && data.method !== 'device') ||
    typeof data.flow_id !== 'string' ||
    typeof data.expires_at !== 'string' ||
    typeof data.created_at !== 'string'
  ) {
    return null
  }
  return {
    flow_id: data.flow_id,
    method: data.method,
    expires_at: data.expires_at,
    created_at: data.created_at,
  }
}

function joinParts(parts: Array<string | undefined>): string {
  return parts
    .filter((part): part is string => typeof part === 'string' && part.length > 0)
    .join(' · ')
}

function readStringField(value: unknown, key: string): string | undefined {
  if (typeof value !== 'object' || value === null) {
    return undefined
  }
  const field = (value as Record<string, unknown>)[key]
  return typeof field === 'string' ? field : undefined
}

function clearCopyTimer(
  kind: keyof CopyState,
  timers: MutableRefObject<{ code: number | null; url: number | null }>,
) {
  const timer = timers.current[kind]
  if (timer !== null) {
    window.clearTimeout(timer)
    timers.current[kind] = null
  }
}
