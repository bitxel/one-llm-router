import { Slot } from '@radix-ui/react-slot'
import { cva, type VariantProps } from 'class-variance-authority'
import { type ButtonHTMLAttributes, forwardRef } from 'react'

import { cn } from '@/lib/utils'

/**
 * v9-aligned button vocabulary (`specs/002-.../mocks/v9-neoretro-grafana.html`):
 *   - 2 px radius, mono-free font, monospace only for explicit .mono
 *   - primary: flame orange background with AA-safe dark-on-orange fg
 *   - secondary / default: panel-2 with line-3 border, v9 neutral button
 *   - ghost: transparent w/ hover to panel-hi
 *   - outline: bordered, v9 "secondary action"
 *   - destructive: danger fg on danger-soft tint (reserved for future)
 *   - link: underline on hover, accent colour
 *
 * Focus ring uses the central `--focus` double-stroke ring so the
 * button inherits the app-wide accent ring rather than a ghost outline.
 */
const buttonVariants = cva(
  [
    'inline-flex cursor-pointer items-center justify-center gap-[6px] whitespace-nowrap font-sans text-[12.5px] font-normal',
    'transition-[background-color,border-color,transform,color] duration-[120ms]',
    'disabled:cursor-not-allowed disabled:opacity-55',
    'focus-visible:outline-none focus-visible:[box-shadow:var(--focus)]',
    'active:translate-y-[1px] active:shadow-none',
    '[&>svg]:h-[14px] [&>svg]:w-[14px] [&>svg]:stroke-[2] [&>svg]:flex-shrink-0',
  ].join(' '),
  {
    variants: {
      variant: {
        default:
          'border border-[var(--accent)] bg-[var(--accent)] font-medium text-[var(--accent-fg)] [box-shadow:var(--shadow-btn)] hover:border-[var(--accent-hi)] hover:bg-[var(--accent-hi)]',
        secondary:
          'border border-[var(--line-3)] bg-[var(--panel-2)] text-[var(--text)] [box-shadow:var(--shadow-btn)] hover:bg-[color-mix(in_oklch,var(--panel-2)_70%,var(--line-3))]',
        ghost:
          'border border-transparent bg-transparent text-[var(--text-dim)] hover:bg-[var(--panel-hi)] hover:text-[var(--text)]',
        outline:
          'border border-[var(--line-3)] bg-transparent text-[var(--text)] [box-shadow:var(--shadow-btn)] hover:bg-[var(--panel-hi)]',
        destructive:
          'border border-[var(--err)] bg-[var(--err)] text-white [box-shadow:var(--shadow-btn)] hover:brightness-110',
        link: '!h-auto !min-h-0 border-transparent bg-transparent !p-0 text-[var(--accent)] underline-offset-4 hover:underline active:translate-y-0',
      },
      size: {
        default: 'min-h-11 px-[14px] py-[10px] sm:min-h-8 sm:py-[8px]',
        sm: 'min-h-10 px-[10px] py-[8px] text-[11.5px] sm:min-h-7 sm:py-[5px]',
        lg: 'min-h-11 px-[18px] py-[10px] text-[13.5px] sm:min-h-10',
        icon: 'h-11 w-11 p-0 sm:h-8 sm:w-8',
      },
    },
    defaultVariants: {
      variant: 'default',
      size: 'default',
    },
  },
)

export interface ButtonProps
  extends ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, ...props }, ref) => {
    const Comp = asChild ? Slot : 'button'
    return (
      <Comp
        className={cn(buttonVariants({ variant, size }), className)}
        style={{ borderRadius: 2 }}
        ref={ref}
        {...props}
      />
    )
  },
)
Button.displayName = 'Button'
