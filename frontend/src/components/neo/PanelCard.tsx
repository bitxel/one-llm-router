import type { HTMLAttributes, ReactNode } from 'react'

import { cn } from '@/lib/utils'

interface PanelCardProps extends HTMLAttributes<HTMLDivElement> {
  /** Uppercase header title rendered in `.hd`. */
  title: string
  /** Optional meta tag rendered right-aligned in the header. */
  meta?: ReactNode
  /** Optional class override for the right-aligned meta tag. */
  metaClassName?: string
  /** Optional test id for the right-aligned meta tag. */
  metaTestId?: string
  /** When true, renders the meta tag with the muted outline style. */
  metaMuted?: boolean
  children: ReactNode
}

/**
 * PanelCard — the workhorse v9 form/data container.
 *
 * Anatomy (mirrors §.panel-card):
 *   ┌───────────────────────────────────────┐
 *   │  CONNECTION          driver = sqlite  │  ← .hd (panel-head bg)
 *   ├───────────────────────────────────────┤
 *   │                                       │
 *   │   <form fields, probe row, env>       │  ← .bd (16/20 px padding)
 *   │                                       │
 *   └───────────────────────────────────────┘
 */
export function PanelCard({
  title,
  meta,
  metaClassName,
  metaTestId,
  metaMuted,
  children,
  className,
  ...rest
}: PanelCardProps) {
  return (
    <div
      data-slot="panel-card"
      className={cn(
        'mb-3 overflow-hidden border border-[var(--line)] bg-[var(--panel)]',
        className,
      )}
      style={{ borderRadius: 2 }}
      {...rest}
    >
      <div className="flex min-h-9 items-center justify-between gap-3 border-b border-[var(--line)] bg-[var(--panel-head)] px-4 py-2">
        <span className="min-w-0 truncate text-[11.5px] font-medium uppercase tracking-[0.1em] text-[var(--text-dim)]">
          {title}
        </span>
        {meta ? (
          <span
            data-testid={metaTestId}
            className={cn(
              'max-w-[52%] shrink-0 truncate border px-2 py-[2px] font-mono text-[10.5px]',
              metaMuted
                ? 'border-[var(--line-2)] bg-transparent text-[var(--text-muted)]'
                : 'border-[var(--accent-hair)] bg-[var(--accent-soft)] text-[var(--accent)]',
              metaClassName,
            )}
            style={{ borderRadius: 2 }}
          >
            {meta}
          </span>
        ) : null}
      </div>
      <div className="px-4 py-4 sm:px-5">{children}</div>
    </div>
  )
}

interface FieldProps {
  /** Primary label rendered in the left column. */
  label: ReactNode
  /** Secondary helper text rendered under the label in the left column. */
  hint?: ReactNode
  /** Optional id to link label to an input via `for`. */
  htmlFor?: string
  children: ReactNode
}

/**
 * Field — two-column row (180 px label + 1fr input/value), with a
 * horizontal hairline between consecutive fields. v9 §.field.
 */
export function Field({ label, hint, htmlFor, children }: FieldProps) {
  return (
    <div className="grid items-start gap-3 py-3 sm:grid-cols-[minmax(0,180px)_minmax(0,1fr)] sm:gap-5 [&+&]:border-t [&+&]:border-[var(--line)]">
      <label htmlFor={htmlFor} className="text-[12.5px] font-medium text-[var(--text)] sm:pt-2">
        {label}
        {hint ? (
          <span className="mt-1 block text-[11.5px] font-normal leading-[1.5] text-[var(--text-muted)]">
            {hint}
          </span>
        ) : null}
      </label>
      <div className="min-w-0">{children}</div>
    </div>
  )
}

export function EnvLine({ k, value, set }: { k: string; value: string; set?: boolean }) {
  return (
    <div className="flex min-w-0 items-center justify-between gap-3 border-t border-[var(--line)] py-[6px] font-mono text-[11.5px] first:border-t-0">
      <span className="shrink-0 text-[var(--accent)]">{k}</span>
      <span
        className={cn(
          'min-w-0 truncate',
          set
            ? 'rounded-[2px] bg-[var(--panel-2)] px-[6px] py-[1px] text-[var(--text)]'
            : 'text-[var(--text-muted)]',
        )}
      >
        {value}
      </span>
    </div>
  )
}
