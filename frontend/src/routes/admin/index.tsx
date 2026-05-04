import { useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import {
  Activity,
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  BarChart3,
  Clock3,
  Eye,
  EyeOff,
  GripVertical,
  RefreshCw,
  Settings2,
  ShieldAlert,
  SlidersHorizontal,
} from 'lucide-react'
import { type DragEvent, type ReactNode, useEffect, useMemo, useState } from 'react'
import {
  Area,
  AreaChart,
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

import { Canvas, PageIntro, Stripe } from '@/components/neo'
import { ErrorBanner } from '@/components/shared/ErrorBanner'
import { Button } from '@/components/ui/button'
import {
  type DashboardData,
  type DashboardRange,
  type DashboardResponseBody,
  dashboardGet,
} from '@/generated/openapi'
import { callAdmin } from '@/lib/router-api'
import { cn } from '@/lib/utils'
import { adminDashboardRouteApi } from '@/router'
import { strings } from './dashboard.strings'

type CardID = 'active_accounts' | 'requests' | 'tokens' | 'error_rate' | 'ttft'
type DashboardDropZone = 'visible' | 'hidden'
type DashboardDropIntent = 'before' | 'after'

interface DashboardSearch {
  range?: DashboardRange
  account_id?: number
}

interface DashboardLayout {
  order: CardID[]
  hidden: CardID[]
}

interface MetricCardProps {
  id: CardID
  title: string
  icon: ReactNode
  value: ReactNode
  meta?: ReactNode
  children?: ReactNode
  editMode?: boolean
  isDragging?: boolean
  isDropTarget?: boolean
  canMoveDown?: boolean
  canMoveUp?: boolean
  onHide?: () => void
  onMoveDown?: () => void
  onMoveUp?: () => void
  onDragStart?: (event: DragEvent<HTMLElement>) => void
  onDragOver?: (event: DragEvent<HTMLElement>) => void
  onDrop?: (event: DragEvent<HTMLElement>) => void
  onDragEnd?: () => void
}

const RANGE_OPTIONS: DashboardRange[] = ['1h', '1d', '7d', '30d']
const DEFAULT_RANGE: DashboardRange = '7d'
const DEFAULT_CARD_ORDER: CardID[] = ['active_accounts', 'requests', 'tokens', 'error_rate', 'ttft']
const LAYOUT_STORAGE_KEY = 'one-llm-router.dashboard.cards.v1'

const numberFormatter = new Intl.NumberFormat('en-US')
const percentFormatter = new Intl.NumberFormat('en-US', {
  style: 'percent',
  maximumFractionDigits: 1,
})
const timeFormatter = new Intl.DateTimeFormat('en-US', {
  month: 'short',
  day: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
})

export function AdminIndex() {
  const navigate = useNavigate()
  const routeSearch = adminDashboardRouteApi.useSearch() as DashboardSearch
  const range = routeSearch.range ?? DEFAULT_RANGE
  const selectedAccountID = routeSearch.account_id
  const dashboardQueryParams = useMemo(
    () => ({
      range,
      account_id: selectedAccountID,
    }),
    [range, selectedAccountID],
  )

  const dashboardQuery = useQuery<DashboardData>({
    queryKey: ['admin', 'dashboard', dashboardQueryParams],
    queryFn: async () =>
      callAdmin<DashboardResponseBody>(
        dashboardGet({
          query:
            typeof selectedAccountID === 'number'
              ? { range, account_id: selectedAccountID }
              : { range },
        }),
      ),
    staleTime: 10_000,
  })

  const [layout, setLayout] = useState<DashboardLayout>(() => readStoredLayout())
  const [editMode, setEditMode] = useState(false)
  const [draggingCardID, setDraggingCardID] = useState<CardID | null>(null)
  const [dropTarget, setDropTarget] = useState<{
    cardID?: CardID
    intent?: DashboardDropIntent
    zone: DashboardDropZone
  } | null>(null)

  useEffect(() => {
    writeStoredLayout(layout)
  }, [layout])

  const dashboard = dashboardQuery.data
  const showNoActiveAccountsBanner = dashboard?.cards.active_accounts.value === 0
  const visibleCardIDs = layout.order.filter((cardID) => !layout.hidden.includes(cardID))
  const hiddenCardIDs = layout.order.filter((cardID) => layout.hidden.includes(cardID))

  function updateSearch(patch: Partial<DashboardSearch>) {
    const next: DashboardSearch = cleanSearch({ ...routeSearch, ...patch })
    void navigate({ to: '/admin', search: next })
  }

  function setCardVisibility(cardID: CardID, hiddenNext: boolean) {
    setLayout((current) => {
      const alreadyHidden = current.hidden.includes(cardID)
      if (alreadyHidden === hiddenNext) return current
      const hidden = hiddenNext
        ? [...current.hidden, cardID]
        : current.hidden.filter((id) => id !== cardID)
      return { ...current, hidden }
    })
  }

  function moveCard(
    cardID: CardID,
    zone: DashboardDropZone,
    targetCardID?: CardID,
    intent?: DashboardDropIntent,
  ) {
    setLayout((current) => {
      if (!current.order.includes(cardID)) return current

      const hidden = new Set(current.hidden)
      if (zone === 'hidden') {
        hidden.add(cardID)
      } else {
        hidden.delete(cardID)
      }

      const orderWithoutDragged = current.order.filter((id) => id !== cardID)
      const targetIndex =
        targetCardID && targetCardID !== cardID ? orderWithoutDragged.indexOf(targetCardID) : -1
      const insertAt =
        targetIndex >= 0
          ? intent === 'after'
            ? targetIndex + 1
            : targetIndex
          : appendIndexForZone(orderWithoutDragged, hidden, zone)
      const nextOrder = [
        ...orderWithoutDragged.slice(0, insertAt),
        cardID,
        ...orderWithoutDragged.slice(insertAt),
      ]

      return { order: nextOrder, hidden: [...hidden].filter((id) => nextOrder.includes(id)) }
    })
  }

  function moveCardByStep(cardID: CardID, zone: DashboardDropZone, direction: -1 | 1) {
    setLayout((current) => {
      if (!current.order.includes(cardID)) return current

      const hidden = new Set(current.hidden)
      const zoneCardIDs = current.order.filter((id) =>
        zone === 'hidden' ? hidden.has(id) : !hidden.has(id),
      )
      const currentZoneIndex = zoneCardIDs.indexOf(cardID)
      const targetCardID = zoneCardIDs[currentZoneIndex + direction]
      if (!targetCardID) return current

      const orderWithoutMoved = current.order.filter((id) => id !== cardID)
      const targetIndex = orderWithoutMoved.indexOf(targetCardID)
      if (targetIndex < 0) return current
      const insertAt = direction < 0 ? targetIndex : targetIndex + 1
      const nextOrder = [
        ...orderWithoutMoved.slice(0, insertAt),
        cardID,
        ...orderWithoutMoved.slice(insertAt),
      ]

      return { ...current, order: nextOrder }
    })
  }

  function handleCardDragStart(cardID: CardID, event: DragEvent<HTMLElement>) {
    if (!editMode) return
    setDraggingCardID(cardID)
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', cardID)
  }

  function dropIntentForTarget(
    _event: DragEvent<HTMLElement>,
    draggedCardID: CardID | null,
    targetCardID: CardID,
  ): DashboardDropIntent {
    const draggedIndex = draggedCardID ? layout.order.indexOf(draggedCardID) : -1
    const targetIndex = layout.order.indexOf(targetCardID)
    return draggedIndex >= 0 && targetIndex >= 0 && draggedIndex < targetIndex ? 'after' : 'before'
  }

  function handleCardDragOver(
    targetCardID: CardID,
    zone: DashboardDropZone,
    event: DragEvent<HTMLElement>,
  ) {
    if (!editMode) return
    event.preventDefault()
    event.stopPropagation()
    event.dataTransfer.dropEffect = 'move'
    if (draggingCardID !== targetCardID) {
      setDropTarget({
        cardID: targetCardID,
        intent: dropIntentForTarget(event, draggingCardID, targetCardID),
        zone,
      })
    }
  }

  function handleCardDrop(
    targetCardID: CardID,
    zone: DashboardDropZone,
    event: DragEvent<HTMLElement>,
  ) {
    if (!editMode) return
    event.preventDefault()
    event.stopPropagation()
    const draggedCardID = draggedCardFromEvent(event, draggingCardID)
    if (draggedCardID && draggedCardID !== targetCardID) {
      moveCard(
        draggedCardID,
        zone,
        targetCardID,
        dropIntentForTarget(event, draggedCardID, targetCardID),
      )
    }
    clearDragState()
  }

  function handleZoneDragOver(zone: DashboardDropZone, event: DragEvent<HTMLElement>) {
    if (!editMode || !draggingCardID) return
    event.preventDefault()
    event.dataTransfer.dropEffect = 'move'
    setDropTarget({ zone })
  }

  function handleZoneDrop(zone: DashboardDropZone, event: DragEvent<HTMLElement>) {
    if (!editMode) return
    event.preventDefault()
    const draggedCardID = draggedCardFromEvent(event, draggingCardID)
    if (draggedCardID) {
      moveCard(draggedCardID, zone)
    }
    clearDragState()
  }

  function clearDragState() {
    setDraggingCardID(null)
    setDropTarget(null)
  }

  return (
    <Canvas variant="wide">
      <Stripe eyebrow={strings.eyebrow}>{strings.stripe}</Stripe>

      <div className="mb-6 flex flex-col gap-4 xl:flex-row xl:items-start xl:justify-between">
        <div className="min-w-0 flex-1">
          <h1 className="mb-3 max-w-[16ch] text-balance">{strings.title}</h1>
          <PageIntro>{strings.intro}</PageIntro>
        </div>

        <div className="grid w-full grid-cols-[minmax(0,1fr)_auto] items-end gap-x-3 gap-y-3 sm:w-auto sm:grid-cols-[220px_auto]">
          <div className="col-span-2 row-start-1 min-w-0">
            <AccountFilter
              accounts={dashboard?.account_options ?? []}
              value={selectedAccountID}
              onChange={(accountID) => updateSearch({ account_id: accountID })}
            />
          </div>
          <div className="col-start-1 row-start-2">
            <RangeFilter
              value={range}
              onChange={(nextRange) => updateSearch({ range: nextRange })}
            />
          </div>
          <div className="col-start-2 row-start-2 flex gap-2">
            <Button
              type="button"
              variant="secondary"
              size="icon"
              aria-label={strings.actions.refresh}
              title={strings.actions.refresh}
              onClick={() => void dashboardQuery.refetch()}
            >
              <RefreshCw />
            </Button>
            <Button
              type="button"
              variant="secondary"
              size="icon"
              aria-label={strings.actions.editDashboard}
              title={strings.actions.editDashboard}
              aria-pressed={editMode}
              onClick={() => setEditMode((current) => !current)}
              className={
                editMode
                  ? 'border-[var(--accent)] bg-[var(--accent-soft)] text-[var(--accent)]'
                  : ''
              }
            >
              <Settings2 />
            </Button>
          </div>
        </div>
      </div>

      {dashboardQuery.isError ? (
        <ErrorBanner error={dashboardQuery.error} title={strings.errorTitle} className="mb-4" />
      ) : null}

      {showNoActiveAccountsBanner ? <NoActiveAccountsBanner /> : null}

      <ul
        data-testid="dashboard-visible-cards"
        className="grid list-none gap-3 lg:grid-cols-2 2xl:grid-cols-3"
        onDragOver={(event) => handleZoneDragOver('visible', event)}
        onDrop={(event) => handleZoneDrop('visible', event)}
      >
        {visibleCardIDs.map((cardID, index) =>
          renderMetricCard(cardID, dashboard, {
            editMode,
            isDragging: draggingCardID === cardID,
            isDropTarget: dropTarget?.zone === 'visible' && dropTarget.cardID === cardID,
            canMoveDown: index < visibleCardIDs.length - 1,
            canMoveUp: index > 0,
            onHide: () => setCardVisibility(cardID, true),
            onMoveDown: () => moveCardByStep(cardID, 'visible', 1),
            onMoveUp: () => moveCardByStep(cardID, 'visible', -1),
            onDragStart: (event) => handleCardDragStart(cardID, event),
            onDragOver: (event) => handleCardDragOver(cardID, 'visible', event),
            onDrop: (event) => handleCardDrop(cardID, 'visible', event),
            onDragEnd: clearDragState,
          }),
        )}
      </ul>

      {editMode && hiddenCardIDs.length > 0 ? (
        <HiddenCardsArea
          hiddenCardIDs={hiddenCardIDs}
          draggingCardID={draggingCardID}
          dropTarget={dropTarget}
          onShow={(cardID) => setCardVisibility(cardID, false)}
          onMoveDown={(cardID) => moveCardByStep(cardID, 'hidden', 1)}
          onMoveUp={(cardID) => moveCardByStep(cardID, 'hidden', -1)}
          onDragStart={handleCardDragStart}
          onDragOver={handleCardDragOver}
          onDrop={handleCardDrop}
          onDragEnd={clearDragState}
          onZoneDragOver={handleZoneDragOver}
          onZoneDrop={handleZoneDrop}
        />
      ) : null}
    </Canvas>
  )
}

function NoActiveAccountsBanner() {
  return (
    <div
      role="alert"
      data-testid="no-healthy-accounts-banner"
      className="mb-4 flex items-start gap-3 border px-4 py-3 text-[12.5px] leading-[1.5]"
      style={{
        borderColor: 'var(--warn)',
        background: 'color-mix(in oklch, var(--warn) 12%, transparent)',
        color: 'var(--warn)',
        borderRadius: 2,
      }}
    >
      <AlertTriangle className="mt-[2px] h-[14px] w-[14px] shrink-0" strokeWidth={2} />
      <div className="flex flex-col gap-1">
        <span className="font-medium">{strings.noActiveAccounts.title}</span>
        <span className="text-[11.5px] text-[var(--text-dim)]">
          {strings.noActiveAccounts.prefix}{' '}
          <Link
            to="/admin/accounts/new"
            className="text-[var(--accent)] underline-offset-4 hover:underline"
          >
            {strings.noActiveAccounts.link}
          </Link>
          {strings.noActiveAccounts.suffix}
        </span>
      </div>
    </div>
  )
}

function RangeFilter({
  value,
  onChange,
}: {
  value: DashboardRange
  onChange: (range: DashboardRange) => void
}) {
  return (
    <div className="flex w-full flex-col gap-2">
      <span className="font-mono text-[11px] uppercase tracking-[0.12em] text-[var(--text-muted)]">
        {strings.labels.range}
      </span>
      <div
        className="grid grid-cols-4 border border-[var(--line)] bg-[var(--panel)]"
        style={{ borderRadius: 2 }}
      >
        {RANGE_OPTIONS.map((range) => (
          <button
            key={range}
            type="button"
            onClick={() => onChange(range)}
            className={cn(
              'min-h-11 min-w-0 cursor-pointer border-l border-[var(--line)] px-3 font-mono text-[12px] first:border-l-0 focus-visible:outline-none focus-visible:[box-shadow:var(--focus)] sm:min-h-8',
              value === range
                ? 'bg-[var(--accent)] text-[var(--accent-fg)]'
                : 'bg-transparent text-[var(--text-dim)] hover:bg-[var(--panel-hi)] hover:text-[var(--text)]',
            )}
          >
            {range}
          </button>
        ))}
      </div>
    </div>
  )
}

function AccountFilter({
  accounts,
  value,
  onChange,
}: {
  accounts: DashboardData['account_options']
  value?: number
  onChange: (accountID: number | undefined) => void
}) {
  return (
    <div className="flex w-full flex-col gap-2">
      <label
        htmlFor="dashboard-account"
        className="font-mono text-[11px] uppercase tracking-[0.12em] text-[var(--text-muted)]"
      >
        {strings.labels.account}
      </label>
      <select
        id="dashboard-account"
        value={typeof value === 'number' ? String(value) : 'all'}
        onChange={(event) => {
          const next = event.currentTarget.value
          onChange(next === 'all' ? undefined : Number(next))
        }}
        className="min-h-11 cursor-pointer border border-[var(--line-2)] bg-[var(--bg-2)] px-[10px] py-[7px] font-mono text-[12.5px] text-[var(--text)] outline-none transition-colors hover:border-[var(--line-3)] focus:border-[var(--line-3)] focus-visible:[box-shadow:0_0_0_1px_var(--line-3)] sm:min-h-8"
        style={{ borderRadius: 2 }}
      >
        <option value="all">{strings.accountAll}</option>
        {accounts.map((account) => (
          <option key={account.id} value={account.id}>
            {account.label}
          </option>
        ))}
      </select>
    </div>
  )
}

interface HiddenCardsAreaProps {
  hiddenCardIDs: CardID[]
  draggingCardID: CardID | null
  dropTarget: { cardID?: CardID; zone: DashboardDropZone } | null
  onShow: (cardID: CardID) => void
  onMoveDown: (cardID: CardID) => void
  onMoveUp: (cardID: CardID) => void
  onDragStart: (cardID: CardID, event: DragEvent<HTMLElement>) => void
  onDragOver: (cardID: CardID, zone: DashboardDropZone, event: DragEvent<HTMLElement>) => void
  onDrop: (cardID: CardID, zone: DashboardDropZone, event: DragEvent<HTMLElement>) => void
  onDragEnd: () => void
  onZoneDragOver: (zone: DashboardDropZone, event: DragEvent<HTMLElement>) => void
  onZoneDrop: (zone: DashboardDropZone, event: DragEvent<HTMLElement>) => void
}

function HiddenCardsArea({
  hiddenCardIDs,
  draggingCardID,
  dropTarget,
  onShow,
  onMoveDown,
  onMoveUp,
  onDragStart,
  onDragOver,
  onDrop,
  onDragEnd,
  onZoneDragOver,
  onZoneDrop,
}: HiddenCardsAreaProps) {
  return (
    <section
      data-testid="dashboard-hidden-cards"
      aria-label={strings.hidden.title}
      className="mt-4 border border-dashed border-[var(--line-3)] bg-[var(--panel)]"
      style={{ borderRadius: 2 }}
      onDragOver={(event) => onZoneDragOver('hidden', event)}
      onDrop={(event) => onZoneDrop('hidden', event)}
    >
      <div className="flex min-h-9 items-center justify-between border-b border-[var(--line)] bg-[var(--panel-head)] px-4 py-2">
        <span className="text-[11.5px] font-medium uppercase tracking-[0.1em] text-[var(--text-dim)]">
          {strings.hidden.title}
        </span>
        <span className="border border-[var(--line-2)] px-2 py-[2px] font-mono text-[10.5px] text-[var(--text-muted)]">
          {strings.hidden.meta(hiddenCardIDs.length)}
        </span>
      </div>
      <ul className="grid list-none gap-3 p-3 md:grid-cols-2 xl:grid-cols-3">
        {hiddenCardIDs.map((cardID, index) => (
          <HiddenCardTile
            key={cardID}
            id={cardID}
            isDragging={draggingCardID === cardID}
            isDropTarget={dropTarget?.zone === 'hidden' && dropTarget.cardID === cardID}
            canMoveDown={index < hiddenCardIDs.length - 1}
            canMoveUp={index > 0}
            onShow={() => onShow(cardID)}
            onMoveDown={() => onMoveDown(cardID)}
            onMoveUp={() => onMoveUp(cardID)}
            onDragStart={(event) => onDragStart(cardID, event)}
            onDragOver={(event) => onDragOver(cardID, 'hidden', event)}
            onDrop={(event) => onDrop(cardID, 'hidden', event)}
            onDragEnd={onDragEnd}
          />
        ))}
      </ul>
    </section>
  )
}

interface MetricCardControls {
  editMode: boolean
  isDragging: boolean
  isDropTarget: boolean
  canMoveDown: boolean
  canMoveUp: boolean
  onHide: () => void
  onMoveDown: () => void
  onMoveUp: () => void
  onDragStart: (event: DragEvent<HTMLElement>) => void
  onDragOver: (event: DragEvent<HTMLElement>) => void
  onDrop: (event: DragEvent<HTMLElement>) => void
  onDragEnd: () => void
}

function renderMetricCard(
  cardID: CardID,
  dashboard: DashboardData | undefined,
  controls?: MetricCardControls,
) {
  switch (cardID) {
    case 'active_accounts':
      return (
        <MetricCard
          key={cardID}
          id={cardID}
          title={strings.cards.activeAccounts.title}
          icon={<Activity />}
          value={formatNumber(dashboard?.cards.active_accounts.value)}
          meta={strings.cards.activeAccounts.meta}
          {...controls}
        />
      )
    case 'requests':
      return (
        <MetricCard
          key={cardID}
          id={cardID}
          title={strings.cards.requests.title}
          icon={<BarChart3 />}
          value={formatNumber(dashboard?.cards.requests.total)}
          meta={strings.rangeTotal}
          {...controls}
        >
          <RequestsChart data={dashboard?.cards.requests.series ?? []} />
        </MetricCard>
      )
    case 'tokens':
      return (
        <MetricCard
          key={cardID}
          id={cardID}
          title={strings.cards.tokens.title}
          icon={<SlidersHorizontal />}
          value={formatNumber(tokenTotal(dashboard))}
          meta={tokenBreakdownLabel(dashboard)}
          {...controls}
        >
          <TokensChart data={dashboard?.cards.tokens.series ?? []} />
        </MetricCard>
      )
    case 'error_rate':
      return (
        <MetricCard
          key={cardID}
          id={cardID}
          title={strings.cards.errorRate.title}
          icon={<ShieldAlert />}
          value={formatPercent(dashboard?.cards.error_rate.value)}
          meta={errorRateMeta(dashboard)}
          {...controls}
        >
          <ErrorRateChart data={dashboard?.cards.error_rate.series ?? []} />
        </MetricCard>
      )
    case 'ttft':
      return (
        <MetricCard
          key={cardID}
          id={cardID}
          title={strings.cards.ttft.title}
          icon={<Clock3 />}
          value={formatMilliseconds(dashboard?.cards.ttft.p95_ms)}
          meta={ttftMeta(dashboard)}
          {...controls}
        >
          <TTFTChart data={dashboard?.cards.ttft.series ?? []} />
        </MetricCard>
      )
  }
}

function HiddenCardTile({
  id,
  isDragging,
  isDropTarget,
  canMoveDown,
  canMoveUp,
  onShow,
  onMoveDown,
  onMoveUp,
  onDragStart,
  onDragOver,
  onDrop,
  onDragEnd,
}: {
  id: CardID
  isDragging: boolean
  isDropTarget: boolean
  canMoveDown: boolean
  canMoveUp: boolean
  onShow: () => void
  onMoveDown: () => void
  onMoveUp: () => void
  onDragStart: (event: DragEvent<HTMLElement>) => void
  onDragOver: (event: DragEvent<HTMLElement>) => void
  onDrop: (event: DragEvent<HTMLElement>) => void
  onDragEnd: () => void
}) {
  return (
    <li
      data-testid={`dashboard-hidden-card-${id}`}
      data-card-id={id}
      draggable
      onDragStart={onDragStart}
      onDragOver={onDragOver}
      onDrop={onDrop}
      onDragEnd={onDragEnd}
      className={cn(
        'flex min-h-16 cursor-grab items-center justify-between gap-3 border bg-[var(--panel-hi)] px-3 py-3 transition-[border-color,background-color,opacity,transform] duration-150 active:cursor-grabbing',
        isDragging ? 'opacity-45' : '',
        isDropTarget ? 'border-[var(--accent)] bg-[var(--accent-soft)]' : 'border-[var(--line)]',
      )}
      style={{ borderRadius: 2 }}
    >
      <div className="flex min-w-0 items-center gap-2">
        <GripVertical className="h-4 w-4 shrink-0 text-[var(--text-muted)]" strokeWidth={1.8} />
        <span className="flex h-7 w-7 shrink-0 items-center justify-center text-[var(--accent)] [&>svg]:h-[14px] [&>svg]:w-[14px]">
          {cardIcon(id)}
        </span>
        <div className="min-w-0">
          <div className="truncate text-[12.5px] font-medium text-[var(--text)]">
            {cardLabel(id)}
          </div>
          <div className="font-mono text-[10.5px] uppercase tracking-[0.08em] text-[var(--text-muted)]">
            {strings.hidden.itemMeta}
          </div>
        </div>
      </div>
      <div className="flex shrink-0 items-center gap-1">
        <Button
          type="button"
          variant="ghost"
          size="icon"
          aria-label={strings.actions.moveUp(cardLabel(id))}
          title={strings.actions.moveUp(cardLabel(id))}
          disabled={!canMoveUp}
          onClick={onMoveUp}
        >
          <ArrowUp />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          aria-label={strings.actions.moveDown(cardLabel(id))}
          title={strings.actions.moveDown(cardLabel(id))}
          disabled={!canMoveDown}
          onClick={onMoveDown}
        >
          <ArrowDown />
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          aria-label={strings.actions.show(cardLabel(id))}
          title={strings.actions.show(cardLabel(id))}
          onClick={onShow}
        >
          <Eye />
        </Button>
      </div>
    </li>
  )
}

function MetricCard({
  id,
  title,
  icon,
  value,
  meta,
  children,
  editMode = false,
  isDragging = false,
  isDropTarget = false,
  canMoveDown = false,
  canMoveUp = false,
  onHide,
  onMoveDown,
  onMoveUp,
  onDragStart,
  onDragOver,
  onDrop,
  onDragEnd,
}: MetricCardProps) {
  const hasChart = Boolean(children)
  return (
    <li
      data-testid={`dashboard-card-${id}`}
      data-card-id={id}
      draggable={editMode}
      onDragStart={editMode ? onDragStart : undefined}
      onDragOver={editMode ? onDragOver : undefined}
      onDrop={editMode ? onDrop : undefined}
      onDragEnd={editMode ? onDragEnd : undefined}
      className={cn(
        'flex flex-col overflow-hidden border bg-[var(--panel)] transition-[border-color,background-color,opacity,transform] duration-150',
        editMode ? 'cursor-grab active:cursor-grabbing' : '',
        isDragging ? 'opacity-45' : '',
        isDropTarget ? 'border-[var(--accent)] bg-[var(--accent-soft)]' : 'border-[var(--line)]',
        hasChart ? 'min-h-[260px]' : 'min-h-[150px]',
      )}
      style={{ borderRadius: 2 }}
    >
      <div className="flex min-h-10 items-center justify-between gap-3 border-b border-[var(--line)] bg-[var(--panel-head)] px-4 py-2">
        <div className="flex min-w-0 items-center gap-2">
          {editMode ? (
            <GripVertical
              className="hidden h-4 w-4 shrink-0 text-[var(--text-muted)] sm:block"
              strokeWidth={1.8}
            />
          ) : null}
          <span className="flex h-7 w-7 shrink-0 items-center justify-center text-[var(--accent)] [&>svg]:h-[14px] [&>svg]:w-[14px]">
            {icon}
          </span>
          <h2 className="truncate text-[11.5px] font-medium uppercase tracking-[0.1em] text-[var(--text-dim)]">
            {title}
          </h2>
        </div>
        <div className="flex min-w-0 items-center gap-2">
          {meta ? (
            <span className="max-w-[180px] truncate border border-[var(--line-2)] px-2 py-[2px] text-right font-mono text-[10.5px] text-[var(--text-muted)]">
              {meta}
            </span>
          ) : null}
          {editMode ? (
            <div className="flex shrink-0 items-center gap-1">
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label={strings.actions.moveUp(cardLabel(id))}
                title={strings.actions.moveUp(cardLabel(id))}
                disabled={!canMoveUp}
                onClick={onMoveUp}
              >
                <ArrowUp />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label={strings.actions.moveDown(cardLabel(id))}
                title={strings.actions.moveDown(cardLabel(id))}
                disabled={!canMoveDown}
                onClick={onMoveDown}
              >
                <ArrowDown />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label={strings.actions.hide(cardLabel(id))}
                title={strings.actions.hide(cardLabel(id))}
                onClick={onHide}
              >
                <EyeOff />
              </Button>
            </div>
          ) : null}
        </div>
      </div>
      <div className="flex min-h-0 flex-1 flex-col px-4 py-4">
        <div className="font-mono text-[32px] leading-none text-[var(--text)]">{value}</div>
        {hasChart ? <div className="mt-4 h-[140px] min-h-[140px]">{children}</div> : null}
      </div>
    </li>
  )
}

function RequestsChart({ data }: { data: DashboardData['cards']['requests']['series'] }) {
  const chartData = data.map((point) => ({
    label: formatTime(point.timestamp),
    count: point.count,
  }))
  return (
    <ResponsiveContainer width="100%" height="100%" minWidth={1} minHeight={140}>
      <LineChart data={chartData} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
        <CartesianGrid stroke="var(--line)" vertical={false} />
        <XAxis dataKey="label" tick={{ fontSize: 10 }} stroke="var(--text-muted)" />
        <YAxis width={34} tick={{ fontSize: 10 }} stroke="var(--text-muted)" />
        <Tooltip />
        <Line
          type="monotone"
          dataKey="count"
          stroke="var(--accent)"
          strokeWidth={2}
          dot={false}
          isAnimationActive={false}
        />
      </LineChart>
    </ResponsiveContainer>
  )
}

function TokensChart({ data }: { data: DashboardData['cards']['tokens']['series'] }) {
  const chartData = data.map((point) => ({
    label: formatTime(point.timestamp),
    input_cached: point.input_cached,
    input_non_cached: point.input_non_cached,
    output: point.output,
  }))
  return (
    <ResponsiveContainer width="100%" height="100%" minWidth={1} minHeight={140}>
      <AreaChart data={chartData} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
        <CartesianGrid stroke="var(--line)" vertical={false} />
        <XAxis dataKey="label" tick={{ fontSize: 10 }} stroke="var(--text-muted)" />
        <YAxis width={34} tick={{ fontSize: 10 }} stroke="var(--text-muted)" />
        <Tooltip />
        <Area
          type="monotone"
          dataKey="input_cached"
          stackId="tokens"
          stroke="var(--ok)"
          fill="var(--ok)"
          fillOpacity={0.24}
          isAnimationActive={false}
        />
        <Area
          type="monotone"
          dataKey="input_non_cached"
          stackId="tokens"
          stroke="var(--accent)"
          fill="var(--accent)"
          fillOpacity={0.22}
          isAnimationActive={false}
        />
        <Area
          type="monotone"
          dataKey="output"
          stackId="tokens"
          stroke="var(--warn)"
          fill="var(--warn)"
          fillOpacity={0.2}
          isAnimationActive={false}
        />
      </AreaChart>
    </ResponsiveContainer>
  )
}

function ErrorRateChart({ data }: { data: DashboardData['cards']['error_rate']['series'] }) {
  const chartData = data.map((point) => ({
    label: formatTime(point.timestamp),
    rate: point.rate * 100,
  }))
  return (
    <ResponsiveContainer width="100%" height="100%" minWidth={1} minHeight={140}>
      <LineChart data={chartData} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
        <CartesianGrid stroke="var(--line)" vertical={false} />
        <XAxis dataKey="label" tick={{ fontSize: 10 }} stroke="var(--text-muted)" />
        <YAxis width={34} tick={{ fontSize: 10 }} stroke="var(--text-muted)" />
        <Tooltip />
        <Line
          type="monotone"
          dataKey="rate"
          stroke="var(--err)"
          strokeWidth={2}
          dot={false}
          isAnimationActive={false}
        />
      </LineChart>
    </ResponsiveContainer>
  )
}

function TTFTChart({ data }: { data: DashboardData['cards']['ttft']['series'] }) {
  const chartData = data.map((point) => ({
    label: formatTime(point.timestamp),
    p95_ms: point.p95_ms,
  }))
  return (
    <ResponsiveContainer width="100%" height="100%" minWidth={1} minHeight={140}>
      <LineChart data={chartData} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
        <CartesianGrid stroke="var(--line)" vertical={false} />
        <XAxis dataKey="label" tick={{ fontSize: 10 }} stroke="var(--text-muted)" />
        <YAxis width={42} tick={{ fontSize: 10 }} stroke="var(--text-muted)" />
        <Tooltip />
        <Line
          type="monotone"
          dataKey="p95_ms"
          stroke="var(--accent)"
          strokeWidth={2}
          dot={false}
          connectNulls={false}
          isAnimationActive={false}
        />
      </LineChart>
    </ResponsiveContainer>
  )
}

function tokenTotal(dashboard: DashboardData | undefined): number | undefined {
  if (!dashboard) return undefined
  const totals = dashboard.cards.tokens.totals
  return totals.input_cached + totals.input_non_cached + totals.output
}

function tokenBreakdownLabel(dashboard: DashboardData | undefined): string {
  if (!dashboard) return strings.loading
  const totals = dashboard.cards.tokens.totals
  return strings.cards.tokens.meta(
    formatNumber(totals.input_cached),
    formatNumber(totals.input_non_cached),
    formatNumber(totals.output),
  )
}

function errorRateMeta(dashboard: DashboardData | undefined): string {
  if (!dashboard) return strings.loading
  const card = dashboard.cards.error_rate
  return strings.cards.errorRate.meta(formatNumber(card.errors), formatNumber(card.total))
}

function ttftMeta(dashboard: DashboardData | undefined): string {
  if (!dashboard) return strings.loading
  return strings.cards.ttft.meta(formatNumber(dashboard.cards.ttft.sample_count))
}

function formatNumber(value: number | undefined): string {
  if (typeof value !== 'number' || Number.isNaN(value)) return strings.emptyValue
  return numberFormatter.format(value)
}

function formatPercent(value: number | undefined): string {
  if (typeof value !== 'number' || Number.isNaN(value)) return strings.emptyValue
  return percentFormatter.format(value)
}

function formatMilliseconds(value: number | null | undefined): string {
  if (typeof value !== 'number' || Number.isNaN(value)) return strings.emptyValue
  return `${numberFormatter.format(Math.round(value))} ms`
}

function formatTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return timeFormatter.format(date)
}

function cardIcon(cardID: CardID): ReactNode {
  switch (cardID) {
    case 'active_accounts':
      return <Activity />
    case 'requests':
      return <BarChart3 />
    case 'tokens':
      return <SlidersHorizontal />
    case 'error_rate':
      return <ShieldAlert />
    case 'ttft':
      return <Clock3 />
  }
}

function cardLabel(cardID: CardID): string {
  switch (cardID) {
    case 'active_accounts':
      return strings.cards.activeAccounts.shortTitle
    case 'requests':
      return strings.cards.requests.shortTitle
    case 'tokens':
      return strings.cards.tokens.shortTitle
    case 'error_rate':
      return strings.cards.errorRate.shortTitle
    case 'ttft':
      return strings.cards.ttft.shortTitle
  }
}

function appendIndexForZone(
  order: CardID[],
  hidden: ReadonlySet<CardID>,
  zone: DashboardDropZone,
): number {
  const zoneCards =
    zone === 'hidden' ? order.filter((id) => hidden.has(id)) : order.filter((id) => !hidden.has(id))
  const lastZoneCard = zoneCards.at(-1)
  return lastZoneCard ? order.indexOf(lastZoneCard) + 1 : order.length
}

function draggedCardFromEvent(
  event: DragEvent<HTMLElement>,
  fallback: CardID | null,
): CardID | null {
  const rawCardID = event.dataTransfer.getData('text/plain')
  if (isCardID(rawCardID)) return rawCardID
  return fallback
}

function cleanSearch(search: DashboardSearch): DashboardSearch {
  return {
    range: search.range === DEFAULT_RANGE ? undefined : search.range,
    account_id: typeof search.account_id === 'number' ? search.account_id : undefined,
  }
}

function readStoredLayout(): DashboardLayout {
  if (typeof window === 'undefined') return defaultLayout()
  try {
    const raw = window.localStorage.getItem(LAYOUT_STORAGE_KEY)
    if (!raw) return defaultLayout()
    const parsed = JSON.parse(raw) as Partial<DashboardLayout>
    return normalizeLayout(parsed)
  } catch {
    return defaultLayout()
  }
}

function writeStoredLayout(layout: DashboardLayout) {
  if (typeof window === 'undefined') return
  window.localStorage.setItem(LAYOUT_STORAGE_KEY, JSON.stringify(normalizeLayout(layout)))
}

function normalizeLayout(layout: Partial<DashboardLayout>): DashboardLayout {
  const order = Array.isArray(layout.order)
    ? [
        ...layout.order.filter((cardID): cardID is CardID => isCardID(cardID)),
        ...DEFAULT_CARD_ORDER.filter((cardID) => !layout.order?.includes(cardID)),
      ]
    : DEFAULT_CARD_ORDER
  const hidden = Array.isArray(layout.hidden)
    ? layout.hidden.filter((cardID): cardID is CardID => isCardID(cardID))
    : []
  return {
    order,
    hidden,
  }
}

function defaultLayout(): DashboardLayout {
  return {
    order: DEFAULT_CARD_ORDER,
    hidden: [],
  }
}

function isCardID(value: unknown): value is CardID {
  return typeof value === 'string' && DEFAULT_CARD_ORDER.includes(value as CardID)
}
