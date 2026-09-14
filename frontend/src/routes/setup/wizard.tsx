import { zodResolver } from '@hookform/resolvers/zod'
import { useNavigate } from '@tanstack/react-router'
import { ArrowLeft, ArrowRight, CheckCircle2, SkipForward, Zap } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { type SubmitHandler, useForm } from 'react-hook-form'
import { Trans, useTranslation } from 'react-i18next'
import { z } from 'zod'

import {
  Canvas,
  EngineCard,
  EnvLine,
  Field,
  Kbd,
  MiniCard,
  PanelCard,
  ProbeLogKw,
  ProbeLogOk,
  ProbeRow,
  Rail,
  RailSection,
  RailSteps,
  ShortcutList,
  Stripe,
} from '@/components/neo'
import { ErrorBanner } from '@/components/shared/ErrorBanner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { ACCOUNT_AUTH_METHODS, type AccountAuthMethod } from '@/lib/account-auth-methods'
import { api, RouterApiError } from '@/lib/api-client'
import { tDynamic } from '@/lib/error-message'

// Step labels are `setup` catalog keys. Their English renderings double
// as the e2e test's `role="heading"` anchors — see
// `frontend/tests/e2e/wizard-happy-path.spec.ts` (Playwright pins
// `locale: 'en-US'`). Keep the English strings stable.
const STEPS = [
  { idx: '01', labelKey: 'steps.welcome' },
  { idx: '02', labelKey: 'steps.database' },
  { idx: '03', labelKey: 'steps.upstreamAccount' },
  { idx: '04', labelKey: 'steps.pluginIntents' },
  { idx: '05', labelKey: 'steps.commit' },
] as const

type Driver = 'sqlite3' | 'postgres' | 'mysql'
type SetupAuthMethod = AccountAuthMethod

// Validation caps MUST match `internal/setup/validator.go` so the
// wizard and the commit handler agree on what they accept:
//   - DSN length: DSNMaxLen = 4096
//   - Account name: ValidAccountName (1-64 chars after trim, no control chars)
//   - Base URL length: BaseURLMaxLen = 256
// Custom messages are `setup` catalog keys rendered through tDynamic().
const WizardSchema = z.object({
  db: z.object({
    driver: z.enum(['sqlite3', 'postgres', 'mysql']) as z.ZodType<Driver>,
    url: z.string().trim().min(1, 'setup:validation.dsnRequired').max(4096),
  }),
  account: z.object({
    name: z
      .string()
      .trim()
      .min(1, 'setup:validation.nameRequired')
      .max(64, 'setup:validation.nameMax'),
    provider: z.literal('openai'),
    api_key: z.string().trim().min(1, 'setup:validation.apiKeyRequired'),
    base_url: z.string().trim().max(256).optional().or(z.literal('')),
  }),
  plugins: z.object({
    admin_auth: z.boolean(),
    client_keys: z.boolean(),
  }),
})
type WizardForm = z.infer<typeof WizardSchema>

const DEFAULTS: WizardForm = {
  db: { driver: 'sqlite3', url: 'router.db' },
  account: { name: 'primary', provider: 'openai', api_key: '', base_url: '' },
  plugins: { admin_auth: false, client_keys: false },
}

// Per-method setup copy. Title/badge live in the `common` catalog
// (shared with the admin accounts picker, resolved via tCommon); the
// long detail sentence is setup-specific. Keys stay literal so both
// t() forms stay type-checked.
const AUTH_METHOD_COPY: Record<
  SetupAuthMethod,
  {
    titleKey: `authMethods.${SetupAuthMethod}.title`
    badgeKey: `authMethods.${SetupAuthMethod}.badge`
    detailKey: `account.methodDetails.${SetupAuthMethod}`
  }
> = {
  api_key: {
    titleKey: 'authMethods.api_key.title',
    badgeKey: 'authMethods.api_key.badge',
    detailKey: 'account.methodDetails.api_key',
  },
  oauth_browser: {
    titleKey: 'authMethods.oauth_browser.title',
    badgeKey: 'authMethods.oauth_browser.badge',
    detailKey: 'account.methodDetails.oauth_browser',
  },
  oauth_device: {
    titleKey: 'authMethods.oauth_device.title',
    badgeKey: 'authMethods.oauth_device.badge',
    detailKey: 'account.methodDetails.oauth_device',
  },
  oauth_import: {
    titleKey: 'authMethods.oauth_import.title',
    badgeKey: 'authMethods.oauth_import.badge',
    detailKey: 'account.methodDetails.oauth_import',
  },
}

interface ProbeResult {
  ok: boolean
  latency_ms?: number
  server_version?: string
  hint?: string
}

interface CommitResult {
  redirect?: string
  account_id?: number
  config_version?: number
}

const ENGINE_COPY: Record<
  Driver,
  {
    numKey: string
    titleKey: string
    version: string
    pitchKey: string
    features: readonly string[]
  }
> = {
  sqlite3: {
    numKey: 'engines.sqlite3.num',
    titleKey: 'engines.sqlite3.title',
    version: '3.40+',
    pitchKey: 'engines.sqlite3.pitch',
    features: ['embedded', 'pure-go', 'zero-ops'],
  },
  postgres: {
    numKey: 'engines.postgres.num',
    titleKey: 'engines.postgres.title',
    version: '14+',
    pitchKey: 'engines.postgres.pitch',
    features: ['lib/pq', 'TLS', 'replica-ready'],
  },
  mysql: {
    numKey: 'engines.mysql.num',
    titleKey: 'engines.mysql.title',
    version: '8.0+',
    pitchKey: 'engines.mysql.pitch',
    features: ['go-sql-driver', 'TLS', 'µs'],
  },
}

export function SetupWizard() {
  const { t } = useTranslation('setup')
  const navigate = useNavigate()
  const [step, setStep] = useState(0)
  const [complete, setComplete] = useState<Set<number>>(() => new Set())
  const [authMethod, setAuthMethod] = useState<SetupAuthMethod>('api_key')

  const form = useForm<WizardForm>({
    resolver: zodResolver(WizardSchema),
    defaultValues: DEFAULTS,
    mode: 'onTouched',
  })

  const [probe, setProbe] = useState<ProbeResult | null>(null)
  const [probeError, setProbeError] = useState<unknown>(null)
  const [probing, setProbing] = useState(false)
  const [commitError, setCommitError] = useState<unknown>(null)
  const [committing, setCommitting] = useState(false)
  // US-1 AC-2 relaxed 2026-04-15: operators may defer upstream-account
  // seeding to the admin portal. When skipped, we omit `first_account`
  // from the commit payload and the review step renders a soft warning
  // that /v1/* will 503 until at least one active account exists.
  const [accountSkipped, setAccountSkipped] = useState(false)
  const usesInlineAccountSeed = !accountSkipped && authMethod === 'api_key'

  // Invalidate the probe result whenever the operator edits the DSN or
  // switches driver. A stale "Reachable" badge against a just-changed
  // DSN would be misleading, and advancing on it would skip validation.
  const watchedDriver = form.watch('db.driver')
  const watchedUrl = form.watch('db.url')
  // biome-ignore lint/correctness/useExhaustiveDependencies: watchedDriver/watchedUrl are the trigger — setState is stable so biome sees the deps as unused, but we specifically want the effect to fire on their change.
  useEffect(() => {
    setProbe(null)
    setProbeError(null)
  }, [watchedDriver, watchedUrl])

  // Ephemeral — never persist DSN or API key.
  useEffect(() => () => form.reset(DEFAULTS), [form])

  const advance = (idx: number) =>
    setComplete((prev) => {
      const next = new Set(prev)
      next.add(idx)
      return next
    })

  async function probeDsn(): Promise<boolean> {
    setProbing(true)
    setProbeError(null)
    try {
      const db = form.getValues('db')
      const res = await api.post<ProbeResult>('/api/setup/probe-dsn', { db })
      setProbe(res)
      return res.ok === true
    } catch (e) {
      if (e instanceof RouterApiError && typeof e.data === 'object' && e.data) {
        setProbe({ ok: false, ...(e.data as Partial<ProbeResult>) })
      }
      setProbeError(e)
      return false
    } finally {
      setProbing(false)
    }
  }

  const commit: SubmitHandler<WizardForm> = async (payload) => {
    setCommitting(true)
    setCommitError(null)
    try {
      const body: Record<string, unknown> = {
        db: payload.db,
        plugins: {
          admin_auth: { enabled: payload.plugins.admin_auth },
          client_keys: { enabled: payload.plugins.client_keys },
        },
      }
      if (usesInlineAccountSeed) {
        body.first_account = {
          name: payload.account.name,
          provider: payload.account.provider,
          api_key: payload.account.api_key,
          base_url: payload.account.base_url || undefined,
        }
      }
      const result = await api.post<CommitResult>('/api/setup/commit', body)
      advance(4)
      if (accountSkipped || authMethod === 'api_key') {
        navigate({ to: result.redirect ?? '/admin' })
        return
      }
      if (authMethod === 'oauth_browser') {
        navigate({ to: '/admin/accounts/new-oauth', search: { from: 'setup' } })
        return
      }
      if (authMethod === 'oauth_device') {
        navigate({ to: '/admin/accounts/new-oauth-device', search: { from: 'setup' } })
        return
      }
      navigate({ to: '/admin/accounts/new-import', search: { from: 'setup' } })
    } catch (e) {
      setCommitError(e)
    } finally {
      setCommitting(false)
    }
  }

  const currentIsValid = useMemo(() => {
    const errors = form.formState.errors
    if (step === 1) return !errors.db?.driver && !errors.db?.url
    if (step === 2) {
      if (!usesInlineAccountSeed) return true
      return !errors.account?.name && !errors.account?.provider && !errors.account?.api_key
    }
    return true
  }, [form.formState.errors, step, usesInlineAccountSeed])

  const goNext = async () => {
    if (step === 1) {
      const ok = await form.trigger('db')
      if (!ok) return
      // SQLite is a local file — `migrate up` on commit is the real
      // acceptance test, so we skip the two-phase probe and advance
      // straight through (spec-002 §FR-014, relaxed 2026-04-15).
      const driver = form.getValues('db.driver')
      if (driver !== 'sqlite3' && !probe?.ok) {
        // Postgres / MySQL: first click runs the probe and keeps the
        // operator on the Database step so the latency badge is visible
        // (plan.md "DSN + probe-dsn call with latency badge"). A second
        // click — now that a fresh successful probe is on screen —
        // advances.
        await probeDsn()
        return
      }
    }
    if (step === 2 && usesInlineAccountSeed) {
      const ok = await form.trigger('account')
      if (!ok) return
    }
    advance(step)
    setStep((s) => Math.min(s + 1, STEPS.length - 1))
  }

  // US-1 AC-2 relaxed 2026-04-15 — Skip for now bypasses the account
  // form, records the intent, and advances to Plugins. Operator still
  // sees a banner on /admin until they seed one via Admin → Accounts.
  const skipAccount = () => {
    setAccountSkipped(true)
    // Clear any user input so we do not accidentally serialize a
    // half-typed key on commit.
    form.setValue('account.name', '', { shouldValidate: false, shouldDirty: false })
    form.setValue('account.api_key', '', { shouldValidate: false, shouldDirty: false })
    form.setValue('account.base_url', '', { shouldValidate: false, shouldDirty: false })
    advance(step)
    setStep((s) => Math.min(s + 1, STEPS.length - 1))
  }

  const selectAuthMethod = (nextMethod: SetupAuthMethod) => {
    setAccountSkipped(false)
    setAuthMethod(nextMethod)
  }
  const goBack = () => setStep((s) => Math.max(0, s - 1))

  // ⌘↵ / Ctrl↵ = Continue (v9 §.kbd contract). We intentionally scope
  // the dep list to reactive state the handler inspects — goNext /
  // goBack / commit / form.handleSubmit are closure-captured
  // freshly-bound per render, and re-binding the listener on every
  // render would spam addEventListener.
  // biome-ignore lint/correctness/useExhaustiveDependencies: see comment above — goNext/goBack/commit/probeDsn deliberately excluded; `step` + `watchedDriver` are the meaningful triggers.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key === 'Enter' && !committing && !probing) {
        e.preventDefault()
        if (step === STEPS.length - 1) {
          if (!usesInlineAccountSeed) {
            void commit(form.getValues())
          } else {
            form.handleSubmit(commit)()
          }
        } else if (currentIsValid) {
          void goNext()
        }
      }
      if ((e.metaKey || e.ctrlKey) && e.key === '[') {
        e.preventDefault()
        goBack()
      }
      // ⌘T / Ctrl+T — Test DB. Only fires on the Database step for
      // server-based drivers (sqlite3 has no Test button); matches the
      // rail's ShortcutList entry. Swallowing the browser "open new
      // tab" default is deliberate here because the wizard already
      // owns this page while setup is pending.
      if (
        (e.metaKey || e.ctrlKey) &&
        (e.key === 't' || e.key === 'T') &&
        step === 1 &&
        watchedDriver !== 'sqlite3' &&
        !probing
      ) {
        e.preventDefault()
        void probeDsn()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [step, currentIsValid, probing, committing, probe?.ok, usesInlineAccountSeed, watchedDriver])

  const active = STEPS[step]

  return (
    <Canvas variant="rail">
      <main className="min-w-0">
        <Stripe eyebrow={t('progress', { idx: active.idx })} titleSide="right">
          {t(active.labelKey)}
        </Stripe>
        {step === 0 ? <WelcomeStep /> : null}
        {step === 1 ? (
          <DatabaseStep
            form={form}
            probe={probe}
            probing={probing}
            probeError={probeError}
            onProbe={() => void probeDsn()}
          />
        ) : null}
        {step === 2 ? (
          <AccountStep
            form={form}
            authMethod={authMethod}
            skipped={accountSkipped}
            onSelectAuthMethod={selectAuthMethod}
            onUndoSkip={() => setAccountSkipped(false)}
          />
        ) : null}
        {step === 3 ? <PluginsStep form={form} /> : null}
        {step === 4 ? (
          <ReviewStep
            form={form}
            authMethod={authMethod}
            commitError={commitError}
            accountSkipped={accountSkipped}
          />
        ) : null}

        {/* Action bar — back · meta · continue */}
        <div className="mt-4 flex flex-col gap-3 border-t border-[var(--line)] pt-5 sm:flex-row sm:flex-wrap sm:items-center sm:justify-between">
          <Button
            variant="ghost"
            onClick={goBack}
            disabled={step === 0}
            data-testid="wizard-back"
            className="w-full sm:w-auto"
          >
            <ArrowLeft />
            {step === 0
              ? t('actions.back')
              : t('actions.backTo', { step: t(STEPS[step - 1].labelKey) })}
          </Button>
          <div className="flex flex-col gap-3 font-mono text-[11px] uppercase tracking-[0.04em] text-[var(--text-muted)] sm:flex-row sm:items-center sm:gap-4">
            {step === 2 && !accountSkipped ? (
              <Button
                variant="ghost"
                onClick={skipAccount}
                type="button"
                data-testid="wizard-skip-account"
                className="w-full sm:w-auto"
              >
                <SkipForward /> {t('actions.skipForNow')}
              </Button>
            ) : null}
            <span className="hidden sm:inline">
              <Kbd>⌘</Kbd> <Kbd>↵</Kbd> {t('actions.toContinue')}
            </span>
            {step < STEPS.length - 1 ? (
              <Button
                onClick={goNext}
                disabled={!currentIsValid || probing}
                data-testid="wizard-next"
                className="w-full sm:w-auto"
              >
                {step === 1 && probing ? (
                  <>
                    <Zap className="animate-pulse" /> {t('actions.testing')}
                  </>
                ) : (
                  <>
                    {t('actions.continue')}
                    <ArrowRight />
                  </>
                )}
              </Button>
            ) : (
              <Button
                onClick={
                  // Skipped accounts leave the zod-guarded
                  // account.{name,api_key} empty, which would block
                  // form.handleSubmit's resolver. Side-step it and go
                  // straight to commit with the current values when
                  // setup is deferring account creation. The backend
                  // validator accepts the omitted first_account block
                  // per setup-api.md v2.4.
                  usesInlineAccountSeed
                    ? form.handleSubmit(commit)
                    : () => void commit(form.getValues())
                }
                disabled={committing}
                data-testid="wizard-commit"
                className="w-full sm:w-auto"
              >
                {committing ? (
                  t('actions.committing')
                ) : (
                  <>
                    {t('actions.commitAndFinish')}
                    <CheckCircle2 />
                  </>
                )}
              </Button>
            )}
          </div>
        </div>
      </main>

      <Rail>
        <RailSection title={t('rail.installerSection')}>
          <RailSteps
            steps={STEPS.map((s) => ({ idx: s.idx, label: t(s.labelKey) }))}
            currentIndex={step}
            completedIndices={complete}
          />
        </RailSection>

        <RailSection title={t('rail.notesSection')}>
          <MiniCard title={t('rail.noteImage.title')}>
            <Trans i18nKey="rail.noteImage.body" ns="setup">
              Set <code>ROUTER_DB_URL</code> before boot and the router skips this wizard entirely —
              same binary, different environments.
            </Trans>
          </MiniCard>
          <MiniCard title={t('rail.noteKeys.title')}>
            <Trans i18nKey="rail.noteKeys.body" ns="setup">
              Fields clear on refresh; DSN + API key only land on disk once you click{' '}
              <em className="not-italic text-[var(--accent)]">Commit and finish</em>.
            </Trans>
          </MiniCard>
        </RailSection>

        <ShortcutList
          items={[
            { label: t('rail.shortcutNext'), keys: '⌘ ↵' },
            { label: t('rail.shortcutBack'), keys: '⌘ [' },
            ...(watchedDriver === 'sqlite3'
              ? []
              : [{ label: t('rail.shortcutTestDB'), keys: '⌘ T' } as const]),
            { label: t('rail.shortcutTheme'), keys: '⌘ ⇧ L' },
          ]}
        />
      </Rail>
    </Canvas>
  )
}

// ---------------------------------------------------------------------------
// Per-step components
// ---------------------------------------------------------------------------

function WelcomeStep() {
  const { t } = useTranslation('setup')
  return (
    <>
      <h1 className="mb-3 max-w-[28ch]">
        <span className="sr-only">{t('welcome.srTitle')}</span>
        {t('welcome.titleLead')} <strong>{t('welcome.titleStrong')}</strong>
      </h1>
      <p className="mb-7 max-w-[60ch] text-[14px] leading-[1.65] text-[var(--text-dim)]">
        <Trans i18nKey="welcome.intro" ns="setup">
          Point your Codex fleet at this router once and let it pool upstream accounts, keep session
          continuity, and record every call. Under two minutes of setup, one{' '}
          <code>config.json</code>, no secret in an env file.
        </Trans>
      </p>

      <PanelCard title={t('welcome.provideTitle')} metaMuted meta={t('welcome.provideMeta')}>
        <ul className="flex flex-col gap-[6px] text-[13px] text-[var(--text-dim)]">
          <li className="flex gap-3">
            <span className="w-16 font-mono text-[11.5px] uppercase tracking-[0.1em] text-[var(--accent)]">
              {t('welcome.storageLabel')}
            </span>
            {t('welcome.storageText')}
          </li>
          <li className="flex gap-3">
            <span className="w-16 font-mono text-[11.5px] uppercase tracking-[0.1em] text-[var(--accent)]">
              {t('welcome.upstreamLabel')}
            </span>
            {t('welcome.upstreamText')}
          </li>
          <li className="flex gap-3">
            <span className="w-16 font-mono text-[11.5px] uppercase tracking-[0.1em] text-[var(--accent)]">
              {t('welcome.pluginsLabel')}
            </span>
            {t('welcome.pluginsText')}
          </li>
        </ul>
      </PanelCard>
    </>
  )
}

interface StepProps {
  form: ReturnType<typeof useForm<WizardForm>>
}

function DatabaseStep({
  form,
  probe,
  probing,
  probeError,
  onProbe,
}: StepProps & {
  probe: ProbeResult | null
  probing: boolean
  probeError: unknown
  onProbe: () => void
}) {
  const { t } = useTranslation('setup')
  const { t: tCommon } = useTranslation('common')
  const driver = form.watch('db.driver')
  return (
    <>
      <h1 className="mb-3 max-w-[22ch]">
        {t('database.titleLead')} <strong>{t('database.titleStrong')}</strong>
      </h1>
      <p className="mb-7 max-w-[60ch] text-[14px] leading-[1.65] text-[var(--text-dim)]">
        {t('database.intro')}
      </p>

      <div
        className="mb-4 grid gap-2 sm:grid-cols-3"
        role="radiogroup"
        aria-label={t('database.engineGroupLabel')}
      >
        {(Object.keys(ENGINE_COPY) as Driver[]).map((d) => {
          const copy = ENGINE_COPY[d]
          return (
            <EngineCard
              key={d}
              value={d}
              selected={driver}
              onSelect={(v) =>
                form.setValue('db.driver', v as Driver, { shouldDirty: true, shouldTouch: true })
              }
              num={t(copy.numKey as 'engines.sqlite3.num')}
              title={t(copy.titleKey as 'engines.sqlite3.title')}
              version={copy.version}
              features={copy.features}
            >
              {t(copy.pitchKey as 'engines.sqlite3.pitch')}
            </EngineCard>
          )
        })}
      </div>

      <PanelCard title={t('database.connectionTitle')} meta={t('database.driverMeta', { driver })}>
        <Field
          htmlFor="db.url"
          label="DSN"
          hint={
            driver === 'sqlite3' ? (
              <Trans i18nKey="database.dsnHintSqlite" ns="setup">
                File path, <code className="mono">file:</code> URI, or full driver DSN.
              </Trans>
            ) : driver === 'postgres' ? (
              <Trans i18nKey="database.dsnHintPostgres" ns="setup">
                Postgres DSN, e.g. <code className="mono">postgres://user:pw@host:5432/db</code>.
              </Trans>
            ) : (
              <Trans i18nKey="database.dsnHintMysql" ns="setup">
                MySQL DSN, e.g. <code className="mono">user:pw@tcp(host:3306)/db</code>.
              </Trans>
            )
          }
        >
          <div className={driver === 'sqlite3' ? '' : 'grid gap-[6px] sm:grid-cols-[1fr_auto]'}>
            <Input
              id="db.url"
              data-testid="dsn"
              autoComplete="off"
              spellCheck={false}
              {...form.register('db.url')}
            />
            {driver === 'sqlite3' ? null : (
              <Button
                variant="secondary"
                onClick={onProbe}
                disabled={probing}
                data-testid="wizard-probe"
                type="button"
              >
                {probing ? t('database.testing') : t('database.test')}
              </Button>
            )}
          </div>
          {form.formState.errors.db?.url ? (
            <p className="mt-2 text-[11.5px] text-[var(--err)]">
              {tDynamic(form.formState.errors.db.url.message)}
            </p>
          ) : null}
          {probe ? (
            <ProbeRow
              status={probe.ok ? 'ok' : 'err'}
              latencyMs={probe.latency_ms}
              statusLabel={probe.ok ? tCommon('probe.healthy') : tCommon('probe.unreachable')}
              log={
                probe.ok ? (
                  <>
                    <ProbeLogKw>{t('database.probeDriverLabel')}</ProbeLogKw> {driver}
                    {probe.server_version ? (
                      <>
                        {' '}
                        · <ProbeLogKw>{t('database.probeVersionLabel')}</ProbeLogKw>{' '}
                        {probe.server_version}
                      </>
                    ) : null}
                    <br />
                    <ProbeLogKw>{t('database.probeSchemaLabel')}</ProbeLogKw>{' '}
                    <ProbeLogOk>{t('database.probeSchemaClean')}</ProbeLogOk> ·{' '}
                    {t('database.probeMigrationsPending')}
                  </>
                ) : (
                  <>
                    <ProbeLogKw>{t('database.probeErrorLabel')}</ProbeLogKw>{' '}
                    {probe.hint ?? t('database.probeFailedHint')}
                  </>
                )
              }
            />
          ) : null}
          {probeError ? (
            <div className="mt-3">
              <ErrorBanner error={probeError} title={t('database.testFailedTitle')} />
            </div>
          ) : null}
        </Field>

        <Field label={t('database.schemaMigrationTitle')} hint={t('database.schemaMigrationHint')}>
          <div className="pt-2 text-[12.5px] text-[var(--text)]">
            {t('database.autoApply')}{' '}
            <span className="mono text-[var(--text-muted)]">migrate up 000001_init</span>
          </div>
        </Field>
      </PanelCard>

      <PanelCard
        title={t('database.envOverrideTitle')}
        meta={t('database.envOverrideMeta')}
        metaMuted
      >
        <EnvLine k="ROUTER_DB_DRIVER" value={t('database.envUnset')} />
        <EnvLine k="ROUTER_DB_URL" value={t('database.envUnset')} />
      </PanelCard>
    </>
  )
}

function AccountStep({
  form,
  authMethod,
  skipped,
  onSelectAuthMethod,
  onUndoSkip,
}: StepProps & {
  authMethod: SetupAuthMethod
  skipped: boolean
  onSelectAuthMethod: (method: SetupAuthMethod) => void
  onUndoSkip: () => void
}) {
  const { t } = useTranslation('setup')
  const { t: tCommon } = useTranslation('common')
  if (skipped) {
    return (
      <>
        <h1 className="mb-3 max-w-[22ch]">
          {t('account.skipped.titleLead')} <strong>{t('account.skipped.titleStrong')}</strong>
        </h1>
        <Trans
          i18nKey="account.skipped.intro"
          ns="setup"
          parent="p"
          className="mb-7 max-w-[60ch] text-[14px] leading-[1.65] text-[var(--text-dim)]"
        >
          You chose to skip upstream-account registration. The router will still migrate the
          database and write <code>config.json</code> on commit; you just have to add at least one
          account under <em className="not-italic text-[var(--accent)]">Admin → Accounts</em> before{' '}
          <code>/v1/*</code> traffic can land. That screen supports browser-OAuth, device-OAuth, and{' '}
          <code>auth.json</code> import onboarding.
        </Trans>
        <PanelCard
          title={t('account.skipped.panelTitle')}
          meta={t('account.skipped.panelMeta')}
          metaMuted
        >
          <p className="text-[12.5px] leading-[1.55] text-[var(--text-dim)]">
            <Trans i18nKey="account.skipped.body" ns="setup">
              Until an active account exists, every <code>/v1/*</code> call returns{' '}
              <span className="mono text-[var(--err)]">503 no_available_account</span> and the admin
              dashboard flags the router as <span className="mono">degraded</span>. You can resume
              the flow at any time from Admin.
            </Trans>
          </p>
          <div className="mt-4">
            <Button
              variant="secondary"
              type="button"
              onClick={onUndoSkip}
              data-testid="wizard-unskip-account"
            >
              {t('account.skipped.chooseInstead')}
            </Button>
          </div>
        </PanelCard>
      </>
    )
  }
  const authMethods = ACCOUNT_AUTH_METHODS.map((method) => ({
    id: method.id,
    title: tCommon(AUTH_METHOD_COPY[method.id].titleKey),
    badge: tCommon(AUTH_METHOD_COPY[method.id].badgeKey),
    detail: t(AUTH_METHOD_COPY[method.id].detailKey),
  }))
  return (
    <>
      <h1 className="mb-3 max-w-[22ch]">
        {t('account.titleLead')} <strong>{t('account.titleStrong')}</strong>
      </h1>
      <p className="mb-7 max-w-[60ch] text-[14px] leading-[1.65] text-[var(--text-dim)]">
        <Trans i18nKey="account.intro" ns="setup">
          The setup wizard and <em className="not-italic text-[var(--accent)]">Admin → Accounts</em>{' '}
          now expose the same four auth modes. API key can seed inline during commit; browser OAuth,
          device OAuth, and <code>auth.json</code> import continue immediately after setup lands.
        </Trans>
      </p>

      <fieldset className="mb-4 grid gap-2 sm:grid-cols-2" aria-label={t('account.fieldsetLabel')}>
        <legend className="sr-only">{t('account.fieldsetLabel')}</legend>
        {authMethods.map((method) => {
          const selected = authMethod === method.id
          const inputID = `setup-auth-method-${method.id}`
          return (
            <label
              key={method.id}
              htmlFor={inputID}
              data-testid={`wizard-auth-method-${method.id}`}
              data-auth-method={method.id}
              className="flex h-full min-h-[148px] cursor-pointer flex-col gap-3 border px-4 py-4 text-left transition-[border-color,background-color] duration-150 focus-within:[box-shadow:var(--focus)]"
              style={{
                borderRadius: 2,
                borderColor: selected ? 'var(--accent)' : 'var(--line)',
                background: selected ? 'var(--panel-hi)' : 'var(--panel)',
              }}
            >
              <input
                id={inputID}
                type="radio"
                name="setup-auth-method"
                value={method.id}
                checked={selected}
                onChange={() => onSelectAuthMethod(method.id)}
                className="sr-only"
              />
              <div className="flex items-start justify-between gap-3">
                <div className="flex flex-col gap-2">
                  <span className="text-[15px] font-medium text-[var(--text)]">{method.title}</span>
                  <Badge variant={selected ? 'accent' : 'outline'}>{method.badge}</Badge>
                </div>
                <span className="font-mono text-[11px] uppercase tracking-[0.12em] text-[var(--text-muted)]">
                  {selected ? t('account.selected') : t('account.choose')}
                </span>
              </div>
              <p className="max-w-[42ch] text-[12.5px] leading-[1.6] text-[var(--text-dim)]">
                {method.detail}
              </p>
            </label>
          )
        })}
      </fieldset>

      {authMethod === 'api_key' ? (
        <PanelCard title={tCommon('authMethods.api_key.title')}>
          <Field
            htmlFor="account.name"
            label={t('account.form.nickname')}
            hint={t('account.form.nicknameHint')}
          >
            <Input
              id="account.name"
              data-testid="account.name"
              autoComplete="off"
              spellCheck={false}
              {...form.register('account.name')}
            />
            {form.formState.errors.account?.name ? (
              <p className="mt-2 text-[11.5px] text-[var(--err)]">
                {tDynamic(form.formState.errors.account.name.message)}
              </p>
            ) : null}
          </Field>
          <Field
            htmlFor="account.provider"
            label={t('account.form.provider')}
            hint={t('account.form.providerHint')}
          >
            <Input id="account.provider" value="openai" readOnly disabled aria-readonly="true" />
          </Field>
          <Field
            htmlFor="account.api_key"
            label={t('account.form.apiKey')}
            hint={t('account.form.apiKeyHint')}
          >
            <Input
              id="account.api_key"
              data-testid="account.api_key"
              type="password"
              autoComplete="off"
              {...form.register('account.api_key')}
            />
            {form.formState.errors.account?.api_key ? (
              <p className="mt-2 text-[11.5px] text-[var(--err)]">
                {tDynamic(form.formState.errors.account.api_key.message)}
              </p>
            ) : null}
          </Field>
          <Field
            htmlFor="account.base_url"
            label={
              <>
                {t('account.form.baseURL')}{' '}
                <span className="font-normal text-[var(--text-muted)]">
                  ({t('account.form.baseURLOptional')})
                </span>
              </>
            }
            hint={t('account.form.baseURLHint')}
          >
            <Input
              id="account.base_url"
              placeholder="https://api.openai.com"
              data-testid="account.base_url"
              {...form.register('account.base_url')}
            />
          </Field>
        </PanelCard>
      ) : (
        <PanelCard
          title={t('account.handoff.panelTitle')}
          meta={t('account.handoff.panelMeta')}
          metaMuted
        >
          <p className="max-w-[60ch] text-[12.5px] leading-[1.65] text-[var(--text-dim)]">
            {t(`account.handoff.${authMethod}` as 'account.handoff.oauth_browser')}
          </p>
          <div className="mt-4 border border-[var(--line)] bg-[var(--panel-2)] px-4 py-3 text-[12px] leading-[1.6] text-[var(--text-dim)]">
            <span className="font-mono uppercase tracking-[0.08em] text-[var(--accent)]">
              {t('account.handoff.payloadTitle')}
            </span>
            <p className="mt-2">
              <Trans i18nKey="account.handoff.payloadBody" ns="setup">
                <code>first_account</code> is omitted for this mode. Setup still writes{' '}
                <code>config.json</code> and migrations first; account creation continues
                immediately after commit on the matching 003 route.
              </Trans>
            </p>
          </div>
        </PanelCard>
      )}
    </>
  )
}

function PluginsStep({ form }: StepProps) {
  const { t } = useTranslation('setup')
  return (
    <>
      <h1 className="mb-3 max-w-[22ch]">
        {t('pluginsStep.titleLead')} <strong>{t('pluginsStep.titleStrong')}</strong>
      </h1>
      <p className="mb-7 max-w-[60ch] text-[14px] leading-[1.65] text-[var(--text-dim)]">
        <Trans i18nKey="pluginsStep.intro" ns="setup">
          Switching these on now records operator preference in <code>config.json</code>. If the
          matching plugin is not installed in this build, flipping the switch is intent-only and
          causes no behavioural change.
        </Trans>
      </p>

      <PanelCard title={t('pluginsStep.panelTitle')}>
        <PluginRow
          id="admin_auth"
          label={t('pluginsStep.admin_auth.label')}
          badge={t('pluginsStep.badge')}
          description={t('pluginsStep.admin_auth.description')}
          checked={form.watch('plugins.admin_auth')}
          onChange={(v) => form.setValue('plugins.admin_auth', v, { shouldDirty: true })}
        />
        <PluginRow
          id="client_keys"
          label={t('pluginsStep.client_keys.label')}
          badge={t('pluginsStep.badge')}
          description={t('pluginsStep.client_keys.description')}
          checked={form.watch('plugins.client_keys')}
          onChange={(v) => form.setValue('plugins.client_keys', v, { shouldDirty: true })}
        />
      </PanelCard>
    </>
  )
}

function PluginRow({
  id,
  label,
  badge,
  description,
  checked,
  onChange,
}: {
  id: string
  label: string
  badge: string
  description: string
  checked: boolean
  onChange: (v: boolean) => void
}) {
  return (
    <div className="grid items-start gap-3 py-3 sm:grid-cols-[minmax(0,180px)_1fr_auto] sm:items-center sm:gap-5 [&+&]:border-t [&+&]:border-[var(--line)]">
      <div className="flex flex-col gap-[6px]">
        <Label htmlFor={`wizard-plugin-${id}`}>{label}</Label>
        <span
          className="inline-flex w-fit border px-[6px] py-[1px] font-mono text-[10.5px] uppercase tracking-[0.08em]"
          style={{
            borderColor: 'var(--line-2)',
            color: 'var(--text-muted)',
            borderRadius: 2,
          }}
        >
          {badge}
        </span>
      </div>
      <p className="text-[12px] leading-[1.55] text-[var(--text-dim)]">{description}</p>
      <div className="justify-self-start sm:justify-self-auto">
        <Switch
          id={`wizard-plugin-${id}`}
          checked={checked}
          onCheckedChange={onChange}
          data-testid={`wizard-plugin-${id}`}
        />
      </div>
    </div>
  )
}

function ReviewStep({
  form,
  authMethod,
  commitError,
  accountSkipped,
}: StepProps & {
  authMethod: SetupAuthMethod
  commitError: unknown
  accountSkipped: boolean
}) {
  const { t } = useTranslation('setup')
  const methodLabel =
    authMethod === 'oauth_browser'
      ? t('review.methodBrowser')
      : authMethod === 'oauth_device'
        ? t('review.methodDevice')
        : t('review.methodImport')
  return (
    <>
      <h1 className="mb-3 max-w-[22ch]">
        {t('review.titleLead')} <strong>{t('review.titleStrong')}</strong>
      </h1>
      <p className="mb-7 max-w-[60ch] text-[14px] leading-[1.65] text-[var(--text-dim)]">
        {t('review.introPrefix')}{' '}
        {accountSkipped ? (
          t('review.branchSkip')
        ) : authMethod === 'api_key' ? (
          t('review.branchApiKey')
        ) : (
          <>
            {t('review.branchOAuthPrefix')}{' '}
            <span className="text-[var(--accent)]">{methodLabel}</span>{' '}
            {t('review.branchOAuthSuffix')}
          </>
        )}{' '}
        {t('review.introSuffix')}
      </p>

      <PanelCard title={t('review.panelTitle')} meta={t('review.panelMeta')}>
        <dl className="grid gap-x-6 gap-y-2 py-1 sm:grid-cols-[minmax(0,180px)_1fr] sm:gap-y-3">
          <dt className="font-mono text-[11.5px] uppercase tracking-[0.1em] text-[var(--text-muted)]">
            {t('review.driver')}
          </dt>
          <dd className="font-mono text-[12.5px] text-[var(--text)]">
            {form.getValues('db.driver')}
          </dd>

          <dt className="font-mono text-[11.5px] uppercase tracking-[0.1em] text-[var(--text-muted)]">
            {t('review.dsn')}
          </dt>
          <dd className="truncate font-mono text-[12.5px] text-[var(--text)]">
            {form.getValues('db.url')}
          </dd>

          <dt className="font-mono text-[11.5px] uppercase tracking-[0.1em] text-[var(--text-muted)]">
            {t('review.account')}
          </dt>
          <dd className="font-mono text-[12.5px] text-[var(--text)]">
            {accountSkipped ? (
              <span className="text-[var(--warn)]">{t('review.deferredShort')}</span>
            ) : authMethod === 'api_key' ? (
              <>
                {form.getValues('account.name')}{' '}
                <span className="text-[var(--text-muted)]">{t('review.providerOpenai')}</span>
              </>
            ) : (
              <span className="text-[var(--accent)]">
                {t('review.deferredContinue', { method: methodLabel })}
              </span>
            )}
          </dd>

          <dt className="font-mono text-[11.5px] uppercase tracking-[0.1em] text-[var(--text-muted)]">
            {t('review.authMethod')}
          </dt>
          <dd className="font-mono text-[12.5px] text-[var(--text)]">
            {accountSkipped ? t('review.authSkip') : authMethod}
          </dd>

          <dt className="font-mono text-[11.5px] uppercase tracking-[0.1em] text-[var(--text-muted)]">
            {t('review.adminAuth')}
          </dt>
          <dd className="text-[12.5px] text-[var(--text)]">
            {form.getValues('plugins.admin_auth') ? t('review.onIntentOnly') : t('review.off')}
          </dd>

          <dt className="font-mono text-[11.5px] uppercase tracking-[0.1em] text-[var(--text-muted)]">
            {t('review.clientKeys')}
          </dt>
          <dd className="text-[12.5px] text-[var(--text)]">
            {form.getValues('plugins.client_keys') ? t('review.onIntentOnly') : t('review.off')}
          </dd>
        </dl>
        {commitError ? (
          <div className="mt-4">
            <ErrorBanner error={commitError} title={t('review.commitFailedTitle')} />
          </div>
        ) : null}
      </PanelCard>
    </>
  )
}
