// Wire types — hand-mirrored from backend/openapi.yaml. Keep in sync with the Go
// response structs.

export type Color = 'red' | 'orange' | 'green' | 'unknown'
export type Level = 'fatal' | 'error' | 'warning'
export type GroupStatus = 'open' | 'resolved' | 'ignored'
export type UptimeWindow = '24h' | '7d' | '30d' | '90d'

export interface UserPublic {
  id: string
  email: string
  display_name: string | null
  roles: string[]
}

export interface SiteSummary {
  id: string
  name: string
  color: Color
  monitor_url: string | null
  monitor_enabled: boolean
  expected_status: number
  crash_window_hours: number
  last_checked_at: string | null
  last_ok: boolean | null
  fail_streak: number
  uptime_pct: number | null
  open_crash_groups: number
  recent_crash_count: number
  created_at: string
}

export interface SiteWithKey extends SiteSummary {
  ingest_key: string
}

export interface SiteCreate {
  id: string
  name: string
  monitor_url?: string | null
  monitor_enabled?: boolean
  expected_status?: number
  crash_window_hours?: number
}

export interface SiteUpdate {
  name?: string
  monitor_url?: string | null
  monitor_enabled?: boolean
  expected_status?: number
  crash_window_hours?: number
}

export interface CrashGroup {
  id: number
  site_id: string
  fingerprint: string
  title: string
  level: Level
  count: number
  status: GroupStatus
  first_seen: string
  last_seen: string
}

export interface CrashEvent {
  id: number
  group_id: number
  site_id: string
  level: Level
  message: string
  stack: string | null
  environment: string | null
  release: string | null
  context: Record<string, unknown> | null
  occurred_at: string
  received_at: string
}

export interface GroupPage {
  items: CrashGroup[]
  next_cursor: string | null
}

export interface GroupDetail {
  group: CrashGroup
  events: CrashEvent[]
  next_cursor: string | null
}

export interface UptimeBucket {
  start: string
  end: string
  ok_pct: number | null
  checks: number
  failed: number
  latency_p50_ms: number | null
}

export interface UptimeSummary {
  window: UptimeWindow
  from: string
  to: string
  uptime_pct: number | null
  checks_total: number
  checks_failed: number
  latency_p50_ms: number | null
  latency_p95_ms: number | null
  buckets: UptimeBucket[]
}

export interface Meta {
  // The configured rolling window (STATUS_UPTIME_WINDOW) that the cached
  // uptime_pct on each site summary is computed over.
  uptime_window_days: number
}
