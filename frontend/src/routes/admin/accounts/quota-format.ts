export function formatDeadline(value: string | null | undefined, now = new Date()): string {
  if (!value) {
    return ''
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  const time = new Intl.DateTimeFormat('en-US', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(date)
  const withinDay = date.getTime() - now.getTime() <= 24 * 60 * 60 * 1000
  if (withinDay) {
    return time
  }
  const day = new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric' }).format(date)
  return `${day} ${time}`
}

export function formatWindowSeconds(seconds: number | null | undefined): string {
  if (seconds == null || !Number.isFinite(seconds) || seconds <= 0) {
    return ''
  }
  const totalMinutes = Math.round(seconds / 60)
  const dayMinutes = 24 * 60
  if (totalMinutes >= dayMinutes) {
    return `${Math.round(totalMinutes / dayMinutes)}d`
  }
  const hours = Math.floor(totalMinutes / 60)
  const minutes = totalMinutes % 60
  if (hours === 0) {
    return `${minutes}m`
  }
  if (minutes === 0) {
    return `${hours}h`
  }
  return `${hours}h ${minutes}m`
}

// Renders the quota sub-line shown under a used-percent value, without
// the leading icon: "Aug 20 13:07:39 / 2h" (the window duration is only
// shown prefixed with "/" when a reset time is also present).
export function formatQuotaDetail(
  resetAt: string | null | undefined,
  windowSeconds: number | null | undefined,
  now = new Date(),
): string {
  const time = resetAt ? formatDeadline(resetAt, now) : ''
  const window = windowSeconds ? formatWindowSeconds(windowSeconds) : ''
  if (time && window) {
    return `${time} / ${window}`
  }
  return time || window
}
