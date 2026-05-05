export const strings = {
  eyebrow: 'Device OAuth',
  stripe: 'Accounts',
  titleLead: 'Sign in with Codex',
  titleStrong: 'Device Code',
  intro:
    'Use this when localhost callback is unavailable. Open the verification page, enter the device code, and this page will continue automatically after approval.',
  panel: {
    code: 'Device code',
    pending: 'Device approval',
    blocked: 'Flow blocked',
    unavailable: 'Device flow unavailable',
    failed: 'Flow ended',
  },
  fields: {
    userCode: {
      label: 'Device code',
      hint: 'Enter this code on the verification page.',
    },
    verificationURL: {
      label: 'Open verification page',
      hint: 'Use the provider page to approve this router session.',
    },
    countdown: {
      label: 'Expiry in',
      hint: '',
    },
    flow: {
      label: 'Pending flow',
      hint: 'Only one OAuth flow may be active per router instance. Cancelling clears the current in-memory slot.',
    },
  },
  actions: {
    openVerification: 'Open verification page',
    copyCode: 'Copy code',
    copyURL: 'Copy URL',
    copied: 'Copied',
    cancelPending: 'Cancel pending flow',
    restart: 'Restart device flow',
    tryBrowser: 'Try browser sign-in instead',
  },
  status: {
    starting: 'Starting device flow…',
    pending: 'Waiting',
    success: 'Signed in — redirecting…',
    expired: 'Flow expired — please restart',
    cancelled: 'Cancelled',
    denied: 'Approval denied',
    failed: 'Flow failed',
  },
  err: {
    oauth_flow_in_progress: 'A flow is already pending — finish or cancel it first.',
    device_auth_unavailable:
      'This account cannot use the device flow — try browser sign-in instead.',
    invalid_oauth_provider: 'The requested OAuth provider is not available for this route.',
    oauth_upstream_error: 'OpenAI rejected the device-code request.',
    oauth_internal_error: 'The router hit an internal OAuth error.',
    oauth_store_failed: 'The router could not persist the OAuth account.',
    transport_error: 'The request did not reach the router cleanly.',
    default: 'The router rejected this request.',
  },
  toasts: {
    flowExpired: 'Flow expired — start again when you are ready.',
    flowCancelled: 'Flow cancelled.',
    copyFailed: 'Clipboard write failed.',
  },
  labels: {
    userCode: 'Device code',
    copyCode: 'Copy code',
    copyURL: 'Copy URL',
    conflictMethod: 'method',
    conflictFlow: 'flow',
    conflictExpires: 'expires',
  },
  countdownExpired: 'Expired',
  countdownAwaitingServer: 'Awaiting server…',
} as const
