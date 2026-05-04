import { forwardRef, type LabelHTMLAttributes } from 'react'

import { cn } from '@/lib/utils'

export const Label = forwardRef<HTMLLabelElement, LabelHTMLAttributes<HTMLLabelElement>>(
  ({ className, ...props }, ref) => (
    <label
      ref={ref}
      className={cn(
        'text-[12.5px] font-medium leading-[1.3] text-[var(--text)]',
        'peer-disabled:cursor-not-allowed peer-disabled:opacity-70',
        className,
      )}
      {...props}
    />
  ),
)
Label.displayName = 'Label'
