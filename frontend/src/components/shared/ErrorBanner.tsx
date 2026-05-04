import { AlertCircle } from 'lucide-react'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { RouterApiError } from '@/lib/api-client'
import { symbolFor } from '@/lib/errcode'

export interface ErrorBannerProps {
  error: unknown
  title?: string
  className?: string
}

function normalize(err: unknown): {
  msg: string
  code: number
  requestId: string | null
  symbol: string
} {
  if (err instanceof RouterApiError) {
    return { msg: err.msg, code: err.code, requestId: err.requestId, symbol: symbolFor(err.code) }
  }
  if (err instanceof Error) {
    return { msg: err.message, code: -1, requestId: null, symbol: 'unknown_error' }
  }
  return { msg: String(err), code: -1, requestId: null, symbol: 'unknown_error' }
}

/**
 * ErrorBanner — renders a RouterApiError (or any thrown value) with
 * the correlation id prominently surfaced so operators can match
 * their browser state to a server log line in one step.
 */
export function ErrorBanner({ error, title, className }: ErrorBannerProps) {
  const { msg, code, requestId, symbol } = normalize(error)
  return (
    <Alert variant="destructive" className={className} data-testid="error-banner">
      <AlertCircle />
      <AlertTitle>{title ?? 'Request failed'}</AlertTitle>
      <AlertDescription className="space-y-2">
        <p className="font-mono text-xs">
          <span className="font-semibold">{symbol}</span>
          <span className="text-(--color-fg-muted)"> ({code})</span>: {msg}
        </p>
        {requestId ? (
          <p className="flex items-center gap-2 text-xs">
            <span>Correlation id</span>
            <Badge variant="outline" className="font-mono">
              {requestId}
            </Badge>
          </p>
        ) : null}
      </AlertDescription>
    </Alert>
  )
}
