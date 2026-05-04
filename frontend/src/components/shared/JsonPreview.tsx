import { Clipboard } from 'lucide-react'
import { Fragment, type ReactNode, useState } from 'react'

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
  copyLabel = 'Copy JSON',
  onCopy,
}: {
  preview: JsonPreviewModel
  className?: string
  bodyClassName?: string
  copyLabel?: string
  onCopy?: (value: string) => void
}) {
  const [mode, setMode] = useState<'beautify' | 'raw'>('beautify')
  const baseClass =
    'max-w-full overflow-auto p-3 font-mono text-[11.5px] leading-[1.6] whitespace-pre-wrap text-[var(--text)] [overflow-wrap:anywhere]'
  const content = mode === 'raw' ? preview.raw : preview.formatted
  return (
    <div className={className}>
      <div className="flex min-h-9 items-center justify-between gap-2 border-b border-[var(--line)] bg-[color-mix(in_oklch,var(--bg-2)_84%,var(--panel))] px-2 py-1">
        <div
          aria-label="JSON preview mode"
          className="inline-flex items-center gap-1"
          role="tablist"
        >
          <JsonPreviewTab
            active={mode === 'beautify'}
            label="Beautify"
            onClick={() => setMode('beautify')}
          />
          <JsonPreviewTab active={mode === 'raw'} label="Raw" onClick={() => setMode('raw')} />
        </div>
        {onCopy ? (
          <Button
            type="button"
            size="icon"
            variant="ghost"
            onClick={() => onCopy(content)}
            aria-label={copyLabel}
            title={copyLabel}
            className="h-8 min-h-8 w-8 border-0 shadow-none sm:h-7 sm:min-h-7 sm:w-7"
          >
            <Clipboard />
          </Button>
        ) : null}
      </div>
      <pre className={bodyClassName ? `${baseClass} ${bodyClassName}` : baseClass}>
        <code data-testid="json-preview">
          {mode === 'beautify' && preview.value !== undefined ? (
            <JsonValueNode value={preview.value} />
          ) : (
            content
          )}
        </code>
      </pre>
    </div>
  )
}

function JsonPreviewTab({
  active,
  label,
  onClick,
}: {
  active: boolean
  label: 'Beautify' | 'Raw'
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

function JsonValueNode({ value, depth = 0 }: { value: JsonPreviewValue; depth?: number }) {
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
        {items.map(({ item, key }, index) => (
          <Fragment key={key}>
            {'\n'}
            {jsonIndent(depth + 1)}
            <JsonValueNode value={item} depth={depth + 1} />
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

  return (
    <>
      <JsonPunctuation>{'{'}</JsonPunctuation>
      {entries.map(([key, item], index) => (
        <Fragment key={`${depth}-${key}`}>
          {'\n'}
          {jsonIndent(depth + 1)}
          <span data-json-token="key" className="text-[var(--accent-hi)]">
            {JSON.stringify(key)}
          </span>
          <JsonPunctuation>: </JsonPunctuation>
          <JsonValueNode value={item} depth={depth + 1} />
          {index < entries.length - 1 ? <JsonPunctuation>,</JsonPunctuation> : null}
        </Fragment>
      ))}
      {'\n'}
      {jsonIndent(depth)}
      <JsonPunctuation>{'}'}</JsonPunctuation>
    </>
  )
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
