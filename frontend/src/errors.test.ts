// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { ApiError, type CreatedProject } from './api'
import {
  addPaperErrLabel,
  checkoutErrLabel,
  createOutcome,
  generic,
  movePaperErrLabel,
  refusal,
  removePaperErrLabel,
  sessionFailureText,
  startErrLabel,
} from './errors'
import { MICRO } from './money'

// The helpers take a translate function, so these tests assert the key, not the
// English copy, which may be reworded.
const t = ((key: string, vars?: Record<string, string>) =>
  vars ? `${key}:${Object.values(vars).join(',')}` : key) as never

const wallet = { wallet: 0, start_reserve: 5 * MICRO }
const funded = { wallet: 40 * MICRO, start_reserve: 5 * MICRO }

// A create records papers and starts nothing (012), so the only thing that can
// go wrong per reference is the reference. Everything the old outcome had to
// explain, a paper refused for credits, a partial batch, a project rolled back
// because nothing started, was removed with the enqueue that caused it.
test('a create reports the references the server did not recognize', () => {
  const data: CreatedProject = {
    project_id: 'p1',
    papers: [
      { ref: 'https://doi.org/10/a', key: 'doi:10/a' },
      { ref: 'just some words', error: 'unresolvable_paper' },
    ],
  }
  const out = createOutcome(t, 201, data)
  // The project exists and is worth opening either way.
  expect(out.ok).toBe(true)
  expect(out.banner).toBe('somePapersNotRecognised')
  expect(out.rows.get('just some words')).toBe('errUnresolvable')
  expect(out.rows.has('https://doi.org/10/a')).toBe(false)
})

// A project may start empty (036), so a create with no papers at all is the
// ordinary case rather than an error; there is nothing to report per reference
// because there were none.
test('a create with no papers is a clean create', () => {
  const out = createOutcome(t, 201, { project_id: 'p1', papers: [] })
  expect(out.ok).toBe(true)
  expect(out.banner).toBe('')
  expect(out.rows.size).toBe(0)
})

// A project cannot start without a name: the title falls back to the first
// paper's reference, and with no paper there is no fallback. The screen guards
// this before asking, so reaching this path means the two disagreed, and it has
// to say the same thing the screen would have.
test('a create with neither a name nor a paper names both fixes', () => {
  const out = createOutcome(t, 400, { error: 'missing_title' })
  expect(out.ok).toBe(false)
  expect(out.banner).toBe('nameOrPaper')
})

// Every reference recognized: nothing to say, so the screen goes through.
test('a clean create says nothing', () => {
  const data: CreatedProject = { project_id: 'p1', papers: [{ ref: 'a', key: 'doi:a' }] }
  const out = createOutcome(t, 201, data)
  expect(out.ok).toBe(true)
  expect(out.banner).toBe('')
  expect(out.rows.size).toBe(0)
})

// A create can no longer be refused for money, so a non-201 is a real fault and
// is reported as one rather than blamed on the papers.
test('a failed create names a fault, not the references', () => {
  expect(createOutcome(t, 403, {}).banner).toBe('errNotAllowed')
  expect(createOutcome(t, 500, {}).banner).toBe('createFailed')
  // A body that was not JSON, as when a broken deployment answers 404 with HTML.
  expect(createOutcome(t, 404, {}).banner).toBe('createFailed')
  expect(createOutcome(t, 500, {}).ok).toBe(false)
})

// Starting a session is where every refusal a create used to report per paper
// now lives, and the one place money is asked about.
test('a refused start names the one thing that went wrong', () => {
  expect(startErrLabel(t, new ApiError(409, 'active_session'), funded)).toBe('activeSession')
  expect(startErrLabel(t, new ApiError(402, 'no_credits'), funded)).toBe('errNoCredits')
  expect(startErrLabel(t, new ApiError(402, 'no_credits'), wallet)).toBe('errNoCreditsAtAll')
  // A frozen account is a distinct refusal from an empty one, with a different
  // remedy.
  expect(startErrLabel(t, new ApiError(402, 'account_frozen'), funded)).toBe('errFrozen')
  expect(startErrLabel(t, new ApiError(404, 'paper_not_found'), funded)).toBe('errPaperGone')
  expect(startErrLabel(t, new ApiError(500, ''), funded)).toBe('startFailed')
  expect(startErrLabel(t, new TypeError('Failed to fetch'), funded)).toBe('errOffline')
})

test('a refused top-up names why', () => {
  expect(checkoutErrLabel(t, new ApiError(501, 'payments_unconfigured'))).toBe('errPaymentsOff')
  expect(checkoutErrLabel(t, new ApiError(400, 'below_min'))).toBe('errBelowMin')
  expect(checkoutErrLabel(t, new ApiError(502, 'checkout_failed'))).toBe('checkoutFailed')
  expect(checkoutErrLabel(t, new TypeError('Failed to fetch'))).toBe('errOffline')
})

// Adding a paper costs nothing, so none of its refusals are about money.
test('adding and removing a paper never mention credits', () => {
  expect(addPaperErrLabel(t, new ApiError(409, 'paper_exists'))).toBe('errPaperExists')
  expect(addPaperErrLabel(t, new ApiError(400, 'unresolvable_paper'))).toBe('unresolvablePaper')
  expect(addPaperErrLabel(t, new ApiError(500, ''))).toBe('addPaperFailed')
  // A paper that has been run stays: the record of what was reproduced is not
  // editable, and that is a different refusal from not being allowed to try.
  expect(removePaperErrLabel(t, new ApiError(409, 'paper_has_sessions'))).toBe('errPaperHasSessions')
  expect(removePaperErrLabel(t, new ApiError(403, 'not_lead'))).toBe('errRemoveNotLead')
})

// 035: each refusal has a different next step, finish the run, withdraw the
// publication or pick another project, so they must not collapse into one
// "could not move". The two 409s in particular differ: one is about timing,
// the other about the destination.
test('moving a paper names the specific refusal', () => {
  expect(movePaperErrLabel(t, new ApiError(409, 'paper_running'))).toBe('errPaperRunning')
  expect(movePaperErrLabel(t, new ApiError(409, 'paper_exists'))).toBe('errPaperAlreadyThere')
  expect(movePaperErrLabel(t, new ApiError(409, 'project_public'))).toBe('errMovePublic')
  expect(movePaperErrLabel(t, new ApiError(409, 'project_trashed'))).toBe('errMoveTrashed')
  expect(movePaperErrLabel(t, new ApiError(403, 'not_lead'))).toBe('errMovePaperNotLead')
  expect(movePaperErrLabel(t, new ApiError(404, 'target_not_found'))).toBe('movePaperFailed')
  expect(movePaperErrLabel(t, new TypeError('offline'))).toBe('errOffline')
})

// A failed session used to print the worker's own error text on the row:
// "put replication-manifest.json in latere/x: unexpected status 422". Each
// class of failure now reads as a sentence, and points at who can move it.
test('a failed run reads as a sentence, not a Go error', () => {
  expect(sessionFailureText(t, 'resolve: "foo" is not a DOI, arXiv ID, or URL')).toBe('failUnresolvable')
  expect(sessionFailureText(t, 'lookup failed: arXiv 2401.00001: context deadline exceeded')).toBe('failSource')
  expect(sessionFailureText(t, 'secret in content: results/key.pem contains a private key')).toBe('failSecret')
  expect(sessionFailureText(t, 'put replication-manifest.json in latere/x: unexpected status 422')).toBe('failPublish')
  expect(sessionFailureText(t, 'project is over its overdraft floor')).toBe('failCredits')
  // Anything unrecognised still says something true, and still says nothing
  // about our internals.
  expect(sessionFailureText(t, 'nil pointer dereference in grade step')).toBe('failGeneric')
})

// A request that never reached a server is not a server fault, and the two want
// different reactions from the reader.
test('offline is told apart from a refusal', () => {
  expect(refusal(new TypeError('Failed to fetch')).offline).toBe(true)
  expect(refusal(new ApiError(500, '')).offline).toBe(false)
  expect(generic(t, new TypeError('Failed to fetch'), 'createFailed')).toBe('errOffline')
  expect(generic(t, new ApiError(500, ''), 'createFailed')).toBe('createFailed')
})
