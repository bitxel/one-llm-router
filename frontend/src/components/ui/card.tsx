import { forwardRef, type HTMLAttributes } from 'react'

import { cn } from '@/lib/utils'

/**
 * v9-aligned card primitives. Kept API-compatible with the earlier
 * shadcn-style surface so in-place callers (Settings page, adhoc
 * panels) continue to render while the wizard migrates to the richer
 * `PanelCard` (neo/PanelCard.tsx) with explicit `.hd` + `.bd`.
 *
 * Visual rules (mock §.panel-card parity):
 *   - 2 px radius, `--line` border, `--panel` background
 *   - Header uses `--panel-head` bg + uppercase eyebrow-style title
 *   - Slightly denser padding than shadcn defaults (20 / 16 px)
 */
export const Card = forwardRef<HTMLDivElement, HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => (
    <div
      ref={ref}
      className={cn(
        'overflow-hidden border border-[var(--line)] bg-[var(--panel)] text-[var(--text)]',
        className,
      )}
      style={{ borderRadius: 2 }}
      {...props}
    />
  ),
)
Card.displayName = 'Card'

export const CardHeader = forwardRef<HTMLDivElement, HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => (
    <div
      ref={ref}
      className={cn(
        'flex flex-col gap-1 border-b border-[var(--line)] bg-[var(--panel-head)] px-5 py-3',
        className,
      )}
      {...props}
    />
  ),
)
CardHeader.displayName = 'CardHeader'

export const CardTitle = forwardRef<HTMLHeadingElement, HTMLAttributes<HTMLHeadingElement>>(
  ({ className, ...props }, ref) => (
    <h3
      ref={ref}
      className={cn(
        'text-[11.5px] font-medium uppercase leading-none tracking-[0.1em] text-[var(--text-dim)]',
        className,
      )}
      {...props}
    />
  ),
)
CardTitle.displayName = 'CardTitle'

export const CardDescription = forwardRef<
  HTMLParagraphElement,
  HTMLAttributes<HTMLParagraphElement>
>(({ className, ...props }, ref) => (
  <p
    ref={ref}
    className={cn('text-[12px] leading-[1.55] text-[var(--text-muted)]', className)}
    {...props}
  />
))
CardDescription.displayName = 'CardDescription'

export const CardContent = forwardRef<HTMLDivElement, HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => (
    <div ref={ref} className={cn('px-5 py-4', className)} {...props} />
  ),
)
CardContent.displayName = 'CardContent'

export const CardFooter = forwardRef<HTMLDivElement, HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => (
    <div
      ref={ref}
      className={cn(
        'flex items-center border-t border-[var(--line)] bg-[var(--panel-head)] px-5 py-3',
        className,
      )}
      {...props}
    />
  ),
)
CardFooter.displayName = 'CardFooter'
