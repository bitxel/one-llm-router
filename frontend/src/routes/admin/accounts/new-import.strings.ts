export const strings = {
  eyebrow: 'Import auth.json',
  stripe: 'Accounts',
  titleLead: 'Import a Codex',
  titleStrong: 'auth.json.',
  intro:
    'The router accepts a Codex-compatible auth.json file or pasted JSON text, derives the account metadata from the embedded ID token, and creates an oauth_import row without rendering token bytes in the UI.',
  panelTitle: 'Import source',
  panelMeta: 'file or pasted JSON',
  loading: 'Importing auth.json…',
  labels: {
    file: 'auth.json file',
    paste: 'Paste auth.json JSON',
    source: 'Source',
  },
  actions: {
    fileMode: 'File',
    pasteMode: 'Paste',
    upload: 'Import auth.json',
  },
  hints: {
    file: 'Select a Codex-compatible auth.json file from your local machine. The browser reads the file locally and submits the raw JSON text.',
    paste:
      'Paste the complete Codex-compatible auth.json content. The browser sends it as JSON text; token bytes are never rendered after submission.',
  },
  validation: {
    fileRequired: 'Choose an auth.json file before importing.',
    pasteRequired: 'Paste auth.json content before importing.',
  },
  toasts: {
    importSuccess: 'auth.json imported',
    importFailed: 'Failed to import auth.json',
  },
} as const
