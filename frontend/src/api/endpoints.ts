import { apiFetch } from './client'
import type {
  AttachmentURL,
  Color,
  DeliveryPage,
  CrashGroup,
  GroupDetail,
  GroupPage,
  GroupStatus,
  Level,
  FeedbackConfig,
  FeedbackConfigWithKey,
  Meta,
  NotificationSettings,
  NotificationSettingsUpdate,
  NotificationTestResult,
  Report,
  ReportKind,
  ReportPage,
  ReportPatch,
  ReportState,
  SiteCreate,
  SiteSummary,
  SiteUpdate,
  SiteWithKey,
  UptimeSummary,
  UptimeWindow,
  UserPublic,
} from './types'

// --- auth ---
export function getSession() {
  return apiFetch<{ user: UserPublic }>('/api/auth/session', { skipAuthRedirect: true })
}
export function login(email: string, password: string) {
  return apiFetch<{ user: UserPublic }>('/api/auth/login', {
    method: 'POST',
    body: { email, password },
    skipAuthRedirect: true,
  })
}
export function logout() {
  return apiFetch<void>('/api/auth/logout', { method: 'POST' })
}

// --- meta ---
export function getMeta() {
  return apiFetch<Meta>('/api/meta')
}

// --- sites ---
export function listSites(status?: Color) {
  const qs = status ? `?status=${encodeURIComponent(status)}` : ''
  return apiFetch<SiteSummary[]>(`/api/sites${qs}`)
}
export function getSite(id: string) {
  return apiFetch<SiteSummary>(`/api/sites/${encodeURIComponent(id)}`)
}
export function createSite(body: SiteCreate) {
  return apiFetch<SiteWithKey>('/api/sites', { method: 'POST', body })
}
export function updateSite(id: string, body: SiteUpdate) {
  return apiFetch<SiteSummary>(`/api/sites/${encodeURIComponent(id)}`, { method: 'PATCH', body })
}
export function deleteSite(id: string) {
  return apiFetch<void>(`/api/sites/${encodeURIComponent(id)}`, { method: 'DELETE' })
}
export function rotateKey(id: string) {
  return apiFetch<{ ingest_key: string }>(`/api/sites/${encodeURIComponent(id)}/rotate-key`, { method: 'POST' })
}

// --- monitoring ---
export function getUptime(id: string, window: UptimeWindow, buckets: number) {
  return apiFetch<UptimeSummary>(
    `/api/sites/${encodeURIComponent(id)}/uptime?window=${window}&buckets=${buckets}`,
  )
}

// --- crashes ---
export interface CrashFilters {
  status?: GroupStatus
  level?: Level
  cursor?: string
}
export function listCrashes(id: string, filters: CrashFilters = {}) {
  const q = new URLSearchParams()
  if (filters.status) q.set('status', filters.status)
  if (filters.level) q.set('level', filters.level)
  if (filters.cursor) q.set('cursor', filters.cursor)
  const qs = q.toString()
  return apiFetch<GroupPage>(`/api/sites/${encodeURIComponent(id)}/crashes${qs ? `?${qs}` : ''}`)
}
export function getCrashGroup(groupId: number, cursor?: string) {
  const qs = cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''
  return apiFetch<GroupDetail>(`/api/crashes/${groupId}${qs}`)
}
export function triageGroup(groupId: number, status: GroupStatus) {
  return apiFetch<CrashGroup>(`/api/crashes/${groupId}`, { method: 'PATCH', body: { status } })
}

// --- feedback (v3) ---
export interface ReportFilters {
  state?: ReportState
  site?: string
  kind?: ReportKind
  cursor?: string
}
export function listReports(filters: ReportFilters = {}) {
  const q = new URLSearchParams()
  if (filters.state) q.set('state', filters.state)
  if (filters.site) q.set('site', filters.site)
  if (filters.kind) q.set('kind', filters.kind)
  if (filters.cursor) q.set('cursor', filters.cursor)
  const qs = q.toString()
  return apiFetch<ReportPage>(`/api/reports${qs ? `?${qs}` : ''}`)
}
export function getReport(ref: string) {
  return apiFetch<Report>(`/api/reports/${encodeURIComponent(ref)}`)
}
export function triageReport(ref: string, body: ReportPatch) {
  return apiFetch<Report>(`/api/reports/${encodeURIComponent(ref)}`, { method: 'PATCH', body })
}
export function deleteReport(ref: string) {
  return apiFetch<void>(`/api/reports/${encodeURIComponent(ref)}`, { method: 'DELETE' })
}
/** attachmentUrl mints a presigned GET. ⚠ It is a bearer token for its lifetime
 *  (5 minutes by default), so it is fetched when an attachment is about to be
 *  shown and never put anywhere shareable. */
export function attachmentUrl(ref: string, attachmentId: number) {
  return apiFetch<AttachmentURL>(
    `/api/reports/${encodeURIComponent(ref)}/attachments/${attachmentId}/url`,
  )
}
export function getFeedbackConfig(siteId: string) {
  return apiFetch<FeedbackConfig>(`/api/sites/${encodeURIComponent(siteId)}/feedback-config`)
}
export function updateFeedbackConfig(siteId: string, body: { enabled?: boolean; console_capture?: boolean }) {
  return apiFetch<FeedbackConfigWithKey>(`/api/sites/${encodeURIComponent(siteId)}/feedback-config`, {
    method: 'PATCH',
    body,
  })
}
export function rotateWidgetKey(siteId: string) {
  return apiFetch<{ widget_key: string }>(`/api/sites/${encodeURIComponent(siteId)}/rotate-widget-key`, {
    method: 'POST',
  })
}

// --- notifications ---
export function getNotificationSettings() {
  return apiFetch<NotificationSettings>('/api/notifications/settings')
}
export function updateNotificationSettings(body: NotificationSettingsUpdate) {
  return apiFetch<NotificationSettings>('/api/notifications/settings', { method: 'PUT', body })
}
export function muteSite(siteId: string) {
  return apiFetch<void>(`/api/notifications/muted-sites/${encodeURIComponent(siteId)}`, { method: 'PUT' })
}
export function unmuteSite(siteId: string) {
  return apiFetch<void>(`/api/notifications/muted-sites/${encodeURIComponent(siteId)}`, { method: 'DELETE' })
}
/** sendTestNotification sends one real email now, to the SAVED recipients. */
export function sendTestNotification() {
  return apiFetch<NotificationTestResult>('/api/notifications/test', { method: 'POST' })
}
export function listDeliveries(limit = 20) {
  return apiFetch<DeliveryPage>(`/api/notifications/deliveries?limit=${limit}`)
}
