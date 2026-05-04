export const ACCOUNT_AUTH_METHODS = [
  {
    id: 'api_key',
    title: 'API key',
    badge: 'Paste an OpenAI API key',
  },
  {
    id: 'oauth_browser',
    title: 'OAuth browser',
    badge: 'Sign in with ChatGPT in your browser',
  },
  {
    id: 'oauth_device',
    title: 'OAuth device',
    badge: 'Headless? Use a device code',
  },
  {
    id: 'oauth_import',
    title: 'Import auth.json',
    badge: 'Upload your local ~/.codex/auth.json',
  },
] as const

export type AccountAuthMethod = (typeof ACCOUNT_AUTH_METHODS)[number]['id']

export const ACCOUNT_AUTH_METHOD_IDS = ACCOUNT_AUTH_METHODS.map((method) => method.id)
