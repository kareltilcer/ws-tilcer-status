import type { CSSProperties } from 'react'
import type { Color, Level } from '@/api/types'

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
