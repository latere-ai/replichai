// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useLayoutEffect, useRef, useState, type HTMLAttributes, type ReactNode } from 'react'
import { stepDollars, typedDollars } from './money'
import { createPortal } from 'react-dom'
import { Ico, type IconName } from './icons'
import type { I18n, TKey } from './i18n'

// The control set. Nothing here delegates to a browser-drawn widget: no native
// checkbox, radio, select, number stepper, disclosure triangle, tooltip, or
// prompt(). Each one is rebuilt so it carries the app's own tokens and reads
// the same on every platform — and each one keeps the role, state, and keyboard
// behaviour the native element would have given us for free.

/* ── Type ── */

// Seams renders a heading that breaks where its copy says it may.
//
// CSS picks a break by width alone: the last space that fits. `text-wrap:
// balance` only re-picks it to equalise line lengths, which is why the landing's
// boundary band read "One loop closes. One never / will." at every viewport from
// a phone to a 2560px display — the algorithm had no idea a sentence had ended
// two words earlier. Where a heading comes apart is a fact about the language,
// so it is written into the string as `|`, and each segment between seams is
// rendered unbreakable. A translator moves the seam with the clause, which is
// the half no stylesheet can follow.
//
// The space joining two segments sits *outside* both spans, because that is the
// only place the line is allowed to break. A string with no seam renders as
// itself, so every heading can be passed through this whether or not its copy
// has anything to say about breaking yet.
//
// It takes the key, not the text, because the seam is only legible to this
// component: `t()` hands every other consumer the sentence with the seams
// removed. A heading that forgets to come through here still reads correctly —
// it just breaks wherever it likes, which is where this all started.
//
// It reads the string with no `vars`, so a heading with a {placeholder} in it
// must not carry seams — the placeholder would render as itself. None do.
export function Seams({ i18n, k }: { i18n: I18n; k: TKey }) {
  const parts = i18n.raw(k).split('|')
  if (parts.length === 1) return <>{parts[0]}</>
  const out: ReactNode[] = []
  parts.forEach((part, i) => {
    if (i > 0) out.push(' ')
    out.push(
      <span key={i} className="seam">
        {part}
      </span>,
    )
  })
  return <>{out}</>
}

/* ── Choice controls ── */

// Check is a checkbox: a button that owns the checked state and paints its own
// box. Space and Enter come from the <button>; role and aria-checked tell a
// screen reader it is still a checkbox.
export function Check({ on, onChange, disabled, children }: { on: boolean; onChange: (next: boolean) => void; disabled?: boolean; children: ReactNode }) {
  return (
    <button
      type="button"
      role="checkbox"
      aria-checked={on}
      disabled={disabled}
      className={'ck' + (on ? ' on' : '')}
      onClick={() => onChange(!on)}
    >
      <span className="ck-box">{on && <Ico name="check" size={11} w={3} />}</span>
      <span className="ck-label">{children}</span>
    </button>
  )
}

// Toggle is the switch form of the same choice, for a setting that takes effect
// at once rather than an affirmation that is submitted with a form.
export function Toggle({ on, onChange, disabled, children }: { on: boolean; onChange: (next: boolean) => void; disabled?: boolean; children?: ReactNode }) {
  return (
    <button type="button" role="switch" aria-checked={on} disabled={disabled} className={'tg' + (on ? ' on' : '')} onClick={() => onChange(!on)}>
      <span className="tg-track">
        <span className="tg-knob" />
      </span>
      {children && <span className="ck-label">{children}</span>}
    </button>
  )
}

/* ── RowButton ── */

// RowButton is a list row that opens something: a project, a file, a session.
// It is a div rather than a <button> because a row holds buttons of its own —
// Stop, Remove — and a button may not contain a button; the role, the tab stop
// and the key handling make it one to the keyboard and to a screen reader all
// the same. Every clickable row goes through here, so none of them is a div a
// keyboard cannot reach, which five of them were.
//
// A key pressed on a control inside the row is that control's, not the row's:
// Enter on Stop stops the session, and does not also open it.
export function RowButton({
  onOpen,
  selected,
  children,
  ...rest
}: { onOpen: () => void; selected?: boolean; children: ReactNode } & Omit<HTMLAttributes<HTMLDivElement>, 'onClick' | 'onKeyDown' | 'role' | 'tabIndex'>) {
  return (
    <div
      {...rest}
      role="button"
      tabIndex={0}
      aria-pressed={selected}
      onClick={onOpen}
      onKeyDown={(e) => {
        if (e.target !== e.currentTarget) return
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          onOpen()
        }
      }}
    >
      {children}
    </div>
  )
}

/* ── Select ── */

export type Option<T extends string> = { value: T; label: string; icon?: IconName }

// Select replaces the OS-drawn <select>. It is a combobox button over a
// listbox: click or Enter opens it, arrows move, Enter picks, Escape closes and
// returns focus, and a click anywhere else dismisses it.
export function Select<T extends string>({
  value,
  options,
  onChange,
  label,
  width,
}: {
  value: T
  options: Option<T>[]
  onChange: (v: T) => void
  label?: string
  width?: number | string
}) {
  const [open, setOpen] = useState(false)
  const [cursor, setCursor] = useState(() => Math.max(0, options.findIndex((o) => o.value === value)))
  const [at, setAt] = useState({ left: 0, top: 0, width: 0 })
  const box = useRef<HTMLDivElement>(null)
  const list = useRef<HTMLUListElement>(null)
  const btn = useRef<HTMLButtonElement>(null)
  const current = options.find((o) => o.value === value) ?? options[0]

  const close = useCallback((refocus = true) => {
    setOpen(false)
    if (refocus) btn.current?.focus()
  }, [])

  // The list is portalled to <body>: every card in this app clips its corners
  // with overflow:hidden, which would slice an absolutely-positioned dropdown
  // off at the card's edge. Fixed coordinates come from the button, and the
  // list flips above it when there is no room below.
  useLayoutEffect(() => {
    if (!open) return
    const place = () => {
      const b = btn.current?.getBoundingClientRect()
      if (!b) return
      const height = list.current?.offsetHeight ?? options.length * 34 + 8
      const below = window.innerHeight - b.bottom
      const top = below < height + 12 && b.top > height + 12 ? b.top - height - 5 : b.bottom + 5
      setAt({ left: b.left, top, width: b.width })
    }
    place()
    // A scroll moves the anchor out from under a fixed list, so it dismisses.
    // Named so the cleanup can take it back: a list closed any other way used
    // to leave its listener armed on window, one per open.
    const dismiss = () => close(false)
    window.addEventListener('resize', place)
    window.addEventListener('scroll', dismiss, { capture: true, once: true })
    return () => {
      window.removeEventListener('resize', place)
      window.removeEventListener('scroll', dismiss, { capture: true })
    }
  }, [open, options.length, close])

  useEffect(() => {
    if (!open) return
    const away = (e: MouseEvent) => {
      const target = e.target as Node
      if (!box.current?.contains(target) && !list.current?.contains(target)) close(false)
    }
    document.addEventListener('mousedown', away)
    return () => document.removeEventListener('mousedown', away)
  }, [open, close])

  const pick = (i: number) => {
    onChange(options[i].value)
    close()
  }

  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') return close()
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      return open ? pick(cursor) : setOpen(true)
    }
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      if (!open) return setOpen(true)
      setCursor((c) => (c + (e.key === 'ArrowDown' ? 1 : options.length - 1)) % options.length)
    }
  }

  return (
    <div className="sel" ref={box} style={{ width }}>
      <button
        ref={btn}
        type="button"
        className={'sel-btn' + (open ? ' open' : '')}
        role="combobox"
        aria-expanded={open}
        aria-haspopup="listbox"
        aria-label={label}
        onClick={() => {
          setCursor(Math.max(0, options.findIndex((o) => o.value === value)))
          setOpen((o) => !o)
        }}
        onKeyDown={onKey}
      >
        {current?.icon && <Ico name={current.icon} size={13} />}
        <span className="sel-value">{current?.label}</span>
        <Ico name="chevronDown" size={13} className="sel-caret" />
      </button>
      {open &&
        createPortal(
          <ul
            ref={list}
            className="sel-list"
            role="listbox"
            aria-label={label}
            tabIndex={-1}
            style={{ left: at.left, top: at.top, minWidth: at.width }}
          >
            {options.map((o, i) => (
              <li key={o.value}>
                <button
                  type="button"
                  role="option"
                  aria-selected={o.value === value}
                  className={'sel-opt' + (i === cursor ? ' cursor' : '') + (o.value === value ? ' on' : '')}
                  onMouseEnter={() => setCursor(i)}
                  onClick={() => pick(i)}
                >
                  {o.icon && <Ico name={o.icon} size={13} />}
                  <span className="sel-value">{o.label}</span>
                  {o.value === value && <Ico name="check" size={13} w={2.2} className="sel-tick" />}
                </button>
              </li>
            ))}
          </ul>,
          document.body,
        )}
    </div>
  )
}

/* ── Segmented ── */

// Segmented is the small exclusive picker used for icon-sized choices — the
// appearance switcher, a two-way list filter.
export function Segmented<T extends string>({
  value,
  options,
  onChange,
  label,
  compact,
}: {
  value: T
  options: (Option<T> & { hint?: string })[]
  onChange: (v: T) => void
  label?: string
  compact?: boolean
}) {
  return (
    <div className={'segmented' + (compact ? ' compact' : '')} role="radiogroup" aria-label={label}>
      {options.map((o) => (
        // The tooltip is styled onto the button rather than wrapped around it:
        // a radiogroup owns its radios directly, and a wrapper span between
        // them breaks that relationship for a screen reader.
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={value === o.value}
          aria-label={compact ? o.label : undefined}
          {...(compact ? tipAttrs(o.hint ?? o.label) : {})}
          className={'seg-btn' + (value === o.value ? ' on' : '') + (compact ? ' tip tip-bottom' : '')}
          onClick={() => onChange(o.value)}
        >
          {o.icon && <Ico name={o.icon} size={13} />}
          {!compact && <span>{o.label}</span>}
        </button>
      ))}
    </div>
  )
}

/* ── Number field ── */

// NumberField is a plain text box with its own steppers — the native number
// input draws OS spinners that no stylesheet can reach, and its scroll-wheel
// behaviour changes a value nobody meant to touch.
export function NumberField({
  value,
  onChange,
  min = 0,
  max,
  id,
  width = 104,
  ariaLabel,
  autoFocus,
}: {
  value: string
  onChange: (next: string) => void
  min?: number
  max?: number
  id?: string
  width?: number
  ariaLabel?: string
  autoFocus?: boolean
}) {
  // Every field this draws holds money, and money has cents. It used to strip
  // the decimal point, so a project could only ever be allocated whole dollars
  // and a $16.82 balance could not be moved back in one go.
  const n = Number.parseFloat(value)
  const step = (by: number) => onChange(stepDollars(value, by, min, max))
  return (
    <div className="numf" style={{ width }}>
      <button type="button" className="numf-step" aria-label="−" onClick={() => step(-1)} disabled={Number.isFinite(n) && n <= min}>
        <Ico name="minus" size={12} w={2.2} />
      </button>
      <input
        id={id}
        aria-label={ariaLabel}
        autoFocus={autoFocus}
        className="numf-input tabular"
        inputMode="decimal"
        value={value}
        // Digits and one decimal point, at most two places. Shaping the value as
        // it is typed rather than validating it afterwards is what lets "12." —
        // not a number, and the keystroke before 12.50 — survive long enough to
        // become one.
        onChange={(e) => onChange(typedDollars(e.target.value))}
        onKeyDown={(e) => {
          if (e.key === 'ArrowUp') {
            e.preventDefault()
            step(1)
          }
          if (e.key === 'ArrowDown') {
            e.preventDefault()
            step(-1)
          }
        }}
      />
      <button type="button" className="numf-step" aria-label="+" onClick={() => step(1)} disabled={max !== undefined && Number.isInteger(n) && n >= max}>
        <Ico name="plus" size={12} w={2.2} />
      </button>
    </div>
  )
}

/* ── Modal + prompt ── */

// Modal is the one dialog shell: a backdrop that dismisses, Escape that
// dismisses, and a body that does not scroll behind it.
export function Modal({ title, eyebrow, icon, onClose, children, foot }: { title: string; eyebrow?: ReactNode; icon?: IconName; onClose: () => void; children: ReactNode; foot?: ReactNode }) {
  useEffect(() => {
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    document.addEventListener('keydown', esc)
    const prior = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.removeEventListener('keydown', esc)
      document.body.style.overflow = prior
    }
  }, [onClose])
  // Portalled to <body>. A fixed backdrop is contained by any promoted
  // ancestor, and both screens that open a dialog run a fade-up animation whose
  // filled transform promotes them — which confined the backdrop to the content
  // column, left the rail live behind a "modal", and centred the dialog on the
  // wrong box.
  return createPortal(
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal" role="dialog" aria-modal="true" aria-label={title} onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          {eyebrow && <span className="attestation">{eyebrow}</span>}
          <h2 className="modal-title">
            {icon && <span className="modal-icon"><Ico name={icon} size={14} /></span>}
            {title}
          </h2>
        </div>
        <div className="modal-body">{children}</div>
        {foot && <div className="modal-foot">{foot}</div>}
      </div>
    </div>,
    document.body,
  )
}

// PromptDialog asks for one number in the app's own dialog. window.prompt draws
// a chrome-owned box that ignores the theme, cannot be styled, and blocks the
// whole tab while it is up.
export function PromptDialog({
  title,
  body,
  initial,
  confirmLabel,
  cancelLabel,
  onCancel,
  onConfirm,
  parse,
}: {
  title: string
  body: string
  initial: string
  confirmLabel: string
  cancelLabel: string
  onCancel: () => void
  onConfirm: (value: number) => void
  // parse reads the field into the number the caller wants. It exists because
  // what somebody types and what the caller stores are not always the same
  // unit — dollars typed, micro-USD stored. Null means "not a number yet", and
  // the confirm button stays disabled. Defaults to a non-negative integer.
  parse?: (text: string) => number | null
}) {
  const [draft, setDraft] = useState(initial)
  const read = parse ?? ((text: string) => (Number.isInteger(Number.parseInt(text, 10)) ? Number.parseInt(text, 10) : null))
  const parsed = read(draft)
  const valid = parsed !== null && parsed >= 0
  return (
    <Modal
      title={title}
      icon="wallet"
      onClose={onCancel}
      foot={
        <>
          <button className="btn ghost" onClick={onCancel}>
            {cancelLabel}
          </button>
          <button className="btn" disabled={!valid} onClick={() => valid && parsed !== null && onConfirm(parsed)}>
            {confirmLabel}
          </button>
        </>
      }
    >
      <p className="muted small" style={{ margin: 0, lineHeight: 1.6 }}>
        {body}
      </p>
      {/* window.prompt let you type at once; the field takes focus so this does too. */}
      <NumberField value={draft} onChange={setDraft} ariaLabel={title} width={132} autoFocus />
    </Modal>
  )
}

/* ── Disclosure ── */

// Disclosure replaces <details>, whose marker is drawn by the browser and
// differs on every engine.
export function Disclosure({ summary, children }: { summary: string; children: ReactNode }) {
  const [open, setOpen] = useState(false)
  return (
    <div className={'disc' + (open ? ' open' : '')}>
      <button type="button" className="disc-head" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
        <Ico name="chevronRight" size={12} w={2} className="disc-caret" />
        <span>{summary}</span>
      </button>
      {open && <div className="disc-body">{children}</div>}
    </div>
  )
}

/* ── Tooltip ── */

// Tip is the app's own tooltip. The `title` attribute is drawn by the browser,
// appears after a delay nobody controls, and cannot be themed.
//
// A trigger only declares its text with `data-tip`; the bubble itself is drawn
// by TipHost. `side` is the preferred edge, and TipHost overrides it when the
// bubble would leave the viewport.
//
// 'right' is for a trigger in a vertical stack. Above and below are both a
// neighbour's row there — the folded navigation rail is the case — so the bubble
// goes out to the side instead, where nothing is stacked.
export type TipSide = 'top' | 'bottom' | 'right'

// tipAttrs applies the tooltip to an element directly, for the cases where a
// wrapper element would break an ARIA relationship.
export function tipAttrs(text: string, side: TipSide = 'bottom') {
  return { 'data-tip': text, 'data-tip-side': side, className: `tip tip-${side}` }
}

export function Tip({ text, children, side = 'bottom' }: { text?: string; children: ReactNode; side?: TipSide }) {
  if (!text) return <>{children}</>
  return (
    <span className={'tip tip-' + side} data-tip={text} data-tip-side={side}>
      {children}
    </span>
  )
}

// TipHost draws every tooltip in the app, from one bubble portalled to <body>.
//
// The bubble used to be a `::after` on the trigger. Absolute positioning is
// contained by the nearest clipping ancestor, and every card in the app rounds
// its corners with `overflow: hidden` — so a tip on any control inside a card
// was sliced off at the card's edge. One fixed bubble outside every card is the
// only placement no container can cut.
//
// It listens on the document rather than per trigger: `data-tip` stays a plain
// attribute, so a tip can sit on an element that cannot take a wrapper — a
// radio inside a radiogroup — and no call site owns hover state.
export function TipHost() {
  const [tip, setTip] = useState<{ text: string; side: TipSide; rect: DOMRect } | null>(null)
  // Placement is a second pass: it needs the bubble's own size, which only
  // exists once the text is in the DOM.
  const [at, setAt] = useState<{ left: number; top: number } | null>(null)
  const bubble = useRef<HTMLDivElement>(null)
  const trigger = useRef<Element | null>(null)

  useEffect(() => {
    const hide = () => {
      trigger.current = null
      setTip(null)
      setAt(null)
    }
    const show = (el: Element) => {
      // Crossing from a button into its own icon resolves to the same trigger.
      // Re-raising the bubble there would restart the animation on every pixel.
      if (el === trigger.current) return
      const text = el.getAttribute('data-tip')
      if (!text) return hide()
      trigger.current = el
      setAt(null)
      // The side is an attribute rather than only a class, because a trigger
      // that cannot take the `tip` class still needs to ask for one: `.tip` is
      // an `inline-flex`, and the rail's nav rows are flex rows of their own
      // that it would undo.
      const asked = el.getAttribute('data-tip-side')
      const side: TipSide = asked === 'top' || asked === 'right' ? asked : el.classList.contains('tip-top') ? 'top' : 'bottom'
      setTip({ text, side, rect: el.getBoundingClientRect() })
    }
    // One pointer handler covers entering and leaving: whatever is under the
    // pointer either carries a tip or dismisses the one that is up.
    const move = (e: Event) => {
      const el = (e.target as Element | null)?.closest?.('[data-tip]')
      el ? show(el) : hide()
    }
    // A trigger that is clicked often disappears — a row is removed, a dialog
    // opens over it — and the bubble would outlive it.
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && hide()
    document.addEventListener('pointerover', move)
    document.addEventListener('focusin', move)
    document.addEventListener('focusout', hide)
    document.addEventListener('pointerdown', hide)
    document.addEventListener('keydown', esc)
    // A fixed bubble does not travel with a trigger inside a scroll container,
    // so anything that moves the page under it takes it down.
    document.addEventListener('scroll', hide, true)
    window.addEventListener('resize', hide)
    window.addEventListener('blur', hide)
    return () => {
      document.removeEventListener('pointerover', move)
      document.removeEventListener('focusin', move)
      document.removeEventListener('focusout', hide)
      document.removeEventListener('pointerdown', hide)
      document.removeEventListener('keydown', esc)
      document.removeEventListener('scroll', hide, true)
      window.removeEventListener('resize', hide)
      window.removeEventListener('blur', hide)
    }
  }, [])

  useLayoutEffect(() => {
    if (!tip || !bubble.current) return
    const b = bubble.current.getBoundingClientRect()
    const M = 6 // the gap from the trigger, and the margin from the viewport
    const above = tip.rect.top - b.height - M
    const below = tip.rect.bottom + M
    // The preferred side, unless it has no room and the other one does.
    const fits = (top: number) => top >= M && top + b.height <= window.innerHeight - M
    // Beside the trigger rather than under it: level with its middle, clear of
    // its right edge. Nothing flips here — the vertical stack this is for has a
    // neighbour above and below, and the clamp below keeps it on screen.
    const beside = tip.side === 'right'
    const top = beside
      ? tip.rect.top + tip.rect.height / 2 - b.height / 2
      : tip.side === 'top'
        ? fits(above) || !fits(below)
          ? above
          : below
        : fits(below) || !fits(above)
          ? below
          : above
    const left = beside ? tip.rect.right + M : tip.rect.left + tip.rect.width / 2 - b.width / 2
    setAt({
      // Pulled back inside for a trigger near the viewport's edge — the header's
      // wallet pill sits there, and a centred bubble would hang off the screen.
      left: Math.min(Math.max(M, left), Math.max(M, window.innerWidth - b.width - M)),
      top: Math.min(Math.max(M, top), Math.max(M, window.innerHeight - b.height - M)),
    })
  }, [tip])

  if (!tip) return null
  // Hidden from the accessibility tree: the trigger carries its own label, and a
  // second reading of it is noise. It cannot be hovered either — a bubble that
  // takes the pointer would dismiss itself.
  return createPortal(
    <div
      ref={bubble}
      className="tip-bubble"
      aria-hidden="true"
      // Measured off-screen on the first pass, placed on the second.
      style={at ? { left: at.left, top: at.top } : { left: 0, top: 0, visibility: 'hidden' }}
    >
      {tip.text}
    </div>,
    document.body,
  )
}

/* ── Pill ── */

export type Tone = 'neutral' | 'brand' | 'ok' | 'warn' | 'err' | 'run'

// Pill is the rounded status mark: a role, a tier, a phase, a count. `dot`
// gives it a leading bullet; `pulse` makes that bullet breathe for something
// still in flight.
export function Pill({ tone = 'neutral', icon, dot, pulse, children, title }: { tone?: Tone; icon?: IconName; dot?: boolean; pulse?: boolean; children: ReactNode; title?: string }) {
  const body = (
    <span className={`pill pill-${tone}` + (pulse ? ' pulse' : '')}>
      {dot && <span className="pill-dot" />}
      {icon && <Ico name={icon} size={11} w={2} />}
      {children}
    </span>
  )
  return title ? <Tip text={title}>{body}</Tip> : body
}
