import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'

/**
 * Rail — sticky right column rendered inside the 2-column canvas.
 * Holds context: the wizard step list, operator notes, shortcuts.
 * v9 §.rail + §.rail-card + §.rail-step.
 */
export function Rail({ children }: { children: ReactNode }) {
  return (
    <aside
      className={cn(
        'sticky top-[72px] order-2 flex min-w-0 flex-col gap-4 self-start',
        'lg:order-none',
      )}
    >
      {children}
    </aside>
  )
}

export function RailSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div>
      <h4 className="mb-[6px] px-[2px] font-mono text-[10.5px] uppercase tracking-[0.14em] text-[var(--text-muted)]">
        {title}
      </h4>
      <div className="flex flex-col gap-2">{children}</div>
    </div>
  )
}

interface RailStepsProps {
  /** Ordered list of steps; `current` is the active one, `done` are checked. */
  steps: readonly { idx: string; label: string }[]
  currentIndex: number
  completedIndices: ReadonlySet<number>
}

export function RailSteps({ steps, currentIndex, completedIndices }: RailStepsProps) {
  return (
    <div
      className="overflow-hidden border border-[var(--line)] bg-[var(--panel)]"
      style={{ borderRadius: 2 }}
    >
      {steps.map((step, i) => {
        const isActive = i === currentIndex
        const isDone = completedIndices.has(i) && !isActive
        const base =
          'flex items-center gap-[10px] border-l-2 px-[14px] py-[10px] text-[12.5px] transition-colors'
        const state = isActive
          ? 'border-l-[var(--accent)] bg-[var(--panel-hi)] text-[var(--text)]'
          : isDone
            ? 'border-l-transparent text-[var(--text)]'
            : 'border-l-transparent text-[var(--text-dim)]'
        const border = i > 0 ? 'border-t border-t-[var(--line)]' : ''
        return (
          <div key={step.idx} className={cn(base, state, border)}>
            <span
              className="w-[22px] font-mono text-[10.5px]"
              style={{
                color: isActive ? 'var(--accent)' : isDone ? 'var(--ok)' : 'var(--text-muted)',
                fontFeatureSettings: '"tnum"',
              }}
            >
              {step.idx}
            </span>
            <span>{step.label}</span>
            <span
              aria-hidden
              className="ml-auto inline-grid h-[14px] w-[14px] place-items-center rounded-full"
              style={{
                background: isDone
                  ? 'var(--ok)'
                  : isActive
                    ? 'var(--accent-soft)'
                    : 'var(--panel-2)',
              }}
            >
              {isDone ? (
                <span
                  aria-hidden
                  className="block"
                  style={{
                    width: 4,
                    height: 7,
                    borderRight: '1.5px solid var(--bg-2)',
                    borderBottom: '1.5px solid var(--bg-2)',
                    transform: 'rotate(45deg) translate(-1px, -1px)',
                  }}
                />
              ) : isActive ? (
                <span
                  aria-hidden
                  className="block rounded-full"
                  style={{ width: 6, height: 6, background: 'var(--accent)' }}
                />
              ) : null}
            </span>
          </div>
        )
      })}
    </div>
  )
}

export function MiniCard({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div
      className="border border-[var(--line)] bg-[var(--panel)] p-[12px_14px]"
      style={{ borderRadius: 2 }}
    >
      <h5 className="mb-1 text-[12.5px] font-medium text-[var(--text)]">{title}</h5>
      <p className="text-[12px] leading-[1.55] text-[var(--text-dim)]">{children}</p>
    </div>
  )
}

export function ShortcutList({ items }: { items: readonly { label: string; keys: string }[] }) {
  return (
    <div
      className="overflow-hidden border border-[var(--line)] bg-[var(--panel)]"
      style={{ borderRadius: 2 }}
    >
      <div className="border-b border-[var(--line)] bg-[var(--panel-head)] px-[14px] py-[8px] text-[11px] font-medium uppercase tracking-[0.1em] text-[var(--text-dim)]">
        Shortcuts
      </div>
      <div className="px-[14px] py-[8px]">
        {items.map((it, i) => (
          <div
            key={it.label}
            className={cn(
              'flex items-center justify-between py-[5px] text-[12px]',
              i > 0 ? 'border-t border-[var(--line)]' : '',
            )}
          >
            <span className="text-[var(--text-dim)]">{it.label}</span>
            <span className="font-mono text-[11px] text-[var(--text-muted)]">{it.keys}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

export function Kbd({ children }: { children: ReactNode }) {
  return (
    <span
      className="inline-block border px-[7px] py-[1px] font-mono text-[11px] text-[var(--text)]"
      style={{
        background: 'var(--bg-2)',
        borderColor: 'var(--line-3)',
        borderRadius: 2,
        boxShadow: 'var(--shadow-btn)',
      }}
    >
      {children}
    </span>
  )
}
