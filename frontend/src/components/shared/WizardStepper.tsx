import { Check } from 'lucide-react'

import { cn } from '@/lib/utils'

export interface WizardStep {
  id: string
  label: string
}

interface WizardStepperProps {
  steps: readonly WizardStep[]
  current: number
  complete: ReadonlySet<number>
}

/**
 * WizardStepper — horizontal step indicator for the setup wizard.
 * Accepts a `current` cursor and a set of completed indices so
 * completion state and cursor advance independently (a user can
 * revisit an earlier step without losing the "completed" indicator
 * on later steps).
 */
export function WizardStepper({ steps, current, complete }: WizardStepperProps) {
  return (
    <ol
      className="flex w-full items-center"
      aria-label="Setup progress"
      data-testid="wizard-stepper"
    >
      {steps.map((step, index) => {
        const isComplete = complete.has(index)
        const isCurrent = index === current
        return (
          <li
            key={step.id}
            className={cn(
              'relative flex flex-1 items-center gap-3 text-xs',
              index !== steps.length - 1 &&
                "after:ml-3 after:h-px after:flex-1 after:bg-(--color-border-subtle) after:content-['']",
            )}
          >
            <span
              className={cn(
                'flex h-7 w-7 items-center justify-center rounded-full text-xs font-medium',
                isComplete
                  ? 'bg-(--color-success)/20 text-(--color-success)'
                  : isCurrent
                    ? 'bg-(--color-primary)/15 text-(--color-primary)'
                    : 'bg-[var(--panel-2)] text-(--color-fg-subtle)',
              )}
              data-testid={`wizard-step-${index}`}
              data-state={isComplete ? 'complete' : isCurrent ? 'current' : 'pending'}
            >
              {isComplete ? <Check className="h-3 w-3" /> : index + 1}
            </span>
            <span
              className={cn(
                'font-medium',
                isCurrent ? 'text-(--color-fg-base)' : 'text-(--color-fg-muted)',
              )}
            >
              {step.label}
            </span>
          </li>
        )
      })}
    </ol>
  )
}
