export const strings = {
  eyebrow: 'Browser OAuth',
  stripe: 'Accounts',
  titleLead: 'Add ChatGPT OAuth',
  titleStrong: 'account',
  intro:
    'Open a ChatGPT sign-in tab. If the browser cannot return to this router, paste the localhost callback URL below.',
  panel: {
    idle: 'Browser OAuth',
    pending: 'Flow in progress',
    conflict: 'A flow is already pending',
    failed: 'Flow failed',
    ready: 'ready',
  },
  badges: {
    pasteOnly: 'Paste-only mode — listener unavailable',
    failed: 'error',
  },
  fields: {
    browserActions: {
      label: 'Browser tab',
      hint: 'Open the ChatGPT sign-in tab now. If your browser blocked the popup, reopen it from here.',
    },
    callback: {
      label: 'Paste callback URL',
      hint: 'Paste the full localhost callback URL from your browser address bar. The raw URL stays hidden by default.',
      placeholder: 'http://localhost:1455/auth/callback?code=...&state=...',
    },
    flow: {
      label: 'Pending flow',
      hint: 'One OAuth flow may be active per router instance.',
    },
    flowStatus: {
      label: 'Flow status',
      hint: 'Automatic callback and manual paste are both accepted for this browser flow.',
    },
  },
  actions: {
    start: 'Start browser sign-in',
    openAgain: 'Open sign-in tab',
    openPending: 'Open in browser',
    submitCallback: 'Submit callback URL',
    cancelPending: 'Cancel flow',
    startAgain: 'Start again',
  },
  status: {
    browserMethod: 'Browser OAuth',
    browserMethodDetail:
      'The router opens one sign-in flow and accepts whichever callback rail finishes first.',
    expiresAt: 'Expires at',
  },
  callbackSummary: {
    title: 'Callback summary',
    invalid: 'Unparseable URL',
    codePresent: 'code present',
    errorPresent: 'error present',
    codeMissing: 'code missing',
    statePresent: 'state present',
    stateMissing: 'state missing',
  },
  validation: {
    callbackRequired: 'Paste the callback URL to continue.',
  },
  err: {
    oauth_flow_in_progress: 'A flow is already pending — finish or cancel it first.',
    oauth_state_mismatch:
      'State mismatch — paste the latest callback URL from the active sign-in tab.',
    invalid_callback_url: 'The pasted callback URL is not valid for this flow.',
    no_flow_in_progress: 'No OAuth flow is pending — start a new browser sign-in first.',
    oauth_invalid_grant: 'OpenAI rejected the authorization code for this flow.',
    oauth_upstream_error: 'OpenAI rejected the callback.',
    oauth_internal_error: 'The router hit an internal OAuth error.',
    oauth_store_failed: 'The router could not persist the OAuth account.',
    transport_error: 'The request did not reach the router cleanly.',
    default: 'The router rejected this request.',
  },
  callbackReason: {
    url_prefix_mismatch:
      'The callback URL must start with http://localhost:1455-1458/auth/callback.',
    missing_code_and_error: 'The callback URL is missing both code and error query parameters.',
  },
  conflict: {
    detail:
      'Only one OAuth flow may be active per router instance. Cancel the pending flow before starting a new browser sign-in.',
    methodPrefix: 'method',
    flowPrefix: 'flow',
  },
  terminal: {
    defaultTitle: 'The flow ended with an error.',
    defaultDetail:
      'Review the provider error, then start a fresh browser sign-in. The callback input stays hidden after a terminal error.',
    restartHint:
      'Restart from a clean flow. The router never reuses an already-consumed callback URL.',
  },
  toasts: {
    popupBlocked:
      'The sign-in tab was blocked by the browser. Use "Open sign-in tab again" to retry.',
    flowExpired: 'Flow expired — please start again.',
    flowCancelled: 'Flow cancelled — start again when you are ready.',
  },
} as const
