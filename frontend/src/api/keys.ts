import type { CrashFilters, ReportFilters } from './endpoints'
import type { Color, UptimeWindow } from './types'

// Centralized TanStack Query keys so invalidation stays consistent (PRD §7).
export const qk = {
  meta: () => ['meta'] as const,
  sites: (status?: Color) => ['sites', status ?? 'all'] as const,
  site: (id: string) => ['site', id] as const,
  uptime: (id: string, window: UptimeWindow) => ['site', id, 'uptime', window] as const,
  crashes: (id: string, filters: CrashFilters) => ['site', id, 'crashes', filters] as const,
  crashGroup: (groupId: number) => ['crashGroup', groupId] as const,
  // v3: mutations on a report invalidate the inbox AND the board, because
  // `new` is what drives the board badge.
  reports: (filters: ReportFilters) => ['reports', filters] as const,
  report: (ref: string) => ['report', ref] as const,
  // One key per attachment: the URL under it is a presigned bearer token that
  // expires in minutes, so it is cached briefly and never shared between reports.
  attachmentUrl: (ref: string, attachmentId: number) => ['report', ref, 'attachment', attachmentId] as const,
  feedbackConfig: (siteId: string) => ['site', siteId, 'feedback-config'] as const,
}
