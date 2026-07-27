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
}

export type Reproduction = { id: string; repo: string; title: string; verdict: string }

// A project is the container a user owns; it holds one or more replication
// sessions (each is a Job). The list carries a session count so the projects
// view needs no per-project read.
export type Project = {
  id: string
  owner: string
  title: string
  visibility: 'private' | 'public'
  created_at: string
}

export type ProjectView = Project & { session_count: number }

export type ProjectDetail = { project: Project; sessions: Job[] }

// One entry in a session's trajectory — an agent message, its thinking, a tool
// call, a tool result, or the grade.
export type Event = {
  role: string
  kind: string
  tool?: string
  content?: string
  time?: string
}

export type Transcript = { events: Event[]; usage: Usage | null }

async function json<T>(r: Response): Promise<T> {
  if (!r.ok) throw new Error(`${r.status}`)
  return r.json() as Promise<T>
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

// listProjects returns the caller's own projects, newest first, each with a
// session count. The session cookie scopes the result server-side.
export const listProjects = () => fetch('/projects').then(json<ProjectView[]>)

// getProject returns one project and its sessions, newest first.
export const getProject = (id: string) => fetch(`/projects/${id}`).then(json<ProjectDetail>)

// getTranscript returns a session's trajectory and token usage. It rejects with
// the HTTP status: 409 while the run has not published a transcript yet.
export const getTranscript = (id: string) => fetch(`/jobs/${id}/transcript`).then(json<Transcript>)

export const listReproductions = () => fetch('/reproductions').then(json<Reproduction[]>)

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
