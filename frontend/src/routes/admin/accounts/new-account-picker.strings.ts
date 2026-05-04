import { ACCOUNT_AUTH_METHODS } from '@/lib/account-auth-methods'

const accountMethods = Object.fromEntries(
  ACCOUNT_AUTH_METHODS.map((method) => [method.id, method]),
) as {
  [K in (typeof ACCOUNT_AUTH_METHODS)[number]['id']]: Extract<
    (typeof ACCOUNT_AUTH_METHODS)[number],
    { id: K }
  >
}

export const strings = {
  eyebrow: 'New account',
  stripe: 'Accounts',
  titleLead: 'Choose how the router',
  titleStrong: 'authenticates.',
  intro:
    'Choose the onboarding path that matches the operator environment. API-key onboarding, browser OAuth, device OAuth, and auth.json import are wired end-to-end.',
  action: {
    available: 'Open flow',
  },
  availability: {
    available: 'Available now',
  },
  cards: {
    apiKey: {
      title: accountMethods.api_key.title,
      badge: accountMethods.api_key.badge,
      detail:
        'Create a plaintext API-key row directly through the existing 002 admin contract. Best for service accounts and compatible OpenAI proxies.',
    },
    oauthBrowser: {
      title: accountMethods.oauth_browser.title,
      badge: accountMethods.oauth_browser.badge,
      detail:
        'Open the ChatGPT authorize tab and keep the paste callback rail available in parallel.',
    },
    oauthDevice: {
      title: accountMethods.oauth_device.title,
      badge: accountMethods.oauth_device.badge,
      detail:
        'Generate a device code, approve it on your own machine, and let the router poll until the account lands.',
    },
    oauthImport: {
      title: accountMethods.oauth_import.title,
      badge: accountMethods.oauth_import.badge,
      detail:
        'Upload a Codex-compatible auth.json file and let the router derive the account metadata from the embedded ID token claims.',
    },
  },
} as const
