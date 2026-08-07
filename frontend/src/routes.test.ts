import { expect, test } from 'bun:test'
import { pathOf, viewOf, type View } from './routes'

// Every screen, with the address it lives at. This table is the spec's route
// table; if a screen is added without a URL, the round-trip test below is where
// it shows up.
const table: [string, View][] = [
  ['/', { name: 'projects' }],
  ['/registry', { name: 'public' }],
  ['/new', { name: 'submit' }],
  ['/settings', { name: 'settings' }],
  ['/console', { name: 'admin' }],
  ['/u/ada-lovelace', { name: 'profile', handle: 'ada-lovelace' }],
  ['/p/4fbff44c3323f724ea8c9ed5d5ecdbca', { name: 'project', id: '4fbff44c3323f724ea8c9ed5d5ecdbca' }],
  ['/p/abc/verdict', { name: 'project', id: 'abc', tab: 'verdict' }],
  ['/p/abc/artifacts', { name: 'project', id: 'abc', tab: 'artifacts' }],
  ['/p/abc/people', { name: 'project', id: 'abc', tab: 'people' }],
  ['/p/abc/s/job-1', { name: 'session', projectId: 'abc', jobId: 'job-1' }],
]

test('every address resolves to its screen', () => {
  for (const [path, view] of table) expect(viewOf(path)).toEqual(view)
})

test('every screen writes the address it is read from', () => {
  for (const [path, view] of table) expect(pathOf(view)).toBe(path)
})

// The sessions tab is the project's own path: a project link somebody shares is
// the same one an invitation mail carries, not a longer synonym for it.
test('the sessions tab is the project path itself', () => {
  expect(pathOf({ name: 'project', id: 'abc', tab: 'sessions' })).toBe('/p/abc')
  expect(viewOf('/p/abc')).toEqual({ name: 'project', id: 'abc' })
})

// Display hints are not identity: a title in a URL is a copy of a record that
// can change, so the path is the same with or without one.
test('display hints never reach the address', () => {
  expect(pathOf({ name: 'project', id: 'abc', title: 'Walk This Beam' })).toBe('/p/abc')
  expect(pathOf({ name: 'session', projectId: 'abc', jobId: 'j1', ref: 'doi:10.1145/x', projectTitle: 'W' })).toBe('/p/abc/s/j1')
})

test('a trailing slash is the same address', () => {
  expect(viewOf('/registry/')).toEqual({ name: 'public' })
  expect(viewOf('/p/abc/')).toEqual({ name: 'project', id: 'abc' })
  expect(viewOf('/u/ada/')).toEqual({ name: 'profile', handle: 'ada' })
})

// A handle is stored and compared lowercased, and a browser hands it back
// percent-encoded (006r).
test('a handle is normalised the way the profile stores it', () => {
  expect(viewOf('/u/Ada')).toEqual({ name: 'profile', handle: 'ada' })
  expect(viewOf('/u/' + encodeURIComponent('ada-lovelace'))).toEqual({ name: 'profile', handle: 'ada-lovelace' })
})

// Anything else is the dashboard, and the app corrects the address. Note the API
// paths in this list: reaching them in a browser is answered by the server, never
// by this mapping, so the mapping must not pretend to own them.
test('an address we do not own is the dashboard', () => {
  const strays = [
    '/nonsense',
    '/p',
    '/p/',
    '/u',
    '/u/',
    '/projects/abc',
    '/public',
    '/admin',
    '/me',
    '/p/abc/nonsense',
    '/p/abc/extra',
    '/p/abc/s',
    '/p/abc/s/j/extra',
    // A false positive here would render a stranger's page instead of the
    // workspace, which is why the profile pattern claims one segment only (006r).
    '/u/ada/extra',
    '/users/ada',
  ]
  for (const path of strays) {
    expect(viewOf(path)).toEqual({ name: 'projects' })
  }
})

// The reverse of api-proxy.test.ts: that one asserts the dev server proxies every
// path the client calls, this one asserts none of those prefixes shadows a screen.
// A collision is invisible in production too — Go's mux matches the API first, so
// the screen would answer with JSON.
test('no screen is shadowed by an API prefix', async () => {
  const vite = await Bun.file(`${import.meta.dir}/../vite.config.ts`).text()
  const block = /const api = \[([^\]]*)\]/s.exec(vite)
  const prefixes = [...(block?.[1] ?? '').matchAll(/'(\/[a-z]+)'/g)].map((m) => m[1])
  expect(prefixes.length).toBeGreaterThan(5)

  const paths = table.map(([p]) => p)
  const shadowed = paths.filter((p) => prefixes.some((pre) => p === pre || p.startsWith(pre + '/')))
  expect(shadowed).toEqual([])
})
