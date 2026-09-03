import { defineConfig } from 'vite'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.dirname(fileURLToPath(import.meta.url))

// The widget is a SECOND build, not a second entry in the SPA build (FR-24).
// The SPA emits hashed filenames so it can be cached for a year; the widget needs
// one fixed, guessable name that a host page can hardcode — `/widget/v1.js`,
// immutable for the life of the v1 contract. A breaking change becomes v2.js and
// every existing embed keeps working.
export default defineConfig({
  build: {
    outDir: 'dist/widget',
    // ⚠ The SPA build runs first and writes the same dist/. Emptying here would
    // delete it.
    emptyOutDir: false,
    // Old enough for the phones this is actually used on, since the bundle ships
    // no polyfills and must not throw on parse in the host page.
    target: 'es2019',
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
