import { describe, expect, it } from 'vitest'

import { planTypeLabel } from './plan-label'

describe('planTypeLabel', () => {
  it('maps the known OpenAI plan slugs to human-readable labels', () => {
    expect(planTypeLabel('plus')).toBe('ChatGPT Plus')
    expect(planTypeLabel('chatgpt-plus')).toBe('ChatGPT Plus')
    expect(planTypeLabel('pro')).toBe('ChatGPT Pro')
    expect(planTypeLabel('prolite')).toBe('ChatGPT Pro')
    expect(planTypeLabel('business')).toBe('ChatGPT Business')
    expect(planTypeLabel('team')).toBe('ChatGPT Team')
    expect(planTypeLabel('chatgpt-team')).toBe('ChatGPT Team')
    expect(planTypeLabel('enterprise')).toBe('ChatGPT Enterprise')
    expect(planTypeLabel('chatgpt-enterprise')).toBe('ChatGPT Enterprise')
  })

  it('falls through to the raw plan_type when the slug is unknown', () => {
    expect(planTypeLabel('chatgpt-edu')).toBe('chatgpt-edu')
  })

  it('preserves an empty string instead of inventing a placeholder', () => {
    expect(planTypeLabel('')).toBe('')
  })
})
