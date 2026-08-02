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

/** relativeTime renders an ISO timestamp as a compact "12s ago" / "3m ago". */
export function relativeTime(iso: string | null): string {
  if (!iso) return '—'
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return '—'
  const secs = Math.max(0, Math.round((Date.now() - then) / 1000))
  if (secs < 5) return 'now'
  if (secs < 60) return `${secs}s ago`
  const mins = Math.round(secs / 60)
  if (mins < 60) return `${mins}m ago`
  const hours = Math.round(mins / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.round(hours / 24)
  return `${days}d ago`
}

/** uptimeText formats a rolling uptime percentage. */
export function uptimeText(pct: number | null): string {
  if (pct === null || pct === undefined) return '—'
  return `${pct.toFixed(2)}%`
}
