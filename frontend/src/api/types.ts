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
  /** Reports in state `new` (v3). ⚠ Null, not zero, when the feedback module is
   *  not composed into this deployment — the card then renders no badge at all
   *  rather than a confident "0" (V3-D53). It never affects `color`. */
  open_reports: number | null
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
  // Whether this deployment has object storage configured. False means the
  // feedback switch on site detail is unavailable rather than merely off:
  // PATCH .../feedback-config answers 503.
  feedback_enabled: boolean
  // Whether this deployment has a mail provider (STATUS_RESEND_API_KEY, or the
  // log-only mailer in development). False means the notifications page can
  // only say so: turning them on answers 503.
  notifications_enabled: boolean
}

// --- feedback (v3) ---

export type ReportState = 'new' | 'open' | 'resolved' | 'declined'
export type ReportKind = 'bug' | 'idea' | 'other'
export type AttachmentState = 'pending' | 'stored' | 'missing'

export interface AttachmentSummary {
  id: number
  state: AttachmentState
  content_type: string
  /** What R2 reported at claim; null until then. The declared size is never
   *  published as if it had been confirmed. */
  byte_size: number | null
  created_at: string
}

export interface ReportSummary {
  ref: string
  site_id: string
  kind: ReportKind
  state: ReportState
  message: string
  reporter_label: string | null
  attachment_count: number
  created_at: string
  updated_at: string
}

export interface Report extends ReportSummary {
  page_url: string | null
  referrer: string | null
  user_agent: string | null
  viewport: string | null
  locale: string | null
  app_release: string | null
  console_tail: string[] | null
  last_error: string | null
  internal_note: string | null
  resolved_at: string | null
  attachments: AttachmentSummary[]
}

export interface ReportPage {
  items: ReportSummary[]
  next_cursor: string | null
}

export interface ReportPatch {
  state?: ReportState
  kind?: ReportKind
  /** null clears the note; absent leaves it alone. */
  internal_note?: string | null
}

export interface FeedbackConfig {
  site_id: string
  enabled: boolean
  console_capture: boolean
  widget_key_set_at: string | null
  updated_at: string | null
}

/** FeedbackConfigWithKey is the PATCH response: on the first enable it carries
 *  the plaintext widget key, shown exactly once — the `ik_` precedent. */
export interface FeedbackConfigWithKey extends FeedbackConfig {
  widget_key?: string
}

export interface AttachmentURL {
  url: string
  expires_at: string
  content_type: string
}

// --- notifications ---

export interface NotificationEvents {
  /** New crash groups and resolved groups that came back — error/fatal, production only. */
  crash: boolean
  /** New feedback reports. */
  feedback: boolean
  /** A site turning red, and the next passing check after it. */
  downtime: boolean
}

export interface NotificationSettings {
  available: boolean
  /** "resend", or "log" on a development deployment; null when unavailable. */
  provider: string | null
  from: string
  enabled: boolean
  /** null for a caller without admin — the addresses are personal data. */
  recipients: string[] | null
  events: NotificationEvents
  muted_sites: string[]
  digest_window_seconds: number
  max_per_hour: number
  updated_at: string | null
}

export interface NotificationSettingsUpdate {
  enabled: boolean
  recipients: string[]
  events: NotificationEvents
}

export type DeliveryState = 'pending' | 'sent' | 'failed'

export interface NotificationDelivery {
  id: number
  created_at: string
  state: DeliveryState
  subject: string
  event_count: number
  recipients: string[] | null
  attempts: number
  /** Set only while pending. */
  next_attempt_at: string | null
  /** For a caller without admin, every address in it reads "[address]". */
  last_error: string | null
  sent_at: string | null
}

export interface DeliveryPage {
  items: NotificationDelivery[]
}

export interface NotificationTestResult {
  provider_message_id: string
  recipients: string[]
}
