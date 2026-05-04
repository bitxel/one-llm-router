import * as SwitchPrimitives from '@radix-ui/react-switch'
import { type ComponentPropsWithoutRef, type ElementRef, forwardRef } from 'react'

import { cn } from '@/lib/utils'

/**
 * v9-aligned Switch — keeps Radix behaviour; adopts the flame accent
 * when checked and sits on `--panel-2` neutral ground when off, so it
 * reads consistently on both dark and light themes without extra
 * theme-specific rules.
 */
export const Switch = forwardRef<
  ElementRef<typeof SwitchPrimitives.Root>,
  ComponentPropsWithoutRef<typeof SwitchPrimitives.Root>
>(({ className, ...props }, ref) => (
  <SwitchPrimitives.Root
    ref={ref}
    className={cn(
      'peer relative inline-flex h-8 w-14 shrink-0 cursor-pointer items-center rounded-full border transition-colors sm:h-5 sm:w-10',
      'focus-visible:outline-none focus-visible:[box-shadow:0_0_0_2px_var(--bg),0_0_0_4px_var(--accent)]',
      'disabled:cursor-not-allowed disabled:opacity-50',
      'data-[state=checked]:bg-[var(--accent)] data-[state=checked]:border-[var(--accent)]',
      'data-[state=unchecked]:bg-[var(--panel-2)] data-[state=unchecked]:border-[var(--line-3)]',
      className,
    )}
    {...props}
  >
    <SwitchPrimitives.Thumb
      className={cn(
        'pointer-events-none block h-6 w-6 rounded-full transition-transform sm:h-[14px] sm:w-[14px]',
        'translate-x-[3px] data-[state=checked]:translate-x-[29px] sm:data-[state=checked]:translate-x-[23px]',
      )}
      style={{
        background: 'var(--bg-2)',
        boxShadow: '0 1px 2px rgba(0,0,0,0.25)',
      }}
    />
  </SwitchPrimitives.Root>
))
Switch.displayName = 'Switch'
