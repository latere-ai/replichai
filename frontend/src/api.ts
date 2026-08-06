// The API client. Every call is same-origin, so the session cookie (or the dev
// bypass) rides along automatically.

// Credits is what this person has to spend (011), in micro-USD. The wallet is
// theirs; it is never spent by a session directly, because a run draws on the
// pot of the project it belongs to. start_reserve is what a project must have
// available before a session may start there — public so the client can explain
// the refusal before it happens. Absent for an ungranted principal.
export type Credits = { wallet: number; start_reserve: number }

export type Me = {
  authenticated: boolean
  principal?: string
  email?: string
  tier?: string
  credits?: Credits
  // Display claims from the IdP (006m): both optional, both purely cosmetic —
  // the email stays the identifier everything binds on.
  name?: string
  picture?: string
}

// Usage is what a run consumed, in tokens, by priced class. Cache writes come
// split by the lifetime they bought where the backend reports it, and in
// CacheWriteTokens where it does not (a per-request span). A backend that
// reports none of a class leaves it absent, so every field is optional.
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
  // What this session has cost so far, in micro-USD — the same number its
  // project is charged when it ends.
  cost?: number
  status?: { State: string; Usage: Usage }
  repo_name?: string
  error?: string
  created_at?: string
  // phase_at records when the session entered each phase — the timeline's spine.
  phase_at?: Record<string, string>
}

// A project is the container a user owns; it holds one or more replication
// sessions (each is a Job). The list carries a session count so the projects
// view needs no per-project read.
export type Project = {
  id: string
  owner: string
  title: string
  visibility: 'private' | 'public'
  created_at: string
  signed_off_by?: string
  signed_off_at?: string
  attestation_version?: string
  // The signer, as they were when they signed (006n): a snapshot recorded at the
  // flip, not a view of their profile — so these never change afterwards. Empty
  // on a v1 sign-off, which predates them.
  signed_off_name?: string
  signed_off_institution?: string
  signed_off_contact?: string
  signed_off_orcid?: string
  signed_off_scholar?: string
  // Already redacted server-side; the signer's full address never reaches here.
  signed_off_email?: string
  // The retraction (006o), absent until a publication is withdrawn. It sits
  // beside the sign-off rather than replacing it, so a project can carry both —
  // read `visibility` to ask whether it is open, never `signed_off_at`.
  withdrawn_at?: string
  withdrawn_by?: string
  withdrawn_reason?: string
}

// A researcher profile: who a sign-off is published under. Self-asserted and
// unverified, always the caller's own, and only ever a default — the publish
// wizard prefills from it and records what the owner confirms.
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
// here — that is what the redacted signer email on an attestation is for.
export type PublicProfile = {
  handle: string
  display_name?: string
  institution?: string
  contact_email?: string
  orcid?: string
  scholar_url?: string
  projects: PublicSigned[]
}

export type PublicSigned = { id: string; title: string; signed_off_at?: string }

// signer_handle is where the signer's name links (006r). It is resolved on
// every read from the signer's current profile and never snapshotted — so a
// profile going private removes the link and changes nothing on the record.
export type ProjectView = Project & {
  session_count: number
  running: number
  latest_phase?: string
  signer_handle?: string
}

// may_submit is the server's answer to "may this caller open a session here" —
// owner or collaborator. A public project is readable by everyone, so the
// client cannot infer it (006l).
export type ProjectDetail = { project: Project; sessions: Job[]; may_submit: boolean; signer_handle?: string; credits?: ProjectCredits }

// ProjectCredits is a project's money as a member reads it, in micro-USD.
// balance is what the pot holds; available is what is still free to commit —
// they differ by the sessions running right now, each holding the start
// reserve, which is why a project with credits can still refuse a submit.
// Absent for anyone who may not work here.
//
// may_reclaim is the server's answer: only a lead may take credits back out.
// The client cannot work it out — it does not know roles.
export type ProjectCredits = { balance: number; available: number; start_reserve: number; may_reclaim: boolean }

// Wallet is one person's own credits and the entries that produced them. The
// statement is the point: a balance nobody can account for is what the ledger
// exists to prevent.
export type Wallet = { balance: number; entries: LedgerEntry[] }

export type LedgerEntry = {
  id: string
  holder: string
  amount: number
  kind: 'topup' | 'allocate' | 'reclaim' | 'hold' | 'release' | 'charge' | 'adjust'
  job_id?: string
  actor: string
  created_at: string
}

export const getWallet = () => fetch('/me/credits').then(json<Wallet>)

// allocateCredits moves credits from your wallet into a project's pot; any
// member may. reclaimCredits moves them back, and only a lead may — the pot
// does not remember who filled it. Both answer with both balances, because both
// moved and a client re-reading them separately could show a moment where the
// money is in neither.
export type CreditMove = { wallet: number; project: ProjectCredits | null }

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

// A project member: the seeded email, the role (1 = collaborator, 2 = lead), and
// whether they have signed in yet (bound).
export type Member = { email: string; role: number; bound: boolean }

// One entry in a session's trajectory — an agent message, its thinking, a tool
// call, a tool result, or the grade.
export type Event = {
  role: string
  kind: string
  tool?: string
  content?: string
  time?: string
  // What the backend charged for this one request (006s). Absent on the events
  // that were charged nothing, which is most of them — so it is rendered only
  // where it exists, never as a zero.
  usage?: Usage
}

// live is true when these events came from an in-flight run's live stream, so
// the client keeps polling for more.
export type Transcript = { events: Event[]; usage: Usage | null; live: boolean }

// A session's manifest summary — the reproduction verdict and metadata, read
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

// ApiError carries both halves of a refusal: the status, and the machine-
// readable code the server put in the body.
//
// The message stays the bare status, because that is what every existing caller
// compares against. The code is what a caller needs when one status means more
// than one thing — a 409 on the public flip is either an unclear tree or a
// profile that is not public, and those want different remedies.
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code = '',
  ) {
    super(String(status))
  }
}

// errCode reads the server's error code off a rejection, empty when there was
// none (a network failure, or a response with no body).
export function errCode(e: unknown): string {
  return e instanceof ApiError ? e.code : ''
}

// refusal builds an ApiError, reading the body's `error` field when there is
// one. The body is consumed either way, so a failed response never leaks a
// stream.
async function refusal(r: Response): Promise<ApiError> {
  let code = ''
  try {
    const body = (await r.json()) as { error?: string }
    code = body?.error ?? ''
  } catch {
    /* no body, or not JSON: the status is all we have */
  }
  return new ApiError(r.status, code)
}

async function json<T>(r: Response): Promise<T> {
  if (!r.ok) throw await refusal(r)
  return r.json() as Promise<T>
}

// ok resolves for a 2xx response with no body (204), rejecting with the status.
async function ok(r: Response): Promise<void> {
  if (!r.ok) throw await refusal(r)
}

export const getMe = () => fetch('/me').then(json<Me>)

// submitPaper opens a replication session: with no project it creates a fresh
// one; with a project the caller owns or collaborates on, it adds a session. It
// returns both ids. What pays is always the project's own pot, so there is
// nothing here to choose.
export const submitPaper = (url: string, project = '') =>
  fetch('/papers', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ url, project }),
  }).then(json<{ job_id: string; project_id: string }>)

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

// One paper's fate in a create: a job id when it started, or an error code.
export type SessionResult = { url: string; job_id?: string; error?: string }
export type CreatedProject = { project_id?: string; title?: string; sessions?: SessionResult[]; members?: string[]; error?: string }

// createProject opens a named project with one session per paper and seeded
// collaborators (006j). It returns the HTTP status alongside the body, so the
// caller can surface per-paper results whether the create fully or partly
// succeeded (201) or every paper failed (422).
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

// cancelSession stops a running session (owner-only). It returns the resulting
// job — failed with a "cancelled" cause.
export const cancelSession = (id: string) => fetch(`/jobs/${id}/cancel`, { method: 'POST' }).then(json<Job>)

// listProjects returns the caller's own projects, newest first, each with a
// session count. The session cookie scopes the result server-side.
export const listProjects = () => fetch('/projects').then(json<ProjectView[]>)

// getProject returns one project and its sessions, newest first.
export const getProject = (id: string) => fetch(`/projects/${id}`).then(json<ProjectDetail>)

// listPublic returns the public zone: every public project, newest first.
export const listPublic = () => fetch('/public').then(json<ProjectView[]>)

// listShared returns projects the caller is a collaborator on (not their own).
export const listShared = () => fetch('/shared').then(json<ProjectView[]>)

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

// A researcher's public page. Open — no sign-in — so a reader who followed a
// link from a published reproduction can look up who signed it. An unknown
// handle and a private profile are the same 404 on purpose.
export const getPublicProfile = (handle: string) =>
  fetch(`/profiles/${encodeURIComponent(handle)}`).then(json<PublicProfile>)

// The current responsibility-attestation text version the sign-off records. v2
// added the signer snapshot and the published contact, so the text a signer
// affirms changed with it (006n).
export const ATTESTATION_VERSION = 'v2'

// The signer as the publish wizard confirmed them. The contact is required; the
// rest are optional. The signer's own identity is not here — the server derives
// it from the session, so it cannot be typed in.
export type SignOff = {
  signed_off_name?: string
  signed_off_institution?: string
  signed_off_contact: string
  signed_off_orcid?: string
  signed_off_scholar?: string
}

// makeProjectPublic is the owner's public flip with a recorded sign-off. See
// withdrawProject for the way back out (006o).
// flipRepo opts the GitHub repository into the flip (default); false keeps the
// repo private while the in-app artifacts still go public. It rejects with the
// HTTP status, so the caller can tell a validation refusal (400) from a project
// whose tree never cleared the gate (409).
export const makeProjectPublic = (id: string, signOff: SignOff, flipRepo = true) =>
  fetch(`/projects/${id}/public`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ ...signOff, attestation_version: ATTESTATION_VERSION, flip_repo: flipRepo }),
  }).then(json<Project>)

// withdrawProject ends a publication (006o): the project returns to private and
// every repository it owns is closed on the forge. The sign-off is kept — a
// withdrawal retracts a publication, it does not erase the attestation — so the
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
// repository file tree; getArtifact one file's text — the in-app pass-through
// that works even when the underlying repository stays private.
export const getReproduction = (id: string) => fetch(`/jobs/${id}/reproduction`).then(json<Reproduction>)
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
}

export type PlatformSettings = {
  // start_reserve is micro-USD: what a project must have available before a
  // session may start there.
  start_reserve: number
  // overdraft_cap is how far below zero a project may fall before a run is
  // stopped. Admin-only — it appears here and on no surface a project's own
  // members can read, because a floor somebody knows is one they plan around.
  overdraft_cap: number
  rates: RateCard
  updated_by?: string
  updated_at?: string
}

// A model's price, in micro-USD per million tokens — the unit vendors quote, so
// an admin copying a price list types the number they read.
export type ModelRates = { model: string; input: number; output: number; cache_read: number; cache_write_5m: number; cache_write_1h: number }

export type RateCard = { models: ModelRates[] }

export const listGrants = () => fetch('/admin/access').then(json<GrantView[]>)

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
// have signed in yet — the wallet is keyed by address, which is what lets an
// admin fund somebody before inviting them.
export const topUp = (email: string, amount: number) =>
  fetch(`/admin/access/${encodeURIComponent(email)}/credits`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ amount }),
  }).then(json<{ email: string; wallet: number }>)

export const getPlatformSettings = () => fetch('/admin/settings').then(json<PlatformSettings>)

export const setPlatformSettings = (s: Pick<PlatformSettings, 'start_reserve' | 'overdraft_cap' | 'rates'>) =>
  fetch('/admin/settings', {
    method: 'PUT',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(s),
  }).then(json<PlatformSettings>)

const terminal = (phase: string) => phase === 'done' || phase === 'failed'

// watchJob streams a job's progress over SSE, calling onJob on each update, and
// closes the stream once the job reaches a terminal phase. It returns a cancel
// function.
export function watchJob(id: string, onJob: (j: Job) => void): () => void {
  const es = new EventSource(`/jobs/${id}/events`)
  es.addEventListener('status', (e) => {
    try {
      const j = JSON.parse((e as MessageEvent).data) as Job
      onJob(j)
      if (terminal(j.phase)) es.close()
    } catch {
      /* ignore a malformed frame */
    }
  })
  return () => es.close()
}
