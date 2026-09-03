import type { WidgetConfig } from './types'

/** Limits is the file half of the widget configuration, resolved once. */
export interface Limits {
  maxFiles: number
  maxImageBytes: number
  maxVideoBytes: number
  accept: string[]
}

export type Rejection =
  | { reason: 'type' }
  | { reason: 'size'; limit: number; video: boolean }
  | { reason: 'count' }

/** limitsFrom reads the caps out of a config response, with the documented
 *  defaults for a server that omitted them. */
export function limitsFrom(cfg: WidgetConfig): Limits {
  return {
    maxFiles: cfg.max_files ?? 3,
    maxImageBytes: cfg.max_image_bytes ?? 10 * 1024 * 1024,
    maxVideoBytes: cfg.max_video_bytes ?? 50 * 1024 * 1024,
    accept: cfg.accept ?? ['image/png', 'image/jpeg', 'image/webp', 'image/gif', 'video/mp4', 'video/webm'],
  }
}

/** isVideo classifies by the declared content type, never by the filename — the
 *  extension in the object key is derived from the type server-side (V3-D09) and
 *  the two must agree about which cap applies. */
export function isVideo(contentType: string): boolean {
  return contentType.toLowerCase().startsWith('video/')
}

export function limitFor(contentType: string, limits: Limits): number {
  return isVideo(contentType) ? limits.maxVideoBytes : limits.maxImageBytes
}

/**
 * validateFile decides whether a picked file may be attached.
 *
 * ⚠ The size check is not politeness, it is correctness. The server signs the
 * upload URL for the size the widget DECLARES, clamped to the cap — so a file
 * over the cap would be signed at the cap and then refused by R2 with a
 * signature mismatch the reporter would never see explained. Refusing here, with
 * the file named and the limit stated, is the only place that failure can be
 * made legible.
 */
export function validateFile(
  file: { type: string; size: number },
  limits: Limits,
  alreadyAttached: number,
): Rejection | null {
  if (alreadyAttached >= limits.maxFiles) return { reason: 'count' }
  const type = file.type.toLowerCase().split(';')[0].trim()
  if (!limits.accept.includes(type)) return { reason: 'type' }
  const video = isVideo(type)
  const limit = video ? limits.maxVideoBytes : limits.maxImageBytes
  if (file.size > limit) return { reason: 'size', limit, video }
  if (file.size <= 0) return { reason: 'type' }
  return null
}
