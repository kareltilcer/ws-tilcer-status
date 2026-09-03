import type { WidgetConfig } from './types'

/** Limits is the file half of the widget configuration, resolved once. */
export interface Limits {
  maxFiles: number
  maxImageBytes: number
  maxVideoBytes: number
  /** maxTextBytes is the server's cap on the whole submission body. ⚠ Read from
   *  the config, not mirrored as a constant: a deployment that lowers
   *  STATUS_FEEDBACK_MAX_TEXT_BYTES would otherwise 413 reports the widget
   *  trimmed against the default and believed were within budget. */
  maxTextBytes: number
  accept: string[]
}

export type Rejection =
  | { reason: 'type' }
  | { reason: 'size'; limit: number; video: boolean }
  | { reason: 'count' }
  /** empty is its own reason, not a type problem: telling someone whose file
   *  reads as 0 bytes to "attach an image instead" names the one thing they did
   *  do. The server's own wording for it is `byte_size must be at least 1`. */
  | { reason: 'empty' }

/** limitsFrom reads the caps out of a config response, with the documented
 *  defaults for a server that omitted them. */
export function limitsFrom(cfg: WidgetConfig): Limits {
  return {
    maxFiles: cfg.max_files ?? 3,
    maxImageBytes: cfg.max_image_bytes ?? 10 * 1024 * 1024,
    maxVideoBytes: cfg.max_video_bytes ?? 50 * 1024 * 1024,
    maxTextBytes: cfg.max_text_bytes ?? 8192,
    accept: cfg.accept ?? ['image/png', 'image/jpeg', 'image/webp', 'image/gif', 'video/mp4', 'video/webm'],
  }
}

/**
 * normalizeType is the one reading of a `File.type`.
 *
 * ⚠ A browser may report `image/PNG` or `image/png; charset=binary`. The server
 * matches case-insensitively and strips parameters, so validating the normalized
 * form and then DECLARING the raw one means the value checked here is not the
 * value sent — and the cap, the icon and the allow-list would each have to
 * repeat the rule to agree with it. Normalize once, at the picker, and carry the
 * result.
 */
export function normalizeType(contentType: string): string {
  return contentType.toLowerCase().split(';')[0].trim()
}

/** isVideo classifies by the declared content type, never by the filename — the
 *  extension in the object key is derived from the type server-side (V3-D09) and
 *  the two must agree about which cap applies. */
export function isVideo(contentType: string): boolean {
  return contentType.toLowerCase().startsWith('video/')
}

/** limitFor is the one place the video/image cap is chosen. It has to stay in
 *  step with the server's `resolveFiles`, which is easier to see when there is a
 *  single copy of the rule. */
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
  const type = normalizeType(file.type)
  if (!limits.accept.includes(type)) return { reason: 'type' }
  // Emptiness before the cap: a 0-byte file is neither too large nor the wrong
  // type, and it happens for real — a screenshot still being written, a file on a
  // disconnected share, a cloud placeholder the OS never hydrated.
  if (file.size <= 0) return { reason: 'empty' }
  const limit = limitFor(type, limits)
  if (file.size > limit) return { reason: 'size', limit, video: isVideo(type) }
  return null
}
