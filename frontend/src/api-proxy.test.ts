import { expect, test } from 'bun:test'

// The dev server proxies the API to the Go backend by path prefix, from a list
// written by hand. It went stale: '/projects', '/resolve', '/public', '/shared'
// and '/profiles' were all added to the client after the list was written, so in
// `bun run dev` every one of them hit Vite's own 404 instead of the backend.
//
// This reads both files rather than trusting either: every path api.ts fetches
// has to be covered by a prefix vite.config.ts proxies.
test('the dev server proxies every path the client calls', () => {
  const client = Bun.file(`${import.meta.dir}/api.ts`).text()
  const config = Bun.file(`${import.meta.dir}/../vite.config.ts`).text()

  return Promise.all([client, config]).then(([api, vite]) => {
    // Both quoted and template-literal forms: fetch('/me'), fetch(`/jobs/${id}`).
    const called = [...api.matchAll(/fetch\([`'](\/[A-Za-z0-9/_-]*)/g)].map((m) => m[1])
    expect(called.length).toBeGreaterThan(10)

    // The list, however it happens to be wrapped — one line or many.
    const block = /const api = \[([^\]]*)\]/s.exec(vite)
    const prefixes = [...(block?.[1] ?? '').matchAll(/'(\/[a-z]+)'/g)].map((m) => m[1])
    expect(prefixes.length).toBeGreaterThan(5)

    const uncovered = [...new Set(called)].filter((p) => !prefixes.some((pre) => p === pre || p.startsWith(pre + '/')))
    expect(uncovered).toEqual([])
  })
})
