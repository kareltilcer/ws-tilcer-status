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
  //
  // ⚠ It sits OUTSIDE the ['report', ref] prefix on purpose. Invalidation in
  // TanStack Query matches by prefix, so nesting these under the report would
  // make every triage click re-mint a presigned URL per attachment and swap
  // every <img src> on the page.
  attachmentUrl: (ref: string, attachmentId: number) => ['attachment', ref, attachmentId] as const,
  // ⚠ Outside the ['site', id] prefix for the same reason, and it was inside it:
  // SiteDetail invalidates qk.site(id) after every save — a rename, a monitor
  // toggle, an interval change — and prefix matching then refetched the feedback
  // configuration too, on a service whose one writer connection every request
  // queues behind. Nothing a site edit changes is in this response; `uptime` and
  // `crashes` stay nested because a monitoring change genuinely moves them.
  feedbackConfig: (siteId: string) => ['feedback-config', siteId] as const,
}
