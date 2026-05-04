import { zodResolver } from '@hookform/resolvers/zod'
import { useCallback, useEffect } from 'react'
import { useForm } from 'react-hook-form'
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

const RuntimeFormSchema = z.object({
  log_client_request_body: z.boolean(),
  log_upstream_request_body: z.boolean(),
  log_upstream_response_body: z.boolean(),
  log_retention_days: z.coerce.number().int().min(1).max(365),
  log_level: z.enum(['debug', 'info', 'warn', 'error']),
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
