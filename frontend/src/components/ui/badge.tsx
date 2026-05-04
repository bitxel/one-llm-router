import { cva, type VariantProps } from 'class-variance-authority'
import type { HTMLAttributes } from 'react'

import { cn } from '@/lib/utils'

/**
 * v9 badge tokens — tight mono uppercase pills with 2 px corners and
 * a border + tinted background stack. Semantic variants map onto
 * the palette's ok/warn/err/info/accent lanes.
 */
const badgeVariants = cva(
  'inline-flex items-center gap-1 border px-[6px] py-[1px] font-mono text-[10.5px] uppercase tracking-[0.08em] transition-colors',
  {
    variants: {
      variant: {
        default: 'border-[var(--accent-hair)] bg-[var(--accent-soft)] text-[var(--accent)]',
        secondary: 'border-[var(--line-2)] bg-[var(--panel-2)] text-[var(--text-muted)]',
        outline: 'border-[var(--line-2)] bg-transparent text-[var(--text-muted)]',
        accent: 'border-[var(--accent)] bg-[var(--accent-soft)] text-[var(--accent)]',
        success: 'border-[var(--ok)] bg-[var(--ok-soft)] text-[var(--ok)]',
        warning:
          'border-[color-mix(in_oklch,var(--warn)_45%,transparent)] bg-transparent text-[var(--warn)]',
        danger: 'border-[var(--err)] bg-[var(--err-soft)] text-[var(--err)]',
      },
    },
    defaultVariants: { variant: 'default' },
  },
)

export interface BadgeProps
  extends HTMLAttributes<HTMLSpanElement>,
    VariantProps<typeof badgeVariants> {}

export function Badge({ className, variant, ...props }: BadgeProps) {
  return (
    <span
      className={cn(badgeVariants({ variant }), className)}
      style={{ borderRadius: 2 }}
      {...props}
    />
  )
}
