// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { ApiError, type PublishBlocker } from './api'
import { blockerTab, publishBlockers, publishBlockerText, publishErrText } from './errors'

const t = ((key: string) => key) as never

// A 409 on the public flip means several different things, and the status alone
// cannot tell them apart: the signer's profile is not public (006r), or the
// project's sessions are not publishable — and that last one is itself four
// facts. Telling somebody the wrong one sends them to fix something that is not
// broken, so the body's code decides.
test('a 409 is disambiguated by the error code, not the status', () => {
  expect(publishErrText(t, new ApiError(409, 'profile_not_public'))).toBe('publishProfileRequired')
  expect(publishErrText(t, new ApiError(409, 'not_clear'))).toBe('publishNotClear')
  expect(publishErrText(t, new ApiError(409, 'nothing_published'))).toBe('publishNothingPublished')
  expect(publishErrText(t, new ApiError(409, 'sessions_unfinished'))).toBe('publishUnfinished')
  expect(publishErrText(t, new ApiError(409, 'manifest_unreadable'))).toBe('publishUnreadable')
})

// A 409 that names no cause must not be reported as the redistribution check.
// That fallback was the same wrong accusation the codes above exist to end: a
// run that never finished, or a forge that did not answer, told the owner their
// reproduction had committed content it may not redistribute.
test('a coded-less 409 says the project is blocked, not that it is contaminated', () => {
  expect(publishErrText(t, new ApiError(409, ''))).toBe('publishBlocked')
})

// A refusal carries the sessions in the way. Each says what is wrong with that
// one — the two not-clear reviews are different problems with different remedies
// — and opens where that is legible: a flagged manifest on the verdict pane, a
// run that never finished on its transcript, which has no manifest to show.
test('a blocker names its own reason and where to read it', () => {
  const blocker = (b: Partial<PublishBlocker>): PublishBlocker => ({ session_id: 's', reason: 'not_clear', ...b })
  expect(publishBlockerText(t, blocker({ review: 'excluded' }))).toBe('blockerNotClear')
  expect(publishBlockerText(t, blocker({ review: 'secret_in_transcript' }))).toBe('blockerSecret')
  expect(publishBlockerText(t, blocker({ reason: 'unfinished' }))).toBe('blockerUnfinished')
  expect(publishBlockerText(t, blocker({ reason: 'unreadable' }))).toBe('blockerUnreadable')

  expect(blockerTab(blocker({}))).toBe('verdict')
  expect(blockerTab(blocker({ reason: 'unfinished' }))).toBe('transcript')
  expect(blockerTab(blocker({ reason: 'unreadable' }))).toBe('transcript')
})

// The blockers survive the trip through the API client. They used to not exist;
// what did exist — a body with more in it than `error` — was dropped on the
// floor, so a refusal could name sessions the screen never saw.
test('a refusal keeps the sessions it named', () => {
  const named: PublishBlocker[] = [{ session_id: 'job-1', repo: 'open-hai/x', reason: 'not_clear', review: 'excluded' }]
  expect(publishBlockers(new ApiError(409, 'not_clear', named))).toEqual(named)
  expect(publishBlockers(new ApiError(409, 'nothing_published'))).toEqual([])
  expect(publishBlockers(new Error('offline'))).toEqual([])
})

// A malformed field points at the field, whatever status carried it.
test('a field refusal names the field', () => {
  expect(publishErrText(t, new ApiError(400, 'invalid_contact'))).toBe('errInvalidContact')
  expect(publishErrText(t, new ApiError(400, 'invalid_orcid'))).toBe('errInvalidOrcid')
  expect(publishErrText(t, new ApiError(400, 'invalid_scholar'))).toBe('errInvalidScholar')
  expect(publishErrText(t, new ApiError(409, 'handle_taken'))).toBe('errHandleTaken')
  expect(publishErrText(t, new ApiError(400, 'handle_reserved'))).toBe('errHandleReserved')
  expect(publishErrText(t, new ApiError(400, 'invalid_handle'))).toBe('errInvalidHandle')
})

// Anything else is the generic failure.
test('an unknown refusal is generic', () => {
  expect(publishErrText(t, new ApiError(500, ''))).toBe('publishError')
  expect(publishErrText(t, new ApiError(502, 'whatever'))).toBe('publishError')
})
