import type { Job, ProjectPaper } from './api'

// What a project is made of, joined. A project holds papers (012) and a paper
// has sessions; the two are related by the offline paper key, which the paper
// row is keyed on and the job carries in `paper_key`. Nothing else joins them —
// there is no foreign key, by design, so a paper outlives every run against it.
//
// This module exists because the screens that report results used to derive one
// session for the whole project and render it under the project's own name
// (015). A join that is a function is a join that can be tested; a join written
// inline in JSX is how the previous one went unnoticed.
export type PaperGroup = {
  paper: ProjectPaper
  sessions: Job[]
}

export type Grouped = {
  papers: PaperGroup[]
  // Sessions whose paper the project no longer holds. A paper with no runs is
  // removable (012), and a job's key is a string rather than a reference, so a
  // run can outlive the row it was started from. It is still a run this project
  // paid for, so it is listed rather than dropped.
  other: Job[]
}

// groupByPaper puts every session under the paper it targeted, keeping the
// papers in the order given and each paper's sessions in the order the project
// listed them (newest first, as the job store returns them).
export function groupByPaper(papers: ProjectPaper[], sessions: Job[]): Grouped {
  const held = new Map<string, Job[]>()
  for (const p of papers) held.set(p.key, [])
  const other: Job[] = []
  for (const s of sessions) {
    const bucket = s.paper_key ? held.get(s.paper_key) : undefined
    if (bucket) bucket.push(s)
    else other.push(s)
  }
  return { papers: papers.map((p) => ({ paper: p, sessions: held.get(p.key) ?? [] })), other }
}

// isGradeable reports whether a session produced a result worth reading a
// verdict for: it finished, and it has a repository to read the manifest from.
//
// Both halves are load-bearing. A repository name alone means publishing
// *began* — `repo_name` is set as the phase opens, and `publishing → failed` is
// a legal transition — so a half-written repository carries one too. And a
// failed session is terminal, so "terminal" is not the test either. The line
// this replaced tested the repository alone, and could present a run that failed
// mid-publish as the project's result.
export const isGradeable = (j: Job): boolean => j.phase === 'done' && !!j.repo_name

// gradeable is every session of a project that has a verdict to show, newest
// first within each paper, flattened with the paper it belongs to — the outcome
// view's rows.
export function gradeable(grouped: Grouped): { paper: ProjectPaper; session: Job }[] {
  const rows: { paper: ProjectPaper; session: Job }[] = []
  for (const g of grouped.papers) for (const s of g.sessions) if (isGradeable(s)) rows.push({ paper: g.paper, session: s })
  return rows
}
