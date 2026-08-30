// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The Go server embeds the build, so output goes straight into the spa package.
// In dev, `bun run dev` serves the SPA on :5173 and proxies the API to the
// running Go server (bring it up with `make up` or `DEV=true go run ./cmd/server`).
const backend = process.env.VITE_BACKEND || 'http://localhost:8080'

// Every path prefix the API owns. A prefix missing here is not a small bug: the
// dev server answers that call with its own HTML 404, the client cannot parse it
// as JSON, and the screen shows whatever it says when it has no reason to give —
// a fault that exists only in dev and looks exactly like a backend refusal.
// api-proxy.test.ts reads the paths out of api.ts and fails if one is uncovered.
//
// The browser routes the app owns are '/', '/p/:id' and '/u/:handle'; none of
// them collide with these, which is why '/projects' and '/profiles' can be
// proxied wholesale.
const api = [
  '/me',
  '/papers',
  '/jobs',
  '/models',
  '/projects',
  '/profiles',
  '/public',
  '/shared',
  '/resolve',
  '/reproductions',
  '/login',
  '/logout',
  '/callback',
  '/admin',
  '/healthz',
]

export default defineConfig({
  plugins: [react()],
  build: { outDir: '../internal/spa/dist', emptyOutDir: true },
  server: {
    port: 5173,
    proxy: Object.fromEntries(api.map((p) => [p, { target: backend, changeOrigin: true }])),
  },
})
