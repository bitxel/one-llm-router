import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { WizardStepper } from './WizardStepper'

const STEPS = [
  { id: 'welcome', label: 'Welcome' },
  { id: 'database', label: 'Database' },
  { id: 'upstream', label: 'Upstream' },
  { id: 'plugins', label: 'Plugins' },
  { id: 'commit', label: 'Commit' },
]

describe('WizardStepper', () => {
  it('marks the current step', () => {
    render(<WizardStepper steps={STEPS} current={2} complete={new Set([0, 1])} />)
    expect(screen.getByTestId('wizard-step-2')).toHaveAttribute('data-state', 'current')
    expect(screen.getByTestId('wizard-step-0')).toHaveAttribute('data-state', 'complete')
    expect(screen.getByTestId('wizard-step-4')).toHaveAttribute('data-state', 'pending')
  })

  it('uses an ordered list for a11y', () => {
    render(<WizardStepper steps={STEPS} current={0} complete={new Set()} />)
    expect(screen.getByTestId('wizard-stepper').tagName).toBe('OL')
  })

  it('renders labels for each supplied step', () => {
    render(<WizardStepper steps={STEPS} current={0} complete={new Set()} />)
    for (const step of STEPS) {
      expect(screen.getByText(step.label)).toBeInTheDocument()
    }
  })
})
