import { forwardRef, type InputHTMLAttributes } from 'react'

import { cn } from '@/lib/utils'

export type InputProps = InputHTMLAttributes<HTMLInputElement>

/**
 * v9 input (`specs/002-.../mocks/v9-neoretro-grafana.html` §.inp):
 *   - Monospaced identifier-grade typography — operators look at DSNs
 *     and API keys more than prose here.
 *   - Sharp 2 px radius + `--line-2` border, firming up on hover.
 *   - Focus colour = accent; uses the `--focus` app-wide ring.
 *   - Error state takes the red palette tone automatically when
 *     wrapped in `[aria-invalid="true"]` or given the `data-invalid`
 *     attribute by the consumer.
 */
export const Input = forwardRef<HTMLInputElement, InputProps>(
  ({ className, type, ...props }, ref) => (
    <input
      type={type}
      ref={ref}
      className={cn(
        'block min-h-11 w-full border bg-[var(--bg-2)] px-[10px] py-[8px] font-mono text-[12.5px] text-[var(--text)] sm:min-h-8',
        'transition-[border-color,background-color] duration-[100ms]',
        'placeholder:text-[var(--text-muted)]',
        'hover:border-[var(--line-3)]',
        'focus:outline-none focus:border-[var(--accent)]',
        'focus-visible:outline-none focus-visible:[box-shadow:0_0_0_2px_var(--bg),0_0_0_4px_var(--accent)] focus-visible:border-[var(--accent)]',
        'disabled:cursor-not-allowed disabled:bg-[var(--panel-2)] disabled:text-[var(--text-muted)]',
        'aria-[invalid=true]:border-[var(--err)] aria-[invalid=true]:focus:border-[var(--err)]',
        className,
      )}
      style={{ borderColor: 'var(--line-2)', borderRadius: 2 }}
      {...props}
    />
  ),
)
Input.displayName = 'Input'
