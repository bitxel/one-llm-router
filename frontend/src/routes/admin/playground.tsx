import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery } from '@tanstack/react-query'
import { BookOpen, CircleHelp, Copy, Play, RotateCcw, X } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { type SubmitHandler, useForm } from 'react-hook-form'
import { toast } from 'sonner'
import { z } from 'zod'

import { Canvas, Field, PageIntro, PanelCard, Stripe } from '@/components/neo'
import { ErrorBanner } from '@/components/shared/ErrorBanner'
import { buildJsonPreviewFromValue, JsonPreview } from '@/components/shared/JsonPreview'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import {
  type AccountListItem,
  type PlaygroundRunRequest,
  type PlaygroundRunResponseBody,
  type PlaygroundRunSuccessData,
  playgroundRun,
} from '@/generated/openapi'
import { api } from '@/lib/api-client'
import { copyToClipboard } from '@/lib/clipboard'
import { callAdmin, RouterApiError } from '@/lib/router-api'
import { adminAccountsListQueryKey } from './accounts/query-keys'
import { strings } from './playground.strings'

const PROMPT_LIMIT = 16_000
const MODEL_LIMIT = 128
const SESSION_KEY_LIMIT = 128
const DEFAULT_MAX_OUTPUT_TOKENS = 1024
type PlaygroundEndpoint = NonNullable<PlaygroundRunRequest['endpoint']>

const playgroundEndpoints: Array<{
  id: PlaygroundEndpoint
  path: '/v1/responses' | '/v1/chat/completions'
}> = [
  { id: 'responses', path: '/v1/responses' },
  { id: 'chat_completions', path: '/v1/chat/completions' },
]

const PlaygroundFormSchema = z
  .object({
    selection_mode: z.enum(['auto', 'account']),
    endpoint: z.enum(['responses', 'chat_completions']),
    account_id: z.number().int().positive(strings.validation.accountRequired).optional(),
    session_key: z
      .string()
      .trim()
      .refine(
        (value) => countUnicode(value) <= SESSION_KEY_LIMIT,
        strings.validation.sessionKeyMax,
      ),
    model: z
      .string()
      .trim()
      .min(1, strings.validation.modelRequired)
      .refine((value) => countUnicode(value) <= MODEL_LIMIT, strings.validation.modelMax),
    text: z
      .string()
      .trim()
      .min(1, strings.validation.textRequired)
      .refine((value) => countUnicode(value) <= PROMPT_LIMIT, strings.validation.textMax),
    max_output_tokens: z.coerce
      .number()
      .int()
      .min(1, strings.validation.maxOutputRange)
      .max(4096, strings.validation.maxOutputRange),
    include_raw_response: z.boolean(),
  })
  .superRefine((value, ctx) => {
    if (value.selection_mode === 'account' && !value.account_id) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: strings.validation.accountRequired,
        path: ['account_id'],
      })
    }
  })

type PlaygroundForm = z.infer<typeof PlaygroundFormSchema>
type IntegrationGuideLanguage = 'curl' | 'python' | 'javascript' | 'go'

interface AccountsListPayload {
  accounts: AccountListItem[]
  total: number
}

interface IntegrationGuideExample {
  language: IntegrationGuideLanguage
  label: string
  endpoint: PlaygroundEndpoint
  code: string
}

const integrationGuideLanguages: Array<{
  id: IntegrationGuideLanguage
  label: string
}> = [
  { id: 'curl', label: strings.guide.tabs.curl },
  { id: 'python', label: strings.guide.tabs.python },
  { id: 'javascript', label: strings.guide.tabs.javascript },
  { id: 'go', label: strings.guide.tabs.go },
]

export function AdminPlayground() {
  const [screenError, setScreenError] = useState<unknown>(null)
  const [lastResult, setLastResult] = useState<PlaygroundRunSuccessData | null>(null)
  const [guideOpen, setGuideOpen] = useState(false)
  const [guideLanguage, setGuideLanguage] = useState<IntegrationGuideLanguage>('curl')
  const [guideEndpoint, setGuideEndpoint] = useState<PlaygroundEndpoint>('responses')
  const form = useForm<PlaygroundForm>({
    resolver: zodResolver(PlaygroundFormSchema),
    defaultValues: {
      selection_mode: 'auto',
      endpoint: 'responses',
      account_id: undefined,
      session_key: '',
      model: strings.defaultModel,
      text: '',
      max_output_tokens: DEFAULT_MAX_OUTPUT_TOKENS,
      include_raw_response: false,
    },
    mode: 'onTouched',
  })

  const accountsQuery = useQuery({
    queryKey: adminAccountsListQueryKey,
    queryFn: () => api.get<AccountsListPayload>('/api/admin/accounts'),
    staleTime: 5_000,
  })

  const activeAccounts = useMemo(
    () => (accountsQuery.data?.accounts ?? []).filter((account) => account.status === 'active'),
    [accountsQuery.data?.accounts],
  )
  const selectionMode = form.watch('selection_mode')
  const endpoint = form.watch('endpoint')
  const selectedAccountID = form.watch('account_id')
  const prompt = form.watch('text')
  const includeRaw = form.watch('include_raw_response')
  const model = form.watch('model')
  const promptChars = countUnicode(prompt.trim())
  const noRunnableAccounts = accountsQuery.isSuccess && activeAccounts.length === 0
  const guideBaseURL = useMemo(() => currentOrigin(), [])
  const guideModel = model.trim() || strings.defaultModel
  const guideExamples = useMemo(
    () => buildIntegrationGuideExamples(guideBaseURL, guideModel, guideEndpoint),
    [guideEndpoint, guideBaseURL, guideModel],
  )

  useEffect(() => {
    if (selectionMode !== 'account') {
      form.setValue('account_id', undefined, { shouldDirty: true, shouldValidate: true })
      return
    }
    if (selectedAccountID && activeAccounts.some((account) => account.id === selectedAccountID)) {
      return
    }
    form.setValue('account_id', activeAccounts[0]?.id, {
      shouldDirty: true,
      shouldValidate: true,
    })
  }, [activeAccounts, form, selectedAccountID, selectionMode])

  const runMutation = useMutation<PlaygroundRunSuccessData, unknown, PlaygroundRunRequest>({
    mutationFn: (body) => callAdmin<PlaygroundRunResponseBody>(playgroundRun({ body })),
    onSuccess: (result) => {
      setLastResult(result)
      setScreenError(null)
      toast.success(strings.toasts.success)
    },
    onError: (error) => {
      setScreenError(error)
      setLastResult(null)
      toast.error(strings.toasts.failed)
    },
  })

  const submit: SubmitHandler<PlaygroundForm> = async (values) => {
    if (runMutation.isPending || noRunnableAccounts) {
      return
    }
    const body: PlaygroundRunRequest = {
      selection_mode: values.selection_mode,
      endpoint: values.endpoint,
      model: values.model.trim(),
      text: values.text.trim(),
      max_output_tokens: values.max_output_tokens,
      include_raw_response: values.include_raw_response,
    }
    const sessionKey = values.session_key.trim()
    if (values.selection_mode === 'auto' && sessionKey !== '') {
      body.session_key = sessionKey
    }
    if (values.selection_mode === 'account') {
      body.account_id = values.account_id
    }
    try {
      await runMutation.mutateAsync(body)
    } catch {
      // useMutation.onError owns the visible error state and toast.
    }
  }

  return (
    <Canvas variant="wide">
      <Stripe eyebrow={strings.eyebrow}>{strings.stripe}</Stripe>
      <div className="mb-8 flex flex-wrap items-end justify-between gap-4">
        <div className="min-w-0 flex-1">
          <h1 className="mb-3 max-w-[15ch] text-balance">
            {strings.titleLead} <strong>{strings.titleStrong}</strong>
          </h1>
          <PageIntro>{strings.intro}</PageIntro>
        </div>
        <div className="flex flex-wrap items-center justify-end gap-2">
          <Button
            type="button"
            variant="secondary"
            data-testid="playground-integration-guide-open"
            onClick={() => {
              setGuideEndpoint(endpoint)
              setGuideOpen(true)
            }}
          >
            <BookOpen />
            {strings.actions.integrationGuide}
          </Button>
          <Button
            type="button"
            variant="secondary"
            onClick={() => {
              setLastResult(null)
              setScreenError(null)
            }}
            disabled={runMutation.isPending || (!lastResult && !screenError)}
          >
            <RotateCcw />
            {strings.actions.reset}
          </Button>
        </div>
      </div>

      {accountsQuery.isError ? (
        <ErrorBanner
          error={accountsQuery.error}
          title={strings.errors.accountListUnavailable}
          className="mb-4"
        />
      ) : null}
      {screenError ? (
        <ErrorBanner error={screenError} title={strings.errorTitle} className="mb-4" />
      ) : null}

      <div
        className="grid items-start gap-4 xl:grid-cols-[minmax(0,0.96fr)_minmax(0,1.04fr)]"
        data-testid="playground-workspace"
      >
        <form
          className="min-w-0"
          onSubmit={form.handleSubmit(submit)}
          data-testid="playground-form"
        >
          <PanelCard title={strings.formTitle} meta={runMutation.isPending ? 'pending' : undefined}>
            <Field label={strings.labels.mode} hint={strings.hints.mode}>
              <fieldset className="grid grid-cols-2 gap-2">
                <legend className="sr-only">{strings.labels.mode}</legend>
                <Button
                  type="button"
                  variant={selectionMode === 'auto' ? 'default' : 'secondary'}
                  aria-pressed={selectionMode === 'auto'}
                  onClick={() =>
                    form.setValue('selection_mode', 'auto', {
                      shouldDirty: true,
                      shouldValidate: true,
                    })
                  }
                >
                  {strings.modes.auto}
                </Button>
                <Button
                  type="button"
                  variant={selectionMode === 'account' ? 'default' : 'secondary'}
                  aria-pressed={selectionMode === 'account'}
                  onClick={() =>
                    form.setValue('selection_mode', 'account', {
                      shouldDirty: true,
                      shouldValidate: true,
                    })
                  }
                  disabled={activeAccounts.length === 0}
                >
                  {strings.modes.account}
                </Button>
              </fieldset>
            </Field>

            {selectionMode === 'account' ? (
              <Field
                htmlFor="playground-account"
                label={strings.labels.account}
                hint={strings.hints.account}
              >
                <Select
                  value={selectedAccountID ? String(selectedAccountID) : ''}
                  onValueChange={(value) =>
                    form.setValue('account_id', Number(value), {
                      shouldDirty: true,
                      shouldValidate: true,
                    })
                  }
                  disabled={activeAccounts.length === 0 || runMutation.isPending}
                >
                  <SelectTrigger id="playground-account" data-testid="playground-account-select">
                    <SelectValue placeholder={strings.noActiveAccounts} />
                  </SelectTrigger>
                  <SelectContent>
                    {activeAccounts.map((account) => (
                      <SelectItem key={account.id} value={String(account.id)}>
                        {formatAccountOption(account)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {form.formState.errors.account_id ? (
                  <p className="mt-2 text-[11.5px] text-[var(--err)]">
                    {form.formState.errors.account_id.message}
                  </p>
                ) : null}
              </Field>
            ) : (
              <Field
                htmlFor="playground-session-key"
                label={
                  <FieldLabel
                    optional
                    optionalTestId="playground-session-key-optional"
                    text={strings.labels.sessionKey}
                  />
                }
                hint={strings.hints.sessionKey}
              >
                <Input
                  id="playground-session-key"
                  data-testid="playground-session-key-input"
                  autoComplete="off"
                  spellCheck={false}
                  aria-invalid={Boolean(form.formState.errors.session_key)}
                  disabled={runMutation.isPending}
                  {...form.register('session_key')}
                />
                {form.formState.errors.session_key ? (
                  <p className="mt-2 text-[11.5px] text-[var(--err)]">
                    {form.formState.errors.session_key.message}
                  </p>
                ) : null}
              </Field>
            )}

            <Field label={strings.labels.endpoint} hint={strings.hints.endpoint}>
              <fieldset className="grid grid-cols-2 gap-2">
                <legend className="sr-only">{strings.labels.endpoint}</legend>
                {playgroundEndpoints.map((item) => (
                  <Button
                    key={item.id}
                    type="button"
                    variant={endpoint === item.id ? 'default' : 'secondary'}
                    aria-pressed={endpoint === item.id}
                    data-testid={`playground-endpoint-${item.id}`}
                    onClick={() =>
                      form.setValue('endpoint', item.id, {
                        shouldDirty: true,
                        shouldValidate: true,
                      })
                    }
                    disabled={runMutation.isPending}
                  >
                    {strings.endpoints[item.id]}
                  </Button>
                ))}
              </fieldset>
            </Field>

            <Field
              htmlFor="playground-model"
              label={strings.labels.model}
              hint={strings.hints.model}
            >
              <Input
                id="playground-model"
                data-testid="playground-model-input"
                autoComplete="off"
                spellCheck={false}
                aria-invalid={Boolean(form.formState.errors.model)}
                disabled={runMutation.isPending}
                {...form.register('model')}
              />
              {form.formState.errors.model ? (
                <p className="mt-2 text-[11.5px] text-[var(--err)]">
                  {form.formState.errors.model.message}
                </p>
              ) : null}
            </Field>

            <Field
              htmlFor="playground-max-output"
              label={strings.labels.maxOutputTokens}
              hint={strings.hints.maxOutputTokens}
            >
              <Input
                id="playground-max-output"
                data-testid="playground-max-output-input"
                type="number"
                min={1}
                max={4096}
                step={1}
                aria-invalid={Boolean(form.formState.errors.max_output_tokens)}
                disabled={runMutation.isPending}
                {...form.register('max_output_tokens', { valueAsNumber: true })}
              />
              {form.formState.errors.max_output_tokens ? (
                <p className="mt-2 text-[11.5px] text-[var(--err)]">
                  {form.formState.errors.max_output_tokens.message}
                </p>
              ) : null}
            </Field>

            <Field
              label={
                <FieldLabel
                  optional
                  optionalTestId="playground-include-raw-optional"
                  text={strings.labels.includeRaw}
                />
              }
              hint={strings.hints.includeRaw}
            >
              <div className="flex min-h-8 items-center gap-3">
                <Switch
                  data-testid="playground-raw-switch"
                  checked={includeRaw}
                  onCheckedChange={(checked) =>
                    form.setValue('include_raw_response', checked, {
                      shouldDirty: true,
                      shouldValidate: true,
                    })
                  }
                  disabled={runMutation.isPending}
                />
                <Badge variant={includeRaw ? 'accent' : 'outline'}>
                  {includeRaw ? strings.status.enabled : strings.status.off}
                </Badge>
              </div>
            </Field>

            <Field
              htmlFor="playground-text"
              label={strings.labels.prompt}
              hint={strings.hints.prompt}
            >
              <textarea
                id="playground-text"
                data-testid="playground-textarea"
                className="min-h-[220px] w-full resize-y border bg-[var(--bg-2)] px-[10px] py-[8px] font-mono text-[12.5px] leading-[1.65] text-[var(--text)] outline-none transition-colors placeholder:text-[var(--text-muted)] hover:border-[var(--line-3)] focus:border-[var(--accent)] focus-visible:[box-shadow:var(--focus)] disabled:cursor-not-allowed disabled:bg-[var(--panel-2)] disabled:text-[var(--text-muted)]"
                style={{ borderColor: 'var(--line-2)', borderRadius: 2 }}
                aria-invalid={Boolean(form.formState.errors.text)}
                spellCheck={false}
                disabled={runMutation.isPending}
                {...form.register('text')}
              />
              <div className="mt-2 flex items-center justify-between gap-3 text-[11.5px]">
                {form.formState.errors.text ? (
                  <span className="text-[var(--err)]">{form.formState.errors.text.message}</span>
                ) : (
                  <span className="text-[var(--text-muted)]">{strings.emptyField}</span>
                )}
                <span
                  className={
                    promptChars > PROMPT_LIMIT
                      ? 'font-mono text-[var(--err)]'
                      : 'font-mono text-[var(--text-muted)]'
                  }
                >
                  {promptChars}/{PROMPT_LIMIT} {strings.charCounter}
                </span>
              </div>
            </Field>

            <div className="mt-1 flex flex-wrap items-center justify-between gap-3 border-t border-[var(--line)] pt-4">
              {noRunnableAccounts ? (
                <div
                  data-testid="playground-no-active"
                  className="flex min-h-8 flex-wrap items-center gap-x-3 gap-y-1 text-[12px] text-[var(--text-muted)]"
                >
                  <span>{strings.noActiveAccounts}</span>
                  <a
                    href="/admin/accounts/new"
                    data-testid="playground-new-account-link"
                    className="text-[var(--accent)] underline-offset-4 hover:underline"
                  >
                    {strings.actions.newAccount}
                  </a>
                </div>
              ) : (
                <span aria-hidden="true" />
              )}
              <Button
                type="submit"
                disabled={runMutation.isPending || noRunnableAccounts}
                data-testid="playground-submit"
              >
                <Play />
                {runMutation.isPending ? strings.actions.running : strings.actions.run}
              </Button>
            </div>
          </PanelCard>
        </form>

        <PanelCard
          title={strings.resultTitle}
          className="min-w-0"
          meta={
            runMutation.isPending
              ? 'pending'
              : screenError
                ? strings.resultMetaError
                : lastResult
                  ? strings.resultMetaDone
                  : strings.resultMetaIdle
          }
        >
          {runMutation.isPending ? (
            <div
              role="status"
              aria-live="polite"
              data-testid="playground-pending"
              className="text-[13px] text-[var(--text-dim)]"
            >
              {strings.actions.running}
            </div>
          ) : null}
          {!runMutation.isPending && !lastResult ? (
            <div
              data-testid="playground-result-empty"
              className="text-[13px] text-[var(--text-dim)]"
            >
              {strings.emptyResult}
            </div>
          ) : null}
          {lastResult ? <PlaygroundResult result={lastResult} /> : null}
          {screenError instanceof RouterApiError ? (
            <PlaygroundErrorDetail error={screenError} />
          ) : null}
        </PanelCard>
      </div>

      {guideOpen ? (
        <IntegrationGuideLayer
          baseURL={guideBaseURL}
          examples={guideExamples}
          selectedEndpoint={guideEndpoint}
          onSelectEndpoint={setGuideEndpoint}
          selectedLanguage={guideLanguage}
          onSelectLanguage={setGuideLanguage}
          onClose={() => setGuideOpen(false)}
        />
      ) : null}
    </Canvas>
  )
}

function FieldLabel({
  text,
  optional = false,
  optionalTestId,
}: {
  text: string
  optional?: boolean
  optionalTestId?: string
}) {
  if (!optional) {
    return text
  }
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5">
      <span>{text}</span>
      <span
        aria-label={`${strings.labels.optionalField}: ${text}`}
        className="inline-flex shrink-0 items-center text-[var(--text-muted)]"
        data-testid={optionalTestId}
        role="img"
        title={strings.labels.optionalField}
      >
        <CircleHelp aria-hidden="true" className="h-3 w-3" strokeWidth={1.8} />
      </span>
    </span>
  )
}

function IntegrationGuideLayer({
  baseURL,
  examples,
  selectedEndpoint,
  onSelectEndpoint,
  selectedLanguage,
  onSelectLanguage,
  onClose,
}: {
  baseURL: string
  examples: Record<IntegrationGuideLanguage, IntegrationGuideExample>
  selectedEndpoint: PlaygroundEndpoint
  onSelectEndpoint: (endpoint: PlaygroundEndpoint) => void
  selectedLanguage: IntegrationGuideLanguage
  onSelectLanguage: (language: IntegrationGuideLanguage) => void
  onClose: () => void
}) {
  useEffect(() => {
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') {
        onClose()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => {
      document.body.style.overflow = previousOverflow
      window.removeEventListener('keydown', handleKeyDown)
    }
  }, [onClose])

  const selected = examples[selectedLanguage]
  const endpoint = `${baseURL}${endpointPathFor(selected.endpoint)}`

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center px-3 py-4 sm:px-6"
      data-testid="playground-integration-guide"
    >
      <button
        type="button"
        tabIndex={-1}
        className="absolute inset-0 cursor-pointer bg-[color-mix(in_oklch,var(--bg)_62%,transparent)]"
        aria-label={strings.actions.closeGuideBackdrop}
        onClick={onClose}
      />
      <section
        role="dialog"
        aria-modal="true"
        aria-labelledby="playground-integration-guide-title"
        className="relative z-10 flex h-[min(760px,calc(100dvh-2rem))] w-[min(980px,calc(100vw-1.5rem))] flex-col overflow-hidden border border-[var(--line)] bg-[var(--panel)] shadow-[0_24px_80px_rgba(0,0,0,0.28)] sm:w-[min(980px,calc(100vw-3rem))]"
        style={{ borderRadius: 2 }}
      >
        <header className="flex min-h-12 items-center justify-between gap-4 border-b border-[var(--line)] bg-[var(--panel-head)] px-4 py-3">
          <div className="min-w-0">
            <h2
              id="playground-integration-guide-title"
              className="text-[11.5px] font-medium uppercase tracking-[0.1em] text-[var(--text-dim)]"
            >
              {strings.guide.title}
            </h2>
            <div className="mt-1 truncate font-mono text-[12px] text-[var(--text-muted)]">
              {endpoint}
            </div>
          </div>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            aria-label={strings.actions.closeGuide}
            className="border-0 shadow-none"
            onClick={onClose}
          >
            <X />
          </Button>
        </header>

        <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4 sm:px-5">
          <div className="grid gap-3 md:grid-cols-[minmax(220px,0.8fr)_minmax(0,1.2fr)_minmax(180px,0.7fr)]">
            <div
              className="border border-[var(--line)] bg-[var(--panel-hi)] px-3 py-2"
              style={{ borderRadius: 2 }}
            >
              <label
                htmlFor="playground-integration-guide-api"
                className="mb-1 block text-[10.5px] font-medium uppercase tracking-[0.1em] text-[var(--text-muted)]"
              >
                {strings.guide.apiLabel}
              </label>
              <Select
                value={selectedEndpoint}
                onValueChange={(value) => onSelectEndpoint(value as PlaygroundEndpoint)}
              >
                <SelectTrigger
                  id="playground-integration-guide-api"
                  data-testid="playground-integration-guide-api-select"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {playgroundEndpoints.map((item) => (
                    <SelectItem key={item.id} value={item.id}>
                      {strings.endpoints[item.id]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <ResultCell label={strings.guide.endpointLabel}>{endpoint}</ResultCell>
            <ResultCell label={strings.guide.apiKeyLabel}>{strings.guide.apiKeyValue}</ResultCell>
          </div>
          <div
            role="tablist"
            aria-label={strings.guide.title}
            className="mt-5 flex flex-wrap gap-2 border-b border-[var(--line)] pb-3"
          >
            {integrationGuideLanguages.map((language) => (
              <Button
                key={language.id}
                type="button"
                role="tab"
                variant={selectedLanguage === language.id ? 'default' : 'secondary'}
                aria-selected={selectedLanguage === language.id}
                aria-controls="playground-integration-guide-panel"
                id={`playground-integration-guide-tab-${language.id}`}
                onClick={() => onSelectLanguage(language.id)}
              >
                {language.label}
              </Button>
            ))}
          </div>

          <div
            id="playground-integration-guide-panel"
            role="tabpanel"
            aria-labelledby={`playground-integration-guide-tab-${selected.language}`}
            className="mt-4"
          >
            <div className="mb-2 flex items-center justify-between gap-3">
              <div className="font-mono text-[11.5px] uppercase tracking-[0.1em] text-[var(--text-dim)]">
                {selected.label}
              </div>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => copyText(selected.code)}
              >
                <Copy />
                {strings.guide.copy}
              </Button>
            </div>
            <pre
              data-testid="playground-integration-guide-code"
              className="max-h-[420px] overflow-auto border border-[var(--line)] bg-[var(--bg-2)] p-4 font-mono text-[12px] leading-[1.7] whitespace-pre-wrap text-[var(--text)]"
              style={{ borderRadius: 2 }}
            >
              {selected.code}
            </pre>
          </div>
        </div>
      </section>
    </div>
  )
}

function PlaygroundResult({ result }: { result: PlaygroundRunSuccessData }) {
  return (
    <div data-testid="playground-result" role="status" aria-live="polite" className="space-y-4">
      <div
        className="grid gap-px overflow-hidden border border-[var(--line)] bg-[var(--line)] sm:grid-cols-2 2xl:grid-cols-3"
        style={{ borderRadius: 2 }}
      >
        <SummaryCell label={strings.labels.selectedAccount}>
          {result.account.name}{' '}
          <span className="text-[var(--text-muted)]">#{result.account.id}</span>
        </SummaryCell>
        <SummaryCell label={strings.labels.mode}>
          {formatSelectionMode(result.run.selection_mode)}
        </SummaryCell>
        <SummaryCell label={strings.labels.endpoint}>
          {formatEndpoint(result.run.endpoint)}
        </SummaryCell>
        <SummaryCell label={strings.labels.outcome}>{result.run.outcome}</SummaryCell>
        <SummaryCell label={strings.labels.latency}>{result.run.latency_ms} ms</SummaryCell>
      </div>

      <div className="grid gap-3 2xl:grid-cols-[minmax(0,1fr)_260px]">
        <div>
          <div className="mb-2 text-[11.5px] font-medium uppercase tracking-[0.1em] text-[var(--text-dim)]">
            {strings.labels.output}
          </div>
          <pre
            className="min-h-[140px] overflow-auto border border-[var(--line)] bg-[var(--bg-2)] p-3 text-[12.5px] leading-[1.65] whitespace-pre-wrap text-[var(--text)]"
            style={{ borderRadius: 2 }}
          >
            {result.output.text_available ? result.output.text : strings.emptyOutput}
          </pre>
        </div>
        <div className="space-y-3">
          <ResultCell label={strings.labels.upstreamStatus}>
            {result.upstream.status_code ?? strings.emptyField}
          </ResultCell>
          <ResultCell label={strings.labels.responseMode}>
            {result.upstream.response_mode}
          </ResultCell>
          <ResultCell label={strings.labels.usage}>{formatUsage(result.usage)}</ResultCell>
          <ResultCell label={strings.labels.authMethod}>
            {formatAuthMethod(result.account.auth_method)}
          </ResultCell>
        </div>
      </div>

      {result.output.raw_response_available && result.output.raw_response ? (
        <div className="space-y-2">
          <div className="text-[11.5px] font-medium uppercase tracking-[0.1em] text-[var(--text-dim)]">
            {strings.labels.rawResponse}
          </div>
          <JsonPreview
            preview={buildJsonPreviewFromValue(result.output.raw_response)}
            className="overflow-hidden border border-[var(--line)] bg-[var(--bg-2)]"
            bodyClassName="max-h-[360px]"
            copyLabel={strings.actions.copyRawResponse}
            onCopy={copyText}
          />
        </div>
      ) : null}
    </div>
  )
}

function SummaryCell({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="min-w-0 bg-[var(--panel-hi)] px-3 py-2">
      <div className="mb-1 text-[10.5px] font-medium uppercase tracking-[0.1em] text-[var(--text-muted)]">
        {label}
      </div>
      <div className="truncate font-mono text-[12px] text-[var(--text)]">{children}</div>
    </div>
  )
}

function PlaygroundErrorDetail({ error }: { error: RouterApiError }) {
  if (typeof error.data !== 'object' || error.data === null) {
    return null
  }
  const data = error.data as Record<string, unknown>
  const rows = Object.entries(data).filter(([, value]) => value !== undefined && value !== null)
  if (rows.length === 0) {
    return null
  }
  const accountID = accountIDFromErrorData(data)
  return (
    <div
      className="mt-4 border border-[var(--line)] bg-[var(--panel-hi)] p-3"
      style={{ borderRadius: 2 }}
    >
      <div className="mb-2 text-[11.5px] font-medium uppercase tracking-[0.1em] text-[var(--text-dim)]">
        {strings.labels.diagnostics}
      </div>
      <div className="grid gap-2 md:grid-cols-2">
        {rows.map(([key, value]) => (
          <ResultCell key={key} label={key}>
            {formatDiagnostic(value)}
          </ResultCell>
        ))}
      </div>
      {accountID ? (
        <a
          href={`/admin/accounts/${accountID}`}
          className="mt-3 inline-block font-mono text-[11.5px] text-[var(--accent)] underline-offset-4 hover:underline"
        >
          {strings.actions.openAccount} #{accountID}
        </a>
      ) : null}
    </div>
  )
}

function ResultCell({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div
      className="border border-[var(--line)] bg-[var(--panel-hi)] px-3 py-2"
      style={{ borderRadius: 2 }}
    >
      <div className="mb-1 text-[10.5px] font-medium uppercase tracking-[0.1em] text-[var(--text-muted)]">
        {label}
      </div>
      <div className="break-words font-mono text-[12px] text-[var(--text)]">{children}</div>
    </div>
  )
}

function formatUsage(usage: Record<string, number | undefined>): string {
  const entries = Object.entries(usage).filter(([, value]) => typeof value === 'number')
  if (entries.length === 0) {
    return strings.emptyField
  }
  return entries.map(([key, value]) => `${key}:${value}`).join(' ')
}

function formatSelectionMode(mode: PlaygroundRunSuccessData['run']['selection_mode']): string {
  return strings.modes[mode] ?? mode
}

function formatEndpoint(endpoint: PlaygroundEndpoint): string {
  return strings.endpoints[endpoint] ?? endpoint
}

function formatAccountOption(account: AccountListItem): string {
  return [
    `#${account.id}`,
    account.name,
    account.provider,
    formatAccountStatus(account.status),
    formatAuthMethod(account.auth_method),
    account.email ?? null,
    planLabelForAccount(account),
  ]
    .filter((value): value is string => typeof value === 'string' && value.trim() !== '')
    .join(' · ')
}

function planLabelForAccount(
  account: Pick<AccountListItem, 'plan_type' | 'plan_type_label'>,
): string | null {
  return account.plan_type_label ?? account.plan_type ?? null
}

function formatAccountStatus(status: AccountListItem['status']): string {
  return strings.accountStatus[status] ?? status
}

function formatAuthMethod(authMethod: AccountListItem['auth_method']): string {
  return strings.authMethod[authMethod] ?? authMethod
}

function formatDiagnostic(value: unknown): string {
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
    return redactDiagnosticString(String(value))
  }
  return redactDiagnosticString(JSON.stringify(value))
}

function redactDiagnosticString(value: string): string {
  const lower = value.toLowerCase()
  if (
    lower.includes('bearer ') ||
    lower.includes('sk-') ||
    lower.includes('sk_') ||
    lower.includes('eyj')
  ) {
    return '[redacted]'
  }
  return value
}

function accountIDFromErrorData(data: Record<string, unknown>): number | null {
  const raw = data.account_id ?? data.requested_account_id
  return typeof raw === 'number' && Number.isInteger(raw) && raw > 0 ? raw : null
}

function currentOrigin(): string {
  if (typeof window === 'undefined') {
    return 'http://localhost:8080'
  }
  return window.location.origin.replace(/\/+$/, '')
}

function buildIntegrationGuideExamples(
  baseURL: string,
  model: string,
  endpoint: PlaygroundEndpoint,
): Record<IntegrationGuideLanguage, IntegrationGuideExample> {
  const prompt = 'Write a one-line router health check.'
  const path = endpointPathFor(endpoint)
  const payload = payloadForEndpoint(endpoint, model, prompt)
  const compactPayloadJSON = JSON.stringify(payload)
  const payloadJSON = JSON.stringify(payload, null, 2)
  return {
    curl: {
      language: 'curl',
      label: strings.guide.tabs.curl,
      endpoint,
      code: `curl -sS ${JSON.stringify(`${baseURL}${path}`)} -H "Content-Type: application/json" -H "Authorization: Bearer " -d '${compactPayloadJSON}'`,
    },
    python: {
      language: 'python',
      label: strings.guide.tabs.python,
      endpoint,
      code: `import requests

router_base_url = ${JSON.stringify(baseURL)}
router_api_key = ""

response = requests.post(
    f"{router_base_url}${path}",
    headers={
        "Content-Type": "application/json",
        "Authorization": f"Bearer {router_api_key}",
    },
    json=${pythonLiteral(payload, 4)},
    timeout=60,
)
print(response.text)`,
    },
    javascript: {
      language: 'javascript',
      label: strings.guide.tabs.javascript,
      endpoint,
      code: `const routerBaseUrl = ${JSON.stringify(baseURL)};
const routerApiKey = "";

const response = await fetch(\`\${routerBaseUrl}${path}\`, {
  method: "POST",
  credentials: "omit",
  headers: {
    "Content-Type": "application/json",
    Authorization: \`Bearer \${routerApiKey}\`,
  },
  body: JSON.stringify(${payloadJSON.replaceAll('\n', '\n  ')}),
});

console.log(await response.text());`,
    },
    go: {
      language: 'go',
      label: strings.guide.tabs.go,
      endpoint,
      code: `package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
)

func main() {
	body := []byte(\`${payloadJSON}\`)
	req, err := http.NewRequest(http.MethodPost, ${JSON.stringify(`${baseURL}${path}`)}, bytes.NewReader(body))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer ")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(data))
}`,
    },
  }
}

function endpointPathFor(endpoint: PlaygroundEndpoint): '/v1/responses' | '/v1/chat/completions' {
  return playgroundEndpoints.find((item) => item.id === endpoint)?.path ?? '/v1/responses'
}

function payloadForEndpoint(
  endpoint: PlaygroundEndpoint,
  model: string,
  prompt: string,
): Record<string, unknown> {
  if (endpoint === 'chat_completions') {
    return {
      model,
      messages: [{ role: 'user', content: prompt }],
      stream: false,
    }
  }
  return {
    model,
    input: prompt,
    stream: false,
  }
}

function pythonLiteral(value: unknown, indent: number): string {
  const spaces = ' '.repeat(indent)
  const innerSpaces = ' '.repeat(indent + 4)
  if (Array.isArray(value)) {
    if (value.length === 0) {
      return '[]'
    }
    return `[\n${value.map((item) => `${innerSpaces}${pythonLiteral(item, indent + 4)},`).join('\n')}\n${spaces}]`
  }
  if (value && typeof value === 'object') {
    const entries = Object.entries(value).map(
      ([key, raw]) => `${innerSpaces}${JSON.stringify(key)}: ${pythonLiteral(raw, indent + 4)},`,
    )
    return `{\n${entries.join('\n')}\n${spaces}}`
  }
  if (typeof value === 'string') {
    return JSON.stringify(value)
  }
  if (typeof value === 'boolean') {
    return value ? 'True' : 'False'
  }
  if (typeof value === 'number') {
    return String(value)
  }
  return 'None'
}

async function copyText(value: string) {
  try {
    await copyToClipboard(value)
    toast.success(strings.toasts.copyDone)
  } catch {
    toast.error(strings.toasts.copyFailed)
  }
}

function countUnicode(value: string): number {
  return Array.from(value).length
}
