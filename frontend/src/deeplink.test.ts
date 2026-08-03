import { expect, test } from 'bun:test'
import { projectFromPath } from './App'

// The invitation mail links straight at a project. It must land on a path the
// app owns: /projects/{id} is the gated JSON API, so a browser sent there gets
// {"error":"not_authenticated"} — which is exactly what the first invitations
// did.
test('projectFromPath reads a project deep link', () => {
  expect(projectFromPath('/p/4fbff44c3323f724ea8c9ed5d5ecdbca')).toBe('4fbff44c3323f724ea8c9ed5d5ecdbca')
  expect(projectFromPath('/p/abc123/')).toBe('abc123')
  expect(projectFromPath('/p/a-b_c')).toBe('a-b_c')
})

// Nothing else is a project link — least of all the API path it replaced.
test('projectFromPath claims nothing the app does not own', () => {
  for (const path of ['/', '/p', '/p/', '/projects/abc', '/p/abc/extra', '/u/ada', '/settings']) {
    expect(projectFromPath(path)).toBe('')
  }
})
