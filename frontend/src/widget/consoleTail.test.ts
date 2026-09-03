import { beforeEach, describe, expect, it, vi } from 'vitest'
import { formatArg, trimToBudget, utf8Length } from './consoleTail'

describe('utf8Length', () => {
  it('counts wire bytes, not UTF-16 units', () => {
    expect(utf8Length('abc')).toBe(3)
    expect(utf8Length('Když')).toBe(5) // three ASCII plus a two-byte ž
    expect(utf8Length('→')).toBe(3)
    expect(utf8Length('🙂')).toBe(4)
  })
})

describe('trimToBudget', () => {
  it('drops the OLDEST lines, because the newest are nearest the failure', () => {
    const lines = ['one', 'two', 'three']
    expect(trimToBudget(lines, 1000)).toEqual(lines)
    // 'three' costs 5+3, 'two' costs 3+3 => 14 bytes fits, 'one' does not.
    expect(trimToBudget(lines, 14)).toEqual(['two', 'three'])
    expect(trimToBudget(lines, 1)).toEqual([])
  })
})

describe('formatArg', () => {
  it('never throws on what a host app logs', () => {
    const circular: Record<string, unknown> = {}
    circular.self = circular
    expect(formatArg(circular)).toBe('[object Object]')
    expect(formatArg(new Error('boom'))).toBe('Error: boom')
    expect(formatArg(undefined)).toBe('undefined')
    expect(formatArg({ a: 1 })).toBe('{"a":1}')
  })
})

describe('installConsoleCapture', () => {
  beforeEach(() => {
    vi.resetModules()
  })

  // ⚠ installConsoleCapture patches five methods, so the teardown restores five.
  // Restoring only `log` leaves this file's later tests running through the
  // first module instance's buffer — a stale capture chain that a future
  // assertion on console.warn would read from.
  const PATCHED = ['log', 'info', 'warn', 'error', 'debug'] as const

  function snapshotConsole(): () => void {
    const saved = PATCHED.map((m) => [m, console[m]] as const)
    return () => {
      for (const [m, fn] of saved) console[m] = fn as typeof console.log
    }
  }

  it('keeps the last 50 lines, caps each at 200 characters, and still logs', async () => {
    const { installConsoleCapture } = await import('./consoleTail')
    const restore = snapshotConsole()
    const seen: unknown[][] = []
    console.log = (...args: unknown[]) => {
      seen.push(args)
    }
    try {
      const capture = installConsoleCapture()
      for (let i = 0; i < 60; i++) console.log(`line ${i}`)
      console.log('x'.repeat(500))
      const lines = capture.lines()
      expect(lines.length).toBeLessThanOrEqual(50)
      expect(lines).not.toContain('line 0')
      expect(lines[lines.length - 1]).toHaveLength(200)
      // ⚠ The host's own logging must behave identically whether or not the
      // widget is on the page.
      expect(seen).toHaveLength(61)
    } finally {
      restore()
    }
  })

  it('is installed once, however many times it is asked for', async () => {
    const { installConsoleCapture } = await import('./consoleTail')
    const restore = snapshotConsole()
    try {
      expect(installConsoleCapture()).toBe(installConsoleCapture())
    } finally {
      restore()
    }
  })
})
