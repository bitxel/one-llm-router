import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { ArrowRight, Pause, Play, Settings } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'

import { Canvas, PageIntro, PanelCard, Stripe } from '@/components/neo'
import { ErrorBanner } from '@/components/shared/ErrorBanner'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api-client'
import { planTypeLabel } from '@/lib/plan-label'
import { strings } from './list.strings'
import { adminAccountsListQueryKey, invalidateAdminAccountQueries } from './query-keys'

type AuthMethod = 'api_key' | 'oauth_browser' | 'oauth_device' | 'oauth_import'

interface AccountListItem {
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

interface AccountsListPayload {
  accounts: AccountListItem[]
  total: number
}

type QuotaLane = 'primary' | 'secondary'

const quotaNumberFormatter = new Intl.NumberFormat('en-US', {
  maximumFractionDigits: 2,
})

const pauseAccountButtonClassName =
  'border-[var(--line-3)] bg-[var(--panel)] text-[var(--text-muted)] [box-shadow:var(--shadow-btn)] hover:border-[var(--err)] hover:bg-[var(--err-soft)] hover:text-[var(--err)] focus-visible:border-[var(--err)] focus-visible:text-[var(--err)]'

const activateAccountButtonClassName =
  'border-[var(--line-3)] bg-[var(--panel)] text-[var(--text-muted)] [box-shadow:var(--shadow-btn)] hover:border-[var(--ok)] hover:bg-[var(--ok-soft)] hover:text-[var(--ok)] focus-visible:border-[var(--ok)] focus-visible:text-[var(--ok)]'

export function AdminAccountsList() {
  const queryClient = useQueryClient()
  const [actionError, setActionError] = useState<unknown>(null)
  const query = useQuery({
    queryKey: adminAccountsListQueryKey,
    queryFn: () => api.get<AccountsListPayload>('/api/admin/accounts'),
    staleTime: 5_000,
  })
  const activateMutation = useMutation({
    mutationFn: (accountID: number) =>
      api.post<Record<string, string>>(`/api/admin/accounts/${accountID}/enable`, {}),
    onMutate: () => {
      setActionError(null)
    },
    onSuccess: async (_data, accountID) => {
      await invalidateAdminAccountQueries(queryClient, accountID)
      toast.success(strings.toasts.activateSuccess)
    },
    onError: (error) => {
      setActionError(error)
      toast.error(strings.toasts.activateFailed)
    },
  })
  const disableMutation = useMutation({
    mutationFn: (accountID: number) =>
      api.post<Record<string, string>>(`/api/admin/accounts/${accountID}/disable`, {}),
    onMutate: () => {
      setActionError(null)
    },
    onSuccess: async (_data, accountID) => {
      await invalidateAdminAccountQueries(queryClient, accountID)
      toast.success(strings.toasts.disableSuccess)
    },
    onError: (error) => {
      setActionError(error)
      toast.error(strings.toasts.disableFailed)
    },
  })

  const accounts = query.data?.accounts ?? []

  return (
    <Canvas variant="wide">
      <Stripe eyebrow={strings.eyebrow}>{strings.stripe}</Stripe>
      <div className="mb-8 flex flex-wrap items-end justify-between gap-4">
        <div className="min-w-0 flex-1">
          <h1 className="mb-3 max-w-[18ch] text-balance">{strings.title}</h1>
          <PageIntro>{strings.intro}</PageIntro>
        </div>
        <Button asChild>
          <Link to="/admin/accounts/new">
            {strings.actions.newAccount}
            <ArrowRight />
          </Link>
        </Button>
      </div>

      {query.isError ? <ErrorBanner error={query.error} className="mb-4" /> : null}
      {actionError ? <ErrorBanner error={actionError} className="mb-4" /> : null}

      <PanelCard title={strings.summaryTitle}>
        {query.isLoading ? (
          <div data-testid="accounts-list-loading" className="text-[13px] text-[var(--text-dim)]">
            {strings.loading}
          </div>
        ) : null}

        {!query.isLoading && accounts.length === 0 ? (
          <div data-testid="accounts-list-empty" className="text-[13px] text-[var(--text-dim)]">
            {strings.empty}
          </div>
        ) : null}

        {!query.isLoading && accounts.length > 0 ? (
          <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3" data-testid="accounts-list">
            {accounts.map((account) => {
              const showOAuthMetadata = account.auth_method !== 'api_key'
              const planLabel =
                account.plan_type_label ??
                (account.plan_type ? planTypeLabel(account.plan_type) : strings.emptyField)
              const quotaSnapshot = getQuotaSnapshot(account)
              const isActive = isActiveStatus(account.status)
              const isActivating =
                activateMutation.isPending && activateMutation.variables === account.id
              const isDisabling =
                disableMutation.isPending && disableMutation.variables === account.id

              return (
                <article
                  key={account.id}
                  data-testid={`account-row-${account.id}`}
                  className="flex min-w-0 flex-col border border-[var(--line)] bg-[var(--panel-hi)] p-4"
                  style={{ borderRadius: 2 }}
                >
                  <div className="flex min-w-0 items-start justify-between gap-3">
                    <div className="min-w-0 space-y-2">
                      <div className="flex flex-wrap items-center gap-2">
                        {!showOAuthMetadata ? (
                          <Badge variant="outline" className="font-mono text-[11px] uppercase">
                            {strings.authMethod[account.auth_method] ?? account.auth_method}
                          </Badge>
                        ) : null}
                        <AccountStatusBadge status={account.status} />
                      </div>
                      <div className="truncate text-[17px] leading-[1.25] font-semibold text-[var(--text)]">
                        {showOAuthMetadata ? (account.email ?? account.name) : account.name}
                      </div>
                      {!showOAuthMetadata ? (
                        <div className="truncate font-mono text-[12px] text-[var(--text-muted)]">
                          {account.provider}
                        </div>
                      ) : null}
                    </div>

                    <div className="flex shrink-0 items-center gap-1">
                      {isActive ? (
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          onClick={() => disableMutation.mutate(account.id)}
                          disabled={isDisabling}
                          data-testid={`disable-account-${account.id}`}
                          aria-label={
                            isDisabling ? strings.actions.disabling : strings.actions.disable
                          }
                          title={isDisabling ? strings.actions.disabling : strings.actions.disable}
                          className={pauseAccountButtonClassName}
                        >
                          <Pause />
                        </Button>
                      ) : (
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          onClick={() => activateMutation.mutate(account.id)}
                          disabled={isActivating}
                          data-testid={`activate-account-${account.id}`}
                          aria-label={
                            isActivating ? strings.actions.activating : strings.actions.activate
                          }
                          title={
                            isActivating ? strings.actions.activating : strings.actions.activate
                          }
                          className={activateAccountButtonClassName}
                        >
                          <Play />
                        </Button>
                      )}
                      <Button variant="ghost" size="icon" asChild>
                        <Link
                          to="/admin/accounts/$accountId"
                          params={{ accountId: String(account.id) }}
                          data-testid={`account-detail-link-${account.id}`}
                          aria-label={strings.actions.openDetail}
                          title={strings.actions.openDetail}
                        >
                          <Settings />
                        </Link>
                      </Button>
                    </div>
                  </div>

                  <div className="mt-4 grid gap-3 border-t border-[var(--line)] pt-3">
                    {showOAuthMetadata ? (
                      <OAuthAccountFacts
                        plan={planLabel}
                        primaryQuota={quotaSnapshot.primary}
                        secondaryQuota={quotaSnapshot.secondary}
                      />
                    ) : (
                      <AccountFact label={strings.labels.baseURL} value={account.base_url} mono />
                    )}
                  </div>
                </article>
              )
            })}
          </div>
        ) : null}
      </PanelCard>
    </Canvas>
  )
}

function OAuthAccountFacts({
  plan,
  primaryQuota,
  secondaryQuota,
}: {
  plan: string
  primaryQuota: string
  secondaryQuota: string
}) {
  return (
    <>
      <AccountFact label={strings.labels.plan} value={plan} />
      <div className="grid grid-cols-2 gap-3 border-t border-[var(--line)] pt-3">
        <QuotaCell label={strings.labels.primaryQuota} value={primaryQuota} />
        <QuotaCell label={strings.labels.secondaryQuota} value={secondaryQuota} />
      </div>
    </>
  )
}

function AccountFact({
  label,
  value,
  mono,
}: {
  label: string
  value?: string | null
  mono?: boolean
}) {
  return (
    <div className="min-w-0">
      <div className="text-[11px] font-medium uppercase tracking-[0.08em] text-[var(--text-muted)]">
        {label}
      </div>
      <div
        className={[
          'mt-1 truncate text-[13px] leading-[1.5] text-[var(--text)]',
          mono ? 'font-mono' : '',
        ].join(' ')}
      >
        {value || strings.emptyField}
      </div>
    </div>
  )
}

function QuotaCell({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <div className="text-[11px] font-medium uppercase tracking-[0.08em] text-[var(--text-muted)]">
        {label}
      </div>
      <div className="mt-1 truncate font-mono text-[14px] leading-[1.35] text-[var(--text)]">
        {value}
      </div>
    </div>
  )
}

function AccountStatusBadge({ status }: { status: string }) {
  const isActive = isActiveStatus(status)
  return (
    <Badge
      variant={isActive ? 'success' : 'danger'}
      className="font-mono text-[11px] uppercase"
      data-testid={`account-status-${isActive ? 'active' : 'inactive'}`}
    >
      {isActive ? strings.accountStatus.active : strings.accountStatus.inactive}
    </Badge>
  )
}

function isActiveStatus(status: string): boolean {
  return status.toLowerCase() === 'active'
}

function getQuotaSnapshot(account: AccountListItem): Record<QuotaLane, string> {
  return {
    primary: formatQuotaValue(findQuotaValue(account, 'primary')),
    secondary: formatQuotaValue(findQuotaValue(account, 'secondary')),
  }
}

function findQuotaValue(account: AccountListItem, lane: QuotaLane): unknown {
  const source = account as unknown as Record<string, unknown>
  const directKeys = quotaKeysFor(lane)

  for (const key of directKeys) {
    if (source[key] !== undefined) {
      return source[key]
    }
  }

  for (const containerKey of ['quota', 'quotas', 'rate_limit', 'rate_limits']) {
    const container = asRecord(source[containerKey])
    if (!container) {
      continue
    }
    const nested = findInRecord(container, directKeys)
    if (nested !== undefined) {
      return nested
    }

    const laneContainer = asRecord(container[lane])
    if (laneContainer) {
      const remaining = findInRecord(laneContainer, remainingKeys)
      if (remaining !== undefined) {
        return remaining
      }
    }
  }

  const additionalRateLimits = source.additional_rate_limits
  if (Array.isArray(additionalRateLimits)) {
    const laneRateLimit = additionalRateLimits
      .map((item) => asRecord(item))
      .find((item): item is Record<string, unknown> => {
        if (!item) {
          return false
        }
        return ['name', 'type', 'bucket', 'scope', 'label'].some((key) =>
          String(item[key] ?? '')
            .toLowerCase()
            .includes(lane),
        )
      })

    if (laneRateLimit) {
      const remaining = findInRecord(laneRateLimit, remainingKeys)
      if (remaining !== undefined) {
        return remaining
      }
    }
  }

  return undefined
}

const remainingKeys = [
  'remaining',
  'remaining_quota',
  'remainingQuota',
  'remaining_percent',
  'remainingPercent',
  'quota_remaining',
  'quotaRemaining',
  'available',
  'left',
] as const

function quotaKeysFor(lane: QuotaLane): string[] {
  const title = lane.charAt(0).toUpperCase() + lane.slice(1)
  return [
    `${lane}_remaining_percent`,
    `${lane}_remaining_quota`,
    `${lane}_quota_remaining`,
    `${lane}_remaining`,
    `${lane}RemainingPercent`,
    `${lane}RemainingQuota`,
    `${lane}QuotaRemaining`,
    `${lane}Remaining`,
    `${title}RemainingPercent`,
    `${title}RemainingQuota`,
    `${title}QuotaRemaining`,
    `${title}Remaining`,
  ]
}

function findInRecord(source: Record<string, unknown>, keys: readonly string[]): unknown {
  for (const key of keys) {
    if (source[key] !== undefined) {
      return source[key]
    }
  }
  return undefined
}

function formatQuotaValue(value: unknown): string {
  if (value === null || value === undefined || value === '') {
    return strings.emptyField
  }

  if (typeof value === 'number') {
    return Number.isFinite(value) ? `${quotaNumberFormatter.format(value)}%` : strings.emptyField
  }

  if (typeof value === 'string') {
    return value
  }

  const source = asRecord(value)
  if (!source) {
    return strings.emptyField
  }

  const remaining = findInRecord(source, remainingKeys)
  if (remaining === value) {
    return strings.emptyField
  }
  return formatQuotaValue(remaining)
}

function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return null
  }
  return value as Record<string, unknown>
}
