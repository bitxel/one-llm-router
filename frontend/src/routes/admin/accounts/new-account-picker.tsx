import { useNavigate } from '@tanstack/react-router'
import {
  ArrowRight,
  FileUp,
  Globe,
  KeyRound,
  type LucideIcon,
  MonitorSmartphone,
} from 'lucide-react'

import { Canvas, Stripe } from '@/components/neo'
import { ACCOUNT_AUTH_METHODS, type AccountAuthMethod } from '@/lib/account-auth-methods'

import { strings } from './new-account-picker.strings'

interface AccountMethodCard {
  authMethod: AccountAuthMethod
  title: string
  detail: string
  to:
    | '/admin/accounts/new-apikey'
    | '/admin/accounts/new-oauth'
    | '/admin/accounts/new-oauth-device'
    | '/admin/accounts/new-import'
  icon: LucideIcon
}

const routeByMethod = {
  api_key: '/admin/accounts/new-apikey',
  oauth_browser: '/admin/accounts/new-oauth',
  oauth_device: '/admin/accounts/new-oauth-device',
  oauth_import: '/admin/accounts/new-import',
} as const satisfies Record<AccountAuthMethod, AccountMethodCard['to']>

const iconByMethod = {
  api_key: KeyRound,
  oauth_browser: Globe,
  oauth_device: MonitorSmartphone,
  oauth_import: FileUp,
} as const satisfies Record<AccountAuthMethod, LucideIcon>

const detailByMethod = {
  api_key: strings.cards.apiKey.detail,
  oauth_browser: strings.cards.oauthBrowser.detail,
  oauth_device: strings.cards.oauthDevice.detail,
  oauth_import: strings.cards.oauthImport.detail,
} as const satisfies Record<AccountAuthMethod, string>

const titleByMethod = {
  api_key: strings.cards.apiKey.title,
  oauth_browser: strings.cards.oauthBrowser.title,
  oauth_device: strings.cards.oauthDevice.title,
  oauth_import: strings.cards.oauthImport.title,
} as const satisfies Record<AccountAuthMethod, string>

const accountMethodCards: readonly AccountMethodCard[] = ACCOUNT_AUTH_METHODS.map((method) => ({
  authMethod: method.id,
  title: titleByMethod[method.id],
  detail: detailByMethod[method.id],
  to: routeByMethod[method.id],
  icon: iconByMethod[method.id],
}))

export function AdminAccountsNewAccountPicker() {
  const navigate = useNavigate()

  function openRoute(card: AccountMethodCard) {
    navigate({ to: card.to })
  }

  return (
    <Canvas variant="wide">
      <Stripe eyebrow={strings.eyebrow}>{strings.stripe}</Stripe>
      <div className="mb-8 max-w-[70ch]">
        <h1 className="mb-3 max-w-[18ch] text-balance">
          {strings.titleLead} <strong>{strings.titleStrong}</strong>
        </h1>
        <p className="max-w-none whitespace-nowrap text-[14px] leading-[1.7] text-[var(--text-dim)]">
          {strings.intro}
        </p>
      </div>

      <div className="grid gap-4 md:grid-cols-2">
        {accountMethodCards.map((card) => {
          const Icon = card.icon
          return (
            <button
              key={card.authMethod}
              type="button"
              data-auth-method={card.authMethod}
              data-available="true"
              aria-disabled="false"
              onClick={() => {
                openRoute(card)
              }}
              className="group flex h-full min-h-[190px] cursor-pointer flex-col overflow-hidden border border-[var(--line)] bg-[var(--panel)] text-left transition-[border-color,background-color,transform] duration-150 hover:border-[var(--accent)] hover:bg-[var(--panel-hi)] focus-visible:outline-none focus-visible:[box-shadow:var(--focus)]"
              style={{ borderRadius: 2 }}
            >
              <span className="flex items-center justify-between border-b border-[var(--line)] px-4 py-3">
                <span className="flex min-w-0 items-center gap-3">
                  <span
                    className="flex h-9 w-9 shrink-0 items-center justify-center border border-[var(--line-2)] bg-[var(--panel-2)]"
                    style={{ color: 'var(--accent)', borderRadius: 2 }}
                  >
                    <Icon className="h-[16px] w-[16px]" />
                  </span>
                  <span className="min-w-0">
                    <span className="block text-[15px] font-medium text-[var(--text)]">
                      {card.title}
                    </span>
                  </span>
                </span>
                <ArrowRight className="h-4 w-4 shrink-0 text-[var(--text-muted)] transition-transform group-hover:translate-x-0.5 group-hover:text-[var(--accent)]" />
              </span>
              <span className="flex flex-1 flex-col justify-between gap-5 px-4 py-4">
                <span className="block max-w-[46ch] text-[13px] leading-[1.6] text-[var(--text-dim)]">
                  {card.detail}
                </span>
                <span className="font-mono text-[11px] uppercase tracking-[0.12em] text-[var(--accent)]">
                  {strings.action.available}
                </span>
              </span>
            </button>
          )
        })}
      </div>
    </Canvas>
  )
}
