export const ACCOUNT_AUTH_METHODS = [
  { id: 'api_key' },
  { id: 'oauth_browser' },
  { id: 'oauth_device' },
  { id: 'oauth_import' },
] as const

export type AccountAuthMethod = (typeof ACCOUNT_AUTH_METHODS)[number]['id']

export const ACCOUNT_AUTH_METHOD_IDS = ACCOUNT_AUTH_METHODS.map((method) => method.id)
