export function planTypeLabel(raw: string): string {
  switch (raw) {
    case 'plus':
    case 'chatgpt-plus':
      return 'ChatGPT Plus'
    case 'pro':
    case 'prolite':
    case 'chatgpt-pro':
      return 'ChatGPT Pro'
    case 'business':
    case 'chatgpt-business':
      return 'ChatGPT Business'
    case 'team':
    case 'chatgpt-team':
      return 'ChatGPT Team'
    case 'enterprise':
    case 'chatgpt-enterprise':
      return 'ChatGPT Enterprise'
    default:
      return raw
  }
}
