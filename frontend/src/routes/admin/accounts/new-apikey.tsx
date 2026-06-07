import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { type SubmitHandler, useForm } from 'react-hook-form'
import { toast } from 'sonner'
import { z } from 'zod'

import { Canvas, Field, PanelCard, Stripe } from '@/components/neo'
import { ErrorBanner } from '@/components/shared/ErrorBanner'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { api } from '@/lib/api-client'
import { strings } from './new-apikey.strings'
import { invalidateAdminAccountQueries } from './query-keys'

const CAPABILITY_OPTIONS = [
  { value: 'op.openai.chat_completions', label: 'Chat Completions' },
  { value: 'op.openai.responses', label: 'Responses' },
] as const

const BASE_URL_MAX_LEN = 256

const APIKeyAccountSchema = z.object({
  name: z
    .string()
    .trim()
    .min(1, strings.validation.nameRequired)
    .max(64)
    .regex(/^[A-Za-z0-9_-]+$/, strings.validation.nameShape),
  api_key: z
    .string()
    .trim()
    .min(1, strings.validation.apiKeyRequired)
    .max(256, strings.validation.apiKeyMax),
  base_url: z
    .string()
    .trim()
    .max(BASE_URL_MAX_LEN, strings.validation.baseURLMax)
    .refine((value) => value === '' || isValidBaseURL(value), strings.validation.baseURLShape),
  capabilities: z.array(z.string()).default([]),
})

type APIKeyAccountForm = z.infer<typeof APIKeyAccountSchema>

interface CreatedAccount {
  id: number
  name: string
  provider: string
  base_url?: string | null
  status: string
  created_at: string
  updated_at: string
}

export function AdminAccountsNewAPIKey() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [screenError, setScreenError] = useState<unknown>(null)
  const form = useForm<APIKeyAccountForm>({
    resolver: zodResolver(APIKeyAccountSchema),
    defaultValues: {
      name: '',
      api_key: '',
      base_url: '',
      capabilities: [],
    },
    mode: 'onTouched',
  })

  const submit: SubmitHandler<APIKeyAccountForm> = async (values) => {
    setScreenError(null)
    try {
      const account = await api.post<CreatedAccount>('/api/admin/accounts', {
        name: values.name,
        provider: 'openai',
        auth_method: 'api_key',
        api_key: values.api_key,
        base_url: values.base_url || undefined,
        capabilities: values.capabilities.length > 0 ? values.capabilities : undefined,
      })
      if (!Number.isInteger(account.id) || account.id <= 0) {
        throw new Error(strings.validation.missingAccountID)
      }
      await invalidateAdminAccountQueries(queryClient, account.id)
      toast.success(strings.toasts.createSuccess)
      await navigate({
        to: '/admin/accounts/$accountId',
        params: { accountId: String(account.id) },
      })
    } catch (error) {
      setScreenError(error)
      toast.error(strings.toasts.createFailed)
    }
  }

  return (
    <Canvas variant="narrow">
      <Stripe eyebrow={strings.eyebrow}>{strings.stripe}</Stripe>
      <div className="mb-8 max-w-[70ch]">
        <h1 className="mb-3 max-w-[18ch] text-balance">
          {strings.titleLead} <strong>{strings.titleStrong}</strong>
        </h1>
        <p className="max-w-[64ch] text-[14px] leading-[1.7] text-[var(--text-dim)]">
          {strings.intro}
        </p>
      </div>

      {screenError ? (
        <ErrorBanner error={screenError} title="API-key account creation failed" className="mb-4" />
      ) : null}

      <form onSubmit={form.handleSubmit(submit)} data-testid="apikey-create-form">
        <PanelCard title={strings.panelTitle}>
          <Field htmlFor="apikey-name" label={strings.labels.name} hint={strings.hints.name}>
            <Input
              id="apikey-name"
              data-testid="apikey-name-input"
              autoComplete="off"
              spellCheck={false}
              aria-invalid={Boolean(form.formState.errors.name)}
              {...form.register('name')}
            />
            {form.formState.errors.name ? (
              <p className="mt-2 text-[11.5px] text-[var(--err)]">
                {form.formState.errors.name.message}
              </p>
            ) : null}
          </Field>

          <Field
            htmlFor="apikey-provider"
            label={strings.labels.provider}
            hint={strings.hints.provider}
          >
            <Input
              id="apikey-provider"
              value="openai"
              readOnly
              disabled
              aria-readonly="true"
              data-testid="apikey-provider-input"
            />
          </Field>

          <Field htmlFor="apikey-api-key" label={strings.labels.apiKey} hint={strings.hints.apiKey}>
            <Input
              id="apikey-api-key"
              data-testid="apikey-api-key-input"
              type="password"
              autoComplete="off"
              aria-invalid={Boolean(form.formState.errors.api_key)}
              {...form.register('api_key')}
            />
            {form.formState.errors.api_key ? (
              <p className="mt-2 text-[11.5px] text-[var(--err)]">
                {form.formState.errors.api_key.message}
              </p>
            ) : null}
          </Field>

          <Field
            htmlFor="apikey-base-url"
            label={
              <>
                {strings.labels.baseURL}{' '}
                <span className="font-normal text-[var(--text-muted)]">(optional)</span>
              </>
            }
            hint={strings.hints.baseURL}
          >
            <Input
              id="apikey-base-url"
              placeholder={strings.placeholders.baseURL}
              data-testid="apikey-base-url-input"
              aria-invalid={Boolean(form.formState.errors.base_url)}
              {...form.register('base_url')}
            />
            {form.formState.errors.base_url ? (
              <p className="mt-2 text-[11.5px] text-[var(--err)]">
                {form.formState.errors.base_url.message}
              </p>
            ) : null}
          </Field>

          <Field label="Capabilities" hint="API operations this account can handle. Leave empty for none.">
            <div className="flex flex-col gap-2">
              {CAPABILITY_OPTIONS.map((opt) => (
                <label
                  key={opt.value}
                  className="flex items-center gap-2 text-[13px] cursor-pointer"
                >
                  <Checkbox
                    data-testid={`capability-${opt.value}`}
                    checked={form.watch('capabilities')?.includes(opt.value)}
                    onCheckedChange={(checked: boolean) => {
                      const current = form.getValues('capabilities') || []
                      if (checked) {
                        form.setValue('capabilities', [...current, opt.value])
                      } else {
                        form.setValue(
                          'capabilities',
                          current.filter((v) => v !== opt.value),
                        )
                      }
                    }}
                  />
                  <span>{opt.label}</span>
                </label>
              ))}
            </div>
          </Field>

          <div className="mt-1 flex flex-wrap items-center justify-between gap-3 border-t border-[var(--line)] pt-4">
            <Button
              type="submit"
              disabled={form.formState.isSubmitting}
              data-testid="apikey-submit"
            >
              {form.formState.isSubmitting ? strings.loading : strings.actions.create}
            </Button>
          </div>
        </PanelCard>
      </form>
    </Canvas>
  )
}

function isValidBaseURL(value: string): boolean {
  try {
    const parsed = new URL(value)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return false
    if (!parsed.host) return false
    return parsed.search === '' && parsed.hash === ''
  } catch {
    return false
  }
}
