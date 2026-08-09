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

import { ApiError, type Credits, type CreatedProject, type PaperResult } from './api'
import type { I18n } from './i18n'

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

/* ─── creating a project ─── */

// CreateOutcome is everything the New-project screen has to say after a create:
// a banner (empty when there is nothing to say) and a message for each paper row
// the server did not recognise, keyed by the reference the row holds.
export type CreateOutcome = { ok: boolean; banner: string; rows: Map<string, string> }

// createOutcome decides what a create attempt tells the person.
//
// It is a short function because the act is now a short one. A create records
// papers and starts nothing (012), so there is no per-paper session to fail, no
// partial success to explain, and no credits to run out of — all of that went
// with the enqueue it no longer does. What is left: the papers were recorded, or
// the server did not recognise some of the references.
export function createOutcome(t: T, status: number, data: CreatedProject): CreateOutcome {
  const rows = new Map<string, string>()
  const papers: PaperResult[] = data.papers ?? []
  for (const p of papers) if (p.error) rows.set(p.ref, t('errUnresolvable'))

  if (status === 201 && data.project_id) {
    return { ok: true, banner: rows.size ? t('somePapersNotRecognised') : '', rows }
  }
  if (status === 403) return { ok: false, banner: t('errNotAllowed'), rows }
  return { ok: false, banner: t('createFailed'), rows }
}

/* ─── working inside a project ─── */

// startErrLabel names why starting a session was refused. This is where every
// refusal a create used to report per paper now lives, and the one place money
// is asked about — so "this project is out of credits" is said on the screen
// showing the pot, which is what makes it something the reader can act on.
export function startErrLabel(t: T, e: unknown, credits?: Credits): string {
  const { status, code, offline } = refusal(e)
  if (offline) return t('errOffline')
  if (status === 409 || code === 'active_session') return t('activeSession')
  if (status === 402 || code === 'no_credits') return emptyWallet(credits) ? t('errNoCreditsAtAll') : t('errNoCredits')
  if (status === 404) return t('errPaperGone')
  return t('startFailed')
}

// addPaperErrLabel names why a paper could not be added. Nothing on this path
// costs anything, so none of it is about money.
export function addPaperErrLabel(t: T, e: unknown): string {
  const { status, code, offline } = refusal(e)
  if (offline) return t('errOffline')
  if (status === 409 || code === 'paper_exists') return t('errPaperExists')
  if (status === 400 || code === 'unresolvable_paper') return t('unresolvablePaper')
  return t('addPaperFailed')
}

// removePaperErrLabel names why a paper could not be dropped. A paper that has
// been run stays: the record of what was actually reproduced is not editable.
export function removePaperErrLabel(t: T, e: unknown): string {
  const { status, code, offline } = refusal(e)
  if (offline) return t('errOffline')
  if (status === 409 || code === 'paper_has_sessions') return t('errPaperHasSessions')
  if (status === 403 || code === 'not_lead') return t('errRemoveNotLead')
  return t('removePaperFailed')
}

// trashErrLabel names why a project could not be moved to the trash. The one
// refusal an owner can act on is a session still running: a run spends this
// project's credits, and hiding the container it spends from is not tidying up.
export function trashErrLabel(t: T, e: unknown): string {
  const { code, offline } = refusal(e)
  if (offline) return t('errOffline')
  if (code === 'sessions_running') return t('errSessionsRunning')
  return t('trashFailed')
}

// purgeErrLabel names why a project could not be removed for good. Credits in
// the pot are the blocker with a remedy: take them back, then delete.
export function purgeErrLabel(t: T, e: unknown): string {
  const { code, offline } = refusal(e)
  if (offline) return t('errOffline')
  if (code === 'credits_remaining') return t('errCreditsRemaining')
  return t('purgeFailed')
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

// checkoutErrLabel names why starting a top-up was refused. Payments being
// unconfigured is a deployment state, not the person's fault; a below-floor
// amount is caught client-side, but the server is the backstop.
export function checkoutErrLabel(t: T, e: unknown): string {
  const { code, offline } = refusal(e)
  if (offline) return t('errOffline')
  if (code === 'payments_unconfigured') return t('errPaymentsOff')
  if (code === 'below_min') return t('errBelowMin')
  return t('checkoutFailed')
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
