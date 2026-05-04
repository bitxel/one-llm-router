import { zodResolver } from '@hookform/resolvers/zod'
import { useNavigate } from '@tanstack/react-router'
import { ArrowLeft, ArrowRight, CheckCircle2, SkipForward, Zap } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { type SubmitHandler, useForm } from 'react-hook-form'
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

// Step labels double as the e2e test's `role="heading"` anchors — see
// `frontend/tests/e2e/wizard-happy-path.spec.ts`. Keep them stable.
const STEPS = [
  { idx: '01', label: 'Welcome' },
  { idx: '02', label: 'Database' },
  { idx: '03', label: 'Upstream account' },
  { idx: '04', label: 'Plugin intents' },
  { idx: '05', label: 'Commit' },
] as const

type Driver = 'sqlite3' | 'postgres' | 'mysql'
type SetupAuthMethod = AccountAuthMethod

// Validation caps MUST match `internal/setup/validator.go` so the
// wizard and the commit handler agree on what they accept:
//   - DSN length: DSNMaxLen = 4096
//   - Account name: accountNameRE = ^[A-Za-z0-9_-]{1,64}$
//   - Base URL length: BaseURLMaxLen = 256
const WizardSchema = z.object({
  db: z.object({
    driver: z.enum(['sqlite3', 'postgres', 'mysql']) as z.ZodType<Driver>,
    url: z.string().trim().min(1, 'DSN is required').max(4096),
  }),
  account: z.object({
    name: z
      .string()
      .trim()
      .min(1, 'name is required')
      .max(64)
      .regex(/^[A-Za-z0-9_-]+$/, 'letters, digits, dashes, underscores only'),
    provider: z.literal('openai'),
    api_key: z.string().trim().min(1, 'API key is required'),
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

const SETUP_AUTH_METHOD_DETAILS = {
  api_key:
    'Seeds the first upstream inline during setup. Fastest path to a ready router when you already have a key.',
  oauth_browser:
    'Commit setup first, then continue immediately into the 003 browser sign-in flow under Admin -> Accounts.',
  oauth_device:
    'Best for SSH or remote hosts. Setup commits first, then opens the device-code onboarding screen.',
  oauth_import:
    'Finish setup, then land directly on the import screen to transplant an existing Codex CLI session.',
} as const satisfies Record<SetupAuthMethod, string>

const SETUP_AUTH_METHODS = ACCOUNT_AUTH_METHODS.map((method) => ({
  ...method,
  detail: SETUP_AUTH_METHOD_DETAILS[method.id],
}))

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
  { num: string; title: string; version: string; pitch: string; features: readonly string[] }
> = {
  sqlite3: {
    num: '01 — Default',
    title: 'SQLite',
    version: '3.40+',
    pitch:
      'One file on disk, no daemon, no credentials. Enabled with WAL + foreign keys out of the box. Best place to start.',
    features: ['embedded', 'pure-go', 'zero-ops'],
  },
  postgres: {
    num: '02 — Scale',
    title: 'PostgreSQL',
    version: '14+',
    pitch:
      "Reuse your team's Postgres: backups, replication, observability. All migrations are idempotent and reviewable.",
    features: ['lib/pq', 'TLS', 'replica-ready'],
  },
  mysql: {
    num: '03 — Parity',
    title: 'MySQL',
    version: '8.0+',
    pitch:
      'DATETIME(6) for microsecond timestamps. Feature-parity with Postgres across migrations, pool, and indexes.',
    features: ['go-sql-driver', 'TLS', 'µs'],
  },
}

export function SetupWizard() {
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
        <Stripe eyebrow={`Step ${active.idx} of 05`} titleSide="right">
          {active.label}
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
            {step === 0 ? 'Back' : `Back to ${STEPS[step - 1].label}`}
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
                <SkipForward /> Skip for now
              </Button>
            ) : null}
            <span className="hidden sm:inline">
              <Kbd>⌘</Kbd> <Kbd>↵</Kbd> to continue
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
                    <Zap className="animate-pulse" /> testing…
                  </>
                ) : (
                  <>
                    Continue
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
                  'Committing…'
                ) : (
                  <>
                    Commit and finish
                    <CheckCircle2 />
                  </>
                )}
              </Button>
            )}
          </div>
        </div>
      </main>

      <Rail>
        <RailSection title="Installer">
          <RailSteps
            steps={STEPS.map((s) => ({ idx: s.idx, label: s.label }))}
            currentIndex={step}
            completedIndices={complete}
          />
        </RailSection>

        <RailSection title="Operator notes">
          <MiniCard title="Shipping the same image twice?">
            Set <code>ROUTER_DB_URL</code> before boot and the router skips this wizard entirely —
            same binary, different environments.
          </MiniCard>
          <MiniCard title="Keys never persist mid-wizard">
            Fields clear on refresh; DSN + API key only land on disk once you click{' '}
            <em className="not-italic text-[var(--accent)]">Commit and finish</em>.
          </MiniCard>
        </RailSection>

        <ShortcutList
          items={[
            { label: 'Next step', keys: '⌘ ↵' },
            { label: 'Back', keys: '⌘ [' },
            ...(watchedDriver === 'sqlite3' ? [] : [{ label: 'Test DB', keys: '⌘ T' } as const]),
            { label: 'Toggle theme', keys: '⌘ ⇧ L' },
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
  return (
    <>
      <h1 className="mb-3 max-w-[28ch]">
        <span className="sr-only">Welcome to one-llm-router. </span>A single endpoint for{' '}
        <strong>every Codex client.</strong>
      </h1>
      <p className="mb-7 max-w-[60ch] text-[14px] leading-[1.65] text-[var(--text-dim)]">
        Point your Codex fleet at this router once and let it pool upstream accounts, keep session
        continuity, and record every call. Under two minutes of setup, one <code>config.json</code>,
        no secret in an env file.
      </p>

      <PanelCard title="What you'll provide" metaMuted meta="≈ 90 seconds">
        <ul className="flex flex-col gap-[6px] text-[13px] text-[var(--text-dim)]">
          <li className="flex gap-3">
            <span className="w-16 font-mono text-[11.5px] uppercase tracking-[0.1em] text-[var(--accent)]">
              Storage
            </span>
            A database — SQLite is the default and needs nothing but a writable directory.
          </li>
          <li className="flex gap-3">
            <span className="w-16 font-mono text-[11.5px] uppercase tracking-[0.1em] text-[var(--accent)]">
              Upstream
            </span>
            Optional first upstream onboarding. API key can seed inline; browser OAuth, device
            OAuth, and imported <code>auth.json</code> hand off immediately after setup.
          </li>
          <li className="flex gap-3">
            <span className="w-16 font-mono text-[11.5px] uppercase tracking-[0.1em] text-[var(--accent)]">
              Plugins
            </span>
            Initial intents for auth plugins — leaving them all off is a perfectly fine default in
            this build.
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
  const driver = form.watch('db.driver')
  return (
    <>
      <h1 className="mb-3 max-w-[22ch]">
        Give the router somewhere to <strong>keep receipts.</strong>
      </h1>
      <p className="mb-7 max-w-[60ch] text-[14px] leading-[1.65] text-[var(--text-dim)]">
        Every routed request, every operator change, every upstream account lives in one OLTP
        engine. Pick it once; the schema is migratable, the engine choice isn't one-way.
      </p>

      <div
        className="mb-4 grid gap-2 sm:grid-cols-3"
        role="radiogroup"
        aria-label="Database engine"
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
              num={copy.num}
              title={copy.title}
              version={copy.version}
              features={copy.features}
            >
              {copy.pitch}
            </EngineCard>
          )
        })}
      </div>

      <PanelCard title="Connection" meta={`driver = ${driver}`}>
        <Field
          htmlFor="db.url"
          label="DSN"
          hint={
            driver === 'sqlite3' ? (
              <>
                File path, <code className="mono">file:</code> URI, or full driver DSN.
              </>
            ) : driver === 'postgres' ? (
              <>
                Postgres DSN, e.g. <code className="mono">postgres://user:pw@host:5432/db</code>.
              </>
            ) : (
              <>
                MySQL DSN, e.g. <code className="mono">user:pw@tcp(host:3306)/db</code>.
              </>
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
                {probing ? 'Testing…' : 'Test'}
              </Button>
            )}
          </div>
          {form.formState.errors.db?.url ? (
            <p className="mt-2 text-[11.5px] text-[var(--err)]">
              {form.formState.errors.db.url.message}
            </p>
          ) : null}
          {probe ? (
            <ProbeRow
              status={probe.ok ? 'ok' : 'err'}
              latencyMs={probe.latency_ms}
              statusLabel={probe.ok ? 'healthy' : 'unreachable'}
              log={
                probe.ok ? (
                  <>
                    <ProbeLogKw>driver</ProbeLogKw> {driver}
                    {probe.server_version ? (
                      <>
                        {' '}
                        · <ProbeLogKw>version</ProbeLogKw> {probe.server_version}
                      </>
                    ) : null}
                    <br />
                    <ProbeLogKw>schema</ProbeLogKw> <ProbeLogOk>clean</ProbeLogOk> · migrations
                    pending on commit
                  </>
                ) : (
                  <>
                    <ProbeLogKw>error</ProbeLogKw> {probe.hint ?? 'probe failed'}
                  </>
                )
              }
            />
          ) : null}
          {probeError ? (
            <div className="mt-3">
              <ErrorBanner error={probeError} title="Database test failed" />
            </div>
          ) : null}
        </Field>

        <Field
          label="Schema migration"
          hint="Applied automatically at boot. You can always re-apply from the CLI."
        >
          <div className="pt-2 text-[12.5px] text-[var(--text)]">
            Auto-apply on startup ·{' '}
            <span className="mono text-[var(--text-muted)]">migrate up 000001_init</span>
          </div>
        </Field>
      </PanelCard>

      <PanelCard title="Environment override" meta="inert — nothing set" metaMuted>
        <EnvLine k="ROUTER_DB_DRIVER" value="(unset)" />
        <EnvLine k="ROUTER_DB_URL" value="(unset)" />
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
  if (skipped) {
    return (
      <>
        <h1 className="mb-3 max-w-[22ch]">
          Account seeding <strong>deferred.</strong>
        </h1>
        <p className="mb-7 max-w-[60ch] text-[14px] leading-[1.65] text-[var(--text-dim)]">
          You chose to skip upstream-account registration. The router will still migrate the
          database and write <code>config.json</code> on commit; you just have to add at least one
          account under <em className="not-italic text-[var(--accent)]">Admin → Accounts</em> before{' '}
          <code>/v1/*</code> traffic can land. That screen supports browser-OAuth, device-OAuth, and{' '}
          <code>auth.json</code> import onboarding.
        </p>
        <PanelCard title="Skipped" meta="no key on disk" metaMuted>
          <p className="text-[12.5px] leading-[1.55] text-[var(--text-dim)]">
            Until an active account exists, every <code>/v1/*</code> call returns{' '}
            <span className="mono text-[var(--err)]">503 no_available_account</span> and the admin
            dashboard flags the router as <span className="mono">degraded</span>. You can resume the
            flow at any time from Admin.
          </p>
          <div className="mt-4">
            <Button
              variant="secondary"
              type="button"
              onClick={onUndoSkip}
              data-testid="wizard-unskip-account"
            >
              Choose an auth method instead
            </Button>
          </div>
        </PanelCard>
      </>
    )
  }
  return (
    <>
      <h1 className="mb-3 max-w-[22ch]">
        Choose your first <strong>upstream path.</strong>
      </h1>
      <p className="mb-7 max-w-[60ch] text-[14px] leading-[1.65] text-[var(--text-dim)]">
        The setup wizard and <em className="not-italic text-[var(--accent)]">Admin → Accounts</em>{' '}
        now expose the same four auth modes. API key can seed inline during commit; browser OAuth,
        device OAuth, and <code>auth.json</code> import continue immediately after setup lands.
      </p>

      <fieldset className="mb-4 grid gap-2 sm:grid-cols-2" aria-label="Upstream auth methods">
        <legend className="sr-only">Upstream auth methods</legend>
        {SETUP_AUTH_METHODS.map((method) => {
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
                  {selected ? 'selected' : 'choose'}
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
        <PanelCard title="Upstream">
          <Field
            htmlFor="account.name"
            label="Nickname"
            hint="Letters, digits, `-`, `_`. 1–64 chars."
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
                {form.formState.errors.account.name.message}
              </p>
            ) : null}
          </Field>
          <Field
            htmlFor="account.provider"
            label="Provider"
            hint="The setup seed stays OpenAI-only. Browser, device, and import still reuse the same provider after commit."
          >
            <Input id="account.provider" value="openai" readOnly disabled aria-readonly="true" />
          </Field>
          <Field
            htmlFor="account.api_key"
            label="API key"
            hint="Stored verbatim (plaintext) in upstream_accounts in this build; encryption-at-rest arrives with the key-vault plugin in a later spec."
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
                {form.formState.errors.account.api_key.message}
              </p>
            ) : null}
          </Field>
          <Field
            htmlFor="account.base_url"
            label={
              <>
                Base URL <span className="font-normal text-[var(--text-muted)]">(optional)</span>
              </>
            }
            hint="Override for Azure OpenAI or compatible proxies. Leave empty for the default."
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
        <PanelCard title="Post-commit handoff" meta="same router, no second wizard" metaMuted>
          <p className="max-w-[60ch] text-[12.5px] leading-[1.65] text-[var(--text-dim)]">
            {authMethod === 'oauth_browser'
              ? 'Setup will finish first, then open the browser OAuth screen under Admin → Accounts so you can start the PKCE sign-in flow there.'
              : authMethod === 'oauth_device'
                ? 'Setup will finish first, then open the device-code screen under Admin → Accounts so you can approve from a second browser.'
                : 'Setup will finish first, then open the import screen under Admin → Accounts so you can upload your local auth.json.'}
          </p>
          <div className="mt-4 border border-[var(--line)] bg-[var(--panel-2)] px-4 py-3 text-[12px] leading-[1.6] text-[var(--text-dim)]">
            <span className="font-mono uppercase tracking-[0.08em] text-[var(--accent)]">
              commit payload
            </span>
            <p className="mt-2">
              <code>first_account</code> is omitted for this mode. Setup still writes{' '}
              <code>config.json</code> and migrations first; account creation continues immediately
              after commit on the matching 003 route.
            </p>
          </div>
        </PanelCard>
      )}
    </>
  )
}

function PluginsStep({ form }: StepProps) {
  return (
    <>
      <h1 className="mb-3 max-w-[22ch]">
        Set plugin <strong>intents.</strong>
      </h1>
      <p className="mb-7 max-w-[60ch] text-[14px] leading-[1.65] text-[var(--text-dim)]">
        Switching these on now records operator preference in <code>config.json</code>. If the
        matching plugin is not installed in this build, flipping the switch is intent-only and
        causes no behavioural change.
      </p>

      <PanelCard title="Plugin intents">
        <PluginRow
          id="admin_auth"
          label="Admin authentication"
          badge="intent only"
          description="Records whether admin sign-in should be enforced once that plugin is available. Today the admin API still uses the trusted-network boundary."
          checked={form.watch('plugins.admin_auth')}
          onChange={(v) => form.setValue('plugins.admin_auth', v, { shouldDirty: true })}
        />
        <PluginRow
          id="client_keys"
          label="Client API keys"
          badge="intent only"
          description="Records whether per-caller `/v1/*` keys should be enforced once that plugin is available."
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
  return (
    <>
      <h1 className="mb-3 max-w-[22ch]">
        Land on disk, then <strong>serve traffic.</strong>
      </h1>
      <p className="mb-7 max-w-[60ch] text-[14px] leading-[1.65] text-[var(--text-dim)]">
        Clicking commit runs the schema migration,{' '}
        {accountSkipped ? (
          <>
            skips upstream-account seeding (add one in{' '}
            <em className="not-italic text-[var(--accent)]">Admin → Accounts</em> to unblock{' '}
            <code>/v1/*</code> traffic),
          </>
        ) : authMethod === 'api_key' ? (
          <>inserts your API-key seed account,</>
        ) : (
          <>
            finishes setup first, then hands you directly into the{' '}
            <span className="text-[var(--accent)]">
              {authMethod === 'oauth_browser'
                ? 'browser OAuth'
                : authMethod === 'oauth_device'
                  ? 'device OAuth'
                  : 'auth.json import'}
            </span>{' '}
            screen,
          </>
        )}{' '}
        atomically writes <code>config.json</code>, and unlatches the setup gate. Takes a few
        seconds.
      </p>

      <PanelCard title="Review" meta="ready to commit">
        <dl className="grid gap-x-6 gap-y-2 py-1 sm:grid-cols-[minmax(0,180px)_1fr] sm:gap-y-3">
          <dt className="font-mono text-[11.5px] uppercase tracking-[0.1em] text-[var(--text-muted)]">
            Driver
          </dt>
          <dd className="font-mono text-[12.5px] text-[var(--text)]">
            {form.getValues('db.driver')}
          </dd>

          <dt className="font-mono text-[11.5px] uppercase tracking-[0.1em] text-[var(--text-muted)]">
            DSN
          </dt>
          <dd className="truncate font-mono text-[12.5px] text-[var(--text)]">
            {form.getValues('db.url')}
          </dd>

          <dt className="font-mono text-[11.5px] uppercase tracking-[0.1em] text-[var(--text-muted)]">
            Account
          </dt>
          <dd className="font-mono text-[12.5px] text-[var(--text)]">
            {accountSkipped ? (
              <span className="text-[var(--warn)]">
                — deferred · add in Admin → Accounts before /v1/* traffic
              </span>
            ) : authMethod === 'api_key' ? (
              <>
                {form.getValues('account.name')}{' '}
                <span className="text-[var(--text-muted)]">· provider openai</span>
              </>
            ) : (
              <span className="text-[var(--accent)]">
                — deferred · continue with{' '}
                {authMethod === 'oauth_browser'
                  ? 'browser sign-in'
                  : authMethod === 'oauth_device'
                    ? 'device code'
                    : 'auth.json import'}{' '}
                immediately after commit
              </span>
            )}
          </dd>

          <dt className="font-mono text-[11.5px] uppercase tracking-[0.1em] text-[var(--text-muted)]">
            Auth method
          </dt>
          <dd className="font-mono text-[12.5px] text-[var(--text)]">
            {accountSkipped ? 'skip for now' : authMethod === 'api_key' ? 'api_key' : authMethod}
          </dd>

          <dt className="font-mono text-[11.5px] uppercase tracking-[0.1em] text-[var(--text-muted)]">
            Admin auth
          </dt>
          <dd className="text-[12.5px] text-[var(--text)]">
            {form.getValues('plugins.admin_auth')
              ? 'on (intent only — plugin not installed)'
              : 'off'}
          </dd>

          <dt className="font-mono text-[11.5px] uppercase tracking-[0.1em] text-[var(--text-muted)]">
            Client keys
          </dt>
          <dd className="text-[12.5px] text-[var(--text)]">
            {form.getValues('plugins.client_keys')
              ? 'on (intent only — plugin not installed)'
              : 'off'}
          </dd>
        </dl>
        {commitError ? (
          <div className="mt-4">
            <ErrorBanner error={commitError} title="Commit failed" />
          </div>
        ) : null}
      </PanelCard>
    </>
  )
}
