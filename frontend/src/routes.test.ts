import { expect, test } from 'bun:test'
import { pathOf, viewOf, type View } from './routes'

// Every screen, with the address it lives at. This table is the spec's route
// table; if a screen is added without a URL, the round-trip test below is where
// it shows up.
const table: [string, View][] = [
  ['/', { name: 'projects' }],
  ['/registry', { name: 'public' }],
  ['/trash', { name: 'trash' }],
  ['/new', { name: 'submit' }],
  ['/settings', { name: 'settings' }],
  ['/settings/profile', { name: 'settings', tab: 'profile' }],
  ['/settings/credits', { name: 'settings', tab: 'credits' }],
  ['/console', { name: 'admin' }],
  ['/u/ada-lovelace', { name: 'profile', handle: 'ada-lovelace' }],
  ['/p/4fbff44c3323f724ea8c9ed5d5ecdbca', { name: 'project', id: '4fbff44c3323f724ea8c9ed5d5ecdbca' }],
  ['/p/abc/verdict', { name: 'project', id: 'abc', tab: 'verdict' }],
  ['/p/abc/people', { name: 'project', id: 'abc', tab: 'people' }],
  ['/p/abc/s/job-1', { name: 'session', projectId: 'abc', jobId: 'job-1' }],
  ['/p/abc/s/job-1/verdict', { name: 'session', projectId: 'abc', jobId: 'job-1', tab: 'verdict' }],
  ['/p/abc/s/job-1/artifacts', { name: 'session', projectId: 'abc', jobId: 'job-1', tab: 'artifacts' }],
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

// The same rule one level down: a session opens on its transcript, and the links
// to one already sent point at the bare path.
test('the transcript tab is the session path itself', () => {
  expect(pathOf({ name: 'session', projectId: 'abc', jobId: 'j1', tab: 'transcript' })).toBe('/p/abc/s/j1')
  expect(viewOf('/p/abc/s/j1')).toEqual({ name: 'session', projectId: 'abc', jobId: 'j1' })
})

// The account tab is settings' own bare path, and a segment that names no tab is
// dropped to it — the same rule a project's unknown tab follows (024a).
test('the account tab is the settings path itself, and an unknown tab drops to it', () => {
  expect(pathOf({ name: 'settings', tab: 'account' })).toBe('/settings')
  expect(viewOf('/settings')).toEqual({ name: 'settings' })
  expect(viewOf('/settings/nonsense')).toEqual({ name: 'settings' })
})

// A project has no repository of its own to browse — there are as many as it has
// published sessions — so the tab that had to pick one is gone (015). The
// address falls back to the project, as any unrecognised segment does.
test('the retired project artifacts address is just the project', () => {
  expect(viewOf('/p/abc/artifacts')).toEqual({ name: 'project', id: 'abc' })
})

// A segment that names no session tab keeps the reader on the session, the same
// way a bad project tab keeps them on the project.
test('a session survives a segment that names no tab', () => {
  for (const path of ['/p/abc/s/j1/nonsense', '/p/abc/s/j1/sessions']) {
    expect(viewOf(path)).toEqual({ name: 'session', projectId: 'abc', jobId: 'j1' })
  }
})

// A segment that names no tab is still a project: a mistyped tab, or a session
// link cut short, keeps the reader on the project they were addressing rather
// than sending them back to the dashboard. The caller corrects the address.
test('a project survives a segment that names no tab', () => {
  for (const path of ['/p/abc/nonsense', '/p/abc/extra', '/p/abc/s']) {
    expect(viewOf(path)).toEqual({ name: 'project', id: 'abc' })
  }
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
    '/p/abc/s/j/one/too/many',
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
