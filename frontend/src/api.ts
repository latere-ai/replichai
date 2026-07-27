// The API client. Every call is same-origin, so the session cookie (or the dev
// bypass) rides along automatically.

export type Me = {
  authenticated: boolean
  principal?: string
  email?: string
  tier?: string
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
}

export type ProjectView = Project & { session_count: number; running: number; latest_phase?: string }

export type ProjectDetail = { project: Project; sessions: Job[] }

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

// submitPaper opens a replication session: with no project it creates a fresh
// one; with a project the caller owns it adds a session. It returns both ids.
export const submitPaper = (url: string, project = '') =>
  fetch('/papers', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ url, project }),
  }).then(json<{ job_id: string; project_id: string }>)

export const getJob = (id: string) => fetch(`/jobs/${id}`).then(json<Job>)

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

// The current responsibility-attestation text version the sign-off records.
export const ATTESTATION_VERSION = 'v1'

// makeProjectPublic is the owner's one-way public flip with a recorded sign-off.
// flipRepo opts the GitHub repository into the flip (default); false keeps the
// repo private while the in-app artifacts still go public.
export const makeProjectPublic = (id: string, flipRepo = true) =>
  fetch(`/projects/${id}/public`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ attestation_version: ATTESTATION_VERSION, flip_repo: flipRepo }),
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
