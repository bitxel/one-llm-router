import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from '@tanstack/react-router'
import { Download, Pause, Play, RefreshCw, Trash2, X } from 'lucide-react'
import { type ReactNode, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Canvas, Field, PanelCard, Stripe } from '@/components/neo'
import { ErrorBanner } from '@/components/shared/ErrorBanner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { accountsExportAuthJson } from '@/generated/openapi'
import { i18n } from '@/i18n'
import { api } from '@/lib/api-client'
import { intlLocale } from '@/lib/intl'
import { planTypeLabel } from '@/lib/plan-label'
import { callAdminAttachment } from '@/lib/router-api'

// Namespace-fixed translator; the language resolves at call time so
// module-scope helpers stay reactive to language switches.
const t = i18n.getFixedT(null, 'accounts')

import { ApiKeyEditPanel } from './detail-edit'
import { adminAccountDetailQueryKey, invalidateAdminAccountQueries } from './query-keys'
import { formatQuotaDetail } from './quota-format'

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
  primary_used_percent?: number | null
  secondary_used_percent?: number | null
  usage_updated_at?: string | null
  primary_reset_at?: string | null
  secondary_reset_at?: string | null
  primary_window_seconds?: number | null
  secondary_window_seconds?: number | null
  capabilities?: string[] | null
  use_proxy?: boolean
}

interface AccountModel {
  id: number
  account_id: number
  model_id: string
  source: 'manual' | 'upstream'
  metadata?: string | null
  created_at: string
  updated_at: string
}

const pauseAccountButtonClassName =
  'border-[var(--line-3)] bg-[var(--panel)] text-[var(--text-muted)] [box-shadow:var(--shadow-btn)] hover:border-[var(--err)] hover:bg-[var(--err-soft)] hover:text-[var(--err)] focus-visible:border-[var(--err)] focus-visible:text-[var(--err)]'

const activateAccountButtonClassName =
  'border-[var(--line-3)] bg-[var(--panel)] text-[var(--text-muted)] [box-shadow:var(--shadow-btn)] hover:border-[var(--ok)] hover:bg-[var(--ok-soft)] hover:text-[var(--ok)] focus-visible:border-[var(--ok)] focus-visible:text-[var(--ok)]'

export function AdminAccountDetail() {
  // Subscribes this subtree to languageChanged so module-t() helpers re-render.
  useTranslation('accounts')
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

  const [newModelID, setNewModelID] = useState('')
  const [isAddingModel, setIsAddingModel] = useState(false)
  const [isRefreshingModels, setIsRefreshingModels] = useState(false)
  const [isSettingProxy, setIsSettingProxy] = useState(false)
  const addModelInputRef = useRef<HTMLInputElement>(null)
  const modelsQueryKey = ['admin', 'account', rawAccountID, 'models'] as const

  const modelsQuery = useQuery({
    queryKey: modelsQueryKey,
    queryFn: () =>
      api.get<{ account_id: number; models: AccountModel[] }>(
        `/api/admin/accounts/${rawAccountID}/models`,
      ),
    enabled: hasValidID,
    staleTime: 10_000,
  })

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
    (account?.plan_type == null ? t('empty') : planTypeLabel(account.plan_type) || t('empty'))
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
      toast.success(t('detail.toasts.activateSuccess'))
    } catch (error) {
      setActionError(error)
      setActionErrorTitle(t('detail.activateTitle'))
      toast.error(t('detail.toasts.activateFailed'))
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
      toast.success(t('detail.toasts.exportSuccess'))
    } catch (error) {
      setActionError(error)
      setActionErrorTitle(t('detail.exportTitle'))
      toast.error(t('detail.toasts.exportFailed'))
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
      toast.success(t('detail.toasts.disableSuccess'))
    } catch (error) {
      setActionError(error)
      setActionErrorTitle(t('detail.disableTitle'))
      toast.error(t('detail.toasts.disableFailed'))
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
      toast.success(t('detail.toasts.deleteSuccess'))
      await navigate({ to: '/admin/accounts' })
    } catch (error) {
      setActionError(error)
      setActionErrorTitle(t('detail.deleteTitle'))
      toast.error(t('detail.toasts.deleteFailed'))
    } finally {
      setIsDeleting(false)
    }
  }

  async function handleAddModel() {
    if (!account || isAddingModel || newModelID.trim() === '') {
      return
    }
    setIsAddingModel(true)
    try {
      await api.post<Record<string, unknown>>(`/api/admin/accounts/${account.id}/models/add`, {
        model_id: newModelID.trim(),
      })
      setNewModelID('')
      await queryClient.invalidateQueries({ queryKey: modelsQueryKey })
      toast.success(t('detail.toasts.modelAddSuccess'))
    } catch (_error) {
      toast.error(t('detail.toasts.modelAddFailed'))
    } finally {
      setIsAddingModel(false)
      addModelInputRef.current?.focus()
    }
  }

  async function handleRemoveModel(modelID: string) {
    if (!account) {
      return
    }
    try {
      await api.post<Record<string, unknown>>(`/api/admin/accounts/${account.id}/models/remove`, {
        model_id: modelID,
      })
      await queryClient.invalidateQueries({ queryKey: modelsQueryKey })
      toast.success(t('detail.toasts.modelRemoveSuccess'))
    } catch (_error) {
      toast.error(t('detail.toasts.modelRemoveFailed'))
    }
  }

  async function handleRefreshModels() {
    if (!account || isRefreshingModels) {
      return
    }
    setIsRefreshingModels(true)
    try {
      await api.post<Record<string, unknown>>(
        `/api/admin/accounts/${account.id}/models/refresh`,
        {},
      )
      await queryClient.invalidateQueries({ queryKey: modelsQueryKey })
      toast.success(t('detail.toasts.modelRefreshSuccess'))
    } catch (_error) {
      toast.error(t('detail.toasts.modelRefreshFailed'))
    } finally {
      setIsRefreshingModels(false)
    }
  }

  async function handleUseProxyChange(useProxy: boolean) {
    if (!account || isSettingProxy) {
      return
    }
    setIsSettingProxy(true)
    try {
      await api.post<Record<string, unknown>>(`/api/admin/accounts/${account.id}/proxy/set`, {
        use_proxy: useProxy,
      })
      await invalidateAdminAccountQueries(queryClient, account.id)
    } catch (_error) {
      toast.error(t('detail.proxySetFailed'))
    } finally {
      setIsSettingProxy(false)
    }
  }

  return (
    <Canvas variant="wide">
      <Stripe eyebrow={t('detail.title')}>{t('detail.parentTitle')}</Stripe>
      <h1 className="mb-3 max-w-[18ch]">
        Account <strong>{hasValidID ? rawAccountID : t('empty')}</strong>
      </h1>
      <p className="mb-7 max-w-[70ch] text-[14px] leading-[1.65] text-[var(--text-dim)]">
        {t('detail.subtitle')}
      </p>

      {!hasValidID ? (
        <ErrorBanner
          error={new Error(t('detail.invalidIdDetail'))}
          title={t('detail.invalidIdTitle')}
          className="mb-4"
        />
      ) : null}

      {accountQuery.isError ? <ErrorBanner error={accountQuery.error} className="mb-4" /> : null}
      {actionError ? (
        <ErrorBanner error={actionError} className="mb-4" title={actionErrorTitle} />
      ) : null}

      {accountQuery.isLoading ? (
        <PanelCard title={t('detail.summaryTitle')} meta={t('detail.loading')}>
          <div className="text-[13px] text-[var(--text-dim)]" data-testid="account-detail-loading">
            {t('detail.loading')}
          </div>
        </PanelCard>
      ) : null}

      {account ? (
        <div data-testid="account-detail-view" className="space-y-3">
          <PanelCard
            title={t('detail.summaryTitle')}
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
                    aria-label={
                      isDisabling ? t('detail.actions.disabling') : t('detail.actions.disable')
                    }
                    title={
                      isDisabling ? t('detail.actions.disabling') : t('detail.actions.disable')
                    }
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
                      isActivating ? t('detail.actions.activating') : t('detail.actions.activate')
                    }
                    title={
                      isActivating ? t('detail.actions.activating') : t('detail.actions.activate')
                    }
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
                    {isExporting ? t('detail.actions.exporting') : t('detail.actions.export')}
                  </Button>
                ) : null}
              </div>
            </div>
          </PanelCard>

          <div className="grid gap-3 md:grid-cols-2">
            <MetadataPanel
              title={t('detail.summaryTitle')}
              meta={accountStatusMeta?.label ?? t('empty')}
              metaClassName={accountStatusMeta?.className}
              rows={[
                [t('detail.labels.id'), String(account.id)],
                [t('detail.labels.provider'), account.provider],
                [t('detail.labels.authMethod'), renderAuthMethodLabel(account.auth_method)],
                [t('detail.labels.status'), account.status],
                [t('detail.labels.baseURL'), account.base_url ?? t('empty')],
              ]}
            />

            {showOAuthMetadata ? (
              <MetadataPanel
                title={t('detail.metadataTitle')}
                testID="account-oauth-metadata"
                rows={[
                  [t('detail.labels.email'), account.email ?? t('empty')],
                  [t('detail.labels.plan'), planLabel],
                  [
                    t('detail.labels.primaryUsage'),
                    <QuotaUsageValue
                      key="primary"
                      percent={account.primary_used_percent}
                      resetAt={account.primary_reset_at}
                      windowSeconds={account.primary_window_seconds}
                    />,
                  ],
                  [
                    t('detail.labels.secondaryUsage'),
                    <QuotaUsageValue
                      key="secondary"
                      percent={account.secondary_used_percent}
                      resetAt={account.secondary_reset_at}
                      windowSeconds={account.secondary_window_seconds}
                    />,
                  ],
                  [t('detail.labels.quotaUpdatedAt'), formatTimestamp(account.usage_updated_at)],
                  [t('detail.labels.chatgptAccountID'), account.chatgpt_account_id ?? t('empty')],
                  [t('detail.labels.lastRefresh'), formatTimestamp(account.last_refresh)],
                  [t('detail.labels.accessExpiresAt'), formatTimestamp(account.access_expires_at)],
                ]}
              />
            ) : null}
          </div>

          {!showOAuthMetadata ? <ApiKeyEditPanel account={account} /> : null}

          <PanelCard title={t('detail.proxyTitle')}>
            <div className="grid items-start gap-3 sm:grid-cols-[minmax(0,220px)_1fr_auto] sm:gap-5">
              <div className="flex flex-col gap-1">
                <Label htmlFor="account-use-proxy">{t('detail.proxyEnableLabel')}</Label>
              </div>
              <p className="pt-[2px] text-[12.5px] leading-[1.55] text-[var(--text-dim)]">
                {t('detail.proxyHint')}
              </p>
              <div className="justify-self-start sm:self-center sm:justify-self-auto">
                <Switch
                  id="account-use-proxy"
                  checked={account.use_proxy ?? false}
                  onCheckedChange={(v) => void handleUseProxyChange(v)}
                  disabled={isSettingProxy}
                  data-testid="account-use-proxy-switch"
                />
              </div>
            </div>
          </PanelCard>

          <PanelCard title={t('detail.deleteTitle')}>
            <div className="flex flex-wrap items-center justify-between gap-3">
              <p className="min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap text-[13px] leading-[1.6] text-[var(--text-dim)]">
                {t('detail.deleteHint')}
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
                  ? t('detail.actions.deleting')
                  : deleteConfirming
                    ? t('detail.actions.confirmDelete')
                    : t('detail.actions.delete')}
              </Button>
            </div>
          </PanelCard>

          <PanelCard title={t('detail.modelsTitle')}>
            {modelsQuery.isLoading ? (
              <div className="text-[13px] text-[var(--text-dim)]">{t('detail.modelsLoading')}</div>
            ) : modelsQuery.isError ? (
              <ErrorBanner error={modelsQuery.error} className="mb-4" />
            ) : (
              <div className="space-y-3">
                {(modelsQuery.data?.models?.length ?? 0) > 0 ? (
                  <div className="divide-y divide-[var(--line-2)]">
                    {modelsQuery.data?.models.map((m) => (
                      <div
                        key={m.model_id}
                        className="flex items-center justify-between gap-3 py-2 text-[13px]"
                      >
                        <div className="flex items-center gap-3 min-w-0">
                          <code className="truncate font-mono text-[var(--text)]">
                            {m.model_id}
                          </code>
                          <span className="rounded border border-[var(--line-3)] px-1.5 py-0.5 text-[11px] text-[var(--text-muted)]">
                            {m.source}
                          </span>
                        </div>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-7 shrink-0"
                          onClick={() => void handleRemoveModel(m.model_id)}
                          aria-label={`${t('detail.removeModelTitle')} ${m.model_id}`}
                          title={`${t('detail.removeModelTitle')} ${m.model_id}`}
                        >
                          <X className="size-3.5" />
                        </Button>
                      </div>
                    ))}
                  </div>
                ) : (
                  <p className="text-[13px] leading-[1.6] text-[var(--text-dim)]">
                    {t('detail.modelsEmpty')}
                  </p>
                )}

                <div className="flex items-center gap-2">
                  <Input
                    ref={addModelInputRef}
                    value={newModelID}
                    onChange={(e) => setNewModelID(e.target.value)}
                    placeholder={t('detail.addModelPlaceholder')}
                    className="h-8 text-[13px]"
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') {
                        void handleAddModel()
                      }
                    }}
                  />
                  <Button
                    size="sm"
                    onClick={() => void handleAddModel()}
                    disabled={isAddingModel || newModelID.trim() === ''}
                  >
                    {isAddingModel ? t('detail.addingModel') : t('detail.addModelButton')}
                  </Button>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => void handleRefreshModels()}
                    disabled={isRefreshingModels}
                  >
                    <RefreshCw className={`size-3.5 ${isRefreshingModels ? 'animate-spin' : ''}`} />
                    {isRefreshingModels
                      ? t('detail.refreshingModels')
                      : t('detail.refreshModelsButton')}
                  </Button>
                </div>
              </div>
            )}
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
  rows: Array<[label: string, value: ReactNode]>
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
  return t(`authMethod.${method}` as 'authMethod.api_key', { defaultValue: method })
}

function QuotaUsageValue({
  percent,
  resetAt,
  windowSeconds,
}: {
  percent?: number | null
  resetAt?: string | null
  windowSeconds?: number | null
}) {
  const detailLine = formatQuotaDetail(resetAt, windowSeconds)
  return (
    <>
      <div className="text-[13px] leading-[1.6] text-[var(--text)]">{formatPercent(percent)}</div>
      {detailLine ? (
        <div className="flex items-center gap-1 text-[11.5px] leading-[1.5] text-[var(--text-dim)]">
          <RefreshCw aria-hidden="true" className="size-3 shrink-0" />
          <span>{detailLine}</span>
        </div>
      ) : null}
    </>
  )
}

function accountIdentity(account: AccountDetail): string {
  return account.email || account.name
}

function statusMetaFor(status: string): { label: string; className: string } {
  if (isActiveStatus(status)) {
    return {
      label: t('accountStatus.active'),
      className: 'border-[var(--ok)] bg-[var(--ok-soft)] text-[var(--ok)]',
    }
  }

  return {
    label: t('accountStatus.inactive'),
    className: 'border-[var(--err)] bg-[var(--err-soft)] text-[var(--err)]',
  }
}

function isActiveStatus(status: string): boolean {
  return status.toLowerCase() === 'active'
}

function formatTimestamp(value: string | null | undefined): string {
  if (!value) {
    return t('empty')
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  return date.toISOString().replace('.000Z', 'Z')
}

function formatPercent(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) {
    return t('empty')
  }
  const formatted = new Intl.NumberFormat(intlLocale(), { maximumFractionDigits: 2 }).format(value)
  return `${formatted}%`
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
