import { ACCOUNT_AUTH_METHODS } from '@/lib/account-auth-methods'

const apiKeyMethod = ACCOUNT_AUTH_METHODS.find((method) => method.id === 'api_key')
if (!apiKeyMethod) {
  throw new Error('missing api_key auth-method metadata')
}

export const strings = {
  eyebrow: apiKeyMethod.title,
  stripe: 'Accounts',
  titleLead: 'Register a',
  titleStrong: 'API Key.',
  intro:
    'Paste an OpenAI API key to create a new upstream account. The key is stored and used for routing requests to the OpenAI-compatible API.',
  panelTitle: 'API-key account',
  loading: 'Creating account...',
  labels: {
    name: 'Nickname',
    provider: 'Provider',
    apiKey: 'API key',
    baseURL: 'Base URL',
  },
  hints: {
    name: 'Letters, digits, dashes, and underscores. 1-64 chars.',
    provider: '003 keeps direct API-key creation OpenAI-only.',
    apiKey:
      '1-256 characters. Stored verbatim in upstream_accounts; encryption-at-rest is deferred to the key-vault plugin.',
    baseURL: 'Optional OpenAI-compatible endpoint override. Leave empty for the default.',
  },
  placeholders: {
    baseURL: 'https://api.openai.com',
  },
  actions: {
    create: 'Create API-key account',
  },
  validation: {
    nameRequired: 'name is required',
    nameShape: 'letters, digits, dashes, underscores only',
    apiKeyRequired: 'API key is required',
    apiKeyMax: 'API key must be 1-256 characters',
    baseURLMax: 'base_url must be <=256 characters',
    baseURLShape: 'base_url must be an absolute http(s) URL without query or fragment',
    missingAccountID: 'create account response missing account id',
  },
  toasts: {
    createSuccess: 'API-key account created',
    createFailed: 'Failed to create API-key account',
  },
} as const
