import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from '@tanstack/react-router'
import { Download, Pause, Play, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'

import { Canvas, Field, PanelCard, Stripe } from '@/components/neo'
import { ErrorBanner } from '@/components/shared/ErrorBanner'
import { Button } from '@/components/ui/button'
import { accountsExportAuthJson } from '@/generated/openapi'
import { api } from '@/lib/api-client'
import { planTypeLabel } from '@/lib/plan-label'
import { callAdminAttachment } from '@/lib/router-api'
import { strings } from './detail.strings'
import { adminAccountDetailQueryKey, invalidateAdminAccountQueries } from './query-keys'

type AuthMethod = 'api_key' | 'oauth_browser' | 'oauth_device' | 'oauth_import'

interface AccountDetail {
  id: number
  name: string
  provider: string
  auth_method: AuthMethod
  status: string
  base_url?: string | null
  created_at: string
  updated_at: string
  email?: string | null
  plan_type?: string | null
  plan_type_label?: string | null
  chatgpt_account_id?: string | null
  last_refresh?: string | null
  access_expires_at?: string | null
  primary_remaining_percent?: number | null
  secondary_remaining_percent?: number | null
  usage_updated_at?: string | null
}

const pauseAccountButtonClassName =
  'border-[var(--line-3)] bg-[var(--panel)] text-[var(--text-muted)] [box-shadow:var(--shadow-btn)] hover:border-[var(--err)] hover:bg-[var(--err-soft)] hover:text-[var(--err)] focus-visible:border-[var(--err)] focus-visible:text-[var(--err)]'

const activateAccountButtonClassName =
  'border-[var(--line-3)] bg-[var(--panel)] text-[var(--text-muted)] [box-shadow:var(--shadow-btn)] hover:border-[var(--ok)] hover:bg-[var(--ok-soft)] hover:text-[var(--ok)] focus-visible:border-[var(--ok)] focus-visible:text-[var(--ok)]'

export function AdminAccountDetail() {
  const params = useParams({ strict: false })
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [actionError, setActionError] = useState<unknown>(null)
  const [actionErrorTitle, setActionErrorTitle] = useState<string | undefined>(undefined)
  const [isExporting, setIsExporting] = useState(false)
  const [isActivating, setIsActivating] = useState(false)
  const [isDisabling, setIsDisabling] = useState(false)
  const [deleteConfirming, setDeleteConfirming] = useState(false)
  const [isDeleting, setIsDeleting] = useState(false)

  const rawAccountID = typeof params.accountId === 'string' ? params.accountId : ''
  const accountID = Number(rawAccountID)
  const hasValidID = Number.isInteger(accountID) && accountID > 0

  const accountQuery = useQuery({
    queryKey: adminAccountDetailQueryKey(rawAccountID),
    queryFn: () => api.get<AccountDetail>(`/api/admin/accounts/${rawAccountID}`),
    enabled: hasValidID,
    staleTime: 10_000,
  })

  const account = accountQuery.data ?? null
  const oauthExportable = account !== null && account.auth_method !== 'api_key'
  const showOAuthMetadata = account !== null && account.auth_method !== 'api_key'
  const planLabel =
    account?.plan_type_label ||
    (account?.plan_type == null ? strings.empty : planTypeLabel(account.plan_type) || strings.empty)
  const accountStatusMeta = account ? statusMetaFor(account.status) : null
  const accountIsActive = account ? isActiveStatus(account.status) : false

  async function handleActivate() {
    if (!account || accountIsActive || isActivating) {
      return
    }

    setIsActivating(true)
    setActionError(null)
    setActionErrorTitle(undefined)
    try {
      await api.post<Record<string, string>>(`/api/admin/accounts/${account.id}/enable`, {})
      await invalidateAdminAccountQueries(queryClient, account.id)
      toast.success(strings.toasts.activateSuccess)
    } catch (error) {
      setActionError(error)
      setActionErrorTitle(strings.activateTitle)
      toast.error(strings.toasts.activateFailed)
    } finally {
      setIsActivating(false)
    }
  }

  async function handleExport() {
    if (!account || account.auth_method === 'api_key' || isExporting) {
      return
    }

    setIsExporting(true)
    setActionError(null)
    setActionErrorTitle(undefined)
    try {
      const attachment = await callAdminAttachment(
        accountsExportAuthJson({
          path: { id: account.id },
          parseAs: 'text',
        }),
      )
      triggerBlobDownload(attachment.blob, attachment.filename)
      toast.success(strings.toasts.exportSuccess)
    } catch (error) {
      setActionError(error)
      setActionErrorTitle(strings.exportTitle)
      toast.error(strings.toasts.exportFailed)
    } finally {
      setIsExporting(false)
    }
  }

  async function handleDisable() {
    if (!account || !accountIsActive || isDisabling) {
      return
    }

    setIsDisabling(true)
    setActionError(null)
    setActionErrorTitle(undefined)
    try {
      await api.post<Record<string, string>>(`/api/admin/accounts/${account.id}/disable`, {})
      await invalidateAdminAccountQueries(queryClient, account.id)
      toast.success(strings.toasts.disableSuccess)
    } catch (error) {
      setActionError(error)
      setActionErrorTitle(strings.disableTitle)
      toast.error(strings.toasts.disableFailed)
    } finally {
      setIsDisabling(false)
    }
  }

  async function handleDelete() {
    if (!account || isDeleting) {
      return
    }
    if (!deleteConfirming) {
      setDeleteConfirming(true)
      setActionError(null)
      setActionErrorTitle(undefined)
      return
    }

    setIsDeleting(true)
    setActionError(null)
    setActionErrorTitle(undefined)
    try {
      await api.post<Record<string, never>>(`/api/admin/accounts/${account.id}/delete`, {})
      await invalidateAdminAccountQueries(queryClient, account.id)
      toast.success(strings.toasts.deleteSuccess)
      await navigate({ to: '/admin/accounts' })
    } catch (error) {
      setActionError(error)
      setActionErrorTitle(strings.deleteTitle)
      toast.error(strings.toasts.deleteFailed)
    } finally {
      setIsDeleting(false)
    }
  }

  return (
    <Canvas variant="wide">
      <Stripe eyebrow={strings.title}>{strings.parentTitle}</Stripe>
      <h1 className="mb-3 max-w-[18ch]">
        Account <strong>{hasValidID ? rawAccountID : strings.empty}</strong>
      </h1>
      <p className="mb-7 max-w-[70ch] text-[14px] leading-[1.65] text-[var(--text-dim)]">
        {strings.subtitle}
      </p>

      {!hasValidID ? (
        <ErrorBanner
          error={new Error(strings.invalidIdDetail)}
          title={strings.invalidIdTitle}
          className="mb-4"
        />
      ) : null}

      {accountQuery.isError ? <ErrorBanner error={accountQuery.error} className="mb-4" /> : null}
      {actionError ? (
        <ErrorBanner error={actionError} className="mb-4" title={actionErrorTitle} />
      ) : null}

      {accountQuery.isLoading ? (
        <PanelCard title={strings.summaryTitle} meta={strings.loading}>
          <div className="text-[13px] text-[var(--text-dim)]" data-testid="account-detail-loading">
            {strings.loading}
          </div>
        </PanelCard>
      ) : null}

      {account ? (
        <div data-testid="account-detail-view" className="space-y-3">
          <PanelCard
            title={strings.summaryTitle}
            meta={accountStatusMeta?.label}
            metaClassName={accountStatusMeta?.className}
            metaTestId="account-summary-status"
            data-testid="account-summary-card"
          >
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div className="space-y-2">
                <div className="text-[22px] leading-[1.2] font-semibold text-[var(--text)]">
                  {accountIdentity(account)}
                </div>
                <div className="font-mono text-[12px] leading-[1.45] text-[var(--text-muted)]">
                  {account.provider}
                </div>
                <div className="text-[12.5px] leading-[1.45] text-[var(--text-dim)]">
                  {renderAuthMethodLabel(account.auth_method)}
                </div>
              </div>

              <div className="flex flex-wrap items-center gap-2">
                {accountIsActive ? (
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => {
                      void handleDisable()
                    }}
                    disabled={isDisabling}
                    data-testid="disable-account-button"
                    aria-label={isDisabling ? strings.actions.disabling : strings.actions.disable}
                    title={isDisabling ? strings.actions.disabling : strings.actions.disable}
                    className={pauseAccountButtonClassName}
                  >
                    <Pause />
                  </Button>
                ) : (
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => {
                      void handleActivate()
                    }}
                    disabled={isActivating}
                    data-testid="activate-account-button"
                    aria-label={
                      isActivating ? strings.actions.activating : strings.actions.activate
                    }
                    title={isActivating ? strings.actions.activating : strings.actions.activate}
                    className={activateAccountButtonClassName}
                  >
                    <Play />
                  </Button>
                )}
                {oauthExportable ? (
                  <Button
                    onClick={() => {
                      void handleExport()
                    }}
                    disabled={isExporting}
                    data-testid="export-auth-json"
                  >
                    <Download />
                    {isExporting ? strings.actions.exporting : strings.actions.export}
                  </Button>
                ) : null}
              </div>
            </div>
          </PanelCard>

          <div className="grid gap-3 md:grid-cols-2">
            <MetadataPanel
              title={strings.summaryTitle}
              meta={accountStatusMeta?.label ?? strings.empty}
              metaClassName={accountStatusMeta?.className}
              rows={[
                [strings.labels.id, String(account.id)],
                [strings.labels.provider, account.provider],
                [strings.labels.authMethod, renderAuthMethodLabel(account.auth_method)],
                [strings.labels.status, account.status],
                [strings.labels.baseURL, account.base_url ?? strings.empty],
              ]}
            />

            {showOAuthMetadata ? (
              <MetadataPanel
                title={strings.metadataTitle}
                testID="account-oauth-metadata"
                rows={[
                  [strings.labels.email, account.email ?? strings.empty],
                  [strings.labels.plan, planLabel],
                  [strings.labels.primaryQuota, formatPercent(account.primary_remaining_percent)],
                  [strings.labels.secondaryQuota, formatPercent(account.secondary_remaining_percent)],
                  [strings.labels.quotaUpdatedAt, formatTimestamp(account.usage_updated_at)],
                  [strings.labels.chatgptAccountID, account.chatgpt_account_id ?? strings.empty],
                  [strings.labels.lastRefresh, formatTimestamp(account.last_refresh)],
                  [strings.labels.accessExpiresAt, formatTimestamp(account.access_expires_at)],
                ]}
              />
            ) : null}
          </div>

          <PanelCard title={strings.deleteTitle}>
            <div className="flex flex-wrap items-center justify-between gap-3">
              <p className="min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap text-[13px] leading-[1.6] text-[var(--text-dim)]">
                {strings.deleteHint}
              </p>
              <Button
                variant="destructive"
                onClick={() => {
                  void handleDelete()
                }}
                disabled={isDeleting}
                data-testid="delete-account-button"
              >
                <Trash2 />
                {isDeleting
                  ? strings.actions.deleting
                  : deleteConfirming
                    ? strings.actions.confirmDelete
                    : strings.actions.delete}
              </Button>
            </div>
          </PanelCard>
        </div>
      ) : null}
    </Canvas>
  )
}

function MetadataPanel({
  title,
  meta,
  metaClassName,
  rows,
  testID,
}: {
  title: string
  meta?: string
  metaClassName?: string
  rows: Array<[label: string, value: string]>
  testID?: string
}) {
  return (
    <PanelCard title={title} meta={meta} metaClassName={metaClassName} data-testid={testID}>
      <div>
        {rows.map(([label, value]) => (
          <Field key={label} label={label}>
            <div className="text-[13px] leading-[1.6] text-[var(--text)]">{value}</div>
          </Field>
        ))}
      </div>
    </PanelCard>
  )
}

function renderAuthMethodLabel(method: AuthMethod): string {
  return strings.authMethod[method] ?? method
}

function accountIdentity(account: AccountDetail): string {
  return account.email || account.name
}

function statusMetaFor(status: string): { label: string; className: string } {
  if (isActiveStatus(status)) {
    return {
      label: strings.accountStatus.active,
      className: 'border-[var(--ok)] bg-[var(--ok-soft)] text-[var(--ok)]',
    }
  }

  return {
    label: strings.accountStatus.inactive,
    className: 'border-[var(--err)] bg-[var(--err-soft)] text-[var(--err)]',
  }
}

function isActiveStatus(status: string): boolean {
  return status.toLowerCase() === 'active'
}

function formatTimestamp(value: string | null | undefined): string {
  if (!value) {
    return strings.empty
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  return date.toISOString().replace('.000Z', 'Z')
}

function formatPercent(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) {
    return strings.empty
  }
  const formatter = new Intl.NumberFormat('en-US', { maximumFractionDigits: 2 })
  return `${formatter.format(value)}%`
}

function triggerBlobDownload(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  anchor.style.display = 'none'
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  window.setTimeout(() => {
    URL.revokeObjectURL(url)
  }, 0)
}
