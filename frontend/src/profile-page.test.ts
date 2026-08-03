import { expect, test } from 'bun:test'
import { publicHandleFromPath } from './App'

// 006r: a profile page is not internal navigation. It is a link on a published
// attestation that a stranger follows from somewhere else, so it has to be a
// real URL that works on a cold load, before any auth gate.
test('publicHandleFromPath recognises a researcher page', () => {
  expect(publicHandleFromPath('/u/ada')).toBe('ada')
  expect(publicHandleFromPath('/u/ada-lovelace')).toBe('ada-lovelace')
  // A trailing slash is the same page.
  expect(publicHandleFromPath('/u/ada/')).toBe('ada')
  // Handles are lowercase, so a shared link in the wrong case still resolves.
  expect(publicHandleFromPath('/u/Ada')).toBe('ada')
  expect(publicHandleFromPath('/u/' + encodeURIComponent('ada'))).toBe('ada')
})

// Everything else is the app, and must not be mistaken for a profile — a false
// positive here would render a stranger's page instead of the workspace.
test('publicHandleFromPath leaves every other path to the app', () => {
  for (const path of ['/', '/projects', '/u', '/u/', '/u/ada/extra', '/users/ada', '/settings']) {
    expect(publicHandleFromPath(path)).toBe('')
  }
})
