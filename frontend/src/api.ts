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

async function json<T>(r: Response): Promise<T> {
  if (!r.ok) throw new Error(`${r.status}`)
  return r.json() as Promise<T>
}

export const getMe = () => fetch('/me').then(json<Me>)

export const submitPaper = (url: string, project = '') =>
  fetch('/papers', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ url, project }),
  }).then(json<{ job_id: string }>)

export const getJob = (id: string) => fetch(`/jobs/${id}`).then(json<Job>)

// listMyJobs returns the caller's own jobs (projects), newest first. The session
// cookie scopes the result to the signed-in principal server-side.
export const listMyJobs = () => fetch('/jobs').then(json<Job[]>)

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
