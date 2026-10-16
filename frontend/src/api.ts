// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// The API client. Every call is same-origin, so the session cookie or the dev
// bypass is sent automatically.
import type { Findings } from './findings'

// Credits is what this person has to spend (011), in micro-USD. The wallet is
// never spent by a session directly, because a run draws on the pot of the
// project it belongs to. start_reserve is what a project must have available
// before a session may start there. It is public so the client can explain the
// refusal before it happens. Absent for an ungranted principal.
export type Credits = { wallet: number; start_reserve: number }

export type Me = {
  authenticated: boolean
  principal?: string
  email?: string
  tier?: string
  credits?: Credits
  // frozen is view-only mode: a negative wallet with no admin override (022d).
  // The banner and the disabled spend controls read this one field.
  frozen?: boolean
  // Display claims from the IdP (006m). Both are optional and cosmetic; the
  // email stays the identifier everything binds on.
  name?: string
  picture?: string
}

// Usage is what a run consumed, in tokens, by priced class. Cache writes are
// split by the lifetime they bought where the backend reports it, and reported
// in CacheWriteTokens, which covers one request, where it does not. A backend
// that reports none of a class leaves it absent, so every field is optional.
export type Usage = {
  InputTokens: number
  OutputTokens: number
  CachedInputTokens: number
  CacheWrite5mTokens?: number
  CacheWrite1hTokens?: number
  CacheWriteTokens?: number
}

export type Job = {
  id: string
  kind: string
  ref: string
  phase: string
  // What this session has cost so far, in micro-USD. It is the number its
  // project is charged when the session ends.
  cost?: number
  // What the run ran on, as the worker reported it. Model is the exact model
  // id, such as `claude-opus-5` or `gpt-6-astra`. In a repeat study it is the
  // only field that distinguishes one session from another under the same
  // paper.
  status?: { State: string; Usage: Usage; Model?: string; ModelProvider?: string; Runtime?: string }
  // The paper this session targeted, as the offline key project_papers is keyed
  // on (012). It is what joins a run to the paper it is a run of.
  paper_key?: string
  // Set when the publishing phase opens, not when it succeeds, so a session
  // that failed while publishing carries one too. `isGradeable` in papers.ts is
  // the test for a result; this field alone is not.
  repo_name?: string
  error?: string
  created_at?: string
  // phase_at records when the session entered each phase. The timeline is drawn
  // from it.
  phase_at?: Record<string, string>
}

// A project is the container a user owns. It holds one or more replication
// sessions, each a Job. The list carries a session count so the projects view
// needs no per-project read.
export type Project = {
  id: string
  owner: string
  title: string
  // What the project is about, in its owner's words (038). Absent for one
  // nobody has described, and for a private project somebody may not read.
  description?: string
  visibility: 'private' | 'public'
  created_at: string
  signed_off_by?: string
  signed_off_at?: string
  attestation_version?: string
  // The signer as they were when they signed (006n): a snapshot recorded at the
  // flip, not a view of their profile, so these never change afterwards. Empty
  // on a v1 sign-off, which predates them.
  signed_off_name?: string
  signed_off_institution?: string
  signed_off_contact?: string
  signed_off_orcid?: string
  signed_off_scholar?: string
  // Already redacted server-side; the signer's full address never reaches here.
  signed_off_email?: string
  // The retraction (006o), absent until a publication is withdrawn. It sits
  // beside the sign-off rather than replacing it, so a project can carry both.
  // Read `visibility` to ask whether a project is open, never `signed_off_at`.
  withdrawn_at?: string
  withdrawn_by?: string
  withdrawn_reason?: string
  // When the owner moved this project to the trash (019), absent for one that is
  // not there. A trashed project leaves every list but keeps its address: its
  // owner still reads it, read-only, with a way back.
  deleted_at?: string
}

// A researcher profile: who a sign-off is published under. Self-asserted and
// unverified, always the caller's own, and only a default: the publish wizard
// prefills from it and records what the owner confirms.
export type Profile = {
  display_name?: string
  institution?: string
  contact_email?: string
  orcid?: string
  scholar_url?: string
  // The public page (006r): a handle that can go in a URL, and whether there is
  // a page at all. A profile is private until its owner says otherwise.
  handle?: string
  public?: boolean
}

// PublicProfile is what a stranger reads at /u/{handle}: the self-asserted
// identity, and the public work signed under it. The account email is never
// included; the redacted signer email on an attestation serves that purpose.
export type PublicProfile = {
  handle: string
  display_name?: string
  institution?: string
  contact_email?: string
  orcid?: string
  scholar_url?: string
  projects: PublicSigned[]
}

type PublicSigned = { id: string; title: string; signed_off_at?: string }

// signer_handle is where the signer's name links (006r). It is resolved on
// every read from the signer's current profile and never snapshotted, so a
// profile going private removes the link and changes nothing on the record.
export type ProjectView = Project & {
  session_count: number
  running: number
  // What this project's sessions cost in the last thirty days, in micro-USD
  // (021e). Windowed on when a session last changed, so a long run that settled
  // recently counts; a running one counts what it has burned so far.
  spend_30d?: number
  latest_phase?: string
  signer_handle?: string
  // Who owns it, as a reader can read them (018). `owner` is an opaque OIDC
  // subject and names nobody, so the list's Owner column reads these: the
  // owner's profile name, and their handle when that profile is public and the
  // name can therefore link. Both absent for somebody who has saved no profile.
  // Neither is ever an email address.
  owner_name?: string
  owner_handle?: string
  // How many of its sessions committed a repository, which is what the archive
  // contains (020). It is the exact condition the download endpoint refuses on,
  // so the button and the handler cannot disagree. A session count says nothing
  // about whether any session published.
  published_sessions?: number
  // When this project last changed: the newest of its creation, its papers, and
  // its sessions. Derived server-side on every read, and what the list sorts on.
  modified_at?: string
  // What this project is a reproduction of (043): every paper it holds, as a
  // citation. All of them are included because one line under a two-paper
  // project would put the second paper's work under the first one's name.
  // Absent for a project that holds none.
  papers?: PaperCitation[]
}

// may_submit is the server's answer to whether this caller, as owner or
// collaborator, may open a session here. A public project is readable by
// everyone, so the client cannot infer it (006l).
type ProjectDetail = { project: Project; sessions: Job[]; may_submit: boolean; signer_handle?: string; credits?: ProjectCredits }

// ProjectCredits is a project's money as a member reads it, in micro-USD.
// balance is what the pot holds; available is what is still free to commit.
// They differ by the sessions running right now, each holding the start
// reserve, which is why a project with credits can still refuse a submit.
// Absent for anyone who may not work here.
//
// may_reclaim is the server's answer to whether the caller may take credits
// back out; only a lead may. The client cannot work it out because it does not
// know roles.
export type ProjectCredits = { balance: number; available: number; start_reserve: number; may_reclaim: boolean }

// Wallet is one person's own credits and the ledger entries that produced them,
// so every balance can be accounted for.
//
// The spread and floor are included so the top-up modal can quote a credit and
// refuse a too-small amount before the processor is called (022b/022e). They
// are zero on a deployment that sells no credits.
export type Wallet = {
  balance: number
  entries: LedgerEntry[]
  spread_bps: number
  spread_fixed_micro: number
  min_topup_micro: number
}

export type LedgerEntry = {
  id: string
  holder: string
  amount: number
  kind: 'topup' | 'allocate' | 'reclaim' | 'hold' | 'release' | 'charge' | 'adjust'
  job_id?: string
  // The project an allocate or reclaim moved credits to or from (024b).
  // project_name is a snapshot taken at the move, so it still reads after the
  // project is purged. project_alive is whether the project still exists: true
  // links to it, false shows the name as plain text.
  project_id?: string
  project_name?: string
  project_alive?: boolean
  actor: string
  created_at: string
}

export const getWallet = () => fetch('/me/credits').then(json<Wallet>)

// startCheckout opens a hosted payment page for a top-up of amountMicro, in
// micro-USD, and returns its URL for the browser to follow. The webhook credits
// the wallet (022b), not the return from that page, so the client only sends
// the person to pay and re-reads the balance when they come back.
export const startCheckout = (amountMicro: number) =>
  fetch('/me/credits/checkout', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ amount_micro: amountMicro }),
  }).then(json<{ url: string }>)

// allocateCredits moves credits from your wallet into a project's pot; any
// member may. reclaimCredits moves them back; only a lead may, because the pot
// does not record who filled it. Both answer with both balances, because both
// moved and a client re-reading them separately could show a moment where the
// money is in neither.
type CreditMove = { wallet: number; project: ProjectCredits | null }

export const allocateCredits = (id: string, amount: number) =>
  fetch(`/projects/${id}/credits`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ amount }),
  }).then(json<CreditMove>)

export const reclaimCredits = (id: string, amount: number) =>
  fetch(`/projects/${id}/credits/reclaim`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ amount }),
  }).then(json<CreditMove>)

// A project member: the seeded email, the role, where 1 is collaborator and 2 is
// lead, and whether they have signed in yet, which is `bound`.
export type Member = { email: string; role: number; bound: boolean }

// One entry in a session's trajectory: an agent message, its thinking, a tool
// call, a tool result, or the grade.
export type Event = {
  role: string
  kind: string
  tool?: string
  content?: string
  time?: string
  // What the backend charged for this one request (006s). Absent on the events
  // that were charged nothing, which is most of them, so it is rendered only
  // where it exists, never as a zero.
  usage?: Usage
}

// live is true when these events came from an in-flight run's live stream, so
// the client keeps polling for more.
export type Transcript = { events: Event[]; usage: Usage | null; live: boolean }

// A session's manifest summary: the reproduction verdict and metadata, read
// from the committed repository.
export type Reproduction = {
  id: string
  repo: string
  title: string
  verdict: string
  created?: string
  method_digest?: string
  review?: string
  can_publish: boolean
}

// PublishBlocker is one session standing between a project and the public flip.
// A refusal carries the whole list, so the owner can be sent to the session
// rather than told to go looking for it.
//
// reason is a closed set. 'not_clear' means the redistribution review flagged
// what the run committed; 'unfinished' means the run has a repository but never
// wrote a manifest; 'unreadable' means the manifest could not be read at all.
export type PublishBlocker = {
  session_id: string
  repo?: string
  ref?: string
  reason: string
  review?: string
}

// ApiError carries both halves of a refusal: the status, and the machine-
// readable code the server put in the body.
//
// The message stays the bare status, because that is what every existing caller
// compares against. The code is what a caller needs when one status means more
// than one thing. A 409 on the public flip is either an unclear tree or a
// profile that is not public, and those need different remedies.
//
// blockers is the rest of the body, for the refusals that name the sessions in
// the way. The code says what happened; blockers says which sessions.
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code = '',
    readonly blockers: PublishBlocker[] = [],
  ) {
    super(String(status))
  }
}

// errCode reads the server's error code off a rejection, empty when there was
// none, as after a network failure or a response with no body.
export function errCode(e: unknown): string {
  return e instanceof ApiError ? e.code : ''
}

// refusal builds an ApiError, reading the body's `error` field when there is
// one. The body is consumed either way, so a failed response never leaks a
// stream.
async function refusal(r: Response): Promise<ApiError> {
  let code = ''
  let blockers: PublishBlocker[] = []
  try {
    const body = (await r.json()) as { error?: string; blockers?: PublishBlocker[] }
    code = body?.error ?? ''
    if (Array.isArray(body?.blockers)) blockers = body.blockers
  } catch {
    /* no body, or not JSON: the status is all we have */
  }
  return new ApiError(r.status, code, blockers)
}

// json reads a successful response's body. A 2xx that does not parse, such as
// the dev server's own HTML for a path it does not proxy or a gateway page in
// front of the API, is a server fault and is reported as one, with its status
// and a code. Before, r.json() threw a SyntaxError that nothing downstream
// recognized, and every screen reported it as "you appear to be offline".
async function json<T>(r: Response): Promise<T> {
  if (!r.ok) throw await refusal(r)
  try {
    return (await r.json()) as T
  } catch {
    throw new ApiError(r.status, 'malformed_response')
  }
}

// ok resolves for a 2xx response with no body, such as a 204, and rejects with
// the status otherwise.
async function ok(r: Response): Promise<void> {
  if (!r.ok) throw await refusal(r)
}

export const getMe = () => fetch('/me').then(json<Me>)

export const getJob = (id: string) => fetch(`/jobs/${id}`).then(json<Job>)

// A previewed paper reference: the resolved identity the New-project screen
// shows before anything is created.
export type ResolvedPaper = { key: string; title: string; scheme: string; slug: string }

// resolvePaper previews a reference against the source. It rejects with the HTTP
// status: 400 for a reference that is not a DOI/arXiv/URL, 502 when the source
// could not be reached.
export const resolvePaper = (url: string) =>
  fetch('/resolve', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ url }),
  }).then(json<ResolvedPaper>)

// One reference's result in a create: the paper it was recorded as, or why it
// was not recorded. A create runs nothing (012), so the only thing that can go
// wrong per reference is the reference itself.
export type PaperResult = { ref: string; key?: string; title?: string; error?: string }
export type CreatedProject = { project_id?: string; title?: string; papers?: PaperResult[]; members?: string[]; error?: string }

// A paper a project is about, with what has happened to it. sessions is 0 for a
// paper nobody has started, which is the ordinary state of one just added.
export type ProjectPaper = {
  key: string
  ref: string
  title?: string
  added_by: string
  added_at: string
  sessions: number
  latest_phase?: string
  // The bibliography, as the paper's own source declared it (037). Absent for a
  // paper whose reference nothing could be looked up for.
  record?: PaperRecord
  // Which fields somebody corrected by hand. Enrichment never touches these.
  edited?: string[]
}

// What a citation is made of. Every field is optional because a source that
// declares half of one is the ordinary case, and a field nobody declared is
// shown as missing rather than guessed at.
export type PaperRecord = {
  authors?: string[]
  venue?: string
  year?: number
  volume?: string
  issue?: string
  pages?: string
  publisher?: string
  // What the paper is about, in its source's own words. Absent for the many
  // records that publish none.
  abstract?: string
}

// PaperCitation is one paper as a list carries it (043): its title and the
// three fields a citation line is made of. The public registry sends this
// rather than the whole record, because an abstract is paragraphs long and a
// page of cards would otherwise ship one per card to print one line per card.
export type PaperCitation = {
  title?: string
  authors?: string[]
  venue?: string
  year?: number
}

// A correction. Only the fields present are written, and each one written is
// marked so no later lookup undoes it.
type PaperEdit = {
  title?: string
  authors?: string[]
  venue?: string
  year?: number
  volume?: string
  issue?: string
  pages?: string
  publisher?: string
  abstract?: string
}

// listPapers returns the papers a project is about.
export const listPapers = (id: string) => fetch(`/projects/${id}/papers`).then(json<ProjectPaper[]>)

// addPaper records another paper on a project. It starts nothing.
export const addPaper = (id: string, url: string) =>
  fetch(`/projects/${id}/papers`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ url }),
  }).then(json<PaperResult>)

// editPaper corrects a paper's record. What it writes stays written: the server
// marks each corrected field so the next lookup fills only what is still empty.
export const editPaper = (id: string, key: string, edit: PaperEdit) =>
  fetch(`/projects/${id}/papers?key=${encodeURIComponent(key)}`, {
    method: 'PATCH',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(edit),
  }).then(json<ProjectPaper>)

// refreshPapers fills in the details of every paper in the project that is
// missing them, and reports how many it completed.
export const refreshPapers = (id: string) =>
  fetch(`/projects/${id}/papers/refresh`, { method: 'POST' }).then(json<{ filled: number; papers: ProjectPaper[] }>)

// removePaper drops a paper the project has never run.
export const removePaper = (id: string, key: string) =>
  fetch(`/projects/${id}/papers?key=${encodeURIComponent(key)}`, { method: 'DELETE' }).then(ok)

// movePaper refiles a paper, and the sessions that are runs of it, under
// another project (035). It is the lead's at both ends, and is refused 409
// while a run is still in flight, while either project is published or in the
// trash, or when the destination already holds that paper.
//
// The key travels in the body, not the path: a paper key such as
// "doi:10.1145/…" spans several path segments and would route nowhere.
export const movePaper = (id: string, key: string, toProject: string) =>
  fetch(`/projects/${id}/papers/move`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ paper_key: key, to_project: toProject }),
  }).then(ok)

// startSession runs one paper the project holds. This is the call that spends:
// it is refused 402 when the project's pot cannot cover the start reserve, and
// 409 when that paper is already running.
//
// tier is the model the session runs on (031). Omitted means the caller did not
// choose and the engine's default applies, which is what every call did before
// the choice existed; a tier the server does not offer is refused 400.
// startSession starts a paper's session. The choice names the tier and, where
// the deployment offers them (048), the runtime and one exact model; a field
// left empty is not sent, so the server's default applies.
export const startSession = (id: string, paperKey: string, choice?: { tier?: string; runtime?: string; model?: string }) =>
  fetch(`/projects/${id}/sessions`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({
      paper_key: paperKey,
      ...(choice?.tier ? { tier: choice.tier } : {}),
      ...(choice?.runtime ? { runtime: choice.runtime } : {}),
      ...(choice?.model ? { model: choice.model } : {}),
    }),
  }).then(json<{ job_id: string; project_id: string }>)

// A model tier as the picker needs it: what it is, what it resolves to today,
// and what that costs in micro-USD per million tokens. The rates come from the
// server: the rate card is the platform's policy, and pricing a run in the
// client would be a second, possibly different, answer.
export type ModelTier = {
  tier: string
  model?: string
  input?: number
  output?: number
  named: boolean
  error?: string
}

// listModels answers what each tier would run on and what it would cost. A
// deployment with no backend still lists the tiers, without prices, so the
// choice can still be offered.
export const listModels = () =>
  fetch('/models').then(json<{ tiers: ModelTier[]; runtimes?: RuntimeOption[]; drafting?: boolean }>)

// One Effort level as a runtime would run it now for the person asking: the
// stored tier, the model its next turn runs on, and that model's price in
// micro-USD per million tokens, absent when it is free or unknown.
export type EffortOption = {
  tier: string
  effort: string
  model?: string
  provider?: string
  input?: number
  output?: number
  error?: string
}

// A runtime a session may run on (048). payer says who pays: the project's
// credits, or the person's own Latere wallet. models lists the exact models a
// person may pick there, and is absent where the runtime offers only levels.
export type RuntimeOption = {
  name: string
  label: string
  default: boolean
  payer: 'project' | 'person'
  efforts: EffortOption[]
  models?: string[]
  error?: string
}

// draftDescription asks the deployment's configured model for a description of
// this project (039). It writes nothing: what comes back is a suggestion, and
// the project's own field changes only when somebody saves it. The project pays
// for the call whether or not they keep the answer.
export const draftDescription = (id: string) =>
  fetch(`/projects/${id}/description/draft`, { method: 'POST' }).then(json<{ description: string; model: string; cost_micro: number }>)

// createProject opens a named project holding its papers, with seeded
// collaborators. It starts no session, so it cannot be refused for money; the
// credit check is startSession's. The status is returned so the caller can tell
// a recorded paper from a reference the server did not recognize.
export async function createProject(
  title: string,
  papers: string[],
  collaborators: string[],
): Promise<{ status: number; data: CreatedProject }> {
  const r = await fetch('/projects', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ title, papers, collaborators }),
  })
  const data = (await r.json().catch(() => ({}))) as CreatedProject
  return { status: r.status, data }
}

// renameProject renames an owned project.
export const renameProject = (id: string, title: string) =>
  fetch(`/projects/${id}`, {
    method: 'PATCH',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ title }),
  }).then(json<Project>)

// describeProject writes what a project is about (038). Empty clears it, which
// is a deliberate edit and not the same as leaving the field alone.
export const describeProject = (id: string, description: string) =>
  fetch(`/projects/${id}`, {
    method: 'PATCH',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ description }),
  }).then(json<Project>)

// cancelSession stops a running session. Owner-only. It returns the resulting
// job, which is failed with a "cancelled" cause.
export const cancelSession = (id: string) => fetch(`/jobs/${id}/cancel`, { method: 'POST' }).then(json<Job>)

// deleteSession removes one finished session from the app (032). Owner-only, and
// refused 409 `session_running` while it is still in flight; stop it first.
//
// It is what clears a run that began publishing and never finished: such a run
// holds a repository with no reproduction record, which blocks its whole project
// from going public and can be resolved no other way. The repository itself is
// made private, not destroyed.
export const deleteSession = (id: string) => fetch(`/jobs/${id}`, { method: 'DELETE' }).then(ok)

// listProjects returns every project the caller may work in, their own and the
// ones they were invited to, newest-modified first, each with a session count
// (018). The session cookie scopes the result server-side.
export const listProjects = () => fetch('/projects').then(json<ProjectView[]>)

// trashProject moves an owned project to the trash. It rejects 409
// `sessions_running` while anything is still in flight there.
export const trashProject = (id: string) => fetch(`/projects/${id}`, { method: 'DELETE' }).then(ok)

// restoreProject takes one back out.
export const restoreProject = (id: string) => fetch(`/projects/${id}/restore`, { method: 'POST' }).then(ok)

// purgeProject removes a trashed project for good. It rejects 409
// `credits_remaining` while its pot still holds money, and `not_trashed` for one
// that was never thrown away; the reversible step cannot be skipped.
export const purgeProject = (id: string) => fetch(`/projects/${id}?purge=1`, { method: 'DELETE' }).then(ok)

// archiveUrl is where a project's whole output is downloaded from (020): one zip
// of every session's committed tree, built server-side from the repositories the
// app already proxies file by file. It is a URL rather than a fetch because the
// browser's own download should handle a body this size; a fetch would buffer
// the archive in the tab before saving it.
export const archiveUrl = (id: string) => `/projects/${id}/archive`

// listTrash returns the caller's own trashed projects, newest-trashed first.
export const listTrash = () => fetch('/projects/trash').then(json<ProjectView[]>)

// getProject returns one project and its sessions, newest first.
export const getProject = (id: string) => fetch(`/projects/${id}`).then(json<ProjectDetail>)

// listPublic returns the public zone: every public project, newest first.
export const listPublic = () => fetch('/public').then(json<ProjectView[]>)

// Project members (owner-only): list, invite by email, remove by email.
export const listMembers = (id: string) => fetch(`/projects/${id}/members`).then(json<Member[]>)
export const addMember = (id: string, email: string) =>
  fetch(`/projects/${id}/members`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ email }),
  }).then(ok)
export const removeMember = (id: string, email: string) =>
  fetch(`/projects/${id}/members/${encodeURIComponent(email)}`, { method: 'DELETE' }).then(ok)

// The caller's own researcher profile. getProfile answers an empty profile
// before one is ever saved, so the settings screen renders the same on first use.
export const getProfile = () => fetch('/me/profile').then(json<Profile>)
export const saveProfile = (p: Profile) =>
  fetch('/me/profile', {
    method: 'PATCH',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(p),
  }).then(json<Profile>)

// A researcher's public page. It needs no sign-in, so a reader who followed a
// link from a published reproduction can look up who signed it. An unknown
// handle and a private profile are the same 404 on purpose.
export const getPublicProfile = (handle: string) =>
  fetch(`/profiles/${encodeURIComponent(handle)}`).then(json<PublicProfile>)

// The current responsibility-attestation text version the sign-off records. v2
// added the signer snapshot and the published contact, so the text a signer
// affirms changed with it (006n).
const ATTESTATION_VERSION = 'v2'

// The signer as the publish wizard confirmed them. The contact is required; the
// rest are optional. The signer's own identity is not here; the server derives
// it from the session, so it cannot be typed in.
export type SignOff = {
  signed_off_name?: string
  signed_off_institution?: string
  signed_off_contact: string
  signed_off_orcid?: string
  signed_off_scholar?: string
}

// makeProjectPublic is the owner's public flip with a recorded sign-off. See
// withdrawProject for the reverse (006o).
// flipRepo, true by default, makes the GitHub repository public too; false
// keeps the repo private while the in-app artifacts still go public. It rejects
// with the HTTP status, so the caller can tell a validation refusal, 400, from
// a project whose tree never cleared the gate, 409.
export const makeProjectPublic = (id: string, signOff: SignOff, flipRepo = true) =>
  fetch(`/projects/${id}/public`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ ...signOff, attestation_version: ATTESTATION_VERSION, flip_repo: flipRepo }),
  }).then(json<Project>)

// withdrawProject ends a publication (006o): the project returns to private and
// every repository it owns is closed on the forge. The sign-off is kept, because
// a withdrawal retracts a publication and does not erase the attestation, so the
// returned project still carries its signed_off_* fields.
export const withdrawProject = (id: string, reason: string) =>
  fetch(`/projects/${id}/private`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ reason }),
  }).then(json<Project>)

// getTranscript returns a session's trajectory and token usage. It rejects with
// the HTTP status: 409 while the run has not published a transcript yet.
export const getTranscript = (id: string) => fetch(`/jobs/${id}/transcript`).then(json<Transcript>)

// getReproduction returns a session's manifest verdict; listArtifacts its
// repository file tree; getArtifact one file's text. They read through the app,
// so they work even when the underlying repository stays private.
export const getReproduction = (id: string) => fetch(`/jobs/${id}/reproduction`).then(json<Reproduction>)

// getFindings returns the run's own verdict.json (021a): the boundary table,
// the decisions, the scorecard, and what the authors released. It is a separate
// call from getReproduction on purpose: that one is the system's manifest, this
// one is the agent's claims, and a session that has one may not have the other.
// A run without findings answers 404, which surfaces as an ApiError the pane
// turns into a stated reason rather than an empty table.
export const getFindings = (id: string) => fetch(`/jobs/${id}/verdict`).then(json<Findings>)
export const listArtifacts = (id: string) => fetch(`/jobs/${id}/artifacts`).then(json<{ files: string[] }>)
export const artifactUrl = (id: string, path: string) => `/jobs/${id}/artifact?path=${encodeURIComponent(path)}`
export const getArtifact = (id: string, path: string) =>
  fetch(`/jobs/${id}/artifact?path=${encodeURIComponent(path)}`).then(async (r) => {
    // An ApiError like every other call, so a caller that asks why can tell a
    // refusal from a network failure. A plain Error would be read as "offline".
    if (!r.ok) throw await refusal(r)
    return r.text()
  })

/* ─── Platform admin (006k) ─── */

// GrantView is one row of the people table: the ACL row, whether it has bound
// to a principal yet, and what their wallet holds in micro-USD.
export type GrantView = {
  email: string
  tier: string
  bound: boolean
  granted_by?: string
  granted_at?: string
  wallet: number
  // frozen is whether this account is in view-only mode: a negative wallet with
  // no admin override (022d), derived the same way the gate derives it.
  frozen?: boolean
}

export type PlatformSettings = {
  // start_reserve is micro-USD: what a project must have available before a
  // session may start there.
  start_reserve: number
  // overdraft_cap is how far below zero a project may fall before a run is
  // stopped. It is admin-only and appears on no surface a project's own members
  // can read, because members who know the floor would plan around it.
  overdraft_cap: number
  rates: RateCard
  // draft is which model writes a project's description when somebody asks for
  // one (039). No credential: that is the deployment's, from its environment.
  draft?: DraftConfig
  // latere_environments are the environments a session on the Latere
  // platform may name (051); known_latere_environments the ones the runtime
  // knows, which the console offers. base is always among the first.
  latere_environments?: string[]
  known_latere_environments?: string[]
  updated_by?: string
  updated_at?: string
}

// The drafting model: which protocol to speak, where, and which model. An empty
// provider is a deployment that does not draft.
type DraftConfig = { provider?: string; base_url?: string; model?: string }

// A model's price, in micro-USD per million tokens, the unit vendors quote, so
// an admin copying a price list types the number they read.
export type ModelRates = { model: string; input: number; output: number; cache_read: number; cache_write_5m: number; cache_write_1h: number }

export type RateCard = { models: ModelRates[] }

// listGrants returns the people table. With negativeOnly it returns only the
// accounts that owe, which are the debts an operator acts on (022d).
export const listGrants = (negativeOnly = false) =>
  fetch('/admin/access' + (negativeOnly ? '?filter=negative' : '')).then(json<GrantView[]>)

// unfreezeAccount grants a one-shot override so a negative-wallet account may
// work again; refreezeAccount clears it (022d). getSolvency reads the platform's
// outstanding sold liability, the sum of all wallets and all pots, which the
// operator watches.
export const unfreezeAccount = (email: string) =>
  fetch(`/admin/access/${encodeURIComponent(email)}/unfreeze`, { method: 'POST' }).then(json<{ email: string; overridden: boolean }>)

export const refreezeAccount = (email: string) =>
  fetch(`/admin/access/${encodeURIComponent(email)}/unfreeze`, { method: 'DELETE' }).then(json<{ email: string; overridden: boolean }>)

export const getSolvency = () => fetch('/admin/solvency').then(json<{ outstanding_micro: number }>)

// grantAccess seeds or promotes by email. The person need not have signed in:
// the grant binds to them on first login.
export const grantAccess = (email: string, tier: 'user' | 'admin') =>
  fetch('/admin/access', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ email, tier }),
  }).then(json<{ email: string; tier: string }>)

export const revokeAccess = (email: string) =>
  fetch(`/admin/access/${encodeURIComponent(email)}`, { method: 'DELETE' }).then(ok)

// topUp adds credits to one person's wallet, in micro-USD. The email need not
// have signed in yet: the wallet is keyed by address, so an admin can fund
// somebody before inviting them.
export const topUp = (email: string, amount: number) =>
  fetch(`/admin/access/${encodeURIComponent(email)}/credits`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ amount }),
  }).then(json<{ email: string; wallet: number }>)

export const getPlatformSettings = () => fetch('/admin/settings').then(json<PlatformSettings>)

export const setPlatformSettings = (s: Pick<PlatformSettings, 'start_reserve' | 'overdraft_cap' | 'rates' | 'draft' | 'latere_environments'>) =>
  fetch('/admin/settings', {
    method: 'PUT',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(s),
  }).then(json<PlatformSettings>)
