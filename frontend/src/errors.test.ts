import { expect, test } from 'bun:test'
import { ApiError, type CreatedProject } from './api'
import { createOutcome, generic, refusal, sessionFailureText, submitErrLabel } from './errors'
import { MICRO } from './money'

// The helpers take a translate function, so asserting the key is what matters
// here — not the English copy, which is free to be reworded.
const t = ((key: string, vars?: Record<string, string>) =>
  vars ? `${key}:${Object.values(vars).join(',')}` : key) as never

const wallet = { wallet: 0, start_reserve: 5 * MICRO }
const funded = { wallet: 40 * MICRO, start_reserve: 5 * MICRO }

// The bug this file exists for: a create that was refused *before* any paper was
// tried carries no per-paper detail, and every one of those refusals used to
// print "No paper could be started — check the references." That named neither
// the cause nor a remedy, and pointed at the one thing that was not wrong.
test('a refusal with no per-paper detail says what actually happened', () => {
  // Out of credits, and the numbers are known: say what the batch needs.
  expect(createOutcome(t, 402, { error: 'no_credits' }, funded, 3).banner).toBe('errCreateNoCreditsAmount:3,$15.00,$40.00')
  // Out of credits with nothing known about the wallet: no invented figures.
  expect(createOutcome(t, 402, { error: 'no_credits' }, undefined, 3).banner).toBe('errCreateNoCredits')
  // A revoked or unknown account is not a money problem.
  expect(createOutcome(t, 403, {}, wallet, 1).banner).toBe('errNotAllowed')
  // A server fault, or a body that was not even JSON (a broken deployment
  // answers 404 with HTML): generic, and never the credits branch.
  expect(createOutcome(t, 500, {}, wallet, 1).banner).toBe('createFailed')
  expect(createOutcome(t, 404, {}, funded, 1).banner).toBe('createFailed')
})

// The create-time 402 must not borrow the message written for an existing
// project. There is no project yet, so "allocate more to it" points at nothing.
test('a create refusal never tells you to top up a project that does not exist', () => {
  const banner = createOutcome(t, 402, { error: 'no_credits' }, wallet, 1).banner
  expect(banner.startsWith('errCreateNoCredits')).toBe(true)
  expect(banner).not.toBe('errNoCredits')
})

// 422: the project was rolled back and each paper carries its own reason.
test('a fully refused batch puts a reason on every paper', () => {
  const data: CreatedProject = {
    error: 'no_sessions_started',
    sessions: [
      { url: 'https://doi.org/10/a', error: 'active_session' },
      { url: 'https://arxiv.org/abs/b', error: 'unresolvable_paper' },
    ],
  }
  const out = createOutcome(t, 422, data, funded, 2)
  expect(out.ok).toBe(false)
  expect(out.banner).toBe('allPapersFailed')
  expect(out.rows.get('https://doi.org/10/a')).toBe('errActiveSession')
  expect(out.rows.get('https://arxiv.org/abs/b')).toBe('errUnresolvable')
})

// 201 means at least one paper started — not that all of them did. The screen
// used to navigate away on any 201, so a batch could silently drop three of four
// papers and say nothing about it.
test('a partly successful create does not swallow the papers it dropped', () => {
  const data: CreatedProject = {
    project_id: 'p1',
    sessions: [
      { url: 'a', job_id: 'j1' },
      { url: 'b', error: 'active_session' },
      { url: 'c', error: 'no_credits' },
    ],
  }
  const out = createOutcome(t, 201, data, funded, 3)
  expect(out.ok).toBe(true)
  expect(out.banner).toBe('partialStarted:1,3')
  expect(out.rows.get('b')).toBe('errActiveSession')
  expect(out.rows.get('c')).toBe('errNoCredits')
})

// A create where everything started has nothing to say, so the screen goes
// straight through to the project.
test('a clean create says nothing', () => {
  const data: CreatedProject = { project_id: 'p1', sessions: [{ url: 'a', job_id: 'j1' }] }
  const out = createOutcome(t, 201, data, funded, 1)
  expect(out.ok).toBe(true)
  expect(out.banner).toBe('')
  expect(out.rows.size).toBe(0)
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
  expect(submitErrLabel(t, new TypeError('Failed to fetch'), funded)).toBe('errOffline')
})

// The code decides where the status is ambiguous — a 400 from the paper lookup
// is a link the person can fix, and says so rather than "could not submit".
test('adding a session to a project names the fixable cases', () => {
  expect(submitErrLabel(t, new ApiError(400, 'unresolvable_paper'), funded)).toBe('unresolvablePaper')
  expect(submitErrLabel(t, new ApiError(502, 'resolve_failed'), funded)).toBe('resolveFailed')
})
