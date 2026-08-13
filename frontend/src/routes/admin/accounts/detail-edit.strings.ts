export const strings = {
  panelTitle: 'Edit API-key account',
  panelHint:
    'Update the nickname, rotate the API key, change the endpoint override, or adjust capabilities. OAuth accounts cannot be edited here.',
  labels: {
    name: 'Nickname',
    apiKey: 'API key',
    baseURL: 'Base URL',
    capabilities: 'Capabilities',
  },
  hints: {
    name: '1-64 characters.',
    apiKey: 'Leave blank to keep the current key. 1-256 characters if set.',
    baseURL: 'Optional OpenAI-compatible endpoint override. Leave empty to clear.',
    capabilities: 'API operations this account can handle. Leave empty for none.',
  },
  placeholders: {
    baseURL: 'https://api.openai.com',
  },
  actions: {
    save: 'Save changes',
    saving: 'Saving…',
  },
  validation: {
    nameRequired: 'name is required',
    nameMax: 'name must be at most 64 characters',
    apiKeyMax: 'API key must be 1-256 characters',
    baseURLMax: 'base_url must be <=256 characters',
    baseURLShape: 'base_url must be an absolute http(s) URL without query or fragment',
  },
  toasts: {
    updateSuccess: 'Account updated',
    updateFailed: 'Failed to update account',
  },
} as const
