import * as SelectPrimitive from '@radix-ui/react-select'
import { Check, ChevronDown } from 'lucide-react'
import { type ComponentPropsWithoutRef, type ElementRef, forwardRef } from 'react'

import { cn } from '@/lib/utils'

export const Select = SelectPrimitive.Root
export const SelectGroup = SelectPrimitive.Group
export const SelectValue = SelectPrimitive.Value

/**
 * v9-aligned Select primitives: 2 px radius, monospaced option text,
 * `--line-2` border, `--accent` focus. Retains Radix's accessibility
 * semantics; only the chrome is repainted.
 */

export const SelectTrigger = forwardRef<
  ElementRef<typeof SelectPrimitive.Trigger>,
  ComponentPropsWithoutRef<typeof SelectPrimitive.Trigger>
>(({ className, children, ...props }, ref) => (
  <SelectPrimitive.Trigger
    ref={ref}
    className={cn(
      'flex min-h-11 w-full items-center justify-between border bg-[var(--bg-2)] px-[10px] py-[8px] font-mono text-[12.5px] text-[var(--text)] transition-colors sm:min-h-8',
      'hover:border-[var(--line-3)]',
      'focus:outline-none focus:border-[var(--accent)]',
      'focus-visible:[box-shadow:0_0_0_2px_var(--bg),0_0_0_4px_var(--accent)]',
      'disabled:cursor-not-allowed disabled:bg-[var(--panel-2)] disabled:text-[var(--text-muted)]',
      '[&>span]:line-clamp-1',
      className,
    )}
    style={{ borderColor: 'var(--line-2)', borderRadius: 2 }}
    {...props}
  >
    {children}
    <SelectPrimitive.Icon asChild>
      <ChevronDown className="h-[14px] w-[14px] text-[var(--text-muted)]" />
    </SelectPrimitive.Icon>
  </SelectPrimitive.Trigger>
))
SelectTrigger.displayName = SelectPrimitive.Trigger.displayName

export const SelectContent = forwardRef<
  ElementRef<typeof SelectPrimitive.Content>,
  ComponentPropsWithoutRef<typeof SelectPrimitive.Content>
>(({ className, children, position = 'popper', ...props }, ref) => (
  <SelectPrimitive.Portal>
    <SelectPrimitive.Content
      ref={ref}
      position={position}
      className={cn(
        'relative z-50 max-h-96 min-w-[8rem] overflow-hidden border',
        position === 'popper' && 'data-[side=bottom]:translate-y-1',
        className,
      )}
      style={{
        background: 'var(--panel-hi)',
        borderColor: 'var(--line-2)',
        borderRadius: 2,
        boxShadow: '0 8px 24px -12px rgba(0,0,0,0.5)',
      }}
      {...props}
    >
      <SelectPrimitive.Viewport className="p-1">{children}</SelectPrimitive.Viewport>
    </SelectPrimitive.Content>
  </SelectPrimitive.Portal>
))
SelectContent.displayName = SelectPrimitive.Content.displayName

export const SelectItem = forwardRef<
  ElementRef<typeof SelectPrimitive.Item>,
  ComponentPropsWithoutRef<typeof SelectPrimitive.Item>
>(({ className, children, ...props }, ref) => (
  <SelectPrimitive.Item
    ref={ref}
    className={cn(
      'relative flex w-full cursor-default select-none items-center py-[6px] pl-8 pr-2 font-mono text-[12.5px] text-[var(--text-dim)] outline-none transition-colors',
      'focus:bg-[var(--panel)] focus:text-[var(--text)]',
      'data-[state=checked]:text-[var(--accent)]',
      'data-[disabled]:pointer-events-none data-[disabled]:opacity-50',
      className,
    )}
    style={{ borderRadius: 2 }}
    {...props}
  >
    <span className="absolute left-2 flex h-[14px] w-[14px] items-center justify-center">
      <SelectPrimitive.ItemIndicator>
        <Check className="h-[14px] w-[14px]" style={{ color: 'var(--accent)' }} />
      </SelectPrimitive.ItemIndicator>
    </span>
    <SelectPrimitive.ItemText>{children}</SelectPrimitive.ItemText>
  </SelectPrimitive.Item>
))
SelectItem.displayName = SelectPrimitive.Item.displayName
