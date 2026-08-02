import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const srcDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), './src')

// The SPA is served same-origin with the API in production (Traefik path-routes
// /api to the backend). In dev, proxy /api to the Go backend (:112) so the fetch
// wrapper works without CORS.
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: { '@': srcDir },
  },
  server: {
    proxy: {
      '/api': { target: 'http://127.0.0.1:112', changeOrigin: true },
    },
  },
})
