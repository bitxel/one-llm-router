import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'

import { SegmentedThemeToggle } from './SegmentedThemeToggle'

export interface TopBarTag {
  kind?: 'plain' | 'info' | 'warn' | 'ok'
  label: string
}

interface TopBarProps {
  /** Breadcrumb after the brand, e.g. `/ installer` or `/ admin · settings`. */
  slash?: string
  /** Right-side informational tags rendered between separators. */
  tags?: readonly TopBarTag[]
  /** Optional slot injected before the theme toggle (e.g. action button). */
  actions?: ReactNode
}

/**
 * TopBar — persistent 48 px brand header.
 *
 * Visual anatomy matches v9-neoretro-grafana.html §.top:
 *   - 4-bar equaliser logo in accent orange (pure CSS, no asset).
 *   - `one-llm-<em>router</em>` wordmark with accent on `router`.
 *   - Mono-break `/ installer | / admin · …` for context.
 *   - Right stack: mono build/host/status tags with LED, separators,
 *     and a segmented dark/light theme toggle.
 *
 * We intentionally keep this presentational — the host chooses which
 * tags to render (setup shows "configuring", admin shows health).
 */
export function TopBar({ slash, tags = [], actions }: TopBarProps) {
  return (
    <header
      data-slot="topbar"
      className="sticky top-0 z-10 flex min-h-12 w-full items-center justify-between gap-3 overflow-hidden border-b border-[var(--line)] bg-[color-mix(in_oklch,var(--bg-2)_92%,transparent)] px-4 backdrop-blur sm:h-12 sm:px-6"
    >
      <div className="flex min-w-0 flex-1 items-center gap-3">
        <Bars />
        <span className="shrink-0 text-[14px] font-medium text-[var(--text)]">
          one-llm-<em className="font-medium text-[var(--accent)] not-italic">router</em>
        </span>
        {slash ? (
          <span className="min-w-0 truncate font-mono text-[12px] text-[var(--text-muted)]">
            {slash}
          </span>
        ) : null}
      </div>

      <div className="flex shrink-0 items-center gap-2 text-[12px] text-[var(--text-muted)]">
        {tags.map((tag, idx) => (
          <span
            // biome-ignore lint/suspicious/noArrayIndexKey: tag list is operator-provided without stable ids; label alone isn't unique enough (two "plain" build tags may coexist).
            key={`${tag.kind ?? 'plain'}-${tag.label}-${idx}`}
            className={cn('items-center gap-2', tag.kind === 'plain' ? 'hidden sm:flex' : 'flex')}
          >
            <Tag kind={tag.kind}>{tag.label}</Tag>
            {idx < tags.length - 1 ? <Sep /> : null}
          </span>
        ))}
        {actions}
        <SegmentedThemeToggle />
      </div>
    </header>
  )
}

function Bars() {
  return (
    <div aria-hidden="true" className="flex h-5 shrink-0 items-end gap-[3px]">
      <span className="block w-[4px] bg-[var(--accent)]" style={{ height: '32%' }} />
      <span className="block w-[4px] bg-[var(--accent)]" style={{ height: '60%' }} />
      <span className="block w-[4px] bg-[var(--accent)]" style={{ height: '100%' }} />
      <span className="block w-[4px] bg-[var(--accent)]" style={{ height: '72%' }} />
    </div>
  )
}

function Sep() {
  return <span aria-hidden className="hidden h-4 w-px bg-[var(--line-2)] sm:inline-block" />
}

function Tag({ kind = 'plain', children }: { kind?: TopBarTag['kind']; children: ReactNode }) {
  const palette =
    kind === 'warn'
      ? {
          border: 'color-mix(in oklch, var(--warn) 45%, transparent)',
          color: 'var(--warn)',
          led: 'var(--warn)',
          glow: '0 0 6px var(--warn)',
        }
      : kind === 'ok'
        ? {
            border: 'color-mix(in oklch, var(--ok) 45%, transparent)',
            color: 'var(--ok)',
            led: 'var(--ok)',
            glow: '0 0 6px var(--ok)',
          }
        : kind === 'info'
          ? {
              border: 'color-mix(in oklch, var(--info) 45%, transparent)',
              color: 'var(--info)',
              led: 'var(--info)',
              glow: '0 0 6px var(--info)',
            }
          : {
              border: 'var(--line-2)',
              color: 'var(--text-dim)',
              led: 'var(--text-faint)',
              glow: 'none',
            }

  return (
    <span
      className="inline-flex h-6 max-w-[132px] items-center gap-[7px] truncate border px-[9px] font-mono text-[11.5px] sm:max-w-none"
      style={{ borderColor: palette.border, color: palette.color, borderRadius: 2 }}
    >
      {kind && kind !== 'plain' ? (
        <span
          aria-hidden
          className="inline-block h-[6px] w-[6px] rounded-full"
          style={{ background: palette.led, boxShadow: palette.glow }}
        />
      ) : null}
      {children}
    </span>
  )
}
