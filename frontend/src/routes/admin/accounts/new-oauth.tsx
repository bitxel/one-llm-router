import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { ExternalLink, Eye, EyeOff, RotateCcw, X } from 'lucide-react'
import {
  type Dispatch,
  type MutableRefObject,
  type SetStateAction,
  useEffect,
  useEffectEvent,
  useRef,
  useState,
} from 'react'
import { useForm } from 'react-hook-form'
import { toast } from 'sonner'
import { z } from 'zod'
import { Canvas, Field, PanelCard, Stripe } from '@/components/neo'
import { ErrorBanner } from '@/components/shared/ErrorBanner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  type BrowserStartEnvelope,
  type ManualCallbackCancelEnvelope,
  type ManualCallbackSuccessEnvelope,
  oauthBrowserManualCallback,
  oauthBrowserStart,
  oauthCancel,
} from '@/generated/openapi'
import {
  Err003AlreadyConsumed,
  Err003FlowExpired,
  Err003FlowIDMismatch,
  Err003InvalidCallbackURL,
  Err003NoFlowInProgress,
  Err003OAuthFlowInProgress,
  Err003OAuthInternalError,
  Err003OAuthInvalidGrant,
  Err003OAuthStateMismatch,
  Err003OAuthStoreFailed,
  Err003OAuthUpstreamError,
  PlatformUnknown,
} from '@/lib/errcode'
import {
  type OAuthFlowActive,
  type OAuthFlowPending,
  type OAuthFlowSnapshot,
  oauthFlowQueryKey,
  useOAuthFlow,
} from '@/lib/oauth-flow'
import { callAdmin } from '@/lib/router-api'
import { RouterApiError } from '@/lib/router-api-error'
import { stableId } from '@/lib/utils'
import { strings } from './new-oauth.strings'
import { invalidateAdminAccountQueries } from './query-keys'

const callbackFieldId = stableId('oauth-browser-callback-url')

const CallbackFormSchema = z.object({
  callback_url: z.string().trim().min(1, strings.validation.callbackRequired),
})

type CallbackForm = z.infer<typeof CallbackFormSchema>
type BrowserStartSuccess = NonNullable<BrowserStartEnvelope['data']>
type ManualCallbackResult =
  | NonNullable<ManualCallbackSuccessEnvelope['data']>
  | NonNullable<ManualCallbackCancelEnvelope['data']>
type BrowserPendingFlow = Extract<OAuthFlowPending, { method: 'browser' }>

interface StartedBrowserFlow {
  flow_id: string
  listener_bound: boolean
  expires_at: string
}

interface PendingConflict {
  flow_id: string
  method: 'browser' | 'device'
  expires_at: string
  created_at: string
}

interface InlineFeedback {
  title: string
  detail?: string
}

interface CallbackSummary {
  target: string
  codeLabel: string
  stateLabel: string
  variant: 'neutral' | 'warning'
}

export function AdminAccountsNewOAuth() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const oauthFlow = useOAuthFlow()
  const authorizeUrlRef = useRef<string | null>(null)
  const navigatedRef = useRef(false)
  const observedBrowserFlowIDRef = useRef<string | null>(null)
  const [startedFlow, setStartedFlow] = useState<StartedBrowserFlow | null>(null)
  const [conflictFlow, setConflictFlow] = useState<PendingConflict | null>(null)
  const [screenError, setScreenError] = useState<unknown>(null)
  const [inlineFeedback, setInlineFeedback] = useState<InlineFeedback | null>(null)
  const [isStarting, setIsStarting] = useState(false)
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [isCancelling, setIsCancelling] = useState(false)
  const [showRawCallbackURL, setShowRawCallbackURL] = useState(false)

  const callbackForm = useForm<CallbackForm>({
    resolver: zodResolver(CallbackFormSchema),
    defaultValues: { callback_url: '' },
  })
  const pastedCallbackURL = callbackForm.watch('callback_url')

  const browserPendingFlow = isPendingBrowserFlow(oauthFlow.flow) ? oauthFlow.flow : null
  const foreignPendingFlow =
    oauthFlow.flow && oauthFlow.flow.status === 'pending' && oauthFlow.flow.method !== 'browser'
      ? {
          flow_id: oauthFlow.flow.flow_id,
          method: oauthFlow.flow.method,
          expires_at: oauthFlow.flow.expires_at,
          created_at: oauthFlow.flow.created_at,
        }
      : null
  const pendingConflict = conflictFlow ?? foreignPendingFlow
  const activeBrowserFlow = browserPendingFlow ?? startedFlow
  const terminalError = oauthFlow.flow?.status === 'error' ? oauthFlow.flow.error : null

  useEffect(() => {
    if (browserPendingFlow) {
      setConflictFlow(null)
    }
  }, [browserPendingFlow])

  useEffect(() => {
    if (activeBrowserFlow?.flow_id) {
      observedBrowserFlowIDRef.current = activeBrowserFlow.flow_id
    }
  }, [activeBrowserFlow?.flow_id])

  useEffect(() => {
    if (oauthFlow.flow?.status !== 'success') {
      return
    }
    if (oauthFlow.flow.flow_id !== observedBrowserFlowIDRef.current) {
      return
    }
    void navigateToAccount(oauthFlow.flow.account.id, navigate, queryClient, navigatedRef)
  }, [navigate, oauthFlow.flow, queryClient])

  useEffect(() => {
    if (oauthFlow.flow?.status !== 'error') {
      return
    }
    const code = oauthFlow.flow.error.code
    if (code === 'flow_expired') {
      toast.error(strings.toasts.flowExpired)
      resetToStart({
        callbackForm,
        queryClient,
        setConflictFlow,
        setInlineFeedback,
        setScreenError,
        setStartedFlow,
        authorizeUrlRef,
      })
      return
    }
    if (code === 'cancelled' || code === 'access_denied') {
      toast.error(strings.toasts.flowCancelled)
      resetToStart({
        callbackForm,
        queryClient,
        setConflictFlow,
        setInlineFeedback,
        setScreenError,
        setStartedFlow,
        authorizeUrlRef,
      })
    }
  }, [callbackForm, oauthFlow.flow, queryClient])

  const currentError = oauthFlow.isError ? oauthFlow.error : screenError
  const isBusy = isStarting || isSubmitting || isCancelling
  const hasCallbackURL = pastedCallbackURL.trim() !== ''
  const callbackSummary = summarizeCallbackURL(pastedCallbackURL)
  const panelMeta = renderPanelMeta(activeBrowserFlow, pendingConflict, terminalError)

  useEffect(() => {
    if (!hasCallbackURL && showRawCallbackURL) {
      setShowRawCallbackURL(false)
    }
  }, [hasCallbackURL, showRawCallbackURL])

  const handleStartBrowser = useEffectEvent(async () => {
    setIsStarting(true)
    setScreenError(null)
    setInlineFeedback(null)
    setConflictFlow(null)
    try {
      const started = (await callAdmin(
        oauthBrowserStart({
          body: { provider: 'openai' },
        }),
      )) as unknown as BrowserStartSuccess
      authorizeUrlRef.current = started.authorize_url
      setStartedFlow({
        flow_id: started.flow_id,
        listener_bound: started.listener_bound,
        expires_at: started.expires_at,
      })
      setShowRawCallbackURL(false)
      callbackForm.reset()
      openAuthorizeTab(started.authorize_url)
      await oauthFlow.refetch()
    } catch (err) {
      if (err instanceof RouterApiError && err.code === Err003OAuthFlowInProgress) {
        setStartedFlow(null)
        setConflictFlow(readPendingConflict(err))
        void oauthFlow.refetch()
        setScreenError(null)
      } else {
        setScreenError(err)
      }
    } finally {
      setIsStarting(false)
    }
  })

  async function handleCancelPending(retryStart: boolean) {
    const flowId = pendingConflict?.flow_id ?? activeBrowserFlow?.flow_id
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
      resetToStart({
        callbackForm,
        queryClient,
        setConflictFlow,
        setInlineFeedback,
        setScreenError,
        setStartedFlow,
        authorizeUrlRef,
      })
      if (retryStart) {
        await handleStartBrowser()
      }
    } catch (err) {
      if (err instanceof RouterApiError && err.code === Err003FlowIDMismatch) {
        resetToStart({
          callbackForm,
          queryClient,
          setConflictFlow,
          setInlineFeedback,
          setScreenError,
          setStartedFlow,
          authorizeUrlRef,
        })
        await oauthFlow.refetch()
      } else {
        setScreenError(err)
      }
    } finally {
      setIsCancelling(false)
    }
  }

  async function handleManualCallbackSubmit(values: CallbackForm) {
    setIsSubmitting(true)
    setScreenError(null)
    setInlineFeedback(null)
    try {
      const result = (await callAdmin(
        oauthBrowserManualCallback({
          body: { callback_url: values.callback_url },
        }),
      )) as unknown as ManualCallbackResult
      setShowRawCallbackURL(false)
      callbackForm.reset()
      if (result.status === 'success') {
        await navigateToAccount(result.account.id, navigate, queryClient, navigatedRef)
        return
      }
      toast.error(strings.toasts.flowCancelled)
      resetToStart({
        callbackForm,
        queryClient,
        setConflictFlow,
        setInlineFeedback,
        setScreenError,
        setStartedFlow,
        authorizeUrlRef,
      })
    } catch (err) {
      if (err instanceof RouterApiError) {
        if (err.code === Err003AlreadyConsumed) {
          setInlineFeedback(null)
          const result = await oauthFlow.refetch()
          if (result.data?.status === 'success') {
            await navigateToAccount(result.data.account.id, navigate, queryClient, navigatedRef)
          }
          return
        }

        if (err.code === Err003FlowExpired) {
          toast.error(strings.toasts.flowExpired)
          resetToStart({
            callbackForm,
            queryClient,
            setConflictFlow,
            setInlineFeedback,
            setScreenError,
            setStartedFlow,
            authorizeUrlRef,
          })
          return
        }

        if (err.code === Err003NoFlowInProgress) {
          resetToStart({
            callbackForm,
            queryClient,
            setConflictFlow,
            setInlineFeedback,
            setScreenError,
            setStartedFlow,
            authorizeUrlRef,
          })
        }

        const feedback = describeManualCallbackError(err)
        if (feedback) {
          setInlineFeedback(feedback)
          return
        }
      }

      setScreenError(err)
    } finally {
      setIsSubmitting(false)
    }
  }

  return (
    <Canvas variant="narrow">
      <Stripe eyebrow={strings.eyebrow}>{strings.stripe}</Stripe>
      <h1 className="mb-2 max-w-[22ch]">
        {strings.titleLead} <strong>{strings.titleStrong}</strong>
      </h1>
      <p className="mb-5 max-w-[58ch] text-[14px] leading-[1.6] text-[var(--text-dim)]">
        {strings.intro}
      </p>

      {currentError ? (
        <div className="mb-4">
          <ErrorBanner error={currentError} title={strings.err.default} />
        </div>
      ) : null}

      <PanelCard
        title={
          pendingConflict
            ? strings.panel.conflict
            : terminalError
              ? strings.panel.failed
              : strings.panel.idle
        }
        meta={panelMeta}
      >
        <Field
          label={activeBrowserFlow ? strings.fields.flowStatus.label : strings.status.browserMethod}
          hint={
            activeBrowserFlow ? strings.fields.flowStatus.hint : strings.status.browserMethodDetail
          }
        >
          {activeBrowserFlow ? (
            <FlowStatusStrip flow={activeBrowserFlow} />
          ) : (
            <div className="flex flex-wrap items-center gap-2 text-[12.5px] text-[var(--text-dim)]">
              <Badge variant="outline">{strings.status.browserMethod}</Badge>
            </div>
          )}
        </Field>

        {pendingConflict && !activeBrowserFlow ? (
          <Field label={strings.fields.flow.label} hint={strings.conflict.detail}>
            <div
              data-testid="oauth-conflict-banner"
              className="space-y-3 border border-[var(--warn)] bg-[var(--warn-soft)] px-4 py-3 text-[12.5px] leading-[1.6] text-[var(--warn)]"
              style={{ borderRadius: 2 }}
            >
              <div>{strings.err.oauth_flow_in_progress}</div>
              <div className="flex flex-wrap items-center gap-2 font-mono text-[11.5px]">
                <span>{strings.conflict.methodPrefix}</span>
                <code>{pendingConflict.method}</code>
                <span>{strings.conflict.flowPrefix}</span>
                <code>{pendingConflict.flow_id}</code>
              </div>
              <div className="flex flex-wrap gap-2">
                <Button
                  type="button"
                  variant="secondary"
                  data-testid="oauth-cancel-pending"
                  disabled={isBusy}
                  onClick={() => void handleCancelPending(true)}
                >
                  {strings.actions.cancelPending}
                </Button>
              </div>
            </div>
          </Field>
        ) : null}

        {!activeBrowserFlow && !pendingConflict && !terminalError ? (
          <Field
            label={strings.fields.browserActions.label}
            hint={strings.fields.browserActions.hint}
          >
            <div className="flex flex-wrap gap-2">
              <Button
                type="button"
                data-testid="oauth-start-button"
                disabled={isBusy}
                onClick={() => void handleStartBrowser()}
              >
                <ExternalLink />
                {strings.actions.start}
              </Button>
            </div>
          </Field>
        ) : null}

        {activeBrowserFlow ? (
          <>
            <Field label={strings.fields.flow.label} hint={strings.fields.flow.hint}>
              <div className="space-y-3">
                <div className="flex flex-wrap items-center gap-2 font-mono text-[11.5px] text-[var(--text-dim)]">
                  <code title={activeBrowserFlow.flow_id}>
                    {formatFlowID(activeBrowserFlow.flow_id)}
                  </code>
                  <Badge variant="outline">{strings.badges.pending}</Badge>
                </div>
                <div className="flex flex-wrap gap-2">
                  <Button
                    type="button"
                    variant="secondary"
                    data-testid="oauth-open-tab-again"
                    disabled={isBusy || !authorizeUrlRef.current}
                    onClick={() => {
                      if (authorizeUrlRef.current) {
                        openAuthorizeTab(authorizeUrlRef.current)
                      }
                    }}
                  >
                    <ExternalLink />
                    {strings.actions.openAgain}
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    data-testid="oauth-cancel-pending"
                    disabled={isBusy}
                    onClick={() => void handleCancelPending(false)}
                  >
                    {strings.actions.cancelPending}
                  </Button>
                </div>
              </div>
            </Field>

            <form onSubmit={callbackForm.handleSubmit(handleManualCallbackSubmit)}>
              <Field
                htmlFor={callbackFieldId}
                label={strings.fields.callback.label}
                hint={strings.fields.callback.hint}
              >
                <div className="space-y-3">
                  <input
                    id={callbackFieldId}
                    data-testid="oauth-callback-input"
                    aria-label={strings.fields.callback.label}
                    type={showRawCallbackURL ? 'text' : 'password'}
                    autoComplete="off"
                    spellCheck={false}
                    className="h-11 w-full border border-[var(--line-3)] bg-[var(--panel-2)] px-3 py-2 font-mono text-[12.5px] leading-[1.6] text-[var(--text)] outline-none transition-colors placeholder:font-sans placeholder:text-[var(--text-faint)] focus:border-[var(--accent)] focus-visible:[box-shadow:var(--focus)]"
                    style={{ borderRadius: 2 }}
                    placeholder={strings.fields.callback.placeholder}
                    {...callbackForm.register('callback_url')}
                  />
                  <CallbackURLSummary summary={callbackSummary} />
                  {callbackForm.formState.errors.callback_url ? (
                    <p className="text-[11.5px] text-[var(--err)]">
                      {callbackForm.formState.errors.callback_url.message}
                    </p>
                  ) : null}
                  {inlineFeedback ? (
                    <div
                      data-testid="oauth-inline-error"
                      className="border border-[var(--err)] bg-[var(--err-soft)] px-4 py-3 text-[12.5px] leading-[1.6] text-[var(--err)]"
                      style={{ borderRadius: 2 }}
                    >
                      <div className="font-medium">{inlineFeedback.title}</div>
                      {inlineFeedback.detail ? (
                        <div className="mt-1 font-mono text-[11.5px]">{inlineFeedback.detail}</div>
                      ) : null}
                    </div>
                  ) : null}
                  <div className="flex flex-wrap items-center gap-2">
                    <Button type="submit" disabled={isBusy || !hasCallbackURL}>
                      {strings.actions.submitCallback}
                    </Button>
                    <Button
                      type="button"
                      variant="secondary"
                      disabled={!hasCallbackURL}
                      onClick={() => setShowRawCallbackURL((current) => !current)}
                    >
                      {showRawCallbackURL ? <EyeOff /> : <Eye />}
                      {showRawCallbackURL
                        ? strings.actions.hideRawCallback
                        : strings.actions.showRawCallback}
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      disabled={isBusy || !hasCallbackURL}
                      onClick={() => {
                        callbackForm.reset()
                        setInlineFeedback(null)
                      }}
                    >
                      <X />
                      {strings.actions.clearCallback}
                    </Button>
                  </div>
                </div>
              </Field>
            </form>
          </>
        ) : null}

        {terminalError && !activeBrowserFlow ? (
          <Field label={strings.panel.failed} hint={strings.terminal.restartHint}>
            <div
              data-testid="oauth-terminal-error"
              className="space-y-3 border border-[var(--err)] bg-[var(--err-soft)] px-4 py-3 text-[12.5px] leading-[1.6] text-[var(--err)]"
              style={{ borderRadius: 2 }}
            >
              <div className="font-medium">
                {terminalError.message || strings.terminal.defaultTitle}
              </div>
              <div className="font-mono text-[11.5px]">
                {terminalError.code || strings.terminal.defaultDetail}
              </div>
              <div>
                <Button
                  type="button"
                  variant="secondary"
                  disabled={isBusy}
                  onClick={() => {
                    resetToStart({
                      callbackForm,
                      queryClient,
                      setConflictFlow,
                      setInlineFeedback,
                      setScreenError,
                      setStartedFlow,
                      authorizeUrlRef,
                    })
                  }}
                >
                  <RotateCcw />
                  {strings.actions.startAgain}
                </Button>
              </div>
            </div>
          </Field>
        ) : null}
      </PanelCard>
    </Canvas>
  )
}

function isPendingBrowserFlow(flow: OAuthFlowActive | null): flow is BrowserPendingFlow {
  return !!flow && flow.status === 'pending' && flow.method === 'browser'
}

function FlowStatusStrip({ flow }: { flow: StartedBrowserFlow }) {
  return (
    <div
      data-testid="oauth-flow-status"
      className="grid gap-px overflow-hidden border border-[var(--line)] bg-[var(--line)] sm:grid-cols-3"
      style={{ borderRadius: 2 }}
    >
      <FlowStatusCell label={strings.status.pending} value={strings.badges.pending} />
      <FlowStatusCell
        label={strings.status.listener}
        value={flow.listener_bound ? strings.badges.loopbackReady : strings.badges.pasteOnly}
        valueTestId={flow.listener_bound ? undefined : 'oauth-paste-only-badge'}
        valueVariant={flow.listener_bound ? 'success' : 'warning'}
      />
      <FlowStatusCell label={strings.status.expires} value={formatFlowExpiry(flow.expires_at)} />
    </div>
  )
}

function FlowStatusCell({
  label,
  value,
  valueTestId,
  valueVariant = 'outline',
}: {
  label: string
  value: string
  valueTestId?: string
  valueVariant?: 'outline' | 'success' | 'warning'
}) {
  return (
    <div className="min-w-0 bg-[var(--panel-hi)] px-3 py-2">
      <div className="mb-1 text-[10.5px] font-medium uppercase tracking-[0.1em] text-[var(--text-muted)]">
        {label}
      </div>
      <Badge data-testid={valueTestId} variant={valueVariant}>
        {value}
      </Badge>
    </div>
  )
}

function CallbackURLSummary({ summary }: { summary: CallbackSummary | null }) {
  if (!summary) {
    return (
      <div
        data-testid="oauth-callback-summary"
        className="border border-[var(--line)] bg-[var(--panel-hi)] px-3 py-2 text-[11.5px] text-[var(--text-muted)]"
        style={{ borderRadius: 2 }}
      >
        {strings.callbackSummary.empty}
      </div>
    )
  }

  const codeBadgeVariant = summary.variant === 'warning' ? 'warning' : 'outline'
  const stateBadgeVariant =
    summary.stateLabel === strings.callbackSummary.statePresent ? 'outline' : 'warning'
  return (
    <div
      data-testid="oauth-callback-summary"
      className="flex min-w-0 flex-wrap items-center gap-2 border border-[var(--line)] bg-[var(--panel-hi)] px-3 py-2 text-[11.5px]"
      style={{ borderRadius: 2 }}
    >
      <span className="font-medium text-[var(--text-dim)]">{strings.callbackSummary.title}</span>
      <code className="min-w-0 max-w-full truncate text-[var(--text)]">{summary.target}</code>
      <Badge variant={codeBadgeVariant}>{summary.codeLabel}</Badge>
      <Badge variant={stateBadgeVariant}>{summary.stateLabel}</Badge>
    </div>
  )
}

function renderPanelMeta(
  activeBrowserFlow: StartedBrowserFlow | null,
  pendingConflict: PendingConflict | null,
  terminalError: { code: string; message: string } | null,
) {
  if (pendingConflict) {
    return <Badge variant="warning">{strings.badges.pending}</Badge>
  }
  if (terminalError) {
    return <Badge variant="danger">{strings.badges.failed}</Badge>
  }
  if (activeBrowserFlow) {
    return <Badge variant="outline">{strings.badges.pending}</Badge>
  }
  return strings.panel.ready
}

async function navigateToAccount(
  accountId: number,
  navigate: ReturnType<typeof useNavigate>,
  queryClient: ReturnType<typeof useQueryClient>,
  navigatedRef: MutableRefObject<boolean>,
) {
  if (navigatedRef.current) {
    return Promise.resolve()
  }
  navigatedRef.current = true
  await invalidateAdminAccountQueries(queryClient, accountId)
  queryClient.setQueryData<OAuthFlowSnapshot>(oauthFlowQueryKey, { status: 'idle' })
  await navigate({
    to: '/admin/accounts/$accountId',
    params: { accountId: String(accountId) },
  })
}

function openAuthorizeTab(authorizeUrl: string) {
  const popup = window.open(authorizeUrl, '_blank', 'noopener,noreferrer')
  if (popup === null) {
    toast.error(strings.toasts.popupBlocked)
  }
}

function resetToStart(args: {
  callbackForm: ReturnType<typeof useForm<CallbackForm>>
  queryClient: ReturnType<typeof useQueryClient>
  setStartedFlow: Dispatch<SetStateAction<StartedBrowserFlow | null>>
  setConflictFlow: Dispatch<SetStateAction<PendingConflict | null>>
  setInlineFeedback: Dispatch<SetStateAction<InlineFeedback | null>>
  setScreenError: Dispatch<SetStateAction<unknown>>
  authorizeUrlRef: MutableRefObject<string | null>
}) {
  args.callbackForm.reset()
  args.queryClient.setQueryData<OAuthFlowSnapshot>(oauthFlowQueryKey, { status: 'idle' })
  args.setStartedFlow(null)
  args.setConflictFlow(null)
  args.setInlineFeedback(null)
  args.setScreenError(null)
  args.authorizeUrlRef.current = null
}

function readPendingConflict(err: RouterApiError): PendingConflict | null {
  const data = asRecord(err.data)
  const flowId = typeof data.flow_id === 'string' ? data.flow_id : ''
  const expiresAt = typeof data.expires_at === 'string' ? data.expires_at : ''
  const createdAt = typeof data.created_at === 'string' ? data.created_at : ''
  const method = data.method === 'device' ? 'device' : data.method === 'browser' ? 'browser' : null
  if (!flowId || !expiresAt || !createdAt || !method) {
    return null
  }
  return {
    flow_id: flowId,
    expires_at: expiresAt,
    created_at: createdAt,
    method,
  }
}

function describeManualCallbackError(err: RouterApiError): InlineFeedback | null {
  const data = asRecord(err.data)
  switch (err.code) {
    case Err003OAuthStateMismatch:
      return { title: strings.err.oauth_state_mismatch }
    case Err003InvalidCallbackURL: {
      const reason = typeof data.reason === 'string' ? data.reason : ''
      return {
        title: strings.err.invalid_callback_url,
        detail:
          reason === 'url_prefix_mismatch'
            ? strings.callbackReason.url_prefix_mismatch
            : reason === 'missing_code_and_error'
              ? strings.callbackReason.missing_code_and_error
              : undefined,
      }
    }
    case Err003NoFlowInProgress:
      return { title: strings.err.no_flow_in_progress }
    case Err003OAuthInvalidGrant:
      return {
        title: strings.err.oauth_invalid_grant,
        detail: joinParts(
          typeof data.provider_error === 'string' ? data.provider_error : undefined,
          typeof data.provider_message === 'string' ? data.provider_message : undefined,
        ),
      }
    case Err003OAuthUpstreamError:
      return {
        title: strings.err.oauth_upstream_error,
        detail: joinParts(
          typeof data.provider_error === 'string' ? data.provider_error : undefined,
          typeof data.provider_message === 'string' ? data.provider_message : undefined,
          typeof data.http_status === 'number' ? `http_${data.http_status}` : undefined,
        ),
      }
    case Err003OAuthInternalError:
      return { title: strings.err.oauth_internal_error }
    case Err003OAuthStoreFailed:
      return { title: strings.err.oauth_store_failed }
    case PlatformUnknown:
      return { title: strings.err.transport_error }
    default:
      return null
  }
}

function asRecord(value: unknown): Record<string, unknown> {
  return typeof value === 'object' && value !== null ? (value as Record<string, unknown>) : {}
}

function joinParts(...parts: Array<string | undefined>) {
  const nonEmpty = parts.filter(
    (part): part is string => typeof part === 'string' && part.length > 0,
  )
  return nonEmpty.length > 0 ? nonEmpty.join(' — ') : undefined
}

function summarizeCallbackURL(raw: string): CallbackSummary | null {
  const trimmed = raw.trim()
  if (!trimmed) {
    return null
  }
  try {
    const parsed = new URL(trimmed)
    const hasCode = parsed.searchParams.has('code')
    const hasError = parsed.searchParams.has('error')
    const hasState = parsed.searchParams.has('state')
    return {
      target: `${parsed.host}${parsed.pathname}`,
      codeLabel: hasCode
        ? strings.callbackSummary.codePresent
        : hasError
          ? strings.callbackSummary.errorPresent
          : strings.callbackSummary.codeMissing,
      stateLabel: hasState
        ? strings.callbackSummary.statePresent
        : strings.callbackSummary.stateMissing,
      variant: hasCode || hasError ? 'neutral' : 'warning',
    }
  } catch {
    return {
      target: strings.callbackSummary.invalid,
      codeLabel: strings.callbackSummary.codeMissing,
      stateLabel: strings.callbackSummary.stateMissing,
      variant: 'warning',
    }
  }
}

function formatFlowExpiry(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  const year = date.getUTCFullYear()
  const month = String(date.getUTCMonth() + 1).padStart(2, '0')
  const day = String(date.getUTCDate()).padStart(2, '0')
  const hour = String(date.getUTCHours()).padStart(2, '0')
  const minute = String(date.getUTCMinutes()).padStart(2, '0')
  return `${year}-${month}-${day} ${hour}:${minute} UTC`
}

function formatFlowID(flowID: string): string {
  if (flowID.length <= 18) {
    return flowID
  }
  return `${flowID.slice(0, 9)}...${flowID.slice(-6)}`
}
