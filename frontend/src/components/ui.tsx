import type { CSSProperties, ReactNode } from 'react'
import type { AttachmentState, Color, Level, ReportKind, ReportState } from '@/api/types'

// The status color system (design comp). Each state carries a fill, an AA-tuned
// text token, a soft background, and a non-color cue (glyph) + label — status is
// never conveyed by color alone.
export const COLOR: Record<Color, { name: string; label: string; cue: string; fill: string; soft: string; text: string }> = {
  green: { name: 'Green', label: 'Healthy', cue: '✓', fill: 'var(--good)', soft: 'var(--good-soft)', text: 'var(--good-text)' },
  orange: { name: 'Orange', label: 'Noisy', cue: '!', fill: 'var(--warn)', soft: 'var(--warn-soft)', text: 'var(--warn-text)' },
  red: { name: 'Red', label: 'Down', cue: '✕', fill: 'var(--danger)', soft: 'var(--danger-soft)', text: 'var(--danger-text)' },
  unknown: { name: 'Unknown', label: 'No data', cue: '–', fill: 'var(--subtle)', soft: 'var(--s3)', text: 'var(--subtle)' },
}

export function StatusPill({ color, size = 'md' }: { color: Color; size?: 'sm' | 'md' }) {
  const c = COLOR[color]
  const fs = size === 'sm' ? 11.5 : 12.5
  const dot = size === 'sm' ? 15 : 16
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 6,
        flex: 'none',
        padding: '3px 9px 3px 5px',
        borderRadius: 999,
        fontSize: fs,
        fontWeight: 700,
        border: `1px solid ${c.fill}`,
        color: c.text,
        background: c.soft,
      }}
    >
      <span
        aria-hidden
        style={{
          display: 'grid',
          placeItems: 'center',
          height: dot,
          width: dot,
          borderRadius: '50%',
          fontSize: 9,
          fontWeight: 800,
          color: 'var(--on-color)',
          background: c.fill,
        }}
      >
        {c.cue}
      </span>
      {c.name}
    </span>
  )
}

const LEVEL_STYLE: Record<Level, CSSProperties> = {
  fatal: { background: 'var(--danger)', color: 'var(--on-color)', border: '1px solid var(--danger)' },
  error: {
    background: 'color-mix(in oklab, var(--err-orange) 16%, transparent)',
    color: 'var(--err-orange-text)',
    border: '1px solid color-mix(in oklab, var(--err-orange) 50%, transparent)',
  },
  warning: {
    background: 'color-mix(in oklab, var(--warn) 14%, transparent)',
    color: 'var(--warn-text)',
    border: '1px solid color-mix(in oklab, var(--warn) 45%, transparent)',
  },
}

export function LevelBadge({ level }: { level: Level }) {
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        padding: '3px 9px',
        borderRadius: 6,
        fontSize: 11,
        fontWeight: 700,
        letterSpacing: '.04em',
        textTransform: 'uppercase',
        ...LEVEL_STYLE[level],
      }}
    >
      {level}
    </span>
  )
}

// v3 — the report palette is deliberately NOT the status palette. A person
// saying "this is confusing" must never make a site look degraded beside a real
// outage, so `new` reads as "someone wrote to you" in the accent and the closed
// states fade to neutral. Green/orange/red stay the machine's language.
const REPORT_STATE: Record<ReportState, { cue: string; style: CSSProperties }> = {
  new: { cue: '●', style: { background: 'var(--accent)', color: 'var(--accent-fg)', border: '1px solid var(--accent)' } },
  open: {
    cue: '○',
    style: {
      background: 'color-mix(in oklab, var(--accent) 14%, transparent)',
      color: 'var(--accent)',
      border: '1px solid color-mix(in oklab, var(--accent) 45%, transparent)',
    },
  },
  resolved: { cue: '✓', style: { background: 'var(--s3)', color: 'var(--muted)', border: '1px solid var(--border-strong)' } },
  declined: { cue: '✕', style: { background: 'transparent', color: 'var(--subtle)', border: '1px dashed var(--border-strong)' } },
}

export function StateChip({ state }: { state: ReportState }) {
  const s = REPORT_STATE[state]
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 5,
        padding: '3px 9px 3px 7px',
        borderRadius: 999,
        fontSize: 11,
        fontWeight: 700,
        letterSpacing: '.02em',
        textTransform: 'uppercase',
        whiteSpace: 'nowrap',
        ...s.style,
      }}
    >
      <span aria-hidden style={{ fontSize: 9 }}>{s.cue}</span>
      {state}
    </span>
  )
}

export function KindChip({ kind }: { kind: ReportKind }) {
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        padding: '2px 8px',
        borderRadius: 6,
        fontFamily: 'var(--mono)',
        fontSize: 10.5,
        fontWeight: 600,
        letterSpacing: '.04em',
        textTransform: 'uppercase',
        background: 'var(--s3)',
        border: '1px solid var(--border)',
        color: 'var(--muted)',
      }}
    >
      {kind}
    </span>
  )
}

/** UnreadBadge is the board card's "someone wrote to you".
 *
 *  ⚠ It carries an envelope and the word "report" as well as the accent, so in
 *  greyscale it still reads as a count of messages — and it lives in the card's
 *  meta row rather than beside the status pill, because the separation from a
 *  health signal has to be spatial before it is chromatic. */
export function UnreadBadge({ count }: { count: number }) {
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 6,
        padding: '3px 10px 3px 8px',
        borderRadius: 7,
        fontSize: 11.5,
        fontWeight: 700,
        whiteSpace: 'nowrap',
        border: '1px solid color-mix(in oklab, var(--accent) 45%, transparent)',
        background: 'color-mix(in oklab, var(--accent) 14%, transparent)',
        color: 'var(--accent)',
      }}
    >
      <span aria-hidden>✉</span>
      {count} new report{count === 1 ? '' : 's'}
    </span>
  )
}

const ATTACHMENT_STATE: Record<AttachmentState, string> = {
  stored: 'var(--muted)',
  missing: 'var(--warn-text)',
  pending: 'var(--subtle)',
}

export function AttachmentStateLabel({ state }: { state: AttachmentState }) {
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 4,
        fontFamily: 'var(--mono)',
        fontSize: 10.5,
        fontWeight: 600,
        color: ATTACHMENT_STATE[state],
      }}
    >
      {state}
    </span>
  )
}

/** FilterChip is the one filter toggle in the app — the board's colours and the
 *  inbox's states are the same control with a different label. `aria-pressed`
 *  is not optional: these are toggles, and a screen reader has no other way to
 *  hear which one is on. */
export function FilterChip({
  label,
  count,
  dot,
  active,
  onClick,
}: {
  label: string
  count?: number
  dot?: string
  active: boolean
  onClick: () => void
}) {
  return (
    <button
      onClick={onClick}
      aria-pressed={active}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 7,
        height: 30,
        padding: '0 12px',
        borderRadius: 999,
        border: `1px solid ${active ? 'var(--border-strong)' : 'var(--border)'}`,
        background: active ? 'var(--s3)' : 'var(--s1)',
        color: 'var(--text)',
        fontSize: 12.5,
        fontWeight: 600,
        textTransform: 'capitalize',
        cursor: 'pointer',
        fontFamily: 'inherit',
      }}
    >
      {dot && <span aria-hidden style={{ height: 8, width: 8, borderRadius: '50%', background: dot }} />}
      {label}
      {count !== undefined && <span style={{ opacity: 0.6, fontVariantNumeric: 'tabular-nums' }}>{count}</span>}
    </button>
  )
}

/** StateBlock is the centred empty / error / nothing-matches treatment, shared by
 *  the board, the inbox and the report page so the three cannot drift. */
export function StateBlock({
  icon,
  title,
  body,
  danger,
  children,
}: {
  icon: string
  title: string
  body: ReactNode
  danger?: boolean
  children?: ReactNode
}) {
  return (
    <div style={{ display: 'grid', placeItems: 'center', minHeight: 380, textAlign: 'center' }}>
      <div style={{ maxWidth: 400 }}>
        <div
          style={{
            margin: '0 auto 16px',
            display: 'grid',
            placeItems: 'center',
            height: 56,
            width: 56,
            borderRadius: 14,
            fontSize: 24,
            background: danger ? 'var(--danger-soft)' : 'color-mix(in oklab, var(--accent) 14%, transparent)',
            color: danger ? 'var(--danger-text)' : 'var(--accent)',
          }}
        >
          {icon}
        </div>
        <div style={{ fontSize: 18, fontWeight: 700, marginBottom: 6 }}>{title}</div>
        <p style={{ margin: '0 0 18px', fontSize: 13.5, color: 'var(--muted)' }}>{body}</p>
        {children}
      </div>
    </div>
  )
}

export function Spinner({ size = 16 }: { size?: number }) {
  return (
    <span
      aria-hidden
      style={{
        height: size,
        width: size,
        border: '2px solid currentColor',
        borderRightColor: 'transparent',
        borderRadius: '50%',
        display: 'inline-block',
        animation: 'om-spin .7s linear infinite',
      }}
    />
  )
}

export const cardStyle: CSSProperties = {
  border: '1px solid var(--border)',
  background: 'var(--s1)',
  borderRadius: 'var(--radius)',
  padding: 18,
  boxShadow: 'var(--shadow)',
}

export const primaryButton: CSSProperties = {
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  gap: 7,
  height: 38,
  padding: '0 15px',
  border: '1px solid var(--accent)',
  background: 'var(--accent)',
  color: 'var(--accent-fg)',
  borderRadius: 8,
  fontSize: 13,
  fontWeight: 700,
  cursor: 'pointer',
}

export const ghostButton: CSSProperties = {
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  gap: 6,
  height: 36,
  padding: '0 13px',
  border: '1px solid var(--border)',
  background: 'var(--s2)',
  color: 'var(--text)',
  borderRadius: 8,
  fontSize: 13,
  fontWeight: 600,
  cursor: 'pointer',
}

export const inputStyle: CSSProperties = {
  height: 38,
  width: '100%',
  border: '1px solid var(--border)',
  background: 'var(--s2)',
  borderRadius: 8,
  padding: '0 12px',
  fontSize: 13.5,
  color: 'var(--text)',
  fontFamily: 'inherit',
  outline: 'none',
}

export function fieldLabel(text: string) {
  return <span style={{ display: 'block', marginBottom: 5, fontSize: 12.5, fontWeight: 600, color: 'var(--subtle)' }}>{text}</span>
}
