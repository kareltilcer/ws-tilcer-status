// Wire shapes for the three public widget routes, mirrored by hand from
// backend/openapi.yaml 0.3.0 (tag `widget`).
//
// ⚠ Deliberately NOT imported from src/api/types.ts. The widget is a separate
// artifact that shares no framework, stylesheet or token with the dashboard
// (V3-D55, PRD §V3-7); a type import would be erased at build time but would tie
// the two bundles' contracts together in the editor, and the widget only ever
// speaks to three of the twenty-two paths.

export type Lang = 'cs' | 'en'
export type Kind = 'bug' | 'idea' | 'other'
export type Position = 'bottom-right' | 'bottom-left' | 'top-right' | 'top-left'

/** WidgetConfig is GET /api/ingest/{siteId}/feedback/config. Everything but
 *  `enabled` is omitted when feedback is off — a disabled site's answer must not
 *  read like a configured one. */
export interface WidgetConfig {
  enabled: boolean
  ticket?: string
  kinds?: Kind[]
  max_files?: number
  max_image_bytes?: number
  max_video_bytes?: number
  accept?: string[]
  console_capture?: boolean
  strings_version?: number
}

/** DeclaredFile is what the client says it is about to upload. The size is
 *  clamped server-side to the cap and then SIGNED into the upload URL, so a
 *  declaration that does not match the file is refused by R2. */
export interface DeclaredFile {
  content_type: string
  byte_size: number
}

export interface Submission {
  message: string
  kind: Kind
  ticket: string
  reporter_label: string | null
  page_url: string | null
  referrer: string | null
  viewport: string | null
  locale: string | null
  app_release: string | null
  console_tail: string[] | null
  last_error: string | null
  /** The honeypot: a hidden field no human fills in. Non-empty is a 422. */
  website: string
  files: DeclaredFile[]
}

export interface UploadSlot {
  attachment_id: number
  url: string
  expires_at: string
  headers: Record<string, string>
}

/** Accepted is the 202 body. ⚠ Upload URLs exist only here — there is no
 *  standalone "give me an upload URL" endpoint (V3-D07). */
export interface Accepted {
  ref: string
  uploads: UploadSlot[]
}
