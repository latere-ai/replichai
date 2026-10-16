// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// The transcript pane of the session screen (006s): the run's events as cards
// against their own clock, the scrubber and lifecycle strip above them, the
// filter rail beside them, and the inspector that shows one event in full. The
// arithmetic it draws from lives in transcript.ts, where it is tested without
// a browser.

import { useCallback, useDeferredValue, useEffect, useMemo, useRef, useState } from 'react'
import { type Event, getTranscript, type Job, type Transcript, type Usage } from '../api'
import { countOf, type I18n, type Lang, type TKey } from '../i18n'
import { clockTime, formatCount } from '../locale'
import {
  cacheWrittenOf,
  clockOf,
  compactTokens,
  freshOf,
  histogram,
  matches,
  offsetOf,
  PHASE_ORDER,
  phaseBands,
  runSpan,
  seekTarget,
  type Span,
  splitTitle,
  timeOf,
  totalOf,
} from '../transcript'
import { Ico, type IconName } from '../icons'
import { Disclosure, RowButton, Toggle } from '../ui'
import { duration } from '../when'
import { terminal } from '../session-stop'
import { phaseClass, phaseLabel } from '../phase'

// plumbingKinds are backend lifecycle ticks, hidden by default.
const plumbingKinds = new Set(['status_idle', 'status_running', 'thread_status_idle', 'thread_status_running', 'model_request_start', 'model_request_end'])

// Card is an event with its place in the whole transcript. The place is the
// event's identity on screen: cards were keyed by their position in the
// filtered list, so a search that dropped one card handed its neighbor's open
// disclosure to whichever event moved into that position.
type Card = { e: Event; n: number }
// gradeKinds is the whole outcome family, which the filter rail folds into one
// row: a reader wants "the grading" as a thing, not four of the backend's
// internal steps (006s).
const gradeKinds = new Set([
  'grade',
  'define_outcome',
  'outcome_evaluation_start',
  'outcome_evaluation_ongoing',
  'outcome_evaluation_end',
])

// SessionTranscript is the run's trajectory in its own time (006s).
export function SessionTranscript({ i18n, jobId, job }: { i18n: I18n; jobId: string; job: Job | null }) {
  const { t } = i18n
  const [tr, setTr] = useState<Transcript | null>(null)
  const [state, setState] = useState<'loading' | 'ready' | 'not_ready' | 'error'>('loading')
  const [hidePlumbing, setHidePlumbing] = useState(true)
  const [off, setOff] = useState<Set<string>>(new Set())
  const [q, setQ] = useState('')
  // The query is deferred so that typing stays responsive on a transcript of
  // several hundred events: the input updates now, the list re-filters when
  // React has room.
  const dq = useDeferredValue(q)
  // Which card the inspector is showing, by the event's place in the whole
  // transcript. It is cleared whenever the filters move, because what it showed
  // may no longer be on screen.
  const [sel, setSel] = useState<number | null>(null)
  const streamRef = useRef<HTMLElement>(null)

  // The job's phase_at feeds the timeline, and its terminal phase tells the
  // transcript poll when to stop chasing a run that will never publish. Read
  // through a ref so the poll does not restart on every observation.
  const jobRef = useRef<Job | null>(job)
  useEffect(() => {
    jobRef.current = job
  }, [job])

  // Poll the transcript. While the run is live, or not yet ready but still in
  // flight, keep re-fetching so an in-flight session's trajectory streams in
  // real time rather than only after it publishes.
  useEffect(() => {
    let alive = true
    let timer: ReturnType<typeof setTimeout> | undefined
    const again = () => {
      timer = setTimeout(poll, 4000)
    }
    const poll = () => {
      getTranscript(jobId)
        .then((data) => {
          if (!alive) return
          setTr(data)
          setState('ready')
          if (data.live) again() // an in-flight run; keep streaming
        })
        .catch((e: Error) => {
          if (!alive) return
          if (e.message === '409') {
            setState('not_ready')
            // A session may observe a run id or publish shortly; keep polling
            // until it reaches a terminal phase with still no transcript.
            if (!jobRef.current || !terminal(jobRef.current.phase)) again()
          } else {
            setState('error')
          }
        })
    }
    poll()
    return () => {
      alive = false
      if (timer) clearTimeout(timer)
    }
  }, [jobId])

  // The span the scrubber draws: the session's extent, widened to cover its
  // events. A transcript whose events carry no timestamps and a session with no
  // recorded phases leave no clock at all, and the lifecycle strip stands in.
  const span = useMemo(() => (tr ? runSpan(tr.events, job?.phase_at) : null), [tr, job?.phase_at])

  // seek scrolls the stream to the first rendered card at or after an instant.
  // The list it searches is what the filters left standing, so scrubbing never
  // aims at an event that is not on screen. Positions come off the DOM rather
  // than from tracked heights, because a card is as tall as its content and
  // nothing here knows that in advance.
  const seek = useCallback((at: number, list: Card[]) => {
    const stream = streamRef.current
    if (!stream) return
    const hit = list.find(({ e }) => {
      const t = timeOf(e)
      return !Number.isNaN(t) && t >= at
    })
    if (!hit) return
    const card = stream.querySelector<HTMLElement>(`.event[data-i="${hit.n}"]`)
    if (card) stream.scrollTo({ top: card.offsetTop - stream.offsetTop - 8, behavior: 'smooth' })
  }, [])

  if (state !== 'ready' || !tr) {
    // A non-terminal session has not failed to produce a transcript; it has not
    // emitted events yet. Show its live timeline and wait, rather than the "not
    // ready" state reserved for terminal sessions with no transcript.
    const waitingLive = state === 'not_ready' && job && !terminal(job.phase)
    return (
      <>
        {waitingLive && (
          <div className="tx-head">
            <span className="live-dot">{t('liveStreaming')}</span>
          </div>
        )}
        {job && <Timeline i18n={i18n} job={job} />}
        <div className="center-load">
          {waitingLive ? t('transcriptWaitingLive') : state === 'not_ready' ? t('transcriptNotReady') : state === 'error' ? t('transcriptError') : '…'}
        </div>
      </>
    )
  }

  // Real counts by kind, and tools used, never fabricated. The counts describe
  // what the search left standing, so the rail always adds up to the stream.
  const shown: Card[] = tr.events.map((e, n) => ({ e, n })).filter(({ e }) => !(hidePlumbing && plumbingKinds.has(e.kind)) && matches(e, dq))
  const kindCounts = new Map<string, number>()
  const toolCounts = new Map<string, number>()
  for (const { e } of shown) {
    kindCounts.set(displayKind(e.kind), (kindCounts.get(displayKind(e.kind)) ?? 0) + 1)
    if (e.kind === 'tool_use' && e.tool) toolCounts.set(e.tool, (toolCounts.get(e.tool) ?? 0) + 1)
  }
  const visible = shown.filter(({ e }) => !off.has(displayKind(e.kind)))
  const usage = tr.usage
  const picked = sel !== null ? visible.find((c) => c.n === sel)?.e : undefined

  return (
    <>
      <div className="tx-head">
        <div className="tx-head-meta">
          <span className="tx-head-ref">
            {t('transcript')}
            {tr.live && <span className="live-dot">{t('liveStreaming')}</span>}
          </span>
          <span className="tx-head-sub">{countOf(t, 'eventsCounted', tr.events.length)}</span>
        </div>
        <div className="tx-stats">
          {usage && (
            <div className="tx-stat">
              <span className="tx-stat-label">
                <Ico name="bolt" size={11} /> {t('usageTotal')}
              </span>
              <span className="tx-stat-value">{formatCount(i18n.lang, totalOf(usage))}</span>
            </div>
          )}
          <div className="tx-stat">
            <span className="tx-stat-label">
              <Ico name="layers" size={11} /> {t('events')}
            </span>
            <span className="tx-stat-value">{formatCount(i18n.lang, tr.events.length)}</span>
          </div>
        </div>
      </div>

      {/* The run's own clock when the events carry one; the lifecycle strip when
          they do not, so a transcript without timestamps still says where the
          session got to. */}
      {span ? (
        <Scrubber i18n={i18n} span={span} events={tr.events} job={job} seekList={visible} onSeek={seek} />
      ) : (
        job && <Timeline i18n={i18n} job={job} />
      )}

      <div className="tx-body">
        <aside className="tx-side">
          <div>
            <div className="eyebrow eyebrow-ico" style={{ marginBottom: 8 }}>
              <Ico name="search" size={11} /> {t('search')}
            </div>
            <input
              className="text tx-search"
              type="search"
              value={q}
              placeholder={t('searchTranscript')}
              aria-label={t('searchTranscript')}
              onChange={(ev) => {
                setQ(ev.target.value)
                setSel(null) // what the inspector showed may no longer be on screen
              }}
            />
          </div>
          <div>
            <div className="eyebrow eyebrow-ico" style={{ marginBottom: 8 }}>
              <Ico name="filter" size={11} /> {t('showEvents')}
            </div>
            {[...kindCounts.entries()].map(([kind, n]) => (
              <button
                key={kind}
                className={'kind-btn' + (off.has(kind) ? ' off' : '')}
                aria-pressed={!off.has(kind)}
                onClick={() => {
                  setOff((s) => toggle(s, kind))
                  setSel(null)
                }}
              >
                <span className="sq" style={{ color: kindColor(kind) }} />
                <span>{kindLabel(t, kind)}</span>
                <span className="n tabular">{n}</span>
              </button>
            ))}
            {kindCounts.size === 0 && (
              <p className="muted small" style={{ margin: '2px 8px' }}>
                {t('searchNoMatch')}
              </p>
            )}
          </div>
          {toolCounts.size > 0 && (
            <div>
              <div className="eyebrow eyebrow-ico" style={{ marginBottom: 8, color: 'var(--warn)' }}>
                <Ico name="wrench" size={11} /> {t('toolsUsed')}
              </div>
              {[...toolCounts.entries()].sort((a, b) => b[1] - a[1]).map(([name, n]) => (
                <div key={name} className="kind-btn" style={{ cursor: 'default' }}>
                  <span className="mono small">{name}</span>
                  <span className="n tabular">{n}</span>
                </div>
              ))}
            </div>
          )}
          <Toggle
            on={hidePlumbing}
            onChange={(v) => {
              setHidePlumbing(v)
              setSel(null)
            }}
          >
            {t('hideLifecycle')}
          </Toggle>
        </aside>

        <main className="tx-stream" ref={streamRef}>
          {visible.map(({ e, n }) => (
            <EventCard key={n} i18n={i18n} e={e} i={n} span={span} on={n === sel} onPick={() => setSel(n === sel ? null : n)} />
          ))}
        </main>

        <aside className="tx-side tx-inspector">
          {picked && <EventDetail i18n={i18n} e={picked} span={span} onClose={() => setSel(null)} />}
          <div>
            <div className="eyebrow eyebrow-ico" style={{ marginBottom: 10, color: 'var(--brand)' }}>
              <Ico name="bolt" size={11} /> {t('usage')}
            </div>
            {usage ? (
              <div className="usage-rows">
                <UsageLine lang={i18n.lang} label={t('usageInput')} v={usage.InputTokens} tone="var(--run)" />
                <UsageLine lang={i18n.lang} label={t('usageOutput')} v={usage.OutputTokens} tone="var(--pub)" />
                <UsageLine lang={i18n.lang} label={t('usageCached')} v={usage.CachedInputTokens} tone="var(--ok)" />
                <UsageLine lang={i18n.lang} label={t('usageCacheWritten')} v={cacheWrittenOf(usage)} tone="var(--warn)" />
                <UsageLine lang={i18n.lang} label={t('usageTotal')} v={totalOf(usage)} tone="var(--brand)" strong />
              </div>
            ) : (
              <p className="muted small" style={{ margin: 0 }}>
                {t('usageNotRecorded')}
              </p>
            )}
          </div>
        </aside>
      </div>
    </>
  )
}

// Scrubber draws the run against its own clock: the lifecycle phases at the
// width their duration gives them, a histogram of where the run spent its time
// or tokens, and a cursor that scrolls the stream to a moment.
//
// It never filters. Dimming the events outside the cursor would mean a style
// recomputed on every one of several hundred cards per pointer frame, and
// scrolling to the moment is what a reader wants anyway.
function Scrubber({
  i18n,
  span,
  events,
  job,
  seekList,
  onSeek,
}: {
  i18n: I18n
  span: Span
  events: Event[]
  job: Job | null
  // seekList is what the filters left standing, the same list the seek
  // resolves against, so the cursor never points at a card that is not on
  // screen.
  seekList: Card[]
  onSeek: (at: number, list: Card[]) => void
}) {
  const { t } = i18n
  const barRef = useRef<HTMLDivElement>(null)
  const [cursor, setCursor] = useState<number | null>(null)
  const bands = useMemo(() => phaseBands(job?.phase_at, span), [job?.phase_at, span])
  const hist = useMemo(() => histogram(events, span, 64), [events, span])

  // A pointer anywhere on the bar seeks; holding and dragging keeps seeking.
  // Pointer capture is what makes a drag survive leaving the element.
  // The cursor follows the event the scrub landed on, not the pointer. A long
  // session that finished its work early leaves a stretch of bar with no events
  // under it; every position in it resolves to the last event, so a cursor left
  // where the pointer was would report a time the transcript is nowhere near.
  const seekTo = (clientX: number) => {
    const box = barRef.current?.getBoundingClientRect()
    if (!box) return
    const f = Math.min(1, Math.max(0, (clientX - box.left) / box.width))
    const target = seekTarget(seekList.map((c) => c.e), span, f)
    if (!target) {
      setCursor(f)
      return
    }
    setCursor(target.cursor)
    onSeek(target.at, seekList)
  }

  return (
    <div className="scrub">
      <span className="scrub-clock">{fmtClock(i18n.lang, span.from)}</span>
      <div
        ref={barRef}
        className="scrub-bar"
        role="slider"
        tabIndex={0}
        aria-label={t('scrubLabel')}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round((cursor ?? 0) * 100)}
        onPointerDown={(e) => {
          e.currentTarget.setPointerCapture(e.pointerId)
          seekTo(e.clientX)
        }}
        onPointerMove={(e) => {
          if (e.currentTarget.hasPointerCapture(e.pointerId)) seekTo(e.clientX)
        }}
        onKeyDown={(e) => {
          const step = e.key === 'ArrowLeft' ? -0.02 : e.key === 'ArrowRight' ? 0.02 : 0
          if (!step) return
          e.preventDefault()
          const box = barRef.current?.getBoundingClientRect()
          if (box) seekTo(box.left + Math.min(1, Math.max(0, (cursor ?? 0) + step)) * box.width)
        }}
      >
        {bands.map((b) => (
          <div
            key={b.phase}
            className={'scrub-band ' + phaseClass(b.phase)}
            data-phase={b.phase}
            style={{ left: pct(b.left), width: pct(b.width) }}
          >
            <span>{phaseLabel(t, b.phase)}</span>
          </div>
        ))}
        <div className="scrub-hist">
          {hist.bars.map((h, i) => (
            <i key={i} style={{ height: pct(h) }} />
          ))}
        </div>
        {cursor !== null && (
          <>
            <div className="scrub-past" style={{ width: pct(cursor) }} />
            <div className="scrub-cursor" style={{ left: pct(cursor) }} />
          </>
        )}
      </div>
      <span className="scrub-clock right">{fmtClock(i18n.lang, span.to)}</span>
      <span className="scrub-legend">{hist.weighted ? t('scrubByTokens') : t('scrubByEvents')}</span>
    </div>
  )
}

// EventDetail is the selected event in full, which is what lets the cards stay
// short. Nothing here is truncated or folded: this is where a reader goes when
// the card was not enough.
function EventDetail({ i18n, e, span, onClose }: { i18n: I18n; e: Event; span: Span | null; onClose: () => void }) {
  const { t } = i18n
  const dk = displayKind(e.kind)
  return (
    <div className="ev-detail">
      <div className="eyebrow eyebrow-ico" style={{ marginBottom: 10, color: kindColor(dk) }}>
        <Ico name={kindIcon(dk)} size={11} /> {kindLabel(t, dk)}
        <button className="icon-btn tiny" onClick={onClose} aria-label={t('close')} style={{ marginLeft: 'auto' }}>
          <Ico name="x" size={12} w={2} />
        </button>
      </div>
      <div className="ev-detail-meta">
        <span>{e.role}</span>
        {e.tool && <code>{e.tool}</code>}
        {clockOf(i18n.lang, e) && <span className="tabular">{clockOf(i18n.lang, e)}</span>}
        {span && offsetOf(t, e, span) && <span className="tabular">{offsetOf(t, e, span)}</span>}
      </div>
      {e.usage && (
        <div className="usage-rows" style={{ marginTop: 10 }}>
          <UsageLine lang={i18n.lang} label={t('usageInput')} v={e.usage.InputTokens} tone="var(--run)" />
          <UsageLine lang={i18n.lang} label={t('usageOutput')} v={e.usage.OutputTokens} tone="var(--pub)" />
          <UsageLine lang={i18n.lang} label={t('usageCached')} v={e.usage.CachedInputTokens} tone="var(--ok)" />
          <UsageLine lang={i18n.lang} label={t('usageCacheWritten')} v={cacheWrittenOf(e.usage)} tone="var(--warn)" />
          <UsageLine lang={i18n.lang} label={t('usageTotal')} v={totalOf(e.usage)} tone="var(--brand)" strong />
        </div>
      )}
      <pre className="ev-detail-body">{e.content || t('noContent')}</pre>
    </div>
  )
}

// pct formats a 0..1 fraction as a CSS percentage.
function pct(f: number) {
  return `${(f * 100).toFixed(3)}%`
}

// fmtClock is a wall-clock label for the ends of the scrubber.
function fmtClock(lang: Lang, ms: number) {
  return clockTime(lang, ms)
}

function UsageLine({ lang, label, v, strong, tone }: { lang: Lang; label: string; v: number; strong?: boolean; tone?: string }) {
  return (
    <div className="usage-line">
      <span className="usage-dot" style={{ background: tone }} />
      {label}
      <span className="v" style={strong ? { color: 'var(--fg-1)', fontWeight: 600 } : undefined}>
        {formatCount(lang, v)}
      </span>
    </div>
  )
}

function EventCard({
  i18n,
  e,
  i,
  span,
  on,
  onPick,
}: {
  i18n: I18n
  e: Event
  i: number
  span: Span | null
  on: boolean
  onPick: () => void
}) {
  const { t } = i18n
  const dk = displayKind(e.kind)
  const isTool = e.kind === 'tool_use' || e.kind === 'tool_result'
  const isGrade = gradeKinds.has(e.kind)
  const cls =
    'event ' + (e.kind === 'thinking' ? 'thinking' : isGrade ? 'grade' : isTool ? 'tool' : 'message') + (on ? ' on' : '')
  // A title is the event's own first line and nothing more; see splitTitle.
  const { title, body } = splitTitle(e.content)
  const at = timeOf(e)
  return (
    <RowButton className={cls} data-i={i} data-at={Number.isNaN(at) ? '' : at} selected={on} onOpen={onPick}>
      <div className="event-gutter">
        <span className="event-clock tabular">{clockOf(i18n.lang, e) || e.role}</span>
        <span className="event-kind">
          <Ico name={kindIcon(dk)} size={10} w={2.2} />
          {kindLabel(t, dk)}
        </span>
        {span && <span className="event-offset tabular">{offsetOf(t, e, span)}</span>}
      </div>
      <div className="event-main">
        {e.kind === 'thinking' ? (
          <div className="event-head">
            <span className="event-title dim">{t('thinking')}</span>
            <EventCost u={e.usage} />
          </div>
        ) : e.kind === 'tool_use' ? (
          <>
            <div className="event-head">
              <span className="event-title">{t('toolCall')}</span>
              {e.tool && <code>{e.tool}</code>}
              <EventCost u={e.usage} />
            </div>
            {e.content && <pre className="event-io">{e.content}</pre>}
          </>
        ) : e.kind === 'tool_result' ? (
          <>
            <div className="event-head">
              <span className="event-title">{t('toolResult')}</span>
              <EventCost u={e.usage} />
            </div>
            <Disclosure summary={title || t('noContent')}>
              <pre>{e.content || t('noContent')}</pre>
            </Disclosure>
          </>
        ) : (
          <>
            <div className="event-head">
              <span className="event-title">{title || t('noContent')}</span>
              <EventCost u={e.usage} />
            </div>
            {body && <div className="event-body">{body}</div>}
          </>
        )}
      </div>
    </RowButton>
  )
}

// EventCost is what this one request pushed through the model; see freshOf for
// why it is not the total. An event that was charged nothing renders nothing: a
// zero here would claim a free request happened, which is not what an absent
// figure means (006s).
// What the figure means is said once, in the scrubber's legend above the
// stream, rather than in a tooltip on each card: a card clips its own overflow,
// so an overlay anchored inside one is cut off, which is why overlays portal to
// the body elsewhere in this app.
function EventCost({ u }: { u?: Usage }) {
  if (!u) return null
  return <span className="event-cost tabular">{compactTokens(freshOf(u))}</span>
}

function toggle(s: Set<string>, k: string) {
  const n = new Set(s)
  n.has(k) ? n.delete(k) : n.add(k)
  return n
}

// Timeline draws the session's phase progression from the durable phase_at
// timestamps: each phase, when it started, and how long it took to reach the
// next. The final phase carries no duration; a still-running phase shows "in
// progress" rather than a fabricated elapsed count.
function Timeline({ i18n, job }: { i18n: I18n; job: Job }) {
  const { t } = i18n
  const at = job.phase_at ?? {}
  const steps = PHASE_ORDER.filter((p) => at[p])
    .map((p) => ({ phase: p, time: new Date(at[p]).getTime() }))
    .sort((a, b) => a.time - b.time)
  if (steps.length === 0) return null
  return (
    <div className="timeline">
      {steps.map((s, i) => {
        const next = steps[i + 1]
        const active = !next && !terminal(job.phase)
        return (
          <div key={s.phase} className={'tl-step' + (active ? ' active' : '')}>
            <span className={'tl-dot ' + phaseClass(s.phase)} />
            <div className="tl-body">
              <span className="tl-phase">{phaseLabel(t, s.phase)}</span>
              <span className="tl-dur">{active ? t('inProgress') : next ? duration(t, Math.round((next.time - s.time) / 1000), { seconds: true }) : ''}</span>
            </div>
          </div>
        )
      })}
    </div>
  )
}
// displayKind collapses the grade family so filters read cleanly.
function displayKind(kind: string) {
  if (gradeKinds.has(kind)) return 'grade'
  return kind
}
// kindLabel names an event kind in the two places a name is all that fits: the
// filter list and the card's gutter. `thinking` has a separate short label
// because its body copy is a whole sentence, which wrapped the gutter into a
// column of single words.
//
// A kind with no name of its own is humanised rather than printed raw: the
// backend adds event types faster than the client learns them, and
// `outcome_evaluation_ongoing` sitting in a filter row is an identifier leaking
// onto the screen, not a label (006s).
function kindLabel(t: I18n['t'], kind: string) {
  const map: Record<string, TKey> = {
    message: 'messageLabel',
    thinking: 'thinkingLabel',
    tool_use: 'toolCall',
    tool_result: 'toolResult',
    grade: 'grade',
  }
  return map[kind] ? t(map[kind]) : kind.replace(/_/g, ' ')
}
function kindColor(kind: string) {
  if (kind === 'grade') return 'var(--run)'
  if (kind === 'tool_use' || kind === 'tool_result') return 'var(--warn)'
  if (kind === 'thinking') return 'var(--pub)'
  if (kind === 'message') return 'var(--brand)'
  return 'var(--fg-2)'
}
// kindIcon pairs each event kind with a glyph, so a filter row and the event it
// filters are recognizable as the same thing.
function kindIcon(kind: string): IconName {
  if (kind === 'grade') return 'gauge'
  if (kind === 'tool_use') return 'wrench'
  if (kind === 'tool_result') return 'terminal'
  if (kind === 'thinking') return 'sparkles'
  if (kind === 'message') return 'message'
  return 'dot'
}
