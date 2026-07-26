import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The Go server embeds the build, so output goes straight into the spa package.
// In dev, `bun run dev` serves the SPA on :5173 and proxies the API to the
// running Go server (bring it up with `make up` or `DEV=true go run ./cmd/server`).
const backend = process.env.VITE_BACKEND || 'http://localhost:8080'
const api = ['/me', '/papers', '/jobs', '/reproductions', '/login', '/logout', '/callback', '/admin', '/healthz']

export default defineConfig({
  plugins: [react()],
  build: { outDir: '../internal/spa/dist', emptyOutDir: true },
  server: {
    port: 5173,
    proxy: Object.fromEntries(api.map((p) => [p, { target: backend, changeOrigin: true }])),
  },
})
