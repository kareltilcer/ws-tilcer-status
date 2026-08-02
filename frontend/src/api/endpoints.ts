import { apiFetch } from './client'
import type {
  Color,
  CrashGroup,
  GroupDetail,
  GroupPage,
  GroupStatus,
  Level,
  Meta,
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
