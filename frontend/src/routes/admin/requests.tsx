import { useQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import {
  ArrowDown,
  Braces,
  CalendarDays,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  CircleHelp,
  Clipboard,
  Columns3,
  type LucideIcon,
  Monitor,
  RefreshCw,
  Reply,
  RotateCcw,
  Router,
  Search,
  SendHorizontal,
  Server,
  SlidersHorizontal,
  X,
} from 'lucide-react'
import {
  type ComponentProps,
  type FormEvent,
  Fragment,
  type ReactNode,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { toast } from 'sonner'

import { Canvas, Field, PageIntro, PanelCard, Stripe } from '@/components/neo'
import { ErrorBanner } from '@/components/shared/ErrorBanner'
import { buildJsonPreviewFromText, JsonPreview } from '@/components/shared/JsonPreview'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  type RequestDetailData,
  type RequestDetailResponseBody,
  type RequestLogAccountOption,
  type RequestLogDetail,
  type RequestLogRow,
  type RequestOutcome,
  type RequestResponseMode,
  type RequestsListData,
  type RequestsListData2,
  type RequestsListResponseBody,
  type RequestsOptionsData,
  type RequestsOptionsData2,
  type RequestsOptionsResponseBody,
  requestsGet,
  requestsList,
  requestsOptions,
} from '@/generated/openapi'
import { callAdmin } from '@/lib/router-api'
import { adminRequestsRouteApi } from '@/router'
import { strings } from './requests.strings'

const DEFAULT_LIMIT = 50
const REQUEST_OUTCOMES: readonly RequestOutcome[] = [
  'success',
  'upstream_error',
  'no_available_account',
  'router_error',
  'account_unavailable',
  'upstream_timeout',
  'cancelled',
  'no_extractable_text',
]
const RESPONSE_MODES: readonly RequestResponseMode[] = ['json', 'sse', 'websocket']
const CALENDAR_WEEKDAYS = ['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su'] as const

interface RequestRouteSearch {
  search?: string
  start?: string
  end?: string
  account_id?: string
  outcome?: string
  model?: string
  response_mode?: string
  before?: number
  page?: number
  limit?: number
  detail?: number
}

type BadgeVariant = ComponentProps<typeof Badge>['variant']
type RequestsListQuery = NonNullable<RequestsListData2['query']>
type RequestsOptionsQuery = NonNullable<RequestsOptionsData2['query']>
interface LocalTimestampParts {
  date: string
  time: string
}
interface CalendarDay {
  key: string
  date: Date
  inMonth: boolean
}
interface CursorResolution {
  cursors: Record<number, number>
  matched: boolean
  resolvedPage: number
}
interface ParsedRequestFilters {
  accountIDs: number[]
  outcomes: RequestOutcome[]
  models: string[]
  responseModes: RequestResponseMode[]
}
type RequestColumnID =
  | 'created_at'
  | 'request_id'
  | 'client_ip'
  | 'route'
  | 'account'
  | 'model'
  | 'status'
  | 'outcome'
  | 'mode'
  | 'tokens'
  | 'latency'
  | 'error'

const REQUEST_COLUMNS: Array<{ id: RequestColumnID; label: string }> = [
  { id: 'created_at', label: strings.labels.createdAt },
  { id: 'request_id', label: strings.labels.requestId },
  { id: 'client_ip', label: strings.labels.clientIp },
  { id: 'route', label: strings.labels.route },
  { id: 'account', label: strings.labels.account },
  { id: 'model', label: strings.labels.model },
  { id: 'status', label: strings.labels.status },
  { id: 'outcome', label: strings.labels.outcome },
  { id: 'mode', label: strings.labels.mode },
  { id: 'tokens', label: strings.labels.tokens },
  { id: 'latency', label: strings.labels.latency },
  { id: 'error', label: strings.labels.error },
]
const DEFAULT_REQUEST_COLUMNS = REQUEST_COLUMNS.map((column) => column.id)
const REQUEST_COLUMNS_STORAGE_KEY = 'one-llm-router.requests.columns.v2'
const REQUEST_COLUMNS_PREVIOUS_STORAGE_KEYS = ['one-llm-router.requests.columns.v1']
const MAX_CURSOR_RESOLUTION_PAGES = 200

export function AdminRequests() {
  const navigate = useNavigate()
  const routeSearch = adminRequestsRouteApi.useSearch() as RequestRouteSearch
  const [draftSearch, setDraftSearch] = useState(routeSearch.search ?? '')
  const [pageCursors, setPageCursors] = useState<Record<number, number>>({})
  const [visibleColumns, setVisibleColumns] = useState<RequestColumnID[]>(() =>
    readStoredRequestColumns(),
  )
  const [advancedFiltersOpen, setAdvancedFiltersOpen] = useState(() =>
    hasAdvancedFiltersInSearch(routeSearch),
  )

  useEffect(() => {
    writeStoredRequestColumns(visibleColumns)
  }, [visibleColumns])

  useEffect(() => {
    setDraftSearch(routeSearch.search ?? '')
  }, [routeSearch.search])

  const filters = useMemo(() => filtersFromSearch(routeSearch), [routeSearch])
  const advancedFilterCount = countAdvancedFilters(routeSearch, filters)
  const listQueryParams = useMemo<RequestsListQuery>(
    () => ({
      start: routeSearch.start,
      end: routeSearch.end,
      account_id: filters.accountIDs.length > 0 ? filters.accountIDs : undefined,
      outcome: filters.outcomes.length > 0 ? filters.outcomes : undefined,
      model: filters.models.length > 0 ? filters.models : undefined,
      response_mode: filters.responseModes.length > 0 ? filters.responseModes : undefined,
      search: normalizeOptional(routeSearch.search),
      before: routeSearch.before,
      limit: routeSearch.limit ?? DEFAULT_LIMIT,
    }),
    [filters, routeSearch],
  )
  const cursorResolutionParams = useMemo<RequestsListQuery>(
    () => ({ ...listQueryParams, before: undefined }),
    [listQueryParams],
  )
  const optionsQueryParams = useMemo<RequestsOptionsQuery>(
    () => ({
      start: routeSearch.start,
      end: routeSearch.end,
      search: normalizeOptional(routeSearch.search),
    }),
    [routeSearch.end, routeSearch.search, routeSearch.start],
  )

  const listQuery = useQuery<RequestsListData>({
    queryKey: ['admin', 'requests', 'list', listQueryParams],
    queryFn: async () =>
      callAdmin<RequestsListResponseBody>(requestsList({ query: listQueryParams })),
    staleTime: 5_000,
  })
  const routePage = routeSearch.page ?? 1
  const cursorResolutionQuery = useQuery<CursorResolution>({
    queryKey: [
      'admin',
      'requests',
      'cursor-resolution',
      cursorResolutionParams,
      routeSearch.before,
      routePage,
    ],
    enabled: Boolean(routeSearch.before || routePage > 1),
    queryFn: () => resolveRequestCursorPage(cursorResolutionParams, routeSearch.before, routePage),
    staleTime: 5_000,
  })
  const optionsQuery = useQuery<RequestsOptionsData>({
    queryKey: ['admin', 'requests', 'options', optionsQueryParams],
    queryFn: async () =>
      callAdmin<RequestsOptionsResponseBody>(requestsOptions({ query: optionsQueryParams })),
    staleTime: 10_000,
  })
  const detailID = routeSearch.detail
  const detailQuery = useQuery<RequestDetailData>({
    queryKey: ['admin', 'requests', 'detail', detailID],
    enabled: typeof detailID === 'number',
    queryFn: async () => {
      if (typeof detailID !== 'number') {
        throw new Error('request detail id is required')
      }
      return callAdmin<RequestDetailResponseBody>(requestsGet({ path: { id: detailID } }))
    },
    staleTime: 5_000,
  })

  const records = listQuery.data?.records ?? []
  const currentPage = cursorResolutionQuery.data?.resolvedPage ?? routePage
  const knownPageCursors = useMemo(
    () => mergePageCursors(pageCursors, cursorResolutionQuery.data?.cursors ?? {}),
    [cursorResolutionQuery.data?.cursors, pageCursors],
  )
  const selectedRecord = detailQuery.data?.record ?? null
  const optionAccounts = mergeAccountOptions(optionsQuery.data?.accounts ?? [], filters.accountIDs)
  const optionOutcomes = mergeStringOptions(optionsQuery.data?.outcomes ?? [], filters.outcomes)
  const optionModels = mergeStringOptions(optionsQuery.data?.models ?? [], filters.models)
  const optionModes = mergeStringOptions(
    optionsQuery.data?.response_modes ?? [],
    filters.responseModes,
  )

  useEffect(() => {
    const resolution = cursorResolutionQuery.data
    if (!resolution) return
    if (Object.keys(resolution.cursors).length > 0) {
      setPageCursors((current) => mergePageCursors(current, resolution.cursors))
    }
    const shouldClearStaleCursor = !resolution.matched && routeSearch.before !== undefined
    if (resolution.resolvedPage !== routePage || shouldClearStaleCursor) {
      void navigate({
        to: '/admin/requests',
        search: cleanSearch({
          ...routeSearch,
          before: shouldClearStaleCursor ? undefined : routeSearch.before,
          page: resolution.resolvedPage,
        }),
      })
    }
  }, [cursorResolutionQuery.data, navigate, routePage, routeSearch])

  useEffect(() => {
    if (advancedFilterCount > 0) {
      setAdvancedFiltersOpen(true)
    }
  }, [advancedFilterCount])

  function updateSearch(patch: Partial<RequestRouteSearch>, resetCursor = true) {
    const detailTouched = Object.hasOwn(patch, 'detail')
    const pageTouched = Object.hasOwn(patch, 'page')
    const next: RequestRouteSearch = cleanSearch({
      ...routeSearch,
      ...patch,
      before: resetCursor ? undefined : (patch.before ?? routeSearch.before),
      page: resetCursor ? undefined : pageTouched ? patch.page : routeSearch.page,
      detail: detailTouched ? patch.detail : undefined,
    })
    void navigate({ to: '/admin/requests', search: next })
  }

  function goToRequestPage(page: number, before: number | undefined) {
    updateSearch({ before, page }, false)
  }

  function goToNextPage() {
    const nextBefore = listQuery.data?.next_before_id
    if (!listQuery.data?.has_more || !nextBefore) return
    const nextPage = currentPage + 1
    setPageCursors((current) => ({ ...current, [nextPage]: nextBefore }))
    goToRequestPage(nextPage, nextBefore)
  }

  function goToPreviousPage() {
    if (currentPage <= 1) return
    const previousPage = currentPage - 1
    const previousBefore = previousPage === 1 ? undefined : knownPageCursors[previousPage]
    if (previousPage > 1 && !previousBefore) return
    goToRequestPage(previousPage, previousBefore)
  }

  function resetSearch() {
    void navigate({ to: '/admin/requests', search: {} })
  }

  function submitSearch(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    updateSearch({ search: normalizeOptional(draftSearch) })
  }

  function refreshRequests() {
    void listQuery.refetch()
    void optionsQuery.refetch()
    if (typeof detailID === 'number') void detailQuery.refetch()
  }

  return (
    <Canvas variant="wide">
      <Stripe eyebrow={strings.eyebrow}>{strings.stripe}</Stripe>
      <div className="mb-8 flex flex-wrap items-end justify-between gap-4">
        <div className="min-w-0 flex-1">
          <h1 className="mb-3 max-w-none whitespace-nowrap">
            {strings.titleLead} <strong>{strings.titleStrong}</strong>
          </h1>
          <PageIntro>{strings.intro}</PageIntro>
        </div>
      </div>

      {listQuery.isError ? (
        <ErrorBanner error={listQuery.error} title={strings.listErrorTitle} className="mb-4" />
      ) : null}
      {optionsQuery.isError ? (
        <ErrorBanner
          error={optionsQuery.error}
          title={strings.optionsErrorTitle}
          className="mb-4"
        />
      ) : null}

      <PanelCard
        title={strings.filtersTitle}
        meta={
          <div className="flex flex-wrap items-center justify-end gap-2">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              aria-expanded={advancedFiltersOpen}
              aria-controls="requests-advanced-filters"
              data-testid="requests-advanced-filter-toggle"
              className="min-h-11 sm:min-h-7"
              onClick={() => setAdvancedFiltersOpen((open) => !open)}
            >
              <SlidersHorizontal />
              <span>{strings.advancedFiltersTitle}</span>
              {advancedFilterCount > 0 ? (
                <span
                  className="border border-[var(--accent-hair)] bg-[var(--accent-soft)] px-1.5 py-[1px] font-mono text-[10.5px] text-[var(--accent)]"
                  style={{ borderRadius: 2 }}
                >
                  {strings.advancedFiltersMeta(advancedFilterCount)}
                </span>
              ) : null}
              <ChevronDown
                className={
                  advancedFiltersOpen ? 'rotate-180 transition-transform' : 'transition-transform'
                }
              />
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="min-h-11 sm:min-h-7"
              onClick={resetSearch}
              data-testid="requests-reset"
            >
              <RotateCcw />
              {strings.actions.reset}
            </Button>
          </div>
        }
        metaClassName="max-w-none border-transparent bg-transparent p-0 text-[var(--text-muted)]"
      >
        <Field label={strings.labels.timeRange}>
          <div className="flex flex-nowrap items-center gap-2 overflow-x-auto pb-1 whitespace-nowrap [scrollbar-width:none] [&::-webkit-scrollbar]:hidden [&>button]:shrink-0">
            <FilterButton
              active={!routeSearch.start && !routeSearch.end}
              onClick={() => updateSearch({ start: undefined, end: undefined })}
            >
              {strings.ranges.all}
            </FilterButton>
            <FilterButton
              active={isQuickRange(routeSearch, 'hour')}
              onClick={() => updateSearch(quickRangePatch('hour'))}
            >
              {strings.ranges.hour}
            </FilterButton>
            <FilterButton
              active={isQuickRange(routeSearch, 'day')}
              onClick={() => updateSearch(quickRangePatch('day'))}
            >
              {strings.ranges.day}
            </FilterButton>
            <FilterButton
              active={isQuickRange(routeSearch, 'week')}
              onClick={() => updateSearch(quickRangePatch('week'))}
            >
              {strings.ranges.week}
            </FilterButton>
            <DateTimePicker
              label={strings.labels.start}
              value={routeSearch.start}
              placeholder={strings.timeBoundary.fromAny}
              defaultTime="00:00"
              onChange={(value) =>
                updateSearch({
                  start: value,
                  before: undefined,
                })
              }
            />
            <DateTimePicker
              label={strings.labels.end}
              value={routeSearch.end}
              placeholder={strings.timeBoundary.toNow}
              defaultTime="23:59"
              onChange={(value) =>
                updateSearch({
                  end: value,
                  before: undefined,
                })
              }
            />
          </div>
        </Field>

        <Field label={strings.labels.accounts}>
          <AccountFacet
            accounts={optionAccounts}
            selected={filters.accountIDs}
            isLoading={optionsQuery.isLoading}
            onToggle={(id) =>
              updateSearch({
                account_id: csvFromNumbers(toggleNumber(filters.accountIDs, id)),
              })
            }
          />
        </Field>

        <Field label={strings.labels.models}>
          <StringFacet
            values={optionModels}
            selected={filters.models}
            isLoading={optionsQuery.isLoading}
            emptyLabel={strings.noFacets}
            onToggle={(value) =>
              updateSearch({ model: csvFromStrings(toggleString(filters.models, value)) })
            }
          />
        </Field>

        {advancedFiltersOpen ? (
          <div
            id="requests-advanced-filters"
            className="mt-5 grid gap-4 border-t border-[var(--line)] pt-4"
          >
            <form onSubmit={submitSearch}>
              <Field
                htmlFor="requests-search"
                label={strings.labels.search}
                hint={strings.labels.searchHint}
              >
                <div className="flex flex-col gap-2 md:flex-row">
                  <Input
                    id="requests-search"
                    data-testid="requests-search-input"
                    value={draftSearch}
                    placeholder={strings.placeholders.search}
                    onChange={(event) => setDraftSearch(event.target.value)}
                    autoComplete="off"
                    spellCheck={false}
                  />
                  <Button type="submit" data-testid="requests-search-apply">
                    <Search />
                    {strings.actions.apply}
                  </Button>
                </div>
              </Field>
            </form>

            <Field label={strings.labels.outcomes}>
              <StringFacet
                values={optionOutcomes}
                selected={filters.outcomes}
                isLoading={optionsQuery.isLoading}
                emptyLabel={strings.noFacets}
                onToggle={(value) =>
                  updateSearch({
                    outcome: csvFromStrings(toggleString(filters.outcomes, value)),
                  })
                }
              />
            </Field>

            <Field label={strings.labels.responseModes}>
              <StringFacet
                values={optionModes}
                selected={filters.responseModes}
                isLoading={optionsQuery.isLoading}
                emptyLabel={strings.noFacets}
                onToggle={(value) =>
                  updateSearch({
                    response_mode: csvFromStrings(toggleString(filters.responseModes, value)),
                  })
                }
              />
            </Field>
          </div>
        ) : null}
      </PanelCard>

      <PanelCard
        title={strings.tableTitle}
        meta={
          <div className="flex flex-wrap items-center justify-end gap-2">
            <ColumnSelector
              visibleColumns={visibleColumns}
              onToggle={(columnID) =>
                setVisibleColumns((current) => toggleColumn(current, columnID))
              }
            />
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="min-h-11 sm:min-h-7"
              onClick={refreshRequests}
              data-testid="requests-refresh"
            >
              <RefreshCw />
              {strings.actions.refresh}
            </Button>
          </div>
        }
        metaClassName="max-w-none border-transparent bg-transparent p-0 text-[var(--text-muted)]"
      >
        <RequestTable
          records={records}
          selectedID={detailID}
          isLoading={listQuery.isLoading}
          isError={listQuery.isError}
          visibleColumns={visibleColumns}
          onOpen={(id) => updateSearch({ detail: id }, false)}
        />
        <div className="mt-4 flex flex-wrap items-center justify-end gap-3 border-t border-[var(--line)] pt-4">
          <div className="flex flex-wrap items-center justify-end gap-3">
            <PaginationControls
              currentPage={currentPage}
              hasNext={Boolean(listQuery.data?.has_more && listQuery.data.next_before_id)}
              canPrevious={currentPage === 2 || knownPageCursors[currentPage - 1] !== undefined}
              onPrevious={goToPreviousPage}
              onNext={goToNextPage}
            />
            <div className="flex items-center gap-2">
              <span className="hidden font-mono text-[10.5px] uppercase tracking-[0.08em] text-[var(--text-muted)] sm:inline">
                {strings.labels.pageSize}
              </span>
              <Select
                value={String(routeSearch.limit ?? DEFAULT_LIMIT)}
                onValueChange={(value) => updateSearch({ limit: Number(value) })}
              >
                <SelectTrigger
                  aria-label={strings.labels.pageSize}
                  data-testid="requests-limit-select"
                  className="h-11 min-h-11 w-[86px] font-mono text-[11.5px] sm:h-8 sm:min-h-8"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {[10, 25, 50, 100, 200].map((limit) => (
                    <SelectItem key={limit} value={String(limit)}>
                      {limit}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
        </div>
      </PanelCard>

      {typeof detailID === 'number' ? (
        <RequestDetailLayer
          record={selectedRecord}
          isLoading={detailQuery.isLoading}
          isError={detailQuery.isError}
          error={detailQuery.error}
          onClose={() => updateSearch({ detail: undefined }, false)}
        />
      ) : null}
    </Canvas>
  )
}

interface RequestTableProps {
  records: RequestLogRow[]
  selectedID?: number
  isLoading: boolean
  isError: boolean
  visibleColumns: RequestColumnID[]
  onOpen: (id: number) => void
}

function RequestTable({
  records,
  selectedID,
  isLoading,
  isError,
  visibleColumns,
  onOpen,
}: RequestTableProps) {
  if (isLoading) {
    return (
      <div
        role="status"
        data-testid="requests-loading"
        className="text-[13px] text-[var(--text-dim)]"
      >
        {strings.loading}
      </div>
    )
  }
  if (isError) {
    return null
  }
  if (records.length === 0) {
    return (
      <div data-testid="requests-empty" className="text-[13px] text-[var(--text-dim)]">
        {strings.empty}
      </div>
    )
  }
  const visible = new Set(visibleColumns)
  const show = (columnID: RequestColumnID) => visible.has(columnID)
  return (
    <div className="overflow-x-auto" data-testid="requests-table">
      <table className="min-w-[720px] w-full border-collapse text-left text-[12.5px]">
        <thead>
          <tr className="border-b border-[var(--line)] text-[10.5px] uppercase tracking-[0.08em] text-[var(--text-muted)]">
            {show('created_at') ? <Th>{strings.labels.createdAt}</Th> : null}
            {show('request_id') ? <Th>{strings.labels.requestId}</Th> : null}
            {show('client_ip') ? <Th>{strings.labels.clientIp}</Th> : null}
            {show('route') ? <Th>{strings.labels.route}</Th> : null}
            {show('account') ? <Th>{strings.labels.account}</Th> : null}
            {show('model') ? <Th>{strings.labels.model}</Th> : null}
            {show('status') ? <Th>{strings.labels.status}</Th> : null}
            {show('outcome') ? <Th>{strings.labels.outcome}</Th> : null}
            {show('mode') ? <Th>{strings.labels.mode}</Th> : null}
            {show('tokens') ? <Th>{strings.labels.tokens}</Th> : null}
            {show('latency') ? <Th>{strings.labels.latency}</Th> : null}
            {show('error') ? <Th>{strings.labels.error}</Th> : null}
            <Th stickyRight>{strings.labels.actions}</Th>
          </tr>
        </thead>
        <tbody>
          {records.map((record) => (
            <tr
              key={record.id}
              data-testid={`request-row-${record.id}`}
              data-active={record.id === selectedID || undefined}
              className="group border-b border-[var(--line)] align-top data-[active=true]:bg-[var(--panel-hi)]"
            >
              {show('created_at') ? (
                <Td mono>
                  <TimeCell value={record.created_at} />
                </Td>
              ) : null}
              {show('request_id') ? (
                <Td mono>
                  <RequestIDValue requestID={record.request_id} />
                </Td>
              ) : null}
              {show('client_ip') ? (
                <Td mono>
                  <div
                    className="max-w-[132px] truncate whitespace-nowrap"
                    title={record.client_ip}
                  >
                    {record.client_ip || strings.emptyField}
                  </div>
                </Td>
              ) : null}
              {show('route') ? (
                <Td>
                  <div
                    className="max-w-[220px] truncate font-mono text-[11.5px] text-[var(--text)]"
                    title={record.path}
                  >
                    {record.path}
                  </div>
                </Td>
              ) : null}
              {show('account') ? (
                <Td>
                  <div
                    className="max-w-[160px] truncate text-[var(--text)]"
                    title={accountLabel(record)}
                  >
                    {accountLabel(record)}
                  </div>
                  <div
                    className="max-w-[160px] truncate font-mono text-[10.5px] text-[var(--text-muted)]"
                    title={record.session_key ?? undefined}
                  >
                    {record.session_key ?? strings.emptyField}
                  </div>
                </Td>
              ) : null}
              {show('model') ? (
                <Td mono>
                  <div
                    className="max-w-[160px] truncate whitespace-nowrap"
                    title={record.model ?? undefined}
                  >
                    {record.model ?? strings.emptyField}
                  </div>
                </Td>
              ) : null}
              {show('status') ? (
                <Td>
                  <Badge variant={statusBadgeVariant(record.status_code)}>
                    {record.status_code}
                  </Badge>
                </Td>
              ) : null}
              {show('outcome') ? (
                <Td>
                  <Badge variant={outcomeBadgeVariant(record.outcome)}>{record.outcome}</Badge>
                </Td>
              ) : null}
              {show('mode') ? (
                <Td>
                  <Badge variant="outline">{record.response_mode}</Badge>
                </Td>
              ) : null}
              {show('tokens') ? <Td mono>{formatUsage(record.token_usage)}</Td> : null}
              {show('latency') ? <Td mono>{record.latency_ms} ms</Td> : null}
              {show('error') ? <Td mono>{record.error_code ?? strings.emptyField}</Td> : null}
              <Td stickyRight>
                <Button
                  type="button"
                  size="sm"
                  variant={record.id === selectedID ? 'default' : 'secondary'}
                  className="min-h-11 focus-visible:[box-shadow:none] sm:min-h-7"
                  onClick={() => onOpen(record.id)}
                  data-testid={`request-open-${record.id}`}
                  aria-label={`Open request detail ${record.request_id}`}
                >
                  {strings.actions.open}
                </Button>
              </Td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function ColumnSelector({
  visibleColumns,
  onToggle,
}: {
  visibleColumns: RequestColumnID[]
  onToggle: (columnID: RequestColumnID) => void
}) {
  const [open, setOpen] = useState(false)
  const visible = new Set(visibleColumns)
  return (
    <div className="relative">
      <Button
        type="button"
        variant="ghost"
        size="sm"
        aria-expanded={open}
        aria-controls="requests-column-selector"
        data-testid="requests-columns-button"
        className="min-h-11 sm:min-h-7"
        onClick={() => setOpen((current) => !current)}
      >
        <Columns3 />
        {strings.labels.columns}
      </Button>
      {open ? (
        <>
          <button
            type="button"
            className="fixed inset-0 z-40 cursor-default bg-transparent"
            aria-label={strings.actions.close}
            tabIndex={-1}
            onClick={() => setOpen(false)}
          />
          <div
            id="requests-column-selector"
            data-testid="requests-column-selector"
            className="fixed top-[132px] right-4 z-50 w-[min(280px,calc(100vw-2rem))] border border-[var(--line)] bg-[var(--panel)] p-2 shadow-[0_18px_60px_rgba(0,0,0,0.24)] sm:right-6"
            style={{ borderRadius: 2 }}
          >
            <div className="mb-2 flex items-center justify-between gap-3 border-b border-[var(--line)] px-2 pb-2">
              <div className="text-[11.5px] font-medium uppercase tracking-[0.1em] text-[var(--text-dim)]">
                {strings.columnsTitle}
              </div>
              <div className="font-mono text-[10.5px] text-[var(--text-muted)]">
                {strings.columnsMeta(visibleColumns.length, REQUEST_COLUMNS.length)}
              </div>
            </div>
            <div className="grid gap-1">
              {REQUEST_COLUMNS.map((column) => {
                const active = visible.has(column.id)
                const disabled = active && visibleColumns.length === 1
                return (
                  <button
                    key={column.id}
                    type="button"
                    className="flex min-h-11 cursor-pointer items-center justify-between gap-3 px-2 text-left text-[12.5px] text-[var(--text)] transition-colors hover:bg-[var(--panel-hi)] disabled:cursor-not-allowed disabled:opacity-55 sm:min-h-9"
                    style={{ borderRadius: 2 }}
                    aria-pressed={active}
                    disabled={disabled}
                    onClick={() => onToggle(column.id)}
                  >
                    <span>{column.label}</span>
                    <span
                      className={
                        active
                          ? 'flex h-5 w-5 items-center justify-center text-[var(--accent)]'
                          : 'h-5 w-5'
                      }
                    >
                      {active ? <Check className="h-4 w-4" /> : null}
                    </span>
                  </button>
                )
              })}
            </div>
          </div>
        </>
      ) : null}
    </div>
  )
}

function PaginationControls({
  currentPage,
  hasNext,
  canPrevious,
  onPrevious,
  onNext,
}: {
  currentPage: number
  hasNext: boolean
  canPrevious: boolean
  onPrevious: () => void
  onNext: () => void
}) {
  return (
    <div
      className="inline-flex items-stretch overflow-hidden border border-[var(--line-2)] bg-[var(--panel-2)] shadow-[0_1px_0_color-mix(in_oklch,var(--line)_65%,transparent)]"
      data-testid="requests-pagination"
      style={{ borderRadius: 2 }}
    >
      <Button
        type="button"
        variant="ghost"
        size="sm"
        disabled={!canPrevious}
        aria-label={strings.pagination.previous}
        onClick={onPrevious}
        className="h-11 min-h-11 w-11 border-0 px-0 text-[var(--text-dim)] shadow-none hover:bg-[var(--panel-hi)] disabled:opacity-35 sm:h-8 sm:min-h-8 sm:w-8"
      >
        <ChevronLeft className="h-4 w-4" />
      </Button>
      <span
        role="status"
        aria-current="page"
        aria-label={strings.pagination.current(currentPage)}
        className="inline-flex h-11 min-w-[74px] items-center justify-center border-x border-[var(--line-2)] bg-[var(--panel)] px-2 font-mono text-[11.5px] font-medium text-[var(--text)] sm:h-8 sm:min-w-[86px] sm:px-3"
      >
        {strings.pagination.page(currentPage)}
      </span>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        disabled={!hasNext}
        aria-label={strings.pagination.next}
        onClick={onNext}
        className="h-11 min-h-11 w-11 border-0 px-0 text-[var(--text-dim)] shadow-none hover:bg-[var(--panel-hi)] disabled:opacity-35 sm:h-8 sm:min-h-8 sm:w-8"
      >
        <ChevronRight className="h-4 w-4" />
      </Button>
    </div>
  )
}

function TimeCell({ value }: { value: string }) {
  const timestamp = formatLocalTimestamp(value)
  return (
    <div className="whitespace-nowrap" title={value}>
      <div>{timestamp.date}</div>
      {timestamp.time ? (
        <div className="text-[10.5px] text-[var(--text-muted)]">{timestamp.time}</div>
      ) : null}
    </div>
  )
}

function RequestIDValue({ requestID }: { requestID: string }) {
  return (
    <div
      className="flex min-h-11 items-center gap-1.5 whitespace-nowrap leading-[16px] sm:min-h-[16px] sm:items-start sm:pt-px"
      title={requestID}
    >
      <span className="leading-[16px]">{middleEllipsis(requestID)}</span>
      <CopyRequestIDButton requestID={requestID} />
    </div>
  )
}

function CopyRequestIDButton({ requestID }: { requestID: string }) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      className="h-11 min-h-11 w-11 shrink-0 p-0 text-[var(--text-muted)] hover:text-[var(--text)] sm:h-4 sm:min-h-4 sm:w-4 [&>svg]:h-4 [&>svg]:w-4 sm:[&>svg]:h-3.5 sm:[&>svg]:w-3.5"
      aria-label={`${strings.actions.copyRequestId} ${requestID}`}
      onClick={() => copyText(requestID)}
    >
      <Clipboard />
    </Button>
  )
}

function RequestDetailLayer({
  record,
  isLoading,
  isError,
  error,
  onClose,
}: {
  record: RequestLogDetail | null
  isLoading: boolean
  isError: boolean
  error: unknown
  onClose: () => void
}) {
  useEffect(() => {
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === 'Escape') {
        onClose()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => {
      document.body.style.overflow = previousOverflow
      window.removeEventListener('keydown', handleKeyDown)
    }
  }, [onClose])

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center px-3 py-4 sm:px-6"
      data-testid="request-detail-layer"
    >
      <button
        type="button"
        tabIndex={-1}
        className="absolute inset-0 bg-[color-mix(in_oklch,var(--bg)_62%,transparent)]"
        aria-label={strings.actions.close}
        onClick={onClose}
      />
      <section
        role="dialog"
        aria-modal="true"
        aria-labelledby="request-detail-title"
        className="relative z-10 flex h-[min(900px,calc(100dvh-2rem))] w-[min(1180px,calc(100vw-1.5rem))] flex-col overflow-hidden border border-[var(--line)] bg-[var(--panel)] shadow-[0_24px_80px_rgba(0,0,0,0.28)] sm:w-[min(1180px,calc(100vw-3rem))]"
        style={{ borderRadius: 2 }}
      >
        <header className="flex min-h-12 items-center justify-between gap-4 border-b border-[var(--line)] bg-[var(--panel-head)] px-4 py-3">
          <div className="min-w-0">
            <h2
              id="request-detail-title"
              className="text-[11.5px] font-medium uppercase tracking-[0.1em] text-[var(--text-dim)]"
            >
              {strings.detailTitle}
            </h2>
            {record ? (
              <div
                className="mt-1 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 font-mono text-[12px] text-[var(--text-muted)]"
                title={record.request_id}
              >
                <DetailTimestamp value={record.created_at} />
                <span className="min-w-0 break-all">{record.request_id}</span>
                <CopyRequestIDButton requestID={record.request_id} />
              </div>
            ) : null}
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label={strings.actions.close}
              className="border-0 shadow-none"
              onClick={onClose}
            >
              <X />
            </Button>
          </div>
        </header>

        <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4 sm:px-5">
          {isError ? (
            <ErrorBanner error={error} title={strings.detailErrorTitle} className="mb-4" />
          ) : null}
          {isLoading ? (
            <div role="status" className="text-[13px] text-[var(--text-dim)]">
              {strings.detailMetaLoading}
            </div>
          ) : null}
          {!isLoading && !isError && !record ? (
            <div data-testid="request-detail-empty" className="text-[13px] text-[var(--text-dim)]">
              {strings.detailEmpty}
            </div>
          ) : null}
          {record ? (
            <div data-testid="request-detail-view" className="space-y-4">
              <RequestDetailSummary record={record} />

              <RequestFlowTrace record={record} />
            </div>
          ) : null}
        </div>
      </section>
    </div>
  )
}

function DetailTimestamp({ value }: { value: string }) {
  const timestamp = formatLocalTimestamp(value)
  const text = timestamp.time ? `${timestamp.date} ${timestamp.time}` : timestamp.date
  return (
    <span
      className="shrink-0 border border-[var(--line)] bg-[var(--panel)] px-1.5 py-0.5 text-[11px] text-[var(--text-dim)]"
      data-testid="request-detail-created-at"
      title={value}
    >
      {text}
    </span>
  )
}

function RequestDetailSummary({ record }: { record: RequestLogDetail }) {
  const tokenUsage = tokenUsageParts(record.token_usage)
  const tokenUsageTooltip = tokenUsage?.tooltip ?? tokenUsageLegendTooltip()
  return (
    <div
      data-testid="request-detail-summary"
      className="grid border border-[var(--line)] bg-[var(--panel-hi)] md:grid-cols-2 xl:grid-cols-6"
      style={{ borderRadius: 2 }}
    >
      <SummaryCell label={strings.labels.account}>{accountLabel(record)}</SummaryCell>
      <SummaryCell label={strings.labels.model}>{record.model ?? strings.emptyField}</SummaryCell>
      <SummaryCell label={strings.labels.status}>
        <Badge variant={statusBadgeVariant(record.status_code)}>{record.status_code}</Badge>
      </SummaryCell>
      <SummaryCell label={strings.labels.outcome}>
        <Badge variant={outcomeBadgeVariant(record.outcome)}>{record.outcome}</Badge>
      </SummaryCell>
      <SummaryCell
        label={<TokenUsageLabel tooltip={tokenUsageTooltip} />}
        contentClassName="whitespace-normal leading-[1.45]"
      >
        <TokenUsageDetail usage={tokenUsage} tooltip={tokenUsageTooltip} />
      </SummaryCell>
      <SummaryCell label={strings.labels.latency}>
        <LatencyDetail totalMs={record.latency_ms} ttftMs={record.ttft_ms} />
      </SummaryCell>
    </div>
  )
}

function SummaryCell({
  label,
  children,
  contentClassName = 'truncate',
}: {
  label: ReactNode
  children: ReactNode
  contentClassName?: string
}) {
  return (
    <div className="min-w-0 border-t border-[var(--line)] px-3 py-2.5 first:border-t-0 xl:border-t-0 xl:border-l xl:first:border-l-0">
      <div className="mb-1 text-[10.5px] font-medium uppercase tracking-[0.1em] text-[var(--text-muted)]">
        {label}
      </div>
      <div className={`font-mono text-[12px] text-[var(--text)] ${contentClassName}`}>
        {children}
      </div>
    </div>
  )
}

function TokenUsageLabel({ tooltip }: { tooltip: string }) {
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5">
      <span>{strings.labels.tokenUsage}</span>
      <InlineTooltip
        aria-label={strings.labels.tokenUsageHelp}
        content={tooltip}
        role="img"
        testId="request-detail-token-usage-help"
      >
        <CircleHelp aria-hidden="true" className="h-3 w-3" strokeWidth={1.8} />
      </InlineTooltip>
    </span>
  )
}

function TokenUsageDetail({ usage, tooltip }: { usage: TokenUsageParts | null; tooltip: string }) {
  if (!usage) {
    return (
      <InlineTooltip
        content={tooltip}
        contentClassName="left-0"
        testId="request-detail-token-usage"
      >
        {strings.emptyField}
      </InlineTooltip>
    )
  }
  return (
    <InlineTooltip
      content={usage.tooltip}
      contentClassName="left-0"
      testId="request-detail-token-usage"
    >
      {usage.label}
    </InlineTooltip>
  )
}

function InlineTooltip({
  children,
  content,
  testId,
  contentClassName = 'left-1/2 -translate-x-1/2',
  ...props
}: {
  children: ReactNode
  content: string
  testId?: string
  contentClassName?: string
} & Omit<ComponentProps<'span'>, 'content'>) {
  return (
    <span
      className="group/tooltip relative inline-flex min-w-0 cursor-help items-center"
      data-testid={testId}
      {...props}
    >
      {children}
      <span
        aria-hidden="true"
        className={`pointer-events-none absolute top-full z-50 mt-1 hidden w-max max-w-[280px] border border-[var(--line)] bg-[var(--panel)] px-2 py-1.5 text-left font-mono text-[11px] leading-[1.45] font-normal tracking-normal whitespace-pre-line text-[var(--text)] normal-case shadow-[0_10px_24px_rgba(0,0,0,0.18)] group-hover/tooltip:block ${contentClassName}`}
        data-testid={testId ? `${testId}-tooltip` : undefined}
        style={{ borderRadius: 2 }}
      >
        {content}
      </span>
    </span>
  )
}

interface FlowPayloadRow {
  label: string
  value: ReactNode
  mono?: boolean
  title?: string
}

interface FlowPayloadStep {
  id: string
  connectorOnly?: false
  title: string
  icon: LucideIcon
  value?: string | null
  rows: FlowPayloadRow[]
}

interface FlowConnectorStep {
  id: string
  connectorOnly: true
}

type FlowTraceStep = FlowPayloadStep | FlowConnectorStep

interface FlowTraceNode {
  id: string
  label: string
  meta: string
  icon: LucideIcon
}

function RequestFlowTrace({ record }: { record: RequestLogDetail }) {
  const route = `${record.method} ${record.path}`
  const baseURL = serverBaseURL(record)
  const endpoint = serverEndpoint(record)
  const nodes: FlowTraceNode[] = [
    {
      id: 'client',
      label: strings.flow.client,
      meta: record.client_ip || strings.emptyField,
      icon: Monitor,
    },
    {
      id: 'router',
      label: strings.flow.router,
      meta: route,
      icon: Router,
    },
    {
      id: 'llm-server',
      label: strings.flow.llmServer,
      meta: baseURL,
      icon: Server,
    },
    {
      id: 'router-return',
      label: strings.flow.router,
      meta: strings.flow.responseRouter,
      icon: Router,
    },
    {
      id: 'client-return',
      label: strings.flow.client,
      meta: record.client_ip || strings.emptyField,
      icon: Monitor,
    },
  ]
  const edges: FlowTraceStep[] = [
    {
      id: 'client-to-router',
      title: strings.flow.clientToRouter,
      icon: Braces,
      value: record.client_request_body,
      rows: [
        {
          label: strings.labels.route,
          value: route,
          mono: true,
        },
        {
          label: strings.labels.requestId,
          value: middleEllipsis(record.request_id),
          title: record.request_id,
          mono: true,
        },
      ],
    },
    {
      id: 'router-to-server',
      title: strings.flow.routerToServer,
      icon: SendHorizontal,
      value: record.upstream_request_body,
      rows: [
        {
          label: strings.labels.baseURL,
          value: baseURL,
          title: baseURL,
          mono: true,
        },
        {
          label: strings.labels.endpoint,
          value: endpoint,
          title: endpoint,
          mono: true,
        },
        {
          label: strings.labels.account,
          value: accountLabel(record),
        },
        {
          label: strings.labels.model,
          value: record.model ?? strings.emptyField,
          mono: true,
        },
      ],
    },
    {
      id: 'server-to-router',
      title: strings.flow.serverToRouter,
      icon: Reply,
      value: record.upstream_response_body,
      rows: [
        {
          label: strings.labels.status,
          value: (
            <Badge variant={statusBadgeVariant(record.status_code)}>{record.status_code}</Badge>
          ),
        },
        {
          label: strings.labels.outcome,
          value: <Badge variant={outcomeBadgeVariant(record.outcome)}>{record.outcome}</Badge>,
        },
        {
          label: strings.labels.latency,
          value: <LatencyDetail totalMs={record.latency_ms} ttftMs={record.ttft_ms} />,
          mono: true,
        },
      ],
    },
    {
      id: 'router-to-client',
      connectorOnly: true,
    },
  ]

  return (
    <section
      data-testid="request-flow-map"
      className="min-w-0 max-w-full overflow-hidden border border-[var(--line)] bg-[var(--panel-hi)]"
      style={{ borderRadius: 2 }}
    >
      <header className="flex min-h-12 flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-[var(--line)] bg-[var(--panel-head)] px-3 py-2 sm:px-4">
        <div className="text-[11.5px] font-medium uppercase tracking-[0.1em] text-[var(--text-dim)]">
          {strings.flow.title}
        </div>
        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 font-mono text-[10.5px] text-[var(--text-muted)]">
          <span className="truncate">{record.account?.provider ?? strings.emptyField}</span>
          <span className="text-[var(--line-3)]">/</span>
          <span>{record.response_mode}</span>
          <span className="text-[var(--line-3)]">/</span>
          <span className="max-w-[220px] truncate" title={baseURL}>
            {baseURL}
          </span>
        </div>
      </header>
      <div
        className="grid min-w-0 gap-y-3 bg-[var(--panel)] px-3 py-4 sm:px-4 xl:grid-cols-[250px_minmax(0,1fr)] xl:gap-y-4"
        style={{
          backgroundImage:
            'linear-gradient(var(--grid) 1px, transparent 1px), linear-gradient(90deg, var(--grid) 1px, transparent 1px)',
          backgroundSize: 'var(--grid-size) var(--grid-size)',
        }}
        data-testid="request-log-inspector"
      >
        {nodes.map((node, index) => (
          <Fragment key={node.id}>
            <FlowTraceNodeRail node={node} />
            <div className="hidden xl:block" />
            {edges[index] ? (
              <>
                <FlowTraceEdgeRail connectorOnly={isConnectorStep(edges[index])} />
                {isConnectorStep(edges[index]) ? (
                  <div className="hidden xl:block" />
                ) : (
                  <FlowPayloadSection
                    title={edges[index].title}
                    icon={edges[index].icon}
                    value={edges[index].value}
                    rows={edges[index].rows}
                  />
                )}
              </>
            ) : null}
          </Fragment>
        ))}
      </div>
    </section>
  )
}

function isConnectorStep(step: FlowTraceStep): step is FlowConnectorStep {
  return step.connectorOnly === true
}

function FlowTraceNodeRail({ node }: { node: FlowTraceNode }) {
  return (
    <div className="relative min-w-0 xl:pr-6">
      <div className="grid min-h-[68px] min-w-0 grid-cols-[32px_minmax(0,1fr)] items-center gap-3 border border-[var(--line)] bg-[var(--panel-hi)] px-3 py-3 shadow-[0_1px_0_rgba(0,0,0,0.08)]">
        <div className="relative flex h-full min-h-9 items-center justify-center">
          <div className="relative z-10 flex h-7 w-7 items-center justify-center bg-[var(--panel-hi)] text-[var(--text-dim)]">
            <node.icon className="size-[18px]" strokeWidth={1.8} aria-hidden="true" />
          </div>
        </div>
        <div className="min-w-0">
          <div className="truncate text-[12.5px] font-medium text-[var(--text)]">{node.label}</div>
          <div
            className="mt-1 truncate font-mono text-[11.5px] text-[var(--text-muted)]"
            title={node.meta}
          >
            {node.meta}
          </div>
        </div>
      </div>
    </div>
  )
}

function FlowTraceEdgeRail({ connectorOnly = false }: { connectorOnly?: boolean }) {
  return (
    <div
      className={
        connectorOnly
          ? 'relative hidden min-h-10 min-w-0 xl:block xl:pr-6'
          : 'relative hidden min-h-[210px] min-w-0 xl:block xl:pr-6'
      }
    >
      <div className="absolute top-[-1rem] bottom-[-1rem] left-[26px] w-px bg-[var(--line-2)]" />
      {connectorOnly ? null : (
        <>
          <div className="absolute top-1/2 left-[23px] z-10 h-2 w-2 -translate-y-1/2 bg-[var(--accent)] shadow-[0_0_0_3px_var(--panel)]" />
          <div className="absolute top-1/2 right-0 left-[58px] border-t border-dashed border-[var(--line-3)]" />
          <ArrowDown
            aria-hidden="true"
            className="absolute top-1/2 left-[18px] size-4 -translate-y-1/2 text-[var(--accent)]"
            strokeWidth={1.8}
          />
        </>
      )}
    </div>
  )
}

function FlowPayloadSection({
  title,
  icon: Icon,
  value,
  rows,
}: {
  title: string
  icon: LucideIcon
  value?: string | null
  rows: FlowPayloadRow[]
}) {
  const preview = buildJsonPreviewFromText(value, strings.bodyNotCaptured)
  return (
    <section className="min-w-0 max-w-full overflow-hidden border border-[var(--line)] bg-[var(--panel-hi)] shadow-[0_1px_0_rgba(0,0,0,0.08)]">
      <header className="flex min-h-11 items-center justify-between gap-3 px-3 pt-3 pb-2">
        <div className="flex min-w-0 items-start gap-2.5">
          <Icon
            className="mt-0.5 size-4 shrink-0 text-[var(--accent)]"
            strokeWidth={1.8}
            aria-hidden="true"
          />
          <div className="min-w-0">
            <div className="truncate text-[12.5px] font-medium text-[var(--text)]">{title}</div>
          </div>
        </div>
      </header>
      <dl className="grid gap-x-4 gap-y-2 px-3 py-2 text-[11.5px] md:grid-cols-2 xl:grid-cols-4">
        {rows.map((row) => (
          <div key={row.label} className="min-w-0">
            <dt className="font-mono uppercase tracking-[0.08em] text-[var(--text-muted)]">
              {row.label}
            </dt>
            <dd
              className={
                row.mono
                  ? 'mt-1 min-w-0 truncate font-mono text-[var(--text)]'
                  : 'mt-1 min-w-0 truncate text-[var(--text)]'
              }
              title={row.title}
            >
              {row.value}
            </dd>
          </div>
        ))}
      </dl>
      <JsonPreview
        preview={preview}
        className="m-3 mt-2 overflow-hidden border border-[var(--line)] bg-[var(--bg-2)]"
        bodyClassName="max-h-64"
        copyLabel={`Copy ${title}`}
        onCopy={copyText}
      />
    </section>
  )
}

function LatencyDetail({ totalMs, ttftMs }: { totalMs: number; ttftMs?: number | null }) {
  if (typeof ttftMs !== 'number') {
    return (
      <span className="whitespace-nowrap">
        {strings.labels.totalLatency} {formatLatencyValue(totalMs)}
      </span>
    )
  }

  return (
    <span className="whitespace-nowrap">
      {strings.labels.ttft} {formatLatencyValue(ttftMs)} / {strings.labels.totalLatency}{' '}
      {formatLatencyValue(totalMs)}
    </span>
  )
}

function formatLatencyValue(value: number | null | undefined): string {
  return typeof value === 'number' ? `${value} ms` : strings.emptyField
}

function AccountFacet({
  accounts,
  selected,
  isLoading,
  onToggle,
}: {
  accounts: RequestLogAccountOption[]
  selected: number[]
  isLoading: boolean
  onToggle: (id: number) => void
}) {
  if (isLoading) {
    return <div className="text-[13px] text-[var(--text-dim)]">{strings.optionsLoading}</div>
  }
  if (accounts.length === 0) {
    return <div className="text-[13px] text-[var(--text-dim)]">{strings.noFacets}</div>
  }
  return (
    <div className="flex flex-wrap gap-2">
      {accounts.map((option) => (
        <FilterButton
          key={option.id}
          active={selected.includes(option.id)}
          onClick={() => onToggle(option.id)}
        >
          {option.label}
        </FilterButton>
      ))}
    </div>
  )
}

function DateTimePicker({
  label,
  value,
  placeholder,
  defaultTime,
  onChange,
}: {
  label: string
  value?: string
  placeholder: string
  defaultTime: string
  onChange: (value: string | undefined) => void
}) {
  const buttonRef = useRef<HTMLButtonElement>(null)
  const [open, setOpen] = useState(false)
  const [draftDate, setDraftDate] = useState<Date>(() => localDateOnly(value) ?? todayDateOnly())
  const [draftTime, setDraftTime] = useState(() => localTimeValue(value) ?? defaultTime)
  const [visibleMonth, setVisibleMonth] = useState(() => startOfMonth(draftDate))
  const [popoverPosition, setPopoverPosition] = useState({ top: 0, left: 0 })
  const selectedDate = localDateOnly(value)

  useEffect(() => {
    if (!open) return
    const nextDate = localDateOnly(value) ?? todayDateOnly()
    setDraftDate(nextDate)
    setDraftTime(localTimeValue(value) ?? defaultTime)
    setVisibleMonth(startOfMonth(nextDate))
  }, [defaultTime, open, value])

  function openPicker() {
    const rect = buttonRef.current?.getBoundingClientRect()
    if (rect) {
      setPopoverPosition({
        top: Math.min(rect.bottom + 8, window.innerHeight - 390),
        left: Math.min(Math.max(12, rect.left), window.innerWidth - 332),
      })
    }
    setOpen(true)
  }

  function applyDraft() {
    onChange(localDateTimeToISO(draftDate, draftTime))
    setOpen(false)
  }

  function clearDraft() {
    onChange(undefined)
    setOpen(false)
  }

  const days = calendarDays(visibleMonth)
  return (
    <div className="grid min-w-[190px] grid-cols-[auto_minmax(0,1fr)] items-center gap-2">
      <span className="shrink-0 font-mono text-[10.5px] uppercase tracking-[0.08em] text-[var(--text-muted)]">
        {label}
      </span>
      <Button
        ref={buttonRef}
        type="button"
        variant="secondary"
        size="sm"
        aria-expanded={open}
        aria-haspopup="dialog"
        aria-label={label}
        data-testid={`requests-time-${label.toLowerCase()}`}
        className="h-11 min-h-11 w-[150px] justify-start px-2 font-mono text-[11.5px] sm:h-8 sm:min-h-8"
        onClick={openPicker}
      >
        <CalendarDays />
        <span
          className={value ? 'truncate text-[var(--text)]' : 'truncate text-[var(--text-muted)]'}
        >
          {value ? formatPickerValue(value) : placeholder}
        </span>
      </Button>
      {open ? (
        <>
          <button
            type="button"
            className="fixed inset-0 z-40 cursor-default bg-transparent"
            aria-label={strings.actions.close}
            tabIndex={-1}
            onClick={() => setOpen(false)}
          />
          <div
            role="dialog"
            aria-label={`${label} calendar`}
            data-testid={`requests-${label.toLowerCase()}-calendar`}
            className="fixed z-50 w-[min(320px,calc(100vw-1.5rem))] border border-[var(--line)] bg-[var(--panel)] p-3 shadow-[0_18px_60px_rgba(0,0,0,0.24)]"
            style={{ top: popoverPosition.top, left: popoverPosition.left, borderRadius: 2 }}
          >
            <div className="mb-3 flex items-center justify-between gap-2 border-b border-[var(--line)] pb-3">
              <Button
                type="button"
                variant="ghost"
                size="sm"
                aria-label={strings.actions.previousMonth}
                className="min-h-11 w-11 px-0 sm:min-h-7 sm:w-auto sm:px-[10px]"
                onClick={() => setVisibleMonth(addMonths(visibleMonth, -1))}
              >
                <ChevronLeft />
              </Button>
              <div className="font-mono text-[12px] text-[var(--text)]">
                {monthFormatter.format(visibleMonth)}
              </div>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                aria-label={strings.actions.nextMonth}
                className="min-h-11 w-11 px-0 sm:min-h-7 sm:w-auto sm:px-[10px]"
                onClick={() => setVisibleMonth(addMonths(visibleMonth, 1))}
              >
                <ChevronRight />
              </Button>
            </div>
            <div className="grid grid-cols-7 gap-1 text-center font-mono text-[10.5px] uppercase tracking-[0.08em] text-[var(--text-muted)]">
              {CALENDAR_WEEKDAYS.map((weekday) => (
                <div key={weekday} className="py-1">
                  {weekday}
                </div>
              ))}
            </div>
            <div className="mt-1 grid grid-cols-7 gap-1">
              {days.map((day) => {
                const active = sameLocalDate(day.date, draftDate)
                const persisted = selectedDate ? sameLocalDate(day.date, selectedDate) : false
                return (
                  <button
                    key={day.key}
                    type="button"
                    className={
                      active
                        ? 'h-11 border border-[var(--accent)] bg-[var(--accent-soft)] font-mono text-[11.5px] text-[var(--accent)] sm:h-8'
                        : persisted
                          ? 'h-11 border border-[var(--accent-hair)] bg-transparent font-mono text-[11.5px] text-[var(--text)] sm:h-8'
                          : day.inMonth
                            ? 'h-11 border border-transparent bg-transparent font-mono text-[11.5px] text-[var(--text)] hover:border-[var(--line-2)] hover:bg-[var(--panel-hi)] sm:h-8'
                            : 'h-11 border border-transparent bg-transparent font-mono text-[11.5px] text-[var(--text-muted)] opacity-45 hover:border-[var(--line-2)] hover:bg-[var(--panel-hi)] sm:h-8'
                    }
                    style={{ borderRadius: 2 }}
                    aria-pressed={active}
                    onClick={() => {
                      setDraftDate(day.date)
                      if (!sameMonth(day.date, visibleMonth)) {
                        setVisibleMonth(startOfMonth(day.date))
                      }
                    }}
                  >
                    {day.date.getDate()}
                  </button>
                )
              })}
            </div>
            <div className="mt-3 flex items-center justify-between gap-3 border-t border-[var(--line)] pt-3">
              <label className="grid gap-1">
                <span className="font-mono text-[10.5px] uppercase tracking-[0.08em] text-[var(--text-muted)]">
                  {strings.labels.time}
                </span>
                <Input
                  type="time"
                  value={draftTime}
                  aria-label={`${label} time`}
                  className="h-11 w-[116px] font-mono text-[11.5px] sm:h-8"
                  onChange={(event) => setDraftTime(event.target.value || defaultTime)}
                />
              </label>
              <div className="flex items-end gap-2 self-end">
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="min-h-11 sm:min-h-7"
                  onClick={clearDraft}
                >
                  {strings.actions.clear}
                </Button>
                <Button
                  type="button"
                  size="sm"
                  className="min-h-11 sm:min-h-7"
                  onClick={applyDraft}
                >
                  {strings.actions.apply}
                </Button>
              </div>
            </div>
          </div>
        </>
      ) : null}
    </div>
  )
}

function StringFacet<T extends string>({
  values,
  selected,
  isLoading,
  emptyLabel,
  onToggle,
}: {
  values: T[]
  selected: T[]
  isLoading: boolean
  emptyLabel: string
  onToggle: (value: T) => void
}) {
  if (isLoading) {
    return <div className="text-[13px] text-[var(--text-dim)]">{strings.optionsLoading}</div>
  }
  if (values.length === 0) {
    return <div className="text-[13px] text-[var(--text-dim)]">{emptyLabel}</div>
  }
  return (
    <div className="flex flex-wrap gap-2">
      {values.map((value) => (
        <FilterButton key={value} active={selected.includes(value)} onClick={() => onToggle(value)}>
          {value}
        </FilterButton>
      ))}
    </div>
  )
}
function FilterButton({
  active,
  onClick,
  children,
}: {
  active: boolean
  onClick: () => void
  children: ReactNode
}) {
  return (
    <Button
      type="button"
      size="sm"
      variant={active ? 'default' : 'secondary'}
      aria-pressed={active}
      className="min-h-11 min-w-11 sm:min-h-7 sm:min-w-0"
      onClick={onClick}
    >
      {children}
    </Button>
  )
}

function Th({
  children,
  stickyRight,
  className,
}: {
  children: ReactNode
  stickyRight?: boolean
  className?: string
}) {
  return (
    <th
      className={
        stickyRight
          ? 'sticky right-0 z-[2] border-l border-[var(--line)] bg-[var(--panel-head)] px-2 py-2 text-right font-medium shadow-[-8px_0_12px_rgba(0,0,0,0.16)]'
          : `px-2 py-2 font-medium${className ? ` ${className}` : ''}`
      }
    >
      {children}
    </th>
  )
}

function Td({
  children,
  mono,
  stickyRight,
}: {
  children: ReactNode
  mono?: boolean
  stickyRight?: boolean
}) {
  const base = mono ? 'px-2 py-3 font-mono text-[11.5px]' : 'px-2 py-3'
  return (
    <td
      className={
        stickyRight
          ? `${base} sticky right-0 z-[1] border-l border-[var(--line)] bg-[var(--panel)] text-right shadow-[-8px_0_12px_rgba(0,0,0,0.12)] group-data-[active=true]:bg-[var(--panel-hi)]`
          : base
      }
    >
      {children}
    </td>
  )
}

function filtersFromSearch(search: RequestRouteSearch): ParsedRequestFilters {
  return {
    accountIDs: parseNumberCSV(search.account_id),
    outcomes: parseEnumCSV(search.outcome, REQUEST_OUTCOMES),
    models: parseStringCSV(search.model),
    responseModes: parseEnumCSV(search.response_mode, RESPONSE_MODES),
  }
}

function hasAdvancedFiltersInSearch(search: RequestRouteSearch): boolean {
  return countAdvancedFilters(search, filtersFromSearch(search)) > 0
}

function countAdvancedFilters(search: RequestRouteSearch, filters: ParsedRequestFilters): number {
  let count = 0
  if (normalizeOptional(search.search)) count += 1
  if (filters.outcomes.length > 0) count += filters.outcomes.length
  if (filters.responseModes.length > 0) count += filters.responseModes.length
  return count
}

function parseStringCSV(value: string | undefined): string[] {
  if (!value) return []
  return value
    .split(',')
    .map((part) => part.trim())
    .filter((part, index, arr) => part !== '' && arr.indexOf(part) === index)
}

function parseNumberCSV(value: string | undefined): number[] {
  return parseStringCSV(value)
    .map((part) => Number(part))
    .filter((part, index, arr) => Number.isInteger(part) && part > 0 && arr.indexOf(part) === index)
}

function parseEnumCSV<T extends string>(value: string | undefined, allowed: readonly T[]): T[] {
  return parseStringCSV(value).filter((item): item is T => includesString(allowed, item))
}

function includesString<T extends string>(allowed: readonly T[], value: string): value is T {
  return (allowed as readonly string[]).includes(value)
}

function csvFromStrings(values: string[]): string | undefined {
  return values.length > 0 ? values.join(',') : undefined
}

function csvFromNumbers(values: number[]): string | undefined {
  return values.length > 0 ? values.join(',') : undefined
}

function toggleString<T extends string>(values: T[], value: T): T[] {
  return values.includes(value) ? values.filter((item) => item !== value) : [...values, value]
}

function toggleNumber(values: number[], value: number): number[] {
  return values.includes(value) ? values.filter((item) => item !== value) : [...values, value]
}

function toggleColumn(values: RequestColumnID[], value: RequestColumnID): RequestColumnID[] {
  if (!values.includes(value)) {
    return REQUEST_COLUMNS.filter(
      (column) => column.id === value || values.includes(column.id),
    ).map((column) => column.id)
  }
  if (values.length === 1) {
    return values
  }
  return values.filter((item) => item !== value)
}

function mergeStringOptions<T extends string>(options: readonly T[], selected: readonly T[]): T[] {
  return [...options, ...selected.filter((option) => !options.includes(option))]
}

function mergeAccountOptions(
  options: RequestLogAccountOption[],
  selected: number[],
): RequestLogAccountOption[] {
  const seen = new Set<number>()
  const merged: RequestLogAccountOption[] = []
  for (const option of options) {
    if (!seen.has(option.id)) {
      merged.push(option)
      seen.add(option.id)
    }
  }
  for (const id of selected) {
    if (!seen.has(id)) {
      merged.push({ id, label: `#${id}` })
      seen.add(id)
    }
  }
  return merged
}

function normalizeOptional(value: string | undefined): string | undefined {
  const trimmed = value?.trim()
  return trimmed ? trimmed : undefined
}

function mergePageCursors(
  current: Record<number, number>,
  incoming: Record<number, number>,
): Record<number, number> {
  let changed = false
  const next = { ...current }
  for (const [page, cursor] of Object.entries(incoming)) {
    const pageNumber = Number(page)
    if (!Number.isInteger(pageNumber) || pageNumber <= 1) continue
    if (next[pageNumber] !== cursor) {
      next[pageNumber] = cursor
      changed = true
    }
  }
  return changed ? next : current
}

async function resolveRequestCursorPage(
  baseQuery: RequestsListQuery,
  targetBefore: number | undefined,
  _fallbackPage: number,
): Promise<CursorResolution> {
  if (!targetBefore) {
    return { cursors: {}, matched: true, resolvedPage: 1 }
  }

  const cursors: Record<number, number> = {}
  let before: number | undefined
  for (let page = 1; page <= MAX_CURSOR_RESOLUTION_PAGES; page += 1) {
    const data = await callAdmin<RequestsListResponseBody>(
      requestsList({
        query: {
          ...baseQuery,
          before,
        },
      }),
    )
    const nextBefore = data.next_before_id ?? undefined
    if (!data.has_more || !nextBefore) {
      return { cursors, matched: false, resolvedPage: 1 }
    }

    const nextPage = page + 1
    cursors[nextPage] = nextBefore
    if (nextBefore === targetBefore) {
      return { cursors, matched: true, resolvedPage: nextPage }
    }
    before = nextBefore
  }

  return { cursors, matched: false, resolvedPage: 1 }
}

function cleanSearch(search: RequestRouteSearch): RequestRouteSearch {
  return {
    search: normalizeOptional(search.search),
    start: normalizeOptional(search.start),
    end: normalizeOptional(search.end),
    account_id: normalizeOptional(search.account_id),
    outcome: normalizeOptional(search.outcome),
    model: normalizeOptional(search.model),
    response_mode: normalizeOptional(search.response_mode),
    before: search.before,
    page: search.page && search.page > 1 ? search.page : undefined,
    limit: search.limit,
    detail: search.detail,
  }
}

function readStoredRequestColumns(): RequestColumnID[] {
  if (typeof window === 'undefined') {
    return DEFAULT_REQUEST_COLUMNS
  }
  const current = readStoredRequestColumnsForKey(REQUEST_COLUMNS_STORAGE_KEY)
  if (current) {
    return current
  }
  for (const key of REQUEST_COLUMNS_PREVIOUS_STORAGE_KEYS) {
    const previous = readStoredRequestColumnsForKey(key)
    if (previous) {
      const migrated = dedupeRequestColumns([...previous, 'client_ip'])
      writeStoredRequestColumns(migrated)
      try {
        window.localStorage.removeItem(key)
      } catch {
        // Local persistence cleanup is optional.
      }
      return migrated
    }
  }
  return DEFAULT_REQUEST_COLUMNS
}

function readStoredRequestColumnsForKey(key: string): RequestColumnID[] | null {
  if (typeof window === 'undefined') {
    return null
  }
  try {
    const raw = window.localStorage.getItem(key)
    if (!raw) return null
    const parsed = JSON.parse(raw) as unknown
    if (!Array.isArray(parsed)) return null
    const columns = parsed.filter(isRequestColumnID)
    return columns.length > 0 ? dedupeRequestColumns(columns) : null
  } catch {
    return null
  }
}

function writeStoredRequestColumns(columns: RequestColumnID[]) {
  if (typeof window === 'undefined') {
    return
  }
  try {
    window.localStorage.setItem(REQUEST_COLUMNS_STORAGE_KEY, JSON.stringify(columns))
  } catch {
    // Local persistence is optional; the table still works for this session.
  }
}

function dedupeRequestColumns(columns: RequestColumnID[]): RequestColumnID[] {
  return REQUEST_COLUMNS.filter((column) => columns.includes(column.id)).map((column) => column.id)
}

function isRequestColumnID(value: unknown): value is RequestColumnID {
  return typeof value === 'string' && REQUEST_COLUMNS.some((column) => column.id === value)
}

function quickRangePatch(
  range: 'hour' | 'day' | 'week',
): Pick<RequestRouteSearch, 'start' | 'end'> {
  const end = new Date()
  const hours = range === 'hour' ? 1 : range === 'day' ? 24 : 24 * 7
  return {
    start: new Date(end.getTime() - hours * 60 * 60 * 1000).toISOString(),
    end: end.toISOString(),
  }
}

function isQuickRange(search: RequestRouteSearch, range: 'hour' | 'day' | 'week'): boolean {
  if (!search.start || !search.end) return false
  const start = new Date(search.start).getTime()
  const end = new Date(search.end).getTime()
  if (!Number.isFinite(start) || !Number.isFinite(end)) return false
  const expected = range === 'hour' ? 1 : range === 'day' ? 24 : 24 * 7
  return Math.abs(end - start - expected * 60 * 60 * 1000) < 60_000
}

function formatPickerValue(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime())
    ? strings.timeBoundary.custom
    : pickerValueFormatter.format(date)
}

function localDateOnly(value: string | undefined): Date | null {
  if (!value) return null
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return null
  return new Date(date.getFullYear(), date.getMonth(), date.getDate())
}

function localTimeValue(value: string | undefined): string | null {
  if (!value) return null
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return null
  return `${pad2(date.getHours())}:${pad2(date.getMinutes())}`
}

function localDateTimeToISO(date: Date, time: string): string {
  const [hours = '0', minutes = '0'] = time.split(':')
  return new Date(
    date.getFullYear(),
    date.getMonth(),
    date.getDate(),
    Number(hours),
    Number(minutes),
    0,
  ).toISOString()
}

function todayDateOnly(): Date {
  const date = new Date()
  return new Date(date.getFullYear(), date.getMonth(), date.getDate())
}

function startOfMonth(date: Date): Date {
  return new Date(date.getFullYear(), date.getMonth(), 1)
}

function addMonths(date: Date, amount: number): Date {
  return new Date(date.getFullYear(), date.getMonth() + amount, 1)
}

function addDays(date: Date, amount: number): Date {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate() + amount)
}

function calendarDays(month: Date): CalendarDay[] {
  const monthStart = startOfMonth(month)
  const mondayOffset = (monthStart.getDay() + 6) % 7
  const gridStart = addDays(monthStart, -mondayOffset)
  return Array.from({ length: 42 }, (_, index) => {
    const date = addDays(gridStart, index)
    return {
      key: localDateKey(date),
      date,
      inMonth: sameMonth(date, monthStart),
    }
  })
}

function sameLocalDate(left: Date, right: Date): boolean {
  return (
    left.getFullYear() === right.getFullYear() &&
    left.getMonth() === right.getMonth() &&
    left.getDate() === right.getDate()
  )
}

function sameMonth(left: Date, right: Date): boolean {
  return left.getFullYear() === right.getFullYear() && left.getMonth() === right.getMonth()
}

function localDateKey(date: Date): string {
  return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())}`
}

function pad2(value: number): string {
  return String(value).padStart(2, '0')
}

function formatLocalTimestamp(value: string): LocalTimestampParts {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return { date: value, time: '' }
  }
  return {
    date: localDateFormatter.format(date),
    time: localTimeFormatter.format(date),
  }
}

const localDateFormatter = new Intl.DateTimeFormat(undefined, {
  day: '2-digit',
  month: '2-digit',
  year: 'numeric',
})

const localTimeFormatter = new Intl.DateTimeFormat(undefined, {
  hour: '2-digit',
  hour12: false,
  minute: '2-digit',
  second: '2-digit',
  timeZoneName: 'short',
})

const pickerValueFormatter = new Intl.DateTimeFormat(undefined, {
  day: '2-digit',
  hour: '2-digit',
  hour12: false,
  minute: '2-digit',
  month: '2-digit',
})

const monthFormatter = new Intl.DateTimeFormat(undefined, {
  month: 'long',
  year: 'numeric',
})

function middleEllipsis(value: string, head = 9, tail = 6): string {
  if (value.length <= head + tail + 3) {
    return value
  }
  return `${value.slice(0, head)}...${value.slice(-tail)}`
}

function accountLabel(record: RequestLogRow): string {
  if (record.account?.name) return record.account.name
  if (record.upstream_account_id) return `#${record.upstream_account_id}`
  return strings.emptyField
}

function serverBaseURL(record: RequestLogRow): string {
  return record.account?.base_url ?? strings.flow.baseURLNotCaptured
}

function serverEndpoint(record: RequestLogRow): string {
  const topLevelEndpoint = metadataString(record.router_metadata, 'upstream_endpoint')
  if (topLevelEndpoint) return topLevelEndpoint
  const bridge = bridgeMetadata(record.router_metadata)
  const explicitEndpoint = bridgeString(bridge, 'upstream_endpoint', 'upstream_path')
  if (explicitEndpoint) return explicitEndpoint
  const inferred =
    upstreamEndpointFromContract(bridgeString(bridge, 'upstream_contract')) ??
    upstreamEndpointFromBridgeID(bridgeString(bridge, 'bridge_id'))
  return inferred ?? strings.emptyField
}

function metadataString(
  metadata: Record<string, unknown> | null | undefined,
  key: string,
): string | null {
  const value = metadata?.[key]
  return typeof value === 'string' && value.trim() !== '' ? value : null
}

function bridgeMetadata(
  metadata: Record<string, unknown> | null | undefined,
): Record<string, unknown> | null {
  const bridge = metadata?.bridge
  return typeof bridge === 'object' && bridge !== null && !Array.isArray(bridge)
    ? (bridge as Record<string, unknown>)
    : null
}

function bridgeString(bridge: Record<string, unknown> | null, ...keys: string[]): string | null {
  if (!bridge) return null
  for (const key of keys) {
    const value = bridge[key]
    if (typeof value === 'string' && value.trim() !== '') {
      return value
    }
  }
  return null
}

function upstreamEndpointFromContract(contract: string | null): string | null {
  if (!contract) return null
  if (contract.includes('codex.responses.compact')) return '/codex/responses/compact'
  if (contract.includes('codex.responses')) return '/codex/responses'
  if (contract.includes('codex.models')) return '/codex/models'
  if (contract.includes('transcribe')) return '/transcribe'
  return null
}

function upstreamEndpointFromBridgeID(bridgeID: string | null): string | null {
  switch (bridgeID) {
    case 'bridge.openai.responses.to_codex':
    case 'bridge.openai.responses.websocket.to_codex':
    case 'bridge.openai.chat_completions.to_codex':
    case 'bridge.codex_native.responses.direct':
    case 'bridge.codex_native.responses.websocket.direct':
      return '/codex/responses'
    case 'bridge.openai.responses.compact.to_codex':
    case 'bridge.codex_native.responses.compact.direct':
      return '/codex/responses/compact'
    case 'bridge.openai.models.from_codex':
    case 'bridge.codex_native.models.direct':
      return '/codex/models'
    case 'bridge.codex_native.transcribe.direct':
      return '/transcribe'
    default:
      return null
  }
}

function statusBadgeVariant(statusCode: number): BadgeVariant {
  if (statusCode >= 500) return 'danger'
  if (statusCode >= 400) return 'warning'
  if (statusCode >= 200 && statusCode < 300) return 'success'
  return 'outline'
}

function outcomeBadgeVariant(outcome: RequestOutcome): BadgeVariant {
  if (outcome === 'success') return 'success'
  if (outcome === 'cancelled') return 'secondary'
  if (outcome === 'no_available_account' || outcome === 'no_extractable_text') return 'warning'
  return 'danger'
}

function formatUsage(value: Record<string, unknown> | null | undefined): string {
  if (!value) return strings.emptyField
  if (typeof value.total_tokens === 'number') return String(value.total_tokens)
  const entries = Object.entries(value)
    .filter(([, item]) => typeof item === 'number')
    .slice(0, 3)
  if (entries.length === 0) return strings.emptyField
  return entries.map(([key, item]) => `${key}:${item}`).join(' ')
}

interface TokenUsageParts {
  label: string
  tooltip: string
}

function tokenUsageParts(
  value: Record<string, unknown> | null | undefined,
): TokenUsageParts | null {
  if (!value) return null
  const input = usageNumber(value, 'input', 'input_tokens', 'prompt_tokens')
  const cached = usageNumber(value, 'cached_input', 'cached_input_tokens', 'cached_tokens')
  const output = usageNumber(value, 'output', 'output_tokens', 'completion_tokens')
  const reasoning = usageNumber(value, 'reasoning', 'reasoning_tokens')
  const explicitTotal = usageNumber(value, 'total', 'total_tokens')

  const label = [input, cached, output, reasoning]
    .map((item) => (item === null ? strings.emptyField : formatTokenCount(item)))
    .join(' / ')
  const hasKnownUsage =
    input !== null ||
    cached !== null ||
    output !== null ||
    reasoning !== null ||
    explicitTotal !== null
  if (!hasKnownUsage) return null

  const tooltipParts: string[] = []
  const computedTotal = explicitTotal ?? (input !== null && output !== null ? input + output : null)
  if (computedTotal !== null) {
    tooltipParts.push(`${strings.labels.totalTokens}: ${formatTokenCount(computedTotal)}`)
  }
  if (input !== null) {
    tooltipParts.push(`${strings.labels.inputTokens}: ${formatTokenCount(input)}`)
  }
  if (cached !== null) {
    tooltipParts.push(`${strings.labels.cachedInputTokens}: ${formatTokenCount(cached)}`)
  }
  if (output !== null) {
    tooltipParts.push(`${strings.labels.outputTokens}: ${formatTokenCount(output)}`)
  }
  if (reasoning !== null) {
    tooltipParts.push(`${strings.labels.reasoningTokens}: ${formatTokenCount(reasoning)}`)
  }

  return {
    label,
    tooltip: tooltipParts.length > 0 ? tooltipParts.join('\n') : tokenUsageLegendTooltip(),
  }
}

function tokenUsageLegendTooltip(): string {
  return [
    strings.labels.inputTokens,
    strings.labels.cachedInputTokens,
    strings.labels.outputTokens,
    strings.labels.reasoningTokens,
  ].join('\n')
}

function usageNumber(value: Record<string, unknown>, ...keys: string[]): number | null {
  for (const key of keys) {
    const item = value[key]
    if (typeof item === 'number' && Number.isFinite(item)) {
      return item
    }
  }
  return null
}

function formatTokenCount(value: number): string {
  return Number.isInteger(value) ? String(value) : String(Math.round(value))
}

async function copyText(value: string) {
  try {
    if (!navigator.clipboard) {
      throw new Error('clipboard unavailable')
    }
    await navigator.clipboard.writeText(value)
    toast.success(strings.copyDone)
  } catch {
    toast.error(strings.copyFailed)
  }
}
