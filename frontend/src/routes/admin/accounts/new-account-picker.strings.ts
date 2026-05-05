export const strings = {
  eyebrow: 'New account',
  stripe: 'Accounts',
  titleLead: 'Add an',
  titleStrong: 'account',
  intro:
    'Choose how to connect this router to LLM. Use browser sign-in locally, or device code when the router runs on a server.',
  action: {
    available: 'Select',
  },
  cards: {
    apiKey: {
      title: 'API key',
      detail: 'Add an OpenAI',
    },
    oauthBrowser: {
      title: 'Codex browser sign-in',
      detail: 'Sign in with your ChatGPT subscription in this browser.',
    },
    oauthDevice: {
      title: 'Codex Device Code',
      detail: 'Sign in from another device when this server cannot complete browser sign-in.',
    },
    oauthImport: {
      title: 'Import Codex auth.json',
      detail: 'Import an existing Codex auth.json file from a local client.',
    },
  },
} as const
