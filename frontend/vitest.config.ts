import { defineConfig } from 'vitest/config'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const srcDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), './src')

// The widget's tests. The dashboard has none — it is the same React the fleet has
// always shipped untested — but the widget is a distributable artifact that runs
// on other people's pages, and half of PRD §V3-11's widget criteria (no launcher
// when disabled, focus trap, Escape, Czech by default, the console opt-out) are
// only assertable in a DOM.
export default defineConfig({
  resolve: { alias: { '@': srcDir } },
  test: {
    environment: 'jsdom',
    include: ['src/widget/**/*.test.ts'],
  },
})
