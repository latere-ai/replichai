// The API client. Every call is same-origin, so the session cookie (or the dev
// bypass) rides along automatically.

// Quota is what this person has spent and may spend on replication sessions
// (006l). Completed and in-flight sessions count; a failed or stopped one is
// refunded. Absent for an ungranted principal.
export type Quota = { used: number; limit: number; unlimited: boolean }

export type Me = {
  authenticated: boolean
  principal?: string
  email?: string
  tier?: string
  quota?: Quota
  // Display claims from the IdP (006m): both optional, both purely cosmetic —
  // the email stays the identifier everything binds on.
  name?: string
  picture?: string
}

export type Usage = { InputTokens: number; OutputTokens: number; CachedInputTokens: number }

export type Job = {
  id: string
  kind: string
  ref: string
  phase: string
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
  // sponsored is the owner's standing consent to cover their collaborators'
  // sessions (006l): with it on, a collaborator may charge the owner instead of
  // themselves.
  sponsored?: boolean
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
}

export type ProjectView = Project & { session_count: number; running: number; latest_phase?: string }

// may_submit is the server's answer to "may this caller open a session here" —
// owner or collaborator. A public project is readable by everyone, so the
// client cannot infer it (006l).
export type ProjectDetail = { project: Project; sessions: Job[]; may_submit: boolean }

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

async function json<T>(r: Response): Promise<T> {
  if (!r.ok) throw new Error(`${r.status}`)
  return r.json() as Promise<T>
}

// ok resolves for a 2xx response with no body (204), rejecting with the status.
async function ok(r: Response): Promise<void> {
  if (!r.ok) throw new Error(`${r.status}`)
}

export const getMe = () => fetch('/me').then(json<Me>)

// chargeTo picks whose quota a session spends: the submitter, or the owner of a
// project they have chosen to sponsor. The server resolves the actual payer —
// this only names which of the two.
export type ChargeTo = 'me' | 'project'

// submitPaper opens a replication session: with no project it creates a fresh
// one; with a project the caller owns or collaborates on, it adds a session. It
// returns both ids.
export const submitPaper = (url: string, project = '', chargeTo: ChargeTo = 'me') =>
  fetch('/papers', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ url, project, charge_to: chargeTo }),
  }).then(json<{ job_id: string; project_id: string }>)

// sponsorProject records or withdraws the owner's standing consent to cover
// their collaborators' sessions (006l). Owner-only.
export const sponsorProject = (id: string, on: boolean) =>
  fetch(`/projects/${id}/sponsor`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ on }),
  }).then(json<Project>)

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

// makeProjectPublic is the owner's one-way public flip with a recorded sign-off.
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
  fetch(`/jobs/${id}/artifact?path=${encodeURIComponent(path)}`).then((r) => {
    if (!r.ok) throw new Error(`${r.status}`)
    return r.text()
  })

/* ─── Platform admin (006k) ─── */

// GrantView is one row of the people table: the ACL row, whether it has bound
// to a principal yet, and any quota headroom above the platform default.
export type GrantView = {
  email: string
  tier: string
  bound: boolean
  granted_by?: string
  granted_at?: string
  topup?: number
  used: number
  limit: number
  unlimited: boolean
}

export type PlatformSettings = {
  default_session_quota: number
  updated_by?: string
  updated_at?: string
}

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

// setTopup gives one person headroom above the platform default; 0 removes it.
export const setTopup = (email: string, extra: number) =>
  fetch(`/admin/access/${encodeURIComponent(email)}/quota`, {
    method: 'PUT',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ extra }),
  }).then(json<{ email: string; extra: number }>)

export const getPlatformSettings = () => fetch('/admin/settings').then(json<PlatformSettings>)

export const setPlatformSettings = (s: Pick<PlatformSettings, 'default_session_quota'>) =>
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
