import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'

type Status = 'ok' | 'err'

interface ProbeRowProps {
  status: Status
  /** Latency in ms. Rendered as a large monospaced primary number. */
  latencyMs?: number
  /** Log rows rendered in the middle column (JSX so callers can colourise). */
  log?: ReactNode
  /** Optional status pill text, e.g. `healthy` / `unreachable`. */
  statusLabel?: string
}

/**
 * ProbeRow — the "12 ms rtt · driver modernc.org/sqlite · …" block
 * rendered under the DSN input after a successful probe. v9 §.probe.
 */
export function ProbeRow({ status, latencyMs, log, statusLabel }: ProbeRowProps) {
  const tone = status === 'ok' ? 'ok' : 'err'
  const palette =
    tone === 'ok'
      ? {
          bg: 'var(--ok-soft)',
          border: 'var(--ok-line)',
          color: 'var(--ok)',
        }
      : {
          bg: 'var(--err-soft)',
          border: 'var(--err-line)',
          color: 'var(--err)',
        }

  return (
    <div
      role="status"
      aria-live="polite"
      data-testid="probe-result"
      className="mt-[10px] grid grid-cols-1 items-start gap-4 border px-[14px] py-[10px] sm:grid-cols-[84px_1fr_auto] sm:items-center"
      style={{
        borderColor: palette.border,
        background: palette.bg,
        borderRadius: 2,
      }}
    >
      <div>
        <div
          className="font-mono text-[22px] font-medium leading-none"
          style={{ color: palette.color, fontFeatureSettings: '"tnum"' }}
        >
          {typeof latencyMs === 'number' ? latencyMs : '—'}
          <small className="mt-1 block font-sans text-[9.5px] font-normal uppercase tracking-[0.12em] text-[var(--text-muted)]">
            ms rtt
          </small>
        </div>
      </div>
      <div className="font-mono text-[11.5px] leading-[1.75] text-[var(--text-dim)]">{log}</div>
      <span
        className={cn('inline-flex w-fit items-center gap-[6px] border font-mono uppercase')}
        style={{
          fontSize: 10.5,
          letterSpacing: '0.08em',
          color: palette.color,
          borderColor: palette.color,
          background: palette.bg,
          padding: '3px 8px',
          borderRadius: 2,
        }}
      >
        {tone === 'ok' ? (
          <span
            aria-hidden
            className="inline-block h-[6px] w-[6px] rounded-full"
            style={{ background: palette.color, boxShadow: `0 0 6px ${palette.color}` }}
          />
        ) : null}
        {statusLabel ?? (tone === 'ok' ? 'healthy' : 'unreachable')}
      </span>
    </div>
  )
}

/** ProbeLogKw — helper for highlighting keywords (`driver`, `version`) in log copy. */
export function ProbeLogKw({ children }: { children: ReactNode }) {
  return <span style={{ color: 'var(--accent)' }}>{children}</span>
}

/** ProbeLogOk — helper for a green "clean" / "ok" token inside log copy. */
export function ProbeLogOk({ children }: { children: ReactNode }) {
  return <span style={{ color: 'var(--ok)' }}>{children}</span>
}
