import { zodResolver } from '@hookform/resolvers/zod'
import { Plus, Trash2 } from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { useFieldArray, useForm } from 'react-hook-form'
import { Trans, useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { Canvas, Field, PageIntro, PanelCard, Stripe } from '@/components/neo'
import { ErrorBanner } from '@/components/shared/ErrorBanner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import type { PluginIntent, SettingsPatch, SettingsPayload } from '@/hooks/use-settings'
import { useSettings, useUpdateSettings } from '@/hooks/use-settings'
import { api, RouterApiError } from '@/lib/api-client'
import { Err009InvalidProxyURL } from '@/lib/errcode'
import { tDynamic } from '@/lib/error-message'

// Custom validation messages are `settings` catalog keys resolved at
// render time via tDynamic(); zod library defaults stay English and
// pass through untouched.
const RuntimeFormSchema = z.object({
  log_client_request_body: z.boolean(),
  log_upstream_request_body: z.boolean(),
  log_upstream_response_body: z.boolean(),
  log_retention_days: z.coerce
    .number()
    .int()
    .min(1, 'settings:validation.retentionRange')
    .max(365, 'settings:validation.retentionRange'),
  log_level: z.enum(['debug', 'info', 'warn', 'error']),
  model_renames: z
    .array(
      z.object({
        from: z
          .string()
          .trim()
          .min(1, 'settings:validation.renameFromRequired')
          .max(128, 'settings:validation.renameMax'),
        to: z
          .string()
          .trim()
          .min(1, 'settings:validation.renameToRequired')
          .max(128, 'settings:validation.renameMax'),
      }),
    )
    .max(32, 'settings:validation.renameMax'),
})
type RuntimeForm = z.infer<typeof RuntimeFormSchema>

const PLUGIN_IDS = ['admin_auth', 'client_keys'] as const

type PluginIntentID = (typeof PLUGIN_IDS)[number]

export function AdminSettings() {
  const { t } = useTranslation('settings')
  const settings = useSettings()
  const update = useUpdateSettings()

  if (settings.isLoading) {
    return <SettingsSkeleton />
  }
  if (settings.isError) {
    return (
      <Canvas variant="narrow">
        <Stripe eyebrow={t('eyebrow')} />
        <ErrorBanner error={settings.error} title={t('loadErrorTitle')} />
      </Canvas>
    )
  }
  if (!settings.data) return null

  const settingsContractError = validateSettingsContract(settings.data)
  if (settingsContractError) {
    return (
      <Canvas variant="narrow">
        <Stripe eyebrow={t('eyebrow')} />
        <ErrorBanner error={settingsContractError} title={t('contractErrorTitle')} />
      </Canvas>
    )
  }

  return <SettingsForm data={settings.data} onPatch={update.mutateAsync} />
}

interface FormProps {
  data: SettingsPayload
  onPatch: (patch: SettingsPatch) => Promise<SettingsPayload>
}

function SettingsForm({ data, onPatch }: FormProps) {
  const { t } = useTranslation('settings')
  const form = useForm<RuntimeForm>({
    resolver: zodResolver(RuntimeFormSchema),
    defaultValues: data.runtime,
  })
  const modelRenameFields = useFieldArray({
    control: form.control,
    name: 'model_renames',
  })

  useEffect(() => {
    form.reset(data.runtime)
  }, [data, form])

  const submit = useCallback(
    async (next: RuntimeForm) => {
      try {
        await onPatch({ runtime: next })
        toast.success(t('toasts.updateSuccess'))
      } catch (err) {
        toast.error(t('toasts.updateFailed'))
        throw err
      }
    },
    [onPatch, t],
  )

  const patchPlugin = useCallback(
    async (id: 'admin_auth' | 'client_keys', enabled: boolean) => {
      try {
        await onPatch({ plugins: { [id]: { enabled } } })
        toast.success(t('toasts.pluginSuccess', { id, enabled }))
      } catch (err) {
        toast.error(t('toasts.pluginFailed'))
        throw err
      }
    },
    [onPatch, t],
  )

  const pluginIntents = pluginIntentMap(data)

  // Persisted operator intent lives in `data.plugin_intents`.
  // `data.plugins` reflects *registered* plugin binaries. If a real
  // plugin later ships for the same id, its live status wins; until
  // then we surface the persisted intent only.
  const pluginEnabled = (id: PluginIntentID) => {
    const registered = data.plugins.find((p) => p.id === id)
    if (registered) return registered.enabled
    return requirePluginIntent(pluginIntents, id).enabled
  }

  const pluginIntentStatus = (id: PluginIntentID) => {
    return requirePluginIntent(pluginIntents, id).status
  }

  return (
    <Canvas variant="narrow">
      <Stripe eyebrow={t('eyebrow')} />
      <h1 className="mb-3 max-w-[22ch]">
        {t('title')} <strong>{t('titleStrong')}</strong>
      </h1>
      <PageIntro className="mb-7">
        <Trans i18nKey="intro" ns="settings">
          Observability toggles apply on the next <code>/v1/*</code> request. Plugin intents persist
          to <code>config.json</code>; if a plugin is not installed in this build, the switch
          records operator preference only.
        </Trans>
      </PageIntro>

      {form.formState.errors.root ? (
        <div className="mb-4">
          <ErrorBanner
            error={new Error(form.formState.errors.root.message ?? 'Validation failed')}
          />
        </div>
      ) : null}

      <form onSubmit={form.handleSubmit(submit)} data-testid="runtime-form">
        <PanelCard title={t('runtime.panelTitle')}>
          <ToggleField
            id="log_client_request_body"
            label={t('runtime.logClientRequestBody')}
            hint={t('runtime.logClientRequestBodyHint')}
            checked={form.watch('log_client_request_body')}
            onChange={(v) => form.setValue('log_client_request_body', v, { shouldDirty: true })}
          />
          <ToggleField
            id="log_upstream_request_body"
            label={t('runtime.logUpstreamRequestBody')}
            hint={t('runtime.logUpstreamRequestBodyHint')}
            checked={form.watch('log_upstream_request_body')}
            onChange={(v) => form.setValue('log_upstream_request_body', v, { shouldDirty: true })}
          />
          <ToggleField
            id="log_upstream_response_body"
            label={t('runtime.logUpstreamResponseBody')}
            hint={t('runtime.logUpstreamResponseBodyHint')}
            checked={form.watch('log_upstream_response_body')}
            onChange={(v) => form.setValue('log_upstream_response_body', v, { shouldDirty: true })}
          />
          <Field
            htmlFor="log_retention_days"
            label={t('runtime.logRetention')}
            hint={t('runtime.logRetentionHint')}
          >
            <div className="flex items-center gap-3">
              <Input
                id="log_retention_days"
                type="number"
                min={1}
                max={365}
                className="w-28"
                {...form.register('log_retention_days', { valueAsNumber: true })}
              />
              <span className="font-mono text-[11.5px] text-[var(--text-muted)]">
                {t('runtime.days')}
              </span>
            </div>
            {form.formState.errors.log_retention_days ? (
              <p className="mt-2 text-[11.5px] text-[var(--err)]">
                {tDynamic(form.formState.errors.log_retention_days.message)}
              </p>
            ) : null}
          </Field>
          <Field htmlFor="log_level" label={t('runtime.logLevel')} hint={t('runtime.logLevelHint')}>
            <div className="w-40">
              <Select
                value={form.watch('log_level')}
                onValueChange={(v) =>
                  form.setValue('log_level', v as RuntimeForm['log_level'], { shouldDirty: true })
                }
              >
                <SelectTrigger id="log_level">
                  <SelectValue placeholder={t('runtime.selectPlaceholder')} />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="debug">debug</SelectItem>
                  <SelectItem value="info">info</SelectItem>
                  <SelectItem value="warn">warn</SelectItem>
                  <SelectItem value="error">error</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </Field>
          <Field label={t('runtime.modelRenames')} hint={t('runtime.modelRenamesHint')}>
            <div className="flex flex-col gap-3">
              {modelRenameFields.fields.map((field, index) => (
                <div
                  key={field.id}
                  className="grid gap-2 rounded-[2px] border border-[var(--line)] bg-[var(--panel-2)] p-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]"
                >
                  <div className="flex flex-col gap-1">
                    <Label htmlFor={`model_renames.${index}.from`}>{t('runtime.from')}</Label>
                    <Input
                      id={`model_renames.${index}.from`}
                      {...form.register(`model_renames.${index}.from`)}
                    />
                    {form.formState.errors.model_renames?.[index]?.from ? (
                      <p className="text-[11.5px] text-[var(--err)]">
                        {tDynamic(form.formState.errors.model_renames[index]?.from?.message)}
                      </p>
                    ) : null}
                  </div>
                  <div className="flex flex-col gap-1">
                    <Label htmlFor={`model_renames.${index}.to`}>{t('runtime.to')}</Label>
                    <Input
                      id={`model_renames.${index}.to`}
                      {...form.register(`model_renames.${index}.to`)}
                    />
                    {form.formState.errors.model_renames?.[index]?.to ? (
                      <p className="text-[11.5px] text-[var(--err)]">
                        {tDynamic(form.formState.errors.model_renames[index]?.to?.message)}
                      </p>
                    ) : null}
                  </div>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    aria-label={t('runtime.removeModelRename')}
                    onClick={() => modelRenameFields.remove(index)}
                    className="self-end justify-self-start sm:justify-self-end"
                  >
                    <Trash2 />
                  </Button>
                </div>
              ))}
              {form.formState.errors.model_renames?.root ? (
                <p className="text-[11.5px] text-[var(--err)]">
                  {tDynamic(form.formState.errors.model_renames.root.message)}
                </p>
              ) : null}
              <div>
                <Button
                  type="button"
                  variant="outline"
                  disabled={modelRenameFields.fields.length >= 32}
                  onClick={() => modelRenameFields.append({ from: '', to: '' })}
                >
                  <Plus />
                  {t('runtime.addRename')}
                </Button>
              </div>
            </div>
          </Field>
          <div className="mt-1 flex flex-col gap-2 border-t border-[var(--line)] pt-4 sm:flex-row sm:justify-end">
            <Button
              type="button"
              variant="ghost"
              disabled={!form.formState.isDirty}
              onClick={() => form.reset(data.runtime)}
              className="w-full sm:w-auto"
            >
              {t('runtime.discard')}
            </Button>
            <Button type="submit" disabled={!form.formState.isDirty} className="w-full sm:w-auto">
              {t('runtime.saveChanges')}
            </Button>
          </div>
        </PanelCard>
      </form>

      <OutboundProxyCard proxyUrl={data.network.proxy_url_masked} onPatch={onPatch} />

      <PanelCard title={t('plugins.panelTitle')}>
        {PLUGIN_IDS.map((pluginId) => {
          const installed = data.plugins.some((p) => p.id === pluginId)
          const checked = pluginEnabled(pluginId)
          return (
            <div
              key={pluginId}
              className="grid items-start gap-3 py-3 sm:grid-cols-[minmax(0,220px)_1fr_auto] sm:gap-5 [&+&]:border-t [&+&]:border-[var(--line)]"
            >
              <div className="flex flex-col gap-[6px]">
                <Label htmlFor={`plugin-${pluginId}`}>{t(`plugins.${pluginId}.label`)}</Label>
                {installed ? (
                  <Badge variant="success">{t('plugins.live')}</Badge>
                ) : (
                  <Badge variant="outline">{pluginIntentStatus(pluginId)}</Badge>
                )}
              </div>
              <div className="flex flex-col gap-1 pt-[2px] text-[12.5px] leading-[1.55] text-[var(--text-dim)]">
                <span>{t(`plugins.${pluginId}.description`)}</span>
                {!installed ? (
                  <span className="text-[11.5px] text-[var(--text-muted)]">
                    <Trans i18nKey="plugins.notInstalled" ns="settings">
                      Plugin not yet installed — flipping the switch only records operator intent in{' '}
                      <code>config.json</code>.
                    </Trans>
                  </span>
                ) : null}
              </div>
              <div className="justify-self-start sm:self-center sm:justify-self-auto">
                <Switch
                  id={`plugin-${pluginId}`}
                  checked={checked}
                  onCheckedChange={(v) => patchPlugin(pluginId, v)}
                  data-testid={`plugin-switch-${pluginId}`}
                />
              </div>
            </div>
          )
        })}
      </PanelCard>

      <PanelCard title={t('database.panelTitle')}>
        <Field label={t('database.driver')}>
          <span className="font-mono text-[12.5px] text-[var(--text)]">{data.db.driver}</span>
        </Field>
        <Field label={t('database.host')}>
          <span className="font-mono text-[12.5px] text-[var(--text)]">{data.db.host || '—'}</span>
        </Field>
        <Field label={t('database.name')}>
          <span className="font-mono text-[12.5px] text-[var(--text)]">
            {data.db.database_name || '—'}
          </span>
        </Field>
      </PanelCard>

      <footer className="mt-6 flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-[var(--line)] pt-4 font-mono text-[11px] uppercase tracking-[0.06em] text-[var(--text-muted)]">
        <span>
          {t('footer.router')}{' '}
          <span className="text-[var(--text-dim)]">{data.system.router_version}</span>
        </span>
        <span aria-hidden>·</span>
        <span>
          {t('footer.git')}{' '}
          <span className="text-[var(--text-dim)]">{data.system.router_git_sha}</span>
        </span>
        <span aria-hidden>·</span>
        <span>{t('footer.built', { builtAt: data.system.router_built_at })}</span>
      </footer>
    </Canvas>
  )
}

function validateSettingsContract(data: SettingsPayload): Error | null {
  if (!Array.isArray(data.plugins)) {
    return new Error('settings payload contract violation: plugins must be an array')
  }
  if (!Array.isArray(data.plugin_intents)) {
    return new Error('settings payload contract violation: plugin_intents must be an array')
  }
  if (!Array.isArray(data.runtime.model_renames)) {
    return new Error('settings payload contract violation: runtime.model_renames must be an array')
  }
  if (typeof data.network?.proxy_configured !== 'boolean') {
    return new Error(
      'settings payload contract violation: network.proxy_configured must be a boolean',
    )
  }
  if (typeof data.network?.proxy_url_masked !== 'string') {
    return new Error(
      'settings payload contract violation: network.proxy_url_masked must be a string',
    )
  }
  for (const pluginId of PLUGIN_IDS) {
    const intent = data.plugin_intents.find((item) => item.id === pluginId)
    if (!intent) {
      return new Error(`settings payload contract violation: missing plugin_intents.${pluginId}`)
    }
    if (typeof intent.enabled !== 'boolean' || typeof intent.status !== 'string') {
      return new Error(`settings payload contract violation: invalid plugin_intents.${pluginId}`)
    }
  }
  return null
}

// OutboundProxyCard edits the single global egress proxy (Feature 009).
// The raw URL is write-only on the wire — a password typed once is never
// echoed back, so the input is prefilled with the server-masked form.
// The Test button probes the typed-in (not yet saved) URL through a
// fresh backend dial; the response never echoes the candidate.
function OutboundProxyCard({
  proxyUrl,
  onPatch,
}: {
  proxyUrl: string
  onPatch: (patch: SettingsPatch) => Promise<SettingsPayload>
}) {
  const { t } = useTranslation('settings')
  const [value, setValue] = useState(proxyUrl)
  const [busy, setBusy] = useState(false)
  const [testing, setTesting] = useState(false)

  const submit = useCallback(
    async (next: string) => {
      setBusy(true)
      try {
        const saved = await onPatch({ network: { proxy_url: next } })
        setValue(saved.network.proxy_url_masked)
        toast.success(t('toasts.proxySuccess'))
      } catch {
        toast.error(t('toasts.proxyFailed'))
      } finally {
        setBusy(false)
      }
    },
    [onPatch, t],
  )

  const test = useCallback(async () => {
    const candidate = value.trim()
    if (candidate === '') {
      return
    }
    setTesting(true)
    try {
      await api.post<{ reachable: boolean }>('/api/admin/settings/proxy/test', {
        proxy_url: candidate,
      })
      toast.success(t('toasts.proxyReachable'))
    } catch (err) {
      if (err instanceof RouterApiError) {
        const detail = (err.data as { detail?: string } | null)?.detail
        if (err.code === Err009InvalidProxyURL) {
          toast.error(
            detail ? t('toasts.proxyInvalidDetail', { detail }) : t('toasts.proxyInvalid'),
          )
        } else {
          toast.error(
            detail ? t('toasts.proxyUnreachableDetail', { detail }) : t('toasts.proxyUnreachable'),
          )
        }
      } else {
        toast.error(t('toasts.proxyTestFailed'))
      }
    } finally {
      setTesting(false)
    }
  }, [value, t])

  return (
    <PanelCard title={t('proxy.panelTitle')}>
      <Field htmlFor="network_proxy_url" label={t('proxy.label')} hint={t('proxy.hint')}>
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
          <Input
            id="network_proxy_url"
            className="max-w-md font-mono"
            placeholder="socks5://127.0.0.1:7890"
            autoComplete="off"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            data-testid="network-proxy-url"
          />
          <div className="flex gap-2">
            <Button
              type="button"
              variant="outline"
              disabled={busy || value.trim() === ''}
              onClick={() => submit(value.trim())}
            >
              {t('proxy.save')}
            </Button>
            <Button
              type="button"
              variant="secondary"
              disabled={testing || busy || value.trim() === ''}
              onClick={() => void test()}
              data-testid="network-proxy-test"
            >
              {testing ? t('proxy.testing') : t('proxy.test')}
            </Button>
          </div>
        </div>
      </Field>
    </PanelCard>
  )
}

function pluginIntentMap(data: SettingsPayload): Map<PluginIntentID, PluginIntent> {
  return new Map(
    PLUGIN_IDS.map((id) => [
      id,
      data.plugin_intents.find((intent) => intent.id === id) as PluginIntent,
    ]),
  )
}

function requirePluginIntent(
  intents: Map<PluginIntentID, PluginIntent>,
  id: PluginIntentID,
): PluginIntent {
  const intent = intents.get(id)
  if (!intent) {
    throw new Error(`settings payload contract violation: missing plugin_intents.${id}`)
  }
  return intent
}

function ToggleField({
  id,
  label,
  hint,
  checked,
  onChange,
}: {
  id: string
  label: string
  hint: string
  checked: boolean
  onChange: (v: boolean) => void
}) {
  return (
    <div className="grid items-start gap-3 py-3 sm:grid-cols-[minmax(0,180px)_1fr_auto] sm:gap-5 [&+&]:border-t [&+&]:border-[var(--line)]">
      <Label htmlFor={id} className="sm:pt-2">
        {label}
      </Label>
      <p className="text-[11.5px] leading-[1.55] text-[var(--text-muted)] sm:pt-2">{hint}</p>
      <div className="justify-self-start sm:self-center sm:justify-self-auto">
        <Switch id={id} checked={checked} onCheckedChange={onChange} data-testid={id} />
      </div>
    </div>
  )
}

function SettingsSkeleton() {
  const { t } = useTranslation('settings')
  return (
    <Canvas variant="narrow">
      <Stripe eyebrow={t('eyebrow')} />
      <div className="flex flex-col gap-4">
        <Skeleton className="h-48 w-full" />
        <Skeleton className="h-56 w-full" />
        <Skeleton className="h-32 w-full" />
      </div>
    </Canvas>
  )
}
