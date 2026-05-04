export const strings = {
  eyebrow: 'Browser OAuth',
  stripe: 'Accounts',
  titleLead: 'Sign in with ChatGPT in your',
  titleStrong: 'browser.',
  intro:
    'The router opens the ChatGPT sign-in tab and keeps a paste rail open in parallel. Paste the final callback URL here if the browser cannot reach localhost on the router host.',
  panel: {
    idle: 'Browser OAuth',
    pending: 'Flow in progress',
    conflict: 'A flow is already pending',
    failed: 'Flow failed',
    ready: 'ready',
  },
  badges: {
    loopbackReady: 'Loopback listener active',
    pasteOnly: 'Paste-only mode — loopback unavailable',
    pending: 'pending',
    failed: 'error',
  },
  fields: {
    browserActions: {
      label: 'Browser tab',
      hint: 'Open the ChatGPT sign-in tab now. If your browser blocked the popup, reopen it from here.',
    },
    callback: {
      label: 'Paste callback URL',
      hint: 'Paste the full localhost callback URL from your browser address bar, even if the page showed a connection-refused error.',
      placeholder: 'http://localhost:1455/auth/callback?code=...&state=...',
    },
    flow: {
      label: 'Pending flow',
      hint: 'One flow may be active per router instance. Cancelling clears the in-memory slot and returns you to a clean start screen.',
    },
  },
  actions: {
    start: 'Start browser sign-in',
    openAgain: 'Open sign-in tab again',
    submitCallback: 'Submit pasted URL',
    cancelPending: 'Cancel pending flow',
    startAgain: 'Start again',
  },
  status: {
    browserMethod: 'Browser OAuth',
    browserMethodDetail:
      'Both callback rails stay open for every browser flow. There is no mode switch and no hostname heuristic.',
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
      'Review the provider error, then start a fresh browser sign-in. The paste textarea stays hidden after a terminal error.',
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
