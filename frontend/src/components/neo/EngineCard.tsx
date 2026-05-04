import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'

interface EngineCardProps {
  /** Radio group member id, e.g. `sqlite3`. */
  value: string
  /** Currently selected value in the enclosing radio group. */
  selected: string
  /** Callback when this card becomes the selection. */
  onSelect: (value: string) => void
  /** Mono uppercase slot above the title, e.g. `01 — DEFAULT`. */
  num: string
  /** Display title, e.g. `SQLite`. */
  title: string
  /** Optional mono version tag after the title, e.g. `3.40+`. */
  version?: string
  /** Multi-line pitch shown below the title. */
  children: ReactNode
  /** Mono feature chips pinned to the bottom, separated by hairlines. */
  features?: readonly string[]
}

/**
 * EngineCard — radio-like selectable card with an orange top bar when
 * active. Mirrors v9 §.eng exactly: numbered header, display title
 * with version chip, descriptive copy, mono feature bar under a
 * hairline border.
 */
export function EngineCard({
  value,
  selected,
  onSelect,
  num,
  title,
  version,
  children,
  features = [],
}: EngineCardProps) {
  const on = value === selected
  return (
    // biome-ignore lint/a11y/useSemanticElements: EngineCard is a radio-like control rendered as a rich card (two-line copy, feature chips, accent top bar). `<input type="radio">` cannot host that visual contract — role="radio" on a button is WAI-ARIA 1.2 equivalent.
    <button
      type="button"
      role="radio"
      aria-checked={on}
      tabIndex={0}
      onClick={() => onSelect(value)}
      className={cn(
        'group relative flex w-full cursor-pointer flex-col items-start overflow-hidden border text-left',
        'px-4 pb-3 pt-4 transition-colors',
      )}
      style={{
        borderRadius: 2,
        borderColor: on ? 'var(--accent)' : 'var(--line)',
        background: on
          ? 'linear-gradient(180deg, var(--accent-soft), transparent 70%), var(--panel)'
          : 'var(--panel)',
      }}
    >
      <span
        aria-hidden
        className="absolute left-0 top-0 h-[2px] w-full transition-colors"
        style={{
          background: on ? 'var(--accent)' : 'color-mix(in oklch, var(--accent) 0%, transparent)',
        }}
      />
      <span
        className="mb-5 font-mono text-[10.5px] font-medium uppercase tracking-[0.1em]"
        style={{ color: on ? 'var(--accent)' : 'var(--text-muted)' }}
      >
        {num}
      </span>
      <span className="mb-1 flex flex-wrap items-baseline gap-2 text-[16px] font-medium text-[var(--text)]">
        {title}
        {version ? (
          <span
            className="border px-[6px] py-[1px] font-mono text-[10.5px] font-normal text-[var(--text-muted)]"
            style={{ borderColor: 'var(--line-2)', borderRadius: 2 }}
          >
            {version}
          </span>
        ) : null}
      </span>
      <span className="mb-[14px] block text-[12.5px] leading-[1.55] text-[var(--text-dim)]">
        {children}
      </span>
      {features.length > 0 ? (
        <span
          className="mt-auto flex w-full flex-wrap border-t pt-3 font-mono text-[10.5px] text-[var(--text-muted)]"
          style={{ borderColor: 'var(--line)' }}
        >
          {features.map((f, i) => (
            <span
              key={f}
              className={cn('px-2', i === 0 && 'pl-0', i > 0 && 'border-l')}
              style={{ borderColor: 'var(--line-2)' }}
            >
              {f}
            </span>
          ))}
        </span>
      ) : null}
    </button>
  )
}
