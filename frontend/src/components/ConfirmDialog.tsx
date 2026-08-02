import type { CSSProperties } from 'react'

export interface ConfirmProps {
  title: string
  body: string
  confirmLabel: string
  danger?: boolean
  onConfirm: () => void
  onCancel: () => void
}

/** ConfirmDialog is a modal confirmation for destructive actions. */
export function ConfirmDialog({ title, body, confirmLabel, danger, onConfirm, onCancel }: ConfirmProps) {
  const confirmStyle: CSSProperties = danger
    ? { background: 'var(--danger)', color: 'var(--on-color)', border: '1px solid var(--danger)' }
    : { background: 'var(--accent)', color: 'var(--accent-fg)', border: '1px solid var(--accent)' }
  return (
    <div style={{ position: 'fixed', inset: 0, zIndex: 70, background: 'oklch(0 0 0 / .55)', display: 'grid', placeItems: 'center', padding: 24, animation: 'om-fadein .16s ease' }}>
      <div role="alertdialog" aria-modal="true" style={{ width: '100%', maxWidth: 440, border: '1px solid var(--border)', background: 'var(--s1)', borderRadius: 14, boxShadow: 'var(--shadow)', overflow: 'hidden' }}>
        <div style={{ padding: '22px 22px 4px' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 12 }}>
            <span style={{ display: 'grid', placeItems: 'center', height: 38, width: 38, borderRadius: 10, background: danger ? 'var(--danger-soft)' : 'var(--accent-soft)', color: danger ? 'var(--danger-text)' : 'var(--accent)' }}>{danger ? '⚠' : '?'}</span>
            <h2 style={{ margin: 0, fontSize: 17, fontWeight: 700 }}>{title}</h2>
          </div>
          <p style={{ margin: '0 0 18px', fontSize: 13.5, color: 'var(--muted)', lineHeight: 1.55 }}>{body}</p>
        </div>
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 10, padding: '14px 22px', borderTop: '1px solid var(--border)', background: 'var(--s2)' }}>
          <button onClick={onCancel} style={{ height: 38, padding: '0 15px', border: '1px solid var(--border)', background: 'var(--s1)', color: 'var(--text)', borderRadius: 8, fontSize: 13, fontWeight: 600, cursor: 'pointer' }}>Cancel</button>
          <button onClick={onConfirm} style={{ height: 38, padding: '0 15px', borderRadius: 8, fontSize: 13, fontWeight: 700, cursor: 'pointer', ...confirmStyle }}>{confirmLabel}</button>
        </div>
      </div>
    </div>
  )
}
