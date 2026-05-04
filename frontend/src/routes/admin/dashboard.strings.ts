export const strings = {
  eyebrow: 'Observability',
  stripe: '',
  title: 'Dashboard',
  intro:
    'Monitor live account capacity, request volume, token mix, error rate, and first-token latency from the router request log.',
  errorTitle: 'Dashboard unavailable',
  loading: 'Loading',
  emptyValue: '-',
  rangeTotal: 'latest window',
  accountAll: 'All accounts',
  noActiveAccounts: {
    title: 'No active upstream accounts',
    prefix: 'Routing is blocked until at least one account is active. Open',
    link: 'Accounts -> New',
    suffix: ' to add browser OAuth, device OAuth, import auth.json, or create an API-key row.',
  },
  labels: {
    range: 'Range',
    account: 'Account',
  },
  actions: {
    refresh: 'Refresh',
    editDashboard: 'Edit dashboard',
    hide: (label: string) => `Hide ${label}`,
    moveDown: (label: string) => `Move ${label} down`,
    moveUp: (label: string) => `Move ${label} up`,
    show: (label: string) => `Show ${label}`,
  },
  hidden: {
    title: 'Hidden cards',
    itemMeta: 'hidden',
    meta: (count: number) => `${count} hidden`,
  },
  cards: {
    activeAccounts: {
      title: 'Active accounts',
      shortTitle: 'Active Accounts',
      meta: 'current',
    },
    requests: {
      title: 'Requests',
      shortTitle: 'Requests',
    },
    tokens: {
      title: 'Tokens',
      shortTitle: 'Tokens',
      meta: (cached: string, nonCached: string, output: string) =>
        `cache ${cached} / input ${nonCached} / output ${output}`,
    },
    errorRate: {
      title: 'Error rate',
      shortTitle: 'Error Rate',
      meta: (errors: string, total: string) => `${errors} / ${total} errors`,
    },
    ttft: {
      title: 'TTFT',
      shortTitle: 'TTFT',
      meta: (samples: string) => `${samples} samples`,
    },
  },
} as const
