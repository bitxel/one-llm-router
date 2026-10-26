export const BASE_URL_MAX_LEN = 256

export const CAPABILITY_OPTIONS = [
  { value: 'op.openai.chat_completions', label: 'Chat Completions' },
  { value: 'op.openai.responses', label: 'Responses' },
  { value: 'op.systemone', label: 'Decision / System One' },
]

export function isValidBaseURL(value: string): boolean {
  try {
    const parsed = new URL(value)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return false
    if (!parsed.host) return false
    return parsed.search === '' && parsed.hash === ''
  } catch {
    return false
  }
}

export function isSystemOneOnly(capabilities?: string[] | null): boolean {
  return (
    !!capabilities?.includes('op.systemone') &&
    !capabilities.includes('op.openai.chat_completions') &&
    !capabilities.includes('op.openai.responses')
  )
}
