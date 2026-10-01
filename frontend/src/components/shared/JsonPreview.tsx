import { Clipboard, Maximize2, Minimize2 } from 'lucide-react'
import { Fragment, type ReactNode, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

export type JsonPreviewValue =
  | string
  | number
  | boolean
  | null
  | JsonPreviewValue[]
  | { [key: string]: JsonPreviewValue }

export interface JsonPreviewModel {
  raw: string
  formatted: string
  value?: JsonPreviewValue
}

export function buildJsonPreviewFromText(
  value: string | null | undefined,
  emptyText: string,
): JsonPreviewModel {
  if (!value) return { raw: emptyText, formatted: emptyText }
  try {
    const parsed = JSON.parse(value) as JsonPreviewValue
    return {
      raw: value,
      formatted: JSON.stringify(parsed, null, 2),
      value: parsed,
    }
  } catch {
    return { raw: value, formatted: value }
  }
}

export function buildJsonPreviewFromValue(value: unknown): JsonPreviewModel {
  const formatted = JSON.stringify(value, null, 2)
  if (typeof formatted !== 'string') return { raw: '', formatted: '' }
  return {
    raw: JSON.stringify(value),
    formatted,
    value: JSON.parse(formatted) as JsonPreviewValue,
  }
}

export function JsonPreview({
  preview,
  className,
  bodyClassName,
  copyLabel,
  onCopy,
  mode: controlledMode,
  onModeChange,
  collapsed: controlledCollapsed,
  onCollapsedChange,
  showExpand = true,
}: {
  preview: JsonPreviewModel
  className?: string
  bodyClassName?: string
  copyLabel?: string
  onCopy?: (value: string) => void
  mode?: 'beautify' | 'raw'
  onModeChange?: (mode: 'beautify' | 'raw') => void
  collapsed?: Set<string>
  onCollapsedChange?: (collapsed: Set<string>) => void
  showExpand?: boolean
}) {
  const { t } = useTranslation('common')
  const resolvedCopyLabel = copyLabel ?? t('jsonPreview.copy')
  const [localMode, setLocalMode] = useState<'beautify' | 'raw'>('beautify')
  const [localCollapsed, setLocalCollapsed] = useState<Set<string>>(() => new Set())
  const mode = controlledMode ?? localMode
  const collapsed = controlledCollapsed ?? localCollapsed
  const setMode = onModeChange ?? setLocalMode
  const setCollapsed = onCollapsedChange ?? setLocalCollapsed
  const [expanded, setExpanded] = useState(false)
  const openButtonRef = useRef<HTMLButtonElement>(null)
  const dialogRef = useRef<HTMLElement>(null)
  useEffect(() => {
    if (!expanded) return
    const previousFocus = document.activeElement
    dialogRef.current?.querySelector<HTMLElement>('button:not([disabled])')?.focus()
    function closeOnEscape(event: KeyboardEvent) {
      if (event.key === 'Escape') setExpanded(false)
      if (event.key === 'Tab') {
        const focusable = dialogRef.current?.querySelectorAll<HTMLElement>(
          'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
        )
        if (!focusable?.length) return
        const first = focusable[0]
        const last = focusable[focusable.length - 1]
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault()
          last.focus()
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault()
          first.focus()
        }
      }
    }
    window.addEventListener('keydown', closeOnEscape)
    return () => {
      window.removeEventListener('keydown', closeOnEscape)
      if (openButtonRef.current?.isConnected) openButtonRef.current.focus()
      else if (previousFocus instanceof HTMLElement && previousFocus.isConnected)
        previousFocus.focus()
    }
  }, [expanded])
  const baseClass =
    'max-w-full overflow-auto p-3 font-mono text-[11.5px] leading-[1.6] whitespace-pre-wrap text-[var(--text)] [overflow-wrap:anywhere]'
  const content = mode === 'raw' ? preview.raw : preview.formatted
  return (
    <div className={className}>
      <div className="flex min-h-9 items-center justify-between gap-2 border-b border-[var(--line)] bg-[color-mix(in_oklch,var(--bg-2)_84%,var(--panel))] px-2 py-1">
        <div
          aria-label={t('jsonPreview.modeLabel')}
          className="inline-flex items-center gap-1"
          role="tablist"
        >
          <JsonPreviewTab
            active={mode === 'beautify'}
            label={t('jsonPreview.beautify')}
            onClick={() => setMode('beautify')}
          />
          <JsonPreviewTab
            active={mode === 'raw'}
            label={t('jsonPreview.raw')}
            onClick={() => setMode('raw')}
          />
        </div>
        <div className="flex items-center gap-1">
          {mode === 'beautify' && preview.value !== undefined ? (
            <>
              <JsonPreviewTab
                active={false}
                label={t('jsonPreview.collapseAll')}
                onClick={() =>
                  setCollapsed(collectContainerPaths(preview.value as JsonPreviewValue))
                }
              />
              <JsonPreviewTab
                active={false}
                label={t('jsonPreview.expandAll')}
                onClick={() => setCollapsed(new Set())}
              />
            </>
          ) : null}
          {showExpand ? (
            <Button
              type="button"
              size="icon"
              variant="ghost"
              aria-label={t('jsonPreview.expandViewer')}
              title={t('jsonPreview.expandViewer')}
              className="h-8 min-h-8 w-8 border-0 shadow-none sm:h-7 sm:min-h-7 sm:w-7"
              onClick={() => setExpanded(true)}
              ref={openButtonRef}
            >
              <Maximize2 />
            </Button>
          ) : null}
          {onCopy ? (
            <Button
              type="button"
              size="icon"
              variant="ghost"
              onClick={() => onCopy(content)}
              aria-label={resolvedCopyLabel}
              title={resolvedCopyLabel}
              className="h-8 min-h-8 w-8 border-0 shadow-none sm:h-7 sm:min-h-7 sm:w-7"
            >
              <Clipboard />
            </Button>
          ) : null}
        </div>
      </div>
      <pre className={bodyClassName ? `${baseClass} ${bodyClassName}` : baseClass}>
        <code data-testid="json-preview">
          {mode === 'beautify' && preview.value !== undefined ? (
            <JsonValueNode
              value={preview.value}
              collapsed={collapsed}
              onToggle={(key) => {
                const next = new Set(collapsed)
                next.has(key) ? next.delete(key) : next.add(key)
                setCollapsed(next)
              }}
            />
          ) : (
            content
          )}
        </code>
      </pre>
      {expanded ? (
        <div className="fixed inset-0 z-[70] flex items-center justify-center p-3 sm:p-6">
          <button
            type="button"
            tabIndex={-1}
            className="absolute inset-0 bg-[color-mix(in_oklch,var(--bg)_72%,transparent)]"
            aria-label={t('jsonPreview.closeExpandedViewer')}
            onClick={() => setExpanded(false)}
          />
          <section
            role="dialog"
            aria-modal="true"
            aria-label={t('jsonPreview.expandedViewerLabel')}
            tabIndex={-1}
            ref={dialogRef}
            className="relative z-10 flex h-[min(96dvh,1100px)] w-[min(96vw,1600px)] flex-col overflow-hidden border border-[var(--line)] bg-[var(--panel)] shadow-[0_24px_80px_rgba(0,0,0,0.4)]"
          >
            <div className="flex justify-end border-b border-[var(--line)] p-1">
              <Button
                type="button"
                size="icon"
                variant="ghost"
                aria-label={t('jsonPreview.closeExpandedViewer')}
                onClick={() => setExpanded(false)}
              >
                <Minimize2 />
              </Button>
            </div>
            <div className="min-h-0 flex-1 overflow-auto">
              <JsonPreview
                preview={preview}
                bodyClassName="min-h-full"
                onCopy={onCopy}
                copyLabel={resolvedCopyLabel}
                mode={mode}
                onModeChange={setMode}
                collapsed={collapsed}
                onCollapsedChange={setCollapsed}
                showExpand={false}
              />
            </div>
          </section>
        </div>
      ) : null}
    </div>
  )
}

function JsonPreviewTab({
  active,
  label,
  onClick,
}: {
  active: boolean
  label: string
  onClick: () => void
}) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      onClick={onClick}
      className={
        active
          ? 'h-7 cursor-pointer bg-[var(--panel-hi)] px-2 font-mono text-[11px] uppercase tracking-[0.08em] text-[var(--accent)] shadow-[inset_0_0_0_1px_var(--line-2)]'
          : 'h-7 cursor-pointer px-2 font-mono text-[11px] uppercase tracking-[0.08em] text-[var(--text-muted)] hover:bg-[var(--panel)] hover:text-[var(--text)]'
      }
    >
      {label}
    </button>
  )
}

function JsonValueNode({
  value,
  depth = 0,
  path = '$',
  collapsed = new Set<string>(),
  onToggle = () => undefined,
}: {
  value: JsonPreviewValue
  depth?: number
  path?: string
  collapsed?: Set<string>
  onToggle?: (path: string) => void
}) {
  if (value === null) {
    return (
      <span data-json-token="null" className="text-[var(--text-faint)]">
        null
      </span>
    )
  }

  if (typeof value === 'string') {
    return <JsonStringNode value={value} />
  }

  if (typeof value === 'number') {
    return (
      <span data-json-token="number" className="text-[var(--info)]">
        {String(value)}
      </span>
    )
  }

  if (typeof value === 'boolean') {
    return (
      <span data-json-token="boolean" className="text-[var(--warn)]">
        {String(value)}
      </span>
    )
  }

  if (Array.isArray(value)) {
    if (value.length === 0) {
      return <JsonPunctuation>[]</JsonPunctuation>
    }
    if (collapsed.has(path))
      return (
        <>
          <JsonPunctuation>[</JsonPunctuation>
          <JsonCollapseToggle
            label={`${value.length} items`}
            expanded={false}
            onClick={() => onToggle(path)}
          />
          <JsonPunctuation>]</JsonPunctuation>
        </>
      )
    const itemKeys = new Map<string, number>()
    const items = value.map((item) => {
      const baseKey = jsonPreviewKey(item)
      const occurrence = itemKeys.get(baseKey) ?? 0
      itemKeys.set(baseKey, occurrence + 1)
      return {
        item,
        key: occurrence === 0 ? baseKey : `${baseKey}-${occurrence}`,
      }
    })
    return (
      <>
        <JsonPunctuation>[</JsonPunctuation>
        <JsonCollapseToggle label="−" expanded onClick={() => onToggle(path)} />
        {items.map(({ item, key }, index) => (
          <Fragment key={key}>
            {'\n'}
            {jsonIndent(depth + 1)}
            <JsonValueNode
              value={item}
              depth={depth + 1}
              path={jsonArrayPath(path, index)}
              collapsed={collapsed}
              onToggle={onToggle}
            />
            {index < value.length - 1 ? <JsonPunctuation>,</JsonPunctuation> : null}
          </Fragment>
        ))}
        {'\n'}
        {jsonIndent(depth)}
        <JsonPunctuation>]</JsonPunctuation>
      </>
    )
  }

  const entries = Object.entries(value)
  if (entries.length === 0) {
    return <JsonPunctuation>{'{}'}</JsonPunctuation>
  }

  if (collapsed.has(path))
    return (
      <>
        <JsonPunctuation>{'{'}</JsonPunctuation>
        <JsonCollapseToggle
          label={`${entries.length} keys`}
          expanded={false}
          onClick={() => onToggle(path)}
        />
        <JsonPunctuation>{'}'}</JsonPunctuation>
      </>
    )

  return (
    <>
      <JsonPunctuation>{'{'}</JsonPunctuation>
      <JsonCollapseToggle label="−" expanded onClick={() => onToggle(path)} />
      {entries.map(([key, item], index) => (
        <Fragment key={`${depth}-${key}`}>
          {'\n'}
          {jsonIndent(depth + 1)}
          <span data-json-token="key" className="text-[var(--accent-hi)]">
            {JSON.stringify(key)}
          </span>
          <JsonPunctuation>: </JsonPunctuation>
          <JsonValueNode
            value={item}
            depth={depth + 1}
            path={jsonObjectPath(path, key)}
            collapsed={collapsed}
            onToggle={onToggle}
          />
          {index < entries.length - 1 ? <JsonPunctuation>,</JsonPunctuation> : null}
        </Fragment>
      ))}
      {'\n'}
      {jsonIndent(depth)}
      <JsonPunctuation>{'}'}</JsonPunctuation>
    </>
  )
}

function JsonCollapseToggle({
  label,
  expanded,
  onClick,
}: {
  label: string
  expanded: boolean
  onClick: () => void
}) {
  const { t } = useTranslation('common')
  const accessibleLabel = expanded
    ? t('jsonPreview.collapseNode')
    : `${t('jsonPreview.expand')} ${label}`
  return (
    <button
      type="button"
      aria-label={accessibleLabel}
      onClick={onClick}
      className="mx-1 cursor-pointer rounded border border-[var(--line)] px-1 text-[10px] text-[var(--text-muted)] hover:text-[var(--text)]"
    >
      {label}
    </button>
  )
}

function jsonObjectPath(path: string, key: string): string {
  return `${path}/o:${JSON.stringify(key)}`
}

function jsonArrayPath(path: string, index: number): string {
  return `${path}/a:${index}`
}

function collectContainerPaths(value: JsonPreviewValue, path = '$'): Set<string> {
  const paths = new Set<string>()
  if (Array.isArray(value)) {
    if (value.length > 0) paths.add(path)
    value.forEach((item, index) => {
      if (item !== null && typeof item === 'object')
        for (const nested of collectContainerPaths(item, jsonArrayPath(path, index)))
          paths.add(nested)
    })
  } else if (value !== null && typeof value === 'object') {
    const entries = Object.entries(value)
    if (entries.length > 0) paths.add(path)
    for (const [key, item] of entries)
      if (item !== null && typeof item === 'object')
        for (const nested of collectContainerPaths(item, jsonObjectPath(path, key)))
          paths.add(nested)
  }
  return paths
}

function JsonStringNode({ value }: { value: string }) {
  return (
    <>
      <JsonPunctuation>"</JsonPunctuation>
      <span data-json-token="string" className="text-[var(--ok)]">
        {renderEscapedStringContent(JSON.stringify(value).slice(1, -1))}
      </span>
      <JsonPunctuation>"</JsonPunctuation>
    </>
  )
}

function JsonPunctuation({ children }: { children: ReactNode }) {
  return (
    <span data-json-token="punctuation" className="text-[var(--text-muted)]">
      {children}
    </span>
  )
}

function renderEscapedStringContent(value: string): ReactNode[] {
  const nodes: ReactNode[] = []
  let textStart = 0
  let index = 0
  let nodeIndex = 0

  while (index < value.length) {
    if (value[index] !== '\\') {
      index += 1
      continue
    }

    let slashEnd = index
    while (slashEnd < value.length && value[slashEnd] === '\\') {
      slashEnd += 1
    }

    const slashCount = slashEnd - index
    if (value[slashEnd] !== 'n' || slashCount % 2 === 0) {
      index = slashEnd
      continue
    }

    const escapedPrefix = value.slice(textStart, slashEnd - 1)
    if (escapedPrefix) {
      nodes.push(<Fragment key={`text-${nodeIndex++}`}>{escapedPrefix}</Fragment>)
    }
    nodes.push(<br key={`newline-${nodeIndex++}`} data-testid="json-string-newline" />)
    index = slashEnd + 1
    textStart = index
  }

  const remainder = value.slice(textStart)
  if (remainder) {
    nodes.push(<Fragment key={`text-${nodeIndex++}`}>{remainder}</Fragment>)
  }
  return nodes
}

function jsonIndent(depth: number): string {
  return '  '.repeat(depth)
}

function jsonPreviewKey(value: JsonPreviewValue): string {
  const text = JSON.stringify(value)
  let hash = 0
  for (let index = 0; index < text.length; index += 1) {
    hash = (hash * 31 + text.charCodeAt(index)) | 0
  }
  return `json-${Math.abs(hash).toString(36)}`
}
