import { cva, type VariantProps } from 'class-variance-authority'
import type { HTMLAttributes } from 'react'

import { cn } from '@/lib/utils'

const alertVariants = cva(
  'relative w-full rounded-lg border p-4 text-sm [&>svg]:absolute [&>svg]:left-4 [&>svg]:top-4 [&>svg+*]:pl-8 [&>svg]:size-4',
  {
    variants: {
      variant: {
        default:
          'border-(--color-border-subtle) bg-(--color-surface-raised) text-(--color-fg-base)',
        destructive: 'border-(--color-danger) bg-(--color-danger)/10 text-(--color-danger)',
        success: 'border-(--color-success) bg-(--color-success)/10 text-(--color-success)',
        warning: 'border-(--color-warning) bg-(--color-warning)/10 text-(--color-warning)',
      },
    },
    defaultVariants: { variant: 'default' },
  },
)

export interface AlertProps
  extends HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof alertVariants> {}

export function Alert({ className, variant, ...props }: AlertProps) {
  return <div role="alert" className={cn(alertVariants({ variant }), className)} {...props} />
}

export function AlertTitle({ className, ...props }: HTMLAttributes<HTMLHeadingElement>) {
  return <h5 className={cn('mb-1 font-medium leading-none tracking-tight', className)} {...props} />
}

export function AlertDescription({ className, ...props }: HTMLAttributes<HTMLParagraphElement>) {
  return <div className={cn('text-sm opacity-90 [&_p]:leading-relaxed', className)} {...props} />
}
