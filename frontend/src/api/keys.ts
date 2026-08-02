import type { CrashFilters } from './endpoints'
import type { Color, UptimeWindow } from './types'

// Centralized TanStack Query keys so invalidation stays consistent (PRD §7).
export const qk = {
  meta: () => ['meta'] as const,
  sites: (status?: Color) => ['sites', status ?? 'all'] as const,
  site: (id: string) => ['site', id] as const,
  uptime: (id: string, window: UptimeWindow) => ['site', id, 'uptime', window] as const,
  crashes: (id: string, filters: CrashFilters) => ['site', id, 'crashes', filters] as const,
  crashGroup: (groupId: number) => ['crashGroup', groupId] as const,
}
