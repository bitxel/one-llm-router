import { zodResolver } from '@hookform/resolvers/zod'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { type SubmitHandler, useForm } from 'react-hook-form'
import { toast } from 'sonner'
import { z } from 'zod'

import { Field, PanelCard } from '@/components/neo'
import { ErrorBanner } from '@/components/shared/ErrorBanner'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { BASE_URL_MAX_LEN, CAPABILITY_OPTIONS, isValidBaseURL } from '@/lib/account-form'
import { api } from '@/lib/api-client'
import { strings } from './detail-edit.strings'
import { invalidateAdminAccountQueries } from './query-keys'

const EditSchema = z.object({
  name: z
    .string()
    .trim()
    .min(1, strings.validation.nameRequired)
    .max(64, strings.validation.nameMax),
  api_key: z.string().trim().max(256, strings.validation.apiKeyMax),
  base_url: z
    .string()
    .trim()
    .max(BASE_URL_MAX_LEN, strings.validation.baseURLMax)
    .refine((value) => value === '' || isValidBaseURL(value), strings.validation.baseURLShape),
  capabilities: z.array(z.string()).default([]),
})

type EditForm = z.infer<typeof EditSchema>

interface ApiKeyAccountRow {
  id: number
  name: string
  base_url?: string | null
  capabilities?: string[] | null
}

export function ApiKeyEditPanel({ account }: { account: ApiKeyAccountRow }) {
  const queryClient = useQueryClient()
  const [screenError, setScreenError] = useState<unknown>(null)
  const form = useForm<EditForm>({
    resolver: zodResolver(EditSchema),
    defaultValues: {
      name: account.name,
      api_key: '',
      base_url: account.base_url ?? '',
      capabilities: account.capabilities ?? [],
    },
    mode: 'onTouched',
  })

  const submit: SubmitHandler<EditForm> = async (values) => {
    setScreenError(null)
    try {
      await api.post<Record<string, unknown>>(`/api/admin/accounts/${account.id}/update`, {
        name: values.name,
        api_key: values.api_key || undefined,
        base_url: values.base_url,
        capabilities: values.capabilities,
      })
      await invalidateAdminAccountQueries(queryClient, account.id)
      toast.success(strings.toasts.updateSuccess)
      form.reset({
        name: values.name,
        api_key: '',
        base_url: values.base_url,
        capabilities: values.capabilities,
      })
    } catch (error) {
      setScreenError(error)
      toast.error(strings.toasts.updateFailed)
    }
  }

  return (
    <PanelCard title={strings.panelTitle}>
      <p className="mb-4 max-w-[70ch] text-[12.5px] leading-[1.6] text-[var(--text-dim)]">
        {strings.panelHint}
      </p>
      {screenError ? <ErrorBanner error={screenError} className="mb-4" /> : null}

      <form onSubmit={form.handleSubmit(submit)} data-testid="apikey-edit-form">
        <Field htmlFor="edit-name" label={strings.labels.name} hint={strings.hints.name}>
          <Input
            id="edit-name"
            data-testid="edit-name-input"
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

        <Field htmlFor="edit-api-key" label={strings.labels.apiKey} hint={strings.hints.apiKey}>
          <Input
            id="edit-api-key"
            data-testid="edit-api-key-input"
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

        <Field htmlFor="edit-base-url" label={strings.labels.baseURL} hint={strings.hints.baseURL}>
          <Input
            id="edit-base-url"
            placeholder={strings.placeholders.baseURL}
            data-testid="edit-base-url-input"
            aria-invalid={Boolean(form.formState.errors.base_url)}
            {...form.register('base_url')}
          />
          {form.formState.errors.base_url ? (
            <p className="mt-2 text-[11.5px] text-[var(--err)]">
              {form.formState.errors.base_url.message}
            </p>
          ) : null}
        </Field>

        <Field label={strings.labels.capabilities} hint={strings.hints.capabilities}>
          <div className="flex flex-col gap-2">
            {CAPABILITY_OPTIONS.map((opt) => (
              <label key={opt.value} className="flex cursor-pointer items-center gap-2 text-[13px]">
                <Checkbox
                  data-testid={`edit-capability-${opt.value}`}
                  checked={form.watch('capabilities')?.includes(opt.value)}
                  onCheckedChange={(checked: boolean) => {
                    const current = form.getValues('capabilities') || []
                    if (checked) {
                      form.setValue('capabilities', [...current, opt.value])
                    } else {
                      form.setValue(
                        'capabilities',
                        current.filter((value) => value !== opt.value),
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
            data-testid="edit-account-submit"
          >
            {form.formState.isSubmitting ? strings.actions.saving : strings.actions.save}
          </Button>
        </div>
      </form>
    </PanelCard>
  )
}
