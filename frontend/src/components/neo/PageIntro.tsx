import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'

interface PageIntroProps {
  children: ReactNode
  className?: string
}

/**
 * PageIntro keeps top-level admin page copy to one scannable line.
 * Long descriptions truncate inside the page header instead of wrapping
 * or widening the document on phone viewports.
 */
export function PageIntro({ children, className }: PageIntroProps) {
  return (
    <p
      className={cn(
        'min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-[14px] leading-[1.65] text-[var(--text-dim)]',
        className,
      )}
    >
      {children}
    </p>
  )
}
