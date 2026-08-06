// Every refusal the user can see, in one place.
//
// The rule this module exists to enforce: a message names what happened and what
// the person can do about it. It never shows a wire code, an HTTP status, or a
// Go error string — those describe our implementation, and the reader is not
// debugging our implementation.
//
// One mapper per flow rather than one table for everything, because the same
// wire code means different things in different places. `no_credits` alone is
// three separate problems with three separate remedies: a wallet that cannot
// fund a new project, a project pot that cannot start another session, and a
// balance that has too little uncommitted to hand back. Flattening them into one
// sentence is how you end up telling somebody to top up a project that does not
// exist yet.

import { ApiError, type Credits, type CreatedProject, type SessionResult } from './api'
import type { I18n } from './i18n'
import { formatUSD } from './money'

type T = I18n['t']

// refusal reads a rejection from the API client: the HTTP status and the
// server's error code. A network failure has neither — fetch rejects with a
// TypeError before any response exists — and that case gets its own message,
// because "check your connection" and "we broke" want different reactions.
export function refusal(e: unknown): { status: number; code: string; offline: boolean } {
  if (e instanceof ApiError) return { status: e.status, code: e.code, offline: false }
  return { status: 0, code: '', offline: true }
}

// generic is the last resort every flow shares: offline, or a fault on our side.
// Each flow passes the sentence that names what did not happen, so the fallback
// still says which action failed rather than "an error occurred".
export function generic(t: T, e: unknown, fallbackKey: string): string {
  return refusal(e).offline ? t('errOffline') : t(fallbackKey)
}

/* ─── starting a session ─── */

// emptyWallet reports the state a new account arrives in: nothing to spend and
// nothing ever spent. It is not the same as a project having run out, and the
// remedy is different — an admin top-up rather than moving your own credits.
export function emptyWallet(credits?: Credits): boolean {
  return !!credits && credits.wallet <= 0
}

// sessionErrLabel turns a per-paper error code from POST /projects into a short
// label that sits under the paper it belongs to. It is deliberately terse: the
// row already names which paper, so the message only has to say what about it.
export function sessionErrLabel(t: T, code?: string, credits?: Credits): string {
  if (code === 'active_session') return t('errActiveSession')
  if (code === 'unresolvable_paper') return t('errUnresolvable')
  // A batch that outran the project's credits reports this per paper; without
  // the case it would read as "could not start", which is not what happened.
  if (code === 'no_credits') return emptyWallet(credits) ? t('errNoCreditsAtAll') : t('errNoCredits')
  return t('errEnqueue')
}

// submitErrLabel names why adding a session to an *existing* project was
// refused. Its 402 is the project's pot, not the wallet — there is a project on
// screen to allocate to, which is what the message tells you to do.
export function submitErrLabel(t: T, e: unknown, credits?: Credits): string {
  const { status, code, offline } = refusal(e)
  if (offline) return t('errOffline')
  if (status === 409 || code === 'active_session') return t('activeSession')
  if (status === 402 || code === 'no_credits') return emptyWallet(credits) ? t('errNoCreditsAtAll') : t('errNoCredits')
  if (status === 400 || code === 'unresolvable_paper') return t('unresolvablePaper')
  if (status === 502 || code === 'resolve_failed') return t('resolveFailed')
  return t('submitError')
}

/* ─── creating a project ─── */

// CreateOutcome is everything the New-project screen has to say after a create:
// a banner (empty when there is nothing to say) and a message for each paper row
// that failed, keyed by the reference the row holds.
export type CreateOutcome = { ok: boolean; banner: string; rows: Map<string, string> }

// createOutcome decides what a create attempt tells the person, from the status
// and body alone. It is a pure function so the four cases that used to be one
// case can each be asserted:
//
//   201 with every paper started  — nothing to say, go to the project
//   201 with some papers refused  — go to the project, but say which were not
//   422                           — no project; every paper carries its reason
//   anything else                 — no project, and no per-paper reasons exist,
//                                   so the banner has to carry the whole story
//
// The last case is the one this replaces. It used to fall through to "no paper
// could be started", which named neither the cause nor a remedy — an out-of-
// credits wallet, a server fault, and a broken deployment all read identically.
export function createOutcome(t: T, status: number, data: CreatedProject, credits?: Credits, papers = 0): CreateOutcome {
  const rows = new Map<string, string>()
  const sessions: SessionResult[] = data.sessions ?? []
  for (const s of sessions) if (s.error) rows.set(s.url, sessionErrLabel(t, s.error, credits))

  if (status === 201 && data.project_id) {
    const started = sessions.filter((s) => s.job_id).length
    // Partial success used to be silent: the screen navigated away having said
    // nothing about the papers it dropped. The project opens either way — the
    // banner is carried to it rather than blocking here.
    return { ok: true, banner: rows.size ? t('partialStarted', { n: String(started) }) : '', rows }
  }

  if (rows.size) return { ok: false, banner: t('allPapersFailed'), rows }

  // No per-paper detail: the request was refused before any paper was tried.
  // A wallet that cannot cover the batch is the common one, and it is the whole
  // request rather than one paper — so it names what a batch of this size needs,
  // not a project that was never created.
  if (status === 402 || data.error === 'no_credits') return { ok: false, banner: createCreditsText(t, credits, papers), rows }
  if (status === 403) return { ok: false, banner: t('errNotAllowed'), rows }
  return { ok: false, banner: t('createFailed'), rows }
}

// createCreditsText says what the batch needed and what the person has, when
// both numbers are known. Naming them turns "not enough credits" into a decision
// they can make — add credits, or submit fewer papers.
function createCreditsText(t: T, credits?: Credits, papers = 0): string {
  if (!credits || !credits.start_reserve || papers <= 0) return t('errCreateNoCredits')
  return t('errCreateNoCreditsAmount', {
    n: String(papers),
    need: formatUSD(credits.start_reserve * papers),
    have: formatUSD(credits.wallet),
  })
}

/* ─── a session that failed while running ─── */

// sessionFailureText turns the cause a worker recorded into a sentence.
//
// `job.error` is a Go error string: "put replication-manifest.json in
// latere/point-of-no-undo: unexpected status 422". It was rendered verbatim on
// the session row, which is the plainest example of the problem this module
// fixes — it tells the reader nothing they can act on and quite a lot about our
// internals. The raw text is still worth keeping, so the caller shows it on
// demand; what the row states is the class of failure and who can move it.
// The order is the point. A GitHub call that returned 502 is our side failing,
// not the paper's source being unreachable — so the publish patterns are tested
// before the network ones, and the network ones match words rather than bare
// status numbers.
export function sessionFailureText(t: T, cause: string): string {
  const c = cause.toLowerCase()
  if (/secret|credential|private key/.test(c)) return t('failSecret')
  if (/not a doi|unresolvable|empty reference|needs a fetcher/.test(c)) return t('failUnresolvable')
  if (/github|repo|push|commit|manifest|installation|unexpected status/.test(c)) return t('failPublish')
  if (/fetch|lookup|arxiv|deadline exceeded|timeout|no such host|connection refused/.test(c)) return t('failSource')
  if (/credit|overdraft|budget/.test(c)) return t('failCredits')
  return t('failGeneric')
}

/* ─── everything else ─── */

// creditMoveErrLabel names why a move between a wallet and a project pot was
// refused. Each refusal has a different remedy, so each gets its own sentence.
export function creditMoveErrLabel(t: T, e: unknown): string {
  const { code, offline } = refusal(e)
  if (offline) return t('errOffline')
  if (code === 'no_credits') return t('errMoveInsufficient')
  if (code === 'not_lead') return t('errMoveNotLead')
  if (code === 'not_member') return t('errMoveNotMember')
  if (code === 'bad_amount') return t('errMoveAmount')
  return t('errMoveFailed')
}

// profileErrorText maps a server validation code to the message for the field
// that is wrong, so a refusal points somewhere rather than saying "invalid".
export function profileErrorText(t: T, code: string): string {
  if (code.includes('invalid_contact')) return t('errInvalidContact')
  if (code.includes('invalid_orcid')) return t('errInvalidOrcid')
  if (code.includes('invalid_scholar')) return t('errInvalidScholar')
  if (code.includes('handle_taken')) return t('errHandleTaken')
  if (code.includes('handle_reserved')) return t('errHandleReserved')
  if (code.includes('invalid_handle')) return t('errInvalidHandle')
  return t('profileSaveError')
}

// publishErrText names why a publish was refused. The status is not enough on
// its own: 409 covers both "this tree never cleared the redistribution check"
// and "your profile is not public", and telling a signer the wrong one sends
// them to fix something that is not broken.
export function publishErrText(t: T, e: unknown): string {
  const { status, code, offline } = refusal(e)
  if (offline) return t('errOffline')
  if (code === 'profile_not_public') return t('publishProfileRequired')
  if (code === 'not_clear') return t('publishNotClear')
  if (code.startsWith('handle_') || code.startsWith('invalid_')) return profileErrorText(t, code)
  if (status === 409) return t('publishNotClear')
  if (status === 400) return profileErrorText(t, code)
  return t('publishError')
}

// profileSaveErrText is profileErrorText with the offline case in front of it —
// a failed save with no response at all is not a malformed ORCID.
export function profileSaveErrText(t: T, e: unknown): string {
  const { code, offline } = refusal(e)
  return offline ? t('errOffline') : profileErrorText(t, code)
}
