import type { ReactNode } from 'react'

interface StripeProps {
  /** Left-side uppercase label. Admin pages use this as the page title. */
  eyebrow?: string
  /** Optional right-side context, usually the parent title. */
  children?: ReactNode
  /** Setup wizard keeps its step title on the right; admin pages default to left. */
  titleSide?: 'left' | 'right'
}

/**
 * Stripe — uppercase monospace section marker with accent bars,
 * mirrors v9 §.stripe exactly. Used above each section hero inside
 * the setup wizard / admin pages to break content vertically.
 *
 *   ──── ACCOUNT DETAIL ─────────────────────── ACCOUNTS
 */
export function Stripe({ eyebrow, children, titleSide = 'left' }: StripeProps) {
  const hasRightContext =
    children !== undefined && children !== null && children !== false && children !== ''
  const titleClassName =
    'm-0 font-mono text-[11px] font-medium uppercase tracking-[0.16em] text-[var(--accent)]'

  return (
    <div className="mb-4 flex items-center gap-3 font-mono text-[11px] font-medium uppercase tracking-[0.16em] text-[var(--accent)]">
      <span aria-hidden className="block h-[2px] w-10 shrink-0 bg-[var(--accent)]" />
      {eyebrow && titleSide === 'left' ? <h2 className={titleClassName}>{eyebrow}</h2> : null}
      {eyebrow && titleSide === 'right' ? <span>{eyebrow}</span> : null}
      <span
        aria-hidden
        className="block h-[2px] flex-1"
        style={{
          background:
            'linear-gradient(90deg, var(--accent), color-mix(in oklch, var(--accent) 5%, transparent) 70%, transparent)',
        }}
      />
      {hasRightContext && titleSide === 'right' ? (
        <h2 className={titleClassName}>{children}</h2>
      ) : null}
      {hasRightContext && titleSide === 'left' ? <span>{children}</span> : null}
    </div>
  )
}
