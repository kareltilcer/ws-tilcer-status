// The copy-pasteable snippets the dashboard hands out.
//
// One origin constant for all of them: the SPA itself talks to the API
// host-relative, so nothing else in the frontend knows its own public URL, and a
// snippet is the one place that has to name it out loud.
export const STATUS_ORIGIN = 'https://status.tilcer.cz'

/**
 * curlIngestSnippet is the crash-ingest one-liner.
 *
 * `ingestKey` is the plaintext `ik_` value beside a freshly issued key and the
 * `$STATUS_INGEST_KEY` shell variable on the site page, which is the only thing
 * that differs between the two places it is shown — so it is the only thing the
 * caller passes. The endpoint, the header name and the body shape live here
 * once.
 */
export function curlIngestSnippet(siteId: string, ingestKey: string): string {
  return [
    `curl -sS -X POST ${STATUS_ORIGIN}/api/ingest/${siteId} \\`,
    `  -H "X-Ingest-Key: ${ingestKey}" \\`,
    `  -H "Content-Type: application/json" \\`,
    `  -d '{"message":"hello from curl","level":"error"}'`,
  ].join('\n')
}

/**
 * widgetEmbedSnippet is the `<script>` tag a monitored app pastes in.
 *
 * `widgetKey` is the plaintext `wk_` value when it has just been issued and a
 * placeholder everywhere else — the key is stored only as a SHA-256, so the
 * dashboard genuinely cannot fill it in after the one time it is shown.
 *
 * Only the required attributes are emitted. `data-reporter`, `data-release`,
 * `data-position` and `data-launcher` are real parts of the v1 contract but
 * carry per-app values, and a snippet that has to be edited before it works is
 * a snippet that gets pasted unedited — docs/widget.md documents them instead.
 */
export function widgetEmbedSnippet(siteId: string, widgetKey = 'wk_…'): string {
  return [
    `<script src="${STATUS_ORIGIN}/widget/v1.js"`,
    `        data-site="${siteId}"`,
    `        data-key="${widgetKey}"`,
    `        data-lang="cs"`,
    `        defer></script>`,
  ].join('\n')
}
