import { i18n } from '@/i18n'
import { RouterApiError } from '@/lib/api-client'
import { symbolFor } from '@/lib/errcode'

/**
 * Localized rendering of router API failures.
 *
 * The backend envelope `msg` stays a stable, non-localized diagnostic
 * string (root AGENTS.md: operator-grep-able, never localized). The
 * frontend maps the numeric error `code` to its stable symbol
 * (`lib/errcode.ts`, mirrored from docs/error-codes.md) and renders the
 * matching `errors` catalog entry. When a symbol has no catalog entry
 * (backend/frontend drift), the canonical server `msg` is shown
 * instead — a display fallback, never a fabricated success.
 */

/**
 * Translate a message that MAY be a catalog key (e.g. validation
 * messages stored as keys in zod schemas). Plain strings — such as zod
 * library defaults — pass through untouched, so this is safe to apply
 * to every `error.message` rendered from React Hook Form.
 */
export function tDynamic(message: string | undefined): string {
  if (!message) return ''
  const exists = i18n.exists as (key: string) => boolean
  const t = i18n.t as (key: string) => string
  return exists(message) ? t(message) : message
}

/** Localized, human-readable message for any thrown value. */
export function localizedErrorMessage(err: unknown): string {
  if (err instanceof RouterApiError) {
    const key = `errors:${symbolFor(err.code)}`
    const exists = i18n.exists as (k: string) => boolean
    const t = i18n.t as (k: string) => string
    return exists(key) ? t(key) : err.msg
  }
  if (err instanceof Error) {
    return err.message
  }
  return String(err)
}
