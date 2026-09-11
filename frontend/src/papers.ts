// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import type { Job, PaperCitation, ProjectPaper } from './api'

// What a project is made of, joined. A project holds papers (012) and a paper
// has sessions; the two are related by the offline paper key, which the paper
// row is keyed on and the job carries in `paper_key`. Nothing else joins them:
// there is no foreign key, by design, so a paper outlives every run against it.
//
// This module exists because the screens that report results used to derive one
// session for the whole project and render it under the project's own name
// (015). The join is a function so it can be tested; the previous one was
// written inline in JSX, which is why the defect went unnoticed.
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
// listed them, newest first, as the job store returns them.
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
// Both halves are needed. A repository name alone means only that publishing
// began: `repo_name` is set as the phase opens, and `publishing → failed` is a
// legal transition, so a half-written repository carries one too. A failed
// session is terminal, so "terminal" is not the test either. The line
// this replaced tested the repository alone, and could present a run that failed
// mid-publish as the project's result.
export const isGradeable = (j: Job): boolean => j.phase === 'done' && !!j.repo_name

// gradeable is every session of a project that has a verdict to show, newest
// first within each paper, flattened with the paper it belongs to. These are
// the outcome view's rows.
export function gradeable(grouped: Grouped): { paper: ProjectPaper; session: Job }[] {
  const rows: { paper: ProjectPaper; session: Job }[] = []
  for (const g of grouped.papers) for (const s of g.sessions) if (isGradeable(s)) rows.push({ paper: g.paper, session: s })
  return rows
}

// citationBits is a bibliography as a reader scans it: who wrote it, where it
// appeared, and when, in that order, each part omitted when the record does not
// declare it. The caller joins them. Three surfaces render the same three facts,
// the project screen, the registry and the landing strip, and they must not
// drift.
//
// Three authors then "et al.", because a fifteen-author paper would otherwise be
// the whole row. The label is passed in rather than translated here: this is a
// pure function, and the only language-dependent part of a citation is the word
// standing in for the authors it dropped.
//
// An empty result is returned for a record nobody could fill in. What the
// caller does with it differs by surface: the project screen invites its owner
// to complete it; the registry, whose reader can do nothing about it, renders
// nothing.
export function citationBits(r: CitationSource | undefined, etAl: string): string[] {
  const bits: string[] = []
  if (r?.authors?.length) {
    bits.push(r.authors.length > 3 ? `${r.authors.slice(0, 3).join(', ')} ${etAl}` : r.authors.join(', '))
  }
  if (r?.venue) bits.push(r.venue)
  if (r?.year) bits.push(String(r.year))
  return bits
}

// CitationSource is the little of a paper a citation line reads. Both shapes
// that carry it satisfy this: the full PaperRecord a project screen holds, and
// the trimmed PaperCitation a list is sent.
type CitationSource = { authors?: string[]; venue?: string; year?: number }

// listCitation is what one row of a list says about the papers behind it: the
// first paper's citation, and how many others are filed under the same project.
//
// The first paper rather than a merged line, because a citation names one
// paper's authors and merging two would attribute one's work to the other. The
// count states the remainder; a reader who wants the rest opens the project,
// which the row links to.
//
// An unfillable first record yields no bits and the row renders nothing, count
// included: "+1 more" under a line that never appeared reads as a broken card,
// not as a fact.
export function listCitation(papers: PaperCitation[] | undefined, etAl: string): { bits: string[]; more: number } {
  const bits = citationBits(papers?.[0], etAl)
  return { bits, more: bits.length === 0 ? 0 : Math.max(0, (papers?.length ?? 0) - 1) }
}
