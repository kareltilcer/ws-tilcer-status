import { useState } from 'react'

/**
 * KeyModal shows a freshly-issued key exactly once.
 *
 * It is the same interaction for both of the service's keys — the `ik_` ingest
 * key and v3's `wk_` widget key — because both are stored as a SHA-256 and
 * neither can ever be shown again. Only the snippet under it differs, so the
 * caller supplies that.
 */
export function KeyModal({
  title,
  subtitle,
  keyValue,
  snippet,
  snippetLabel = 'Ready to paste',
  onClose,
}: {
  title: string
  subtitle: string
  keyValue: string
  snippet: string
  snippetLabel?: string
  onClose: () => void
}) {
  const [copied, setCopied] = useState(false)
  const copy = () => {
    void navigator.clipboard?.writeText(keyValue).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }

  return (
    <div style={{ position: 'fixed', inset: 0, zIndex: 70, background: 'oklch(0 0 0 / .55)', display: 'grid', placeItems: 'center', padding: 24, animation: 'om-fadein .18s ease' }}>
      <div role="dialog" aria-modal="true" style={{ width: '100%', maxWidth: 520, border: '1px solid var(--border)', background: 'var(--s1)', borderRadius: 14, boxShadow: 'var(--shadow)', overflow: 'hidden' }}>
        <div style={{ padding: '20px 22px', borderBottom: '1px solid var(--border)' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <span style={{ display: 'grid', placeItems: 'center', height: 34, width: 34, borderRadius: 9, background: 'var(--good-soft)', color: 'var(--good-text)' }}>🔑</span>
            <div>
              <div style={{ fontSize: 16, fontWeight: 700 }}>{title}</div>
              <div style={{ fontSize: 12.5, color: 'var(--muted)' }}>{subtitle}</div>
            </div>
          </div>
        </div>
        <div style={{ padding: 22 }}>
          <div style={{ display: 'flex', alignItems: 'flex-start', gap: 9, padding: '11px 13px', borderRadius: 9, background: 'color-mix(in oklab, var(--warn) 12%, transparent)', border: '1px solid color-mix(in oklab, var(--warn) 40%, transparent)', marginBottom: 16 }}>
            <span style={{ color: 'var(--warn-text)', flex: 'none', marginTop: 1 }}>⚠</span>
            <div style={{ fontSize: 12.5, color: 'var(--text)' }}>
              <b>You won't see this key again.</b> Store it now — you can rotate it later if it leaks, but this exact value is shown only once.
            </div>
          </div>
          <div style={{ display: 'flex', gap: 8, marginBottom: 18 }}>
            <code style={{ flex: 1, minWidth: 0, fontFamily: 'var(--mono)', fontSize: 13, background: 'var(--s2)', border: '1px solid var(--border-strong)', borderRadius: 8, padding: '11px 13px', whiteSpace: 'nowrap', overflowX: 'auto', color: 'var(--text)' }}>{keyValue}</code>
            <button onClick={copy} style={{ flex: 'none', display: 'inline-flex', alignItems: 'center', gap: 7, padding: '0 14px', border: '1px solid var(--accent)', background: 'var(--accent)', color: 'var(--accent-fg)', borderRadius: 8, fontSize: 13, fontWeight: 700, cursor: 'pointer' }}>{copied ? 'Copied' : 'Copy'}</button>
          </div>
          <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--subtle)', marginBottom: 6 }}>{snippetLabel}</div>
          <pre style={{ margin: '0 0 20px', fontFamily: 'var(--mono)', fontSize: 11.5, lineHeight: 1.6, background: 'var(--s2)', border: '1px solid var(--border)', borderRadius: 8, padding: 13, overflow: 'auto', color: 'var(--text)', whiteSpace: 'pre' }}>{snippet}</pre>
          <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
            <button onClick={onClose} style={{ height: 38, padding: '0 18px', border: '1px solid var(--border)', background: 'var(--s2)', color: 'var(--text)', borderRadius: 8, fontSize: 13.5, fontWeight: 600, cursor: 'pointer' }}>I've stored it</button>
          </div>
        </div>
      </div>
    </div>
  )
}
