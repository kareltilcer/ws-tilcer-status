import { defineConfig, type Plugin } from 'vite'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.dirname(fileURLToPath(import.meta.url))

/**
 * asciiOnly rewrites every non-ASCII character in the bundle as a `\uXXXX`
 * escape, and refuses to emit a file that still contains one.
 *
 * ⚠ This is not a style preference, it is a correctness fix, and it was found by
 * opening the page. The bundle is a classic `<script>` loaded CROSS-ORIGIN by a
 * host we do not control. When the response carries no `charset`, the browser
 * does NOT inherit the host document's UTF-8 for a cross-origin classic script —
 * it falls back to windows-1252, and every Czech string in the file becomes
 * mojibake. "Odešle se také" renders as "OdeÅ¡le se takÃ©"; worse, the widget then
 * SENDS the corrupted text, so the report that lands in Karel's inbox is corrupt
 * too, with nothing anywhere to say why. Measured against a real cross-origin
 * host page: a report filed from one arrived reading "KdyÅ¾ v
 * NÃ¡kupech".
 *
 * `frontend/nginx.widget.conf` now sends `charset=utf-8`, which fixes it at the
 * server. This fixes it in the artifact — the half that survives a CDN, a proxy,
 * or a host that copies the file somewhere else and serves it however it likes.
 *
 * Asking the code generator instead (`esbuild: { charset: 'ascii' }`) is not
 * available: Vite owns that option and omits it from its own `ESBuildOptions`.
 * Rewriting the emitted chunk is safe because `\uXXXX` means the same character
 * in every context a non-ASCII one can legally appear in — string, template,
 * regex, identifier — and a surrogate pair escapes to the two halves that
 * compose it.
 *
 * ⚠ It also invalidates the `gzip:` figure Vite prints for this chunk — and only
 * that one, which is what makes the line hard to distrust. The size before the
 * `│` is the file this hook wrote; the gzip figure beside it is measured on the
 * code as it stood BEFORE the escapes, which gzips about 90 bytes WORSE (`\uXXXX`
 * runs compress better than the UTF-8 they replace). Read straight, the console
 * says a bundle that is 14 9xx gzipped is over §V3-8's 15 kB. The budget is about
 * the artifact, so measure the artifact:
 *
 *   node -e "const z=require('zlib'),f=require('fs');console.log(z.gzipSync(f.readFileSync('dist/widget/v1.js')).length)"
 *
 * ⚠ The post-condition reads the file back OFF DISK, in `writeBundle`. Re-testing
 * the same regex on the string the global replace above has just produced cannot
 * fail — it is the replace's own output — and would have proved nothing about the
 * artifact while reading as though it did. What can actually go wrong is a
 * mutation of `file.code` that the writer ignores, or bytes that never passed
 * through this hook at all: an asset rather than a chunk, a legacy or polyfill
 * chunk, a plugin ordered after this one. The file on disk is the thing the host
 * page downloads, so the file on disk is the thing that gets checked.
 */
function asciiOnly(): Plugin {
  return {
    name: 'sfb-ascii-only',
    generateBundle(_options, bundle) {
      for (const file of Object.values(bundle)) {
        if (file.type !== 'chunk') continue
        file.code = file.code.replace(/[^\x00-\x7F]/g, (c) => `\\u${c.charCodeAt(0).toString(16).padStart(4, '0')}`)
      }
    },
    writeBundle(options, bundle) {
      const dir = options.dir ?? path.dirname(options.file ?? '')
      // ⚠ Every emitted file, not only the `.js` ones. The escape above runs on
      // chunks; a filter here would have made the two halves cover different
      // sets, so exactly the output that escaping skipped — an asset, a
      // stylesheet, a chunk from a plugin ordered after this one — would also be
      // the output nothing checked, and it would ship with raw UTF-8 bytes on a
      // green build. The widget emits one file today; the guard is for the day
      // it does not.
      for (const name of Object.keys(bundle)) {
        const written = readFileSync(path.join(dir, name))
        const offset = written.findIndex((b) => b > 0x7f)
        if (offset >= 0) {
          throw new Error(
            `sfb-ascii-only: ${name} was written with a non-ASCII byte at offset ${offset} — a cross-origin ` +
              'classic script served without a charset would decode it as windows-1252',
          )
        }
      }
    },
  }
}

// The widget is a SECOND build, not a second entry in the SPA build (FR-24).
// The SPA emits hashed filenames so it can be cached for a year; the widget needs
// one fixed, guessable name that a host page can hardcode — `/widget/v1.js`,
// cached for a year and immutable, because its contract is frozen for the life of
// v1. A breaking change becomes v2.js and every existing embed keeps working.
//
// ⚠ The filename is unhashed, so `immutable` also means a non-breaking fix cannot
// reach a browser that already has this file: it ships as v2.js instead. That
// trade is FR-24's and §V3-11 checks for it; frontend/nginx.widget.conf carries
// the note.
export default defineConfig({
  plugins: [asciiOnly()],
  build: {
    outDir: 'dist/widget',
    // ⚠ The SPA build runs first and writes the same dist/. Emptying here would
    // delete it.
    emptyOutDir: false,
    // ⚠ And public/ is the SPA's, not the widget's. Without this Vite copies all
    // of it into dist/widget/ — today one stray favicon.svg published under a
    // path docs/widget.md describes as holding exactly two files, and silently
    // whatever is added to public/ next.
    copyPublicDir: false,
    // Old enough for the phones this is actually used on, since the bundle ships
    // no polyfills and a parse error means no widget at all on that device.
    // es2020 is Safari 13.1 / Chrome 80 / Firefox 72 — March 2020, and every
    // iPhone that can run iOS 13.
    //
    // ⚠ It is also a size lever, and the size target is tight (§V3-8: under
    // 15 kB gzipped). Measured on this bundle: es2019 14 975 · **es2020 14 754** ·
    // es2022 14 424. Down-levelling `?.` and `??` for es2019 cost 221 bytes for
    // browsers older than the oldest one anybody in this household holds. es2022
    // would buy another 330 and is the next lever if the budget needs it — the
    // cost there is Safari 15.4, March 2022, which is a real if unlikely phone.
    target: 'es2020',
    lib: {
      entry: path.resolve(root, 'src/widget/main.ts'),
      formats: ['iife'],
      // main.ts exports nothing, so this names the IIFE without defining a global:
      // the widget's only public surface is window.StatusFeedback (V3-D56).
      name: 'StatusFeedbackWidget',
      fileName: () => 'v1.js',
    },
  },
})
