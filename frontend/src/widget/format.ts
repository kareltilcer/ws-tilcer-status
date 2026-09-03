import type { Lang } from './types'

const KIB = 1024
const MIB = 1024 * 1024

/** formatBytes renders a file size the way the reporter's phone does: "420 kB",
 *  "18,4 MB". Czech uses a comma as the decimal separator.
 *
 *  ⚠ The base is 1024, matching how the server derives its caps
 *  (`STATUS_FEEDBACK_MAX_IMAGE_MB * 1024 * 1024`) — so a 10 MB cap prints as
 *  "10 MB" here rather than as "10.5 MB", and the number the reporter is told
 *  is the number that was enforced. */
export function formatBytes(bytes: number, lang: Lang): string {
  if (bytes < MIB) {
    return `${Math.max(1, Math.round(bytes / KIB))} kB`
  }
  const mb = bytes / MIB
  // Whole megabytes print without a decimal: the caps are whole numbers and
  // "10,0 MB" reads like a measurement rather than a limit.
  const text = Number.isInteger(mb) ? String(mb) : mb.toFixed(1)
  return `${lang === 'cs' ? text.replace('.', ',') : text} MB`
}

/** viewportText is the `viewport` field: the layout viewport, not the screen. */
export function viewportText(w: number, h: number): string {
  return `${Math.round(w)} × ${Math.round(h)}`
}

/** browserLabel names the browser for the disclosure block — "Safari · iPhone"
 *  rather than 130 characters of user-agent string.
 *
 *  ⚠ It is display only, and it falls back to the raw user agent rather than to
 *  "Unknown": the disclosure's whole job is to show the reporter what is actually
 *  being sent, and the user agent IS sent (as the request header the server
 *  reads). A label that hid an unrecognised one would be the one place in this
 *  widget where the honest thing lost to the tidy thing. */
export function browserLabel(ua: string): string {
  const name = /Firefox\/\d/.test(ua)
    ? 'Firefox'
    : /Edg\/\d/.test(ua)
      ? 'Edge'
      : /OPR\/\d/.test(ua)
        ? 'Opera'
        : /Chrome\/\d/.test(ua)
          ? 'Chrome'
          : /Safari\/\d/.test(ua) && /Version\/\d/.test(ua)
            ? 'Safari'
            : null
  if (!name) return ua
  const platform = /iPhone/.test(ua)
    ? 'iPhone'
    : /iPad/.test(ua)
      ? 'iPad'
      : /Android/.test(ua)
        ? 'Android'
        : /Macintosh/.test(ua)
          ? 'Mac'
          : /Windows/.test(ua)
            ? 'Windows'
            : /Linux/.test(ua)
              ? 'Linux'
              : null
  return platform ? `${name} · ${platform}` : name
}
