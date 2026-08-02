import type { CSSProperties } from 'react'
import type { UptimeBucket, UptimeSummary } from '@/api/types'

function barColor(ok: number | null): string {
  if (ok === null) return 'var(--s3)' // gap — overridden with a hatch below
  if (ok >= 99.5) return 'var(--good)'
  if (ok >= 90) return 'var(--warn)'
  return 'var(--danger)'
}

function bucketStyle(b: UptimeBucket): CSSProperties {
  if (b.ok_pct === null) {
    // Distinct "no data" (gap) state — hatched, never confused with 0% or healthy.
    return {
      flex: 1,
      minWidth: 2,
      height: '100%',
      borderRadius: 2,
      background: 'repeating-linear-gradient(45deg, var(--s3), var(--s3) 2px, var(--s2) 2px, var(--s2) 4px)',
      opacity: 0.8,
    }
  }
  return { flex: 1, minWidth: 2, height: '100%', borderRadius: 2, background: barColor(b.ok_pct) }
}

function bucketTitle(b: UptimeBucket): string {
  const day = b.start.slice(0, 10)
  if (b.ok_pct === null) return `${day}: no data`
  return `${day}: ${b.ok_pct.toFixed(1)}% up (${b.failed}/${b.checks} failed)`
}

const legend = [
  { label: 'Healthy', bg: 'var(--good)' },
  { label: 'Degraded', bg: 'var(--warn)' },
  { label: 'Down', bg: 'var(--danger)' },
  { label: 'No data', bg: 'repeating-linear-gradient(45deg, var(--s3), var(--s3) 2px, var(--s2) 2px, var(--s2) 4px)' },
]

/** UptimeStrip renders the per-bucket availability strip from the /uptime endpoint. */
export function UptimeStrip({ summary }: { summary: UptimeSummary }) {
  return (
    <div>
      <div style={{ display: 'flex', gap: 2, alignItems: 'flex-end', height: 32 }}>
        {summary.buckets.map((b, i) => (
          <div key={i} title={bucketTitle(b)} style={bucketStyle(b)} />
        ))}
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 16, flexWrap: 'wrap', marginTop: 12 }}>
        {legend.map((l) => (
          <span key={l.label} style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 11.5, color: 'var(--muted)' }}>
            <span style={{ height: 10, width: 10, borderRadius: 3, background: l.bg, display: 'inline-block' }} />
            {l.label}
          </span>
        ))}
      </div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10, flexWrap: 'wrap', marginTop: 10, fontSize: 11.5, color: 'var(--subtle)' }}>
        <span>{summary.buckets.length} buckets · {summary.window} window</span>
        <span style={{ fontFamily: 'var(--mono)' }}>
          {summary.latency_p50_ms !== null ? `p50 ${summary.latency_p50_ms}ms` : 'p50 —'} ·{' '}
          {summary.latency_p95_ms !== null ? `p95 ${summary.latency_p95_ms}ms` : 'p95 —'}
        </span>
      </div>
    </div>
  )
}
