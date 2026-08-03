import { expect, test } from 'bun:test'
import { publishErrText } from './App'

const t = ((key: string) => key) as never

// A 409 on the public flip means two different things, and the status alone
// cannot tell them apart: the tree never cleared the redistribution gate, or
// the signer's profile is not public (006r). Telling somebody the wrong one
// sends them to fix something that is not broken — so the body's code decides.
test('a 409 is disambiguated by the error code, not the status', () => {
  expect(publishErrText(t, '409', 'profile_not_public')).toBe('publishProfileRequired')
  expect(publishErrText(t, '409', 'not_clear')).toBe('publishNotClear')
  // A 409 with no code at all falls back to the older of the two meanings.
  expect(publishErrText(t, '409', '')).toBe('publishNotClear')
})

// A malformed field points at the field, whatever status carried it.
test('a field refusal names the field', () => {
  expect(publishErrText(t, '400', 'invalid_contact')).toBe('errInvalidContact')
  expect(publishErrText(t, '400', 'invalid_orcid')).toBe('errInvalidOrcid')
  expect(publishErrText(t, '400', 'invalid_scholar')).toBe('errInvalidScholar')
  expect(publishErrText(t, '409', 'handle_taken')).toBe('errHandleTaken')
  expect(publishErrText(t, '400', 'handle_reserved')).toBe('errHandleReserved')
  expect(publishErrText(t, '400', 'invalid_handle')).toBe('errInvalidHandle')
})

// Anything else is the generic failure.
test('an unknown refusal is generic', () => {
  expect(publishErrText(t, '500', '')).toBe('publishError')
  expect(publishErrText(t, '502', 'whatever')).toBe('publishError')
})
