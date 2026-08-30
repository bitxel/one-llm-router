import { zodResolver } from '@hookform/resolvers/zod'
import { Plus, Trash2 } from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { useFieldArray, useForm } from 'react-hook-form'
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

const RuntimeFormSchema = z.object({
  log_client_request_body: z.boolean(),
  log_upstream_request_body: z.boolean(),
  log_upstream_response_body: z.boolean(),
  log_retention_days: z.coerce.number().int().min(1).max(365),
  log_level: z.enum(['debug', 'info', 'warn', 'error']),
  model_renames: z
    .array(
      z.object({
        from: z.string().trim().min(1).max(128),
        to: z.string().trim().min(1).max(128),
      }),
    )
    .max(32),
})
type RuntimeForm = z.infer<typeof RuntimeFormSchema>

const PLUGIN_INTENTS = [
  {
    id: 'admin_auth',
    label: 'Admin authentication',
    description:
      'Records whether admin authentication should be enabled once that plugin is available. This build still uses the trusted-network admin boundary.',
  },
  {
    id: 'client_keys',
    label: 'Client API keys',
    description:
      'Records whether per-caller `/v1/*` keys should be enforced once that plugin is available.',
  },
] as const

type PluginIntentID = (typeof PLUGIN_INTENTS)[number]['id']

export function AdminSettings() {
  const settings = useSettings()
  const update = useUpdateSettings()

  if (settings.isLoading) {
    return <SettingsSkeleton />
  }
  if (settings.isError) {
    return (
      <Canvas variant="narrow">
        <Stripe eyebrow="Settings" />
        <ErrorBanner error={settings.error} title="Settings failed to load" />
      </Canvas>
    )
  }
  if (!settings.data) return null

  const settingsContractError = validateSettingsContract(settings.data)
  if (settingsContractError) {
    return (
      <Canvas variant="narrow">
        <Stripe eyebrow="Settings" />
        <ErrorBanner error={settingsContractError} title="Settings response is invalid" />
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
        toast.success('Runtime settings updated')
      } catch (err) {
        toast.error('Update failed — see the banner for details')
        throw err
      }
    },
    [onPatch],
  )

  const patchPlugin = useCallback(
    async (id: 'admin_auth' | 'client_keys', enabled: boolean) => {
      try {
        await onPatch({ plugins: { [id]: { enabled } } })
        toast.success(`Plugin intent recorded: ${id}=${enabled}`)
      } catch (err) {
        toast.error('Could not update plugin intent')
        throw err
      }
    },
    [onPatch],
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
      <Stripe eyebrow="Settings" />
      <h1 className="mb-3 max-w-[22ch]">
        Tune without a <strong>restart.</strong>
      </h1>
      <PageIntro className="mb-7">
        Observability toggles apply on the next <code>/v1/*</code> request. Plugin intents persist
        to <code>config.json</code>; if a plugin is not installed in this build, the switch records
        operator preference only.
      </PageIntro>

      {form.formState.errors.root ? (
        <div className="mb-4">
          <ErrorBanner
            error={new Error(form.formState.errors.root.message ?? 'Validation failed')}
          />
        </div>
      ) : null}

      <form onSubmit={form.handleSubmit(submit)} data-testid="runtime-form">
        <PanelCard title="Runtime" meta="applied on next /v1/*">
          <ToggleField
            id="log_client_request_body"
            label="Log client request bodies"
            hint="Attach payloads received from clients to request_records."
            checked={form.watch('log_client_request_body')}
            onChange={(v) => form.setValue('log_client_request_body', v, { shouldDirty: true })}
          />
          <ToggleField
            id="log_upstream_request_body"
            label="Log upstream request bodies"
            hint="Attach payloads sent to upstream providers after router/provider adaptation."
            checked={form.watch('log_upstream_request_body')}
            onChange={(v) => form.setValue('log_upstream_request_body', v, { shouldDirty: true })}
          />
          <ToggleField
            id="log_upstream_response_body"
            label="Log upstream response bodies"
            hint="Attach payloads returned by upstream providers to request_records."
            checked={form.watch('log_upstream_response_body')}
            onChange={(v) => form.setValue('log_upstream_response_body', v, { shouldDirty: true })}
          />
          <Field
            htmlFor="log_retention_days"
            label="Log retention"
            hint="Days to keep request records and logs before automatic cleanup. 1–365."
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
              <span className="font-mono text-[11.5px] text-[var(--text-muted)]">days</span>
            </div>
            {form.formState.errors.log_retention_days ? (
              <p className="mt-2 text-[11.5px] text-[var(--err)]">
                {form.formState.errors.log_retention_days.message}
              </p>
            ) : null}
          </Field>
          <Field
            htmlFor="log_level"
            label="Log level"
            hint="Minimum slog level emitted by the router."
          >
            <div className="w-40">
              <Select
                value={form.watch('log_level')}
                onValueChange={(v) =>
                  form.setValue('log_level', v as RuntimeForm['log_level'], { shouldDirty: true })
                }
              >
                <SelectTrigger id="log_level">
                  <SelectValue placeholder="Select" />
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
          <Field
            label="Model renames"
            hint="Exact client model ids rewritten before upstream forwarding."
          >
            <div className="flex flex-col gap-3">
              {modelRenameFields.fields.map((field, index) => (
                <div
                  key={field.id}
                  className="grid gap-2 rounded-[2px] border border-[var(--line)] bg-[var(--panel-2)] p-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]"
                >
                  <div className="flex flex-col gap-1">
                    <Label htmlFor={`model_renames.${index}.from`}>From</Label>
                    <Input
                      id={`model_renames.${index}.from`}
                      {...form.register(`model_renames.${index}.from`)}
                    />
                    {form.formState.errors.model_renames?.[index]?.from ? (
                      <p className="text-[11.5px] text-[var(--err)]">
                        {form.formState.errors.model_renames[index]?.from?.message}
                      </p>
                    ) : null}
                  </div>
                  <div className="flex flex-col gap-1">
                    <Label htmlFor={`model_renames.${index}.to`}>To</Label>
                    <Input
                      id={`model_renames.${index}.to`}
                      {...form.register(`model_renames.${index}.to`)}
                    />
                    {form.formState.errors.model_renames?.[index]?.to ? (
                      <p className="text-[11.5px] text-[var(--err)]">
                        {form.formState.errors.model_renames[index]?.to?.message}
                      </p>
                    ) : null}
                  </div>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    aria-label="Remove model rename"
                    onClick={() => modelRenameFields.remove(index)}
                    className="self-end justify-self-start sm:justify-self-end"
                  >
                    <Trash2 />
                  </Button>
                </div>
              ))}
              {form.formState.errors.model_renames?.root ? (
                <p className="text-[11.5px] text-[var(--err)]">
                  {form.formState.errors.model_renames.root.message}
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
                  Add rename
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
              Discard
            </Button>
            <Button type="submit" disabled={!form.formState.isDirty} className="w-full sm:w-auto">
              Save changes
            </Button>
          </div>
        </PanelCard>
      </form>

      <OutboundProxyCard proxyUrl={data.network.proxy_url_masked} onPatch={onPatch} />

      <PanelCard title="Plugin intents" meta="persisted to config.json" metaMuted>
        {PLUGIN_INTENTS.map((plugin) => {
          const installed = data.plugins.some((p) => p.id === plugin.id)
          const checked = pluginEnabled(plugin.id)
          return (
            <div
              key={plugin.id}
              className="grid items-start gap-3 py-3 sm:grid-cols-[minmax(0,220px)_1fr_auto] sm:gap-5 [&+&]:border-t [&+&]:border-[var(--line)]"
            >
              <div className="flex flex-col gap-[6px]">
                <Label htmlFor={`plugin-${plugin.id}`}>{plugin.label}</Label>
                {installed ? (
                  <Badge variant="success">live</Badge>
                ) : (
                  <Badge variant="outline">{pluginIntentStatus(plugin.id)}</Badge>
                )}
              </div>
              <div className="flex flex-col gap-1 pt-[2px] text-[12.5px] leading-[1.55] text-[var(--text-dim)]">
                <span>{plugin.description}</span>
                {!installed ? (
                  <span className="text-[11.5px] text-[var(--text-muted)]">
                    Plugin not yet installed — flipping the switch only records operator intent in{' '}
                    <code>config.json</code>.
                  </span>
                ) : null}
              </div>
              <div className="justify-self-start sm:self-center sm:justify-self-auto">
                <Switch
                  id={`plugin-${plugin.id}`}
                  checked={checked}
                  onCheckedChange={(v) => patchPlugin(plugin.id, v)}
                  data-testid={`plugin-switch-${plugin.id}`}
                />
              </div>
            </div>
          )
        })}
      </PanelCard>

      <PanelCard title="Database" meta="read-only" metaMuted>
        <Field label="Driver">
          <span className="font-mono text-[12.5px] text-[var(--text)]">{data.db.driver}</span>
        </Field>
        <Field label="Host">
          <span className="font-mono text-[12.5px] text-[var(--text)]">{data.db.host || '—'}</span>
        </Field>
        <Field label="Database">
          <span className="font-mono text-[12.5px] text-[var(--text)]">
            {data.db.database_name || '—'}
          </span>
        </Field>
      </PanelCard>

      <footer className="mt-6 flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-[var(--line)] pt-4 font-mono text-[11px] uppercase tracking-[0.06em] text-[var(--text-muted)]">
        <span>
          router <span className="text-[var(--text-dim)]">{data.system.router_version}</span>
        </span>
        <span aria-hidden>·</span>
        <span>
          git <span className="text-[var(--text-dim)]">{data.system.router_git_sha}</span>
        </span>
        <span aria-hidden>·</span>
        <span>built {data.system.router_built_at}</span>
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
  for (const plugin of PLUGIN_INTENTS) {
    const intent = data.plugin_intents.find((item) => item.id === plugin.id)
    if (!intent) {
      return new Error(`settings payload contract violation: missing plugin_intents.${plugin.id}`)
    }
    if (typeof intent.enabled !== 'boolean' || typeof intent.status !== 'string') {
      return new Error(`settings payload contract violation: invalid plugin_intents.${plugin.id}`)
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
  const [value, setValue] = useState(proxyUrl)
  const [busy, setBusy] = useState(false)
  const [testing, setTesting] = useState(false)

  const submit = useCallback(
    async (next: string) => {
      setBusy(true)
      try {
        const saved = await onPatch({ network: { proxy_url: next } })
        setValue(saved.network.proxy_url_masked)
        toast.success('Outbound proxy updated')
      } catch {
        toast.error('Could not update the outbound proxy')
      } finally {
        setBusy(false)
      }
    },
    [onPatch],
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
      toast.success('Proxy reachable — the upstream responded through it')
    } catch (err) {
      if (err instanceof RouterApiError) {
        const detail = (err.data as { detail?: string } | null)?.detail
        if (err.code === Err009InvalidProxyURL) {
          toast.error(detail ? `Invalid proxy URL — ${detail}` : 'Invalid proxy URL')
        } else {
          toast.error(detail ? `Proxy unreachable — ${detail}` : 'Proxy test failed')
        }
      } else {
        toast.error('Proxy test failed')
      }
    } finally {
      setTesting(false)
    }
  }, [value])

  return (
    <PanelCard title="Outbound proxy">
      <Field
        htmlFor="network_proxy_url"
        label="Proxy URL"
        hint="Single global egress proxy (http/https/socks5/socks5h), e.g. socks5://127.0.0.1:7890"
      >
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
              Save proxy
            </Button>
            <Button
              type="button"
              variant="secondary"
              disabled={testing || busy || value.trim() === ''}
              onClick={() => void test()}
              data-testid="network-proxy-test"
            >
              {testing ? 'Testing…' : 'Test'}
            </Button>
          </div>
        </div>
      </Field>
    </PanelCard>
  )
}

function pluginIntentMap(data: SettingsPayload): Map<PluginIntentID, PluginIntent> {
  return new Map(
    PLUGIN_INTENTS.map((plugin) => [
      plugin.id,
      data.plugin_intents.find((intent) => intent.id === plugin.id) as PluginIntent,
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
  return (
    <Canvas variant="narrow">
      <Stripe eyebrow="Settings" />
      <div className="flex flex-col gap-4">
        <Skeleton className="h-48 w-full" />
        <Skeleton className="h-56 w-full" />
        <Skeleton className="h-32 w-full" />
      </div>
    </Canvas>
  )
}
