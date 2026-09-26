// Small formatting helpers (no date-fns dependency).

import type { UptimeWindow } from '@/api/types'

/** uptimeWindow maps the configured uptime-window day count (STATUS_UPTIME_WINDOW)
 * to the nearest strip window enum, its natural bucket count, and a human label.
 * The /uptime endpoint is enum-based (24h|7d|30d|90d), so a configured window is
 * snapped to the closest supported view; the standard values (7/30/90) map
 * exactly. Keeps the board and the detail strip describing the same window. */
export function uptimeWindow(days: number): { window: UptimeWindow; buckets: number; label: string } {
  if (days <= 1) return { window: '24h', buckets: 24, label: '24 hours' }
  if (days <= 7) return { window: '7d', buckets: 7, label: '7 days' }
  if (days <= 30) return { window: '30d', buckets: 30, label: '30 days' }
  return { window: '90d', buckets: 90, label: '90 days' }
}

/** compactSpan renders a positive number of seconds as "12s" / "3m" / "2h" /
 *  "4d" — the one rounding rule both directions of relative time share. */
function compactSpan(secs: number): string {
  if (secs < 60) return `${secs}s`
  const mins = Math.round(secs / 60)
  if (mins < 60) return `${mins}m`
  const hours = Math.round(mins / 60)
  if (hours < 24) return `${hours}h`
  return `${Math.round(hours / 24)}d`
}

/** relativeTime renders an ISO timestamp as a compact "12s ago" / "3m ago". */
export function relativeTime(iso: string | null): string {
  if (!iso) return '—'
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return '—'
  const secs = Math.max(0, Math.round((Date.now() - then) / 1000))
  if (secs < 5) return 'now'
  return `${compactSpan(secs)} ago`
}

/** relativeTimeAhead renders a future ISO timestamp as "in 3m"; anything due
 *  within a few seconds, already past, or unreadable reads "shortly". */
export function relativeTimeAhead(iso: string): string {
  const secs = Math.round((new Date(iso).getTime() - Date.now()) / 1000)
  if (Number.isNaN(secs) || secs <= 5) return 'shortly'
  return `in ${compactSpan(secs)}`
}

/** fileSize renders an attachment's size the way a phone would — "420 kB",
 *  "18.4 MB". Base 1024, matching the caps the server derives from
 *  STATUS_FEEDBACK_MAX_*_MB, so a file at the limit reads as the limit.
 *
 *  ⚠ The widget has its own `formatBytes` (src/widget/format.ts) and the two are
 *  deliberately separate — the widget bundle imports nothing from src/api or
 *  src/lib, because it is a distributable artifact built ASCII-only against a
 *  15 kB budget. They MUST agree on the rounding rule, and do: base 1024, a
 *  1 kB floor, whole megabytes without a decimal and one decimal otherwise. The
 *  same attachment is shown by both — as a chip while it is being sent, as a card
 *  afterwards — so a divergence would print two sizes for one file. The only
 *  intended differences are this function's null/0 case (a `pending` attachment
 *  has no size yet; a `File` always does) and the widget's Czech decimal comma.
 *  Change one, change the other. */
export function fileSize(bytes: number | null): string {
  if (bytes === null || bytes <= 0) return 'size unknown'
  if (bytes < 1024 * 1024) return `${Math.max(1, Math.round(bytes / 1024))} kB`
  const mb = bytes / (1024 * 1024)
  return `${Number.isInteger(mb) ? mb : mb.toFixed(1)} MB`
}

/** uptimeText formats a rolling uptime percentage. */
export function uptimeText(pct: number | null): string {
  if (pct === null || pct === undefined) return '—'
  return `${pct.toFixed(2)}%`
}
