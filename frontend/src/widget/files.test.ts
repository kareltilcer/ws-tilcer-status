import { describe, expect, it } from 'vitest'
import { limitsFrom, validateFile } from './files'
import { formatBytes, browserLabel } from './format'

const limits = limitsFrom({
  enabled: true,
  max_files: 3,
  max_image_bytes: 10 * 1024 * 1024,
  max_video_bytes: 50 * 1024 * 1024,
  accept: ['image/png', 'image/jpeg', 'video/mp4'],
})

describe('validateFile', () => {
  it('accepts an allow-listed type within its cap', () => {
    expect(validateFile({ type: 'image/png', size: 420 * 1024 }, limits, 0)).toBeNull()
    expect(validateFile({ type: 'video/mp4', size: 40 * 1024 * 1024 }, limits, 2)).toBeNull()
  })

  it('refuses a type outside the allow-list', () => {
    expect(validateFile({ type: 'application/pdf', size: 1000 }, limits, 0)).toEqual({ reason: 'type' })
    // A file the browser could not type at all is refused the same way — the
    // server matches the declared type against its own list and would 422.
    expect(validateFile({ type: '', size: 1000 }, limits, 0)).toEqual({ reason: 'type' })
  })

  it('applies the video cap to video and the image cap to images', () => {
    expect(validateFile({ type: 'image/png', size: 11 * 1024 * 1024 }, limits, 0)).toEqual({
      reason: 'size',
      limit: 10 * 1024 * 1024,
      video: false,
    })
    expect(validateFile({ type: 'video/mp4', size: 11 * 1024 * 1024 }, limits, 0)).toBeNull()
    expect(validateFile({ type: 'video/mp4', size: 51 * 1024 * 1024 }, limits, 0)).toEqual({
      reason: 'size',
      limit: 50 * 1024 * 1024,
      video: true,
    })
  })

  it('refuses a fourth file', () => {
    expect(validateFile({ type: 'image/png', size: 10 }, limits, 3)).toEqual({ reason: 'count' })
  })
})

describe('formatBytes', () => {
  it('uses the Czech decimal comma and whole numbers for the caps', () => {
    expect(formatBytes(10 * 1024 * 1024, 'cs')).toBe('10 MB')
    expect(formatBytes(18.4 * 1024 * 1024, 'cs')).toBe('18,4 MB')
    expect(formatBytes(18.4 * 1024 * 1024, 'en')).toBe('18.4 MB')
    expect(formatBytes(420 * 1024, 'cs')).toBe('420 kB')
  })
})

describe('browserLabel', () => {
  it('names what it recognises and falls back to the raw user agent', () => {
    expect(browserLabel('Mozilla/5.0 (iPhone; CPU iPhone OS 19_2) AppleWebKit/620.1 Version/26.0 Safari/620.1')).toBe(
      'Safari · iPhone',
    )
    expect(browserLabel('Mozilla/5.0 (Windows NT 10.0) Chrome/141.0 Safari/537.36')).toBe('Chrome · Windows')
    // ⚠ The disclosure's job is to show what is actually sent, so an
    // unrecognised agent is shown as itself rather than hidden behind "Unknown".
    expect(browserLabel('Weird/1.0')).toBe('Weird/1.0')
  })
})
