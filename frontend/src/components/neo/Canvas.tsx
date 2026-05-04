import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'

interface CanvasProps {
  children: ReactNode
  /**
   * Layout variant:
   *   - `rail`  → 2-column (main + 300 px right rail), used by the wizard.
   *   - `wide`  → single column, max-width 1200 px, used by admin pages.
   *   - `narrow`→ single column, max-width 780 px, used by Settings.
   */
  variant?: 'rail' | 'wide' | 'narrow'
}

/**
 * Canvas — central viewport wrapper. All setup / admin pages mount
 * their content inside a Canvas so the horizontal rhythm (24 px
 * gutter, 300 px rail) is enforced centrally. Mirrors v9 §.canvas.
 */
export function Canvas({ children, variant = 'wide' }: CanvasProps) {
  if (variant === 'rail') {
    return (
      <div
        className={cn(
          'mx-auto grid max-w-[1200px] gap-5 px-4 pb-14 pt-6 sm:px-6 sm:pt-8',
          'grid-cols-1 lg:grid-cols-[minmax(0,1fr)_300px]',
        )}
      >
        {children}
      </div>
    )
  }
  if (variant === 'narrow') {
    return <div className="mx-auto max-w-3xl px-4 pb-14 pt-6 sm:px-6 sm:pt-8">{children}</div>
  }
  return <div className="mx-auto max-w-[1200px] px-4 pb-14 pt-6 sm:px-6 sm:pt-8">{children}</div>
}
