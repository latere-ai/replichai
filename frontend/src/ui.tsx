// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useId, useLayoutEffect, useRef, useState, type HTMLAttributes, type ReactNode } from 'react'
import { GlassModal } from 'latere-ui/react'
import { stepDollars, typedDollars } from './money'
import { createPortal } from 'react-dom'
import { Ico, type IconName } from './icons'
import type { I18n, TKey } from './i18n'

// The control set. Nothing here delegates to a browser-drawn widget: no native
// checkbox, radio, select, number stepper, disclosure triangle, tooltip, or
// prompt(). Each one is drawn by the app so it carries the app's own tokens and
// reads the same on every platform, and each one keeps the role, state, and
// keyboard behavior the native element has.

/* ── Type ── */

// Seams renders a heading that breaks where its copy says it may. CSS picks a
// break by width alone, and `text-wrap: balance` only evens out line lengths;
// neither knows where a clause ends. So the copy marks each allowed break with
// `|`, each segment between seams renders unbreakable, and a translator moves
// the seam with the clause.
//
// The space joining two segments sits outside both spans, the only place the
// line may break. A string with no seam renders as itself.
//
// It takes the key, not the text, because `t()` hands every other consumer the
// sentence with the seams removed. It reads the string with no `vars`, so a
// heading with a {placeholder} in it must not carry seams.
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

/* ── RowButton ── */

// RowButton is a list row that opens something: a project, a file, a session.
// It is a div rather than a <button> because a row holds buttons of its own,
// such as Stop and Remove, and a button may not contain a button; the role, the
// tab stop and the key handling make it one to the keyboard and to a screen
// reader. Every clickable row goes through here, so every one is reachable from
// the keyboard.
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

type Option<T extends string> = { value: T; label: string; icon?: IconName }

/* ── Segmented ── */

// Segmented is the small exclusive picker used for icon-sized choices, such as
// the appearance switcher or a two-way list filter.
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
          className={'seg-btn' + (value === o.value ? ' on' : '') + (compact ? ' tip' : '')}
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

// NumberField is a plain text box with its own steppers. The native number
// input draws OS spinners that no stylesheet can reach, and its scroll-wheel
// behavior changes a value nobody meant to touch.
export function NumberField({
  i18n,
  value,
  onChange,
  min = 0,
  id,
  width = 104,
  ariaLabel,
  autoFocus,
}: {
  i18n: I18n
  value: string
  onChange: (next: string) => void
  min?: number
  id?: string
  width?: number
  ariaLabel?: string
  autoFocus?: boolean
}) {
  const { t } = i18n
  // Every field this draws holds money, so a value keeps its cents.
  const n = Number.parseFloat(value)
  const step = (by: number) => onChange(stepDollars(value, by, min))
  return (
    <div className="numf" style={{ width }}>
      <button type="button" className="numf-step" aria-label={t('stepDown')} onClick={() => step(-1)} disabled={Number.isFinite(n) && n <= min}>
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
        // it is typed rather than validating it afterwards lets "12.", which is
        // not a number but is the keystroke before 12.50, survive long enough to
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
      <button type="button" className="numf-step" aria-label={t('stepUp')} onClick={() => step(1)}>
        <Ico name="plus" size={12} w={2.2} />
      </button>
    </div>
  )
}

/* ── Prompt ── */

// PromptDialog asks for one number in the app's own dialog. window.prompt draws
// a chrome-owned box that ignores the theme, cannot be styled, and blocks the
// whole tab while it is up.
export function PromptDialog({
  i18n,
  title,
  body,
  initial,
  confirmLabel,
  cancelLabel,
  onCancel,
  onConfirm,
  parse,
}: {
  i18n: I18n
  title: string
  body: string
  initial: string
  confirmLabel: string
  cancelLabel: string
  onCancel: () => void
  onConfirm: (value: number) => void
  // parse reads the field into the number the caller wants. It exists because
  // what somebody types and what the caller stores are not always the same
  // unit: dollars typed, micro-USD stored. Null means "not a number yet", and
  // the confirm button stays disabled. Defaults to a non-negative integer.
  parse?: (text: string) => number | null
}) {
  const [draft, setDraft] = useState(initial)
  // The field takes focus on open, so typing can start at once. It is moved
  // there from an effect rather than through autoFocus: the dialog's focus trap
  // records what had focus when it opened, to return it on close, and that has
  // to be the control that opened the dialog, not this field. The dialog's
  // effects run before this one, since it is a child.
  const field = useId()
  useEffect(() => {
    document.getElementById(field)?.focus()
  }, [field])
  const read = parse ?? ((text: string) => (Number.isInteger(Number.parseInt(text, 10)) ? Number.parseInt(text, 10) : null))
  const parsed = read(draft)
  const valid = parsed !== null && parsed >= 0
  return (
    <GlassModal
      open
      title={title}
      onClose={onCancel}
      footer={
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
      <NumberField i18n={i18n} id={field} value={draft} onChange={setDraft} ariaLabel={title} width={132} />
    </GlassModal>
  )
}

/* ── Tooltip ── */

// Tip is the app's own tooltip; the `title` attribute is drawn by the browser,
// appears after a delay nobody controls, and cannot be themed. A trigger
// declares its text with `data-tip`, and TipHost draws the bubble.
//
// `data-tip-side="right"` is for a trigger in a vertical stack, such as the
// folded navigation rail, where the rows above and below are neighbors, so the
// bubble goes out to the side instead.
type TipSide = 'bottom' | 'right'

// tipAttrs applies the tooltip to an element directly, for the cases where a
// wrapper element would break an ARIA relationship.
export function tipAttrs(text: string) {
  return { 'data-tip': text, className: 'tip' }
}

export function Tip({ text, children }: { text?: string; children: ReactNode }) {
  if (!text) return <>{children}</>
  return (
    <span className="tip" data-tip={text}>
      {children}
    </span>
  )
}

// The bubble's attributes for the top layer. Spread rather than written as JSX
// because @types/react 18 does not declare `popover`.
const TOP_LAYER: Record<string, string> = { popover: 'manual' }

// raise shows the bubble in the top layer. An engine without the Popover API
// ignores the attribute and draws the same fixed bubble in place; one that
// refuses to show it gets the attribute removed, so it is drawn in place rather
// than left display:none.
function raise(el: HTMLElement) {
  if (typeof el.showPopover !== 'function' || !el.hasAttribute('popover')) return
  try {
    if (!el.matches(':popover-open')) el.showPopover()
  } catch {
    el.removeAttribute('popover')
  }
}

// TipHost draws every tooltip in the app, from one bubble portalled to <body>
// and shown in the browser's top layer. Every card in the app rounds its
// corners with `overflow: hidden`, which would clip a bubble positioned inside
// the trigger, and a dialog paints over anything outside the top layer; one
// fixed bubble there is the only placement no container or dialog can cover.
// latere-ui's GlassTooltip is positioned inside its trigger, so it is clipped
// by the same cards and has no side placement for the folded rail.
//
// It listens on the document rather than per trigger: `data-tip` stays a plain
// attribute, so a tip can sit on an element that cannot take a wrapper, such as
// a radio inside a radiogroup, and no call site owns hover state.
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
      const side: TipSide = el.getAttribute('data-tip-side') === 'right' ? 'right' : 'bottom'
      setTip({ text, side, rect: el.getBoundingClientRect() })
    }
    // One pointer handler covers entering and leaving: whatever is under the
    // pointer either carries a tip or dismisses the one that is up.
    const move = (e: Event) => {
      const el = (e.target as Element | null)?.closest?.('[data-tip]')
      el ? show(el) : hide()
    }
    // A trigger that is clicked often disappears, because a row is removed or a
    // dialog opens over it, and the bubble would outlive it.
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
    // A popover is display:none until shown, so it is raised before it is measured.
    raise(bubble.current)
    const b = bubble.current.getBoundingClientRect()
    const M = 6 // the gap from the trigger, and the margin from the viewport
    const above = tip.rect.top - b.height - M
    const below = tip.rect.bottom + M
    // The preferred side, unless it has no room and the other one does.
    const fits = (top: number) => top >= M && top + b.height <= window.innerHeight - M
    // Beside the trigger rather than under it: level with its middle, clear of
    // its right edge. Nothing flips here, because the vertical stack this is for has a
    // neighbor above and below, and the clamp below keeps it on screen.
    const beside = tip.side === 'right'
    const top = beside ? tip.rect.top + tip.rect.height / 2 - b.height / 2 : fits(below) || !fits(above) ? below : above
    const left = beside ? tip.rect.right + M : tip.rect.left + tip.rect.width / 2 - b.width / 2
    setAt({
      // Pulled back inside for a trigger near the viewport's edge, where the
      // header's wallet pill sits, and a centered bubble would hang off the screen.
      left: Math.min(Math.max(M, left), Math.max(M, window.innerWidth - b.width - M)),
      top: Math.min(Math.max(M, top), Math.max(M, window.innerHeight - b.height - M)),
    })
  }, [tip])

  if (!tip) return null
  // Hidden from the accessibility tree: the trigger carries its own label, and a
  // second reading of it is noise. It cannot be hovered either, because a bubble
  // that takes the pointer would dismiss itself.
  return createPortal(
    <div
      ref={bubble}
      {...TOP_LAYER}
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
export function Pill({ tone = 'neutral', icon, dot, pulse, children }: { tone?: Tone; icon?: IconName; dot?: boolean; pulse?: boolean; children: ReactNode }) {
  return (
    <span className={`pill pill-${tone}` + (pulse ? ' pulse' : '')}>
      {dot && <span className="pill-dot" />}
      {icon && <Ico name={icon} size={11} w={2} />}
      {children}
    </span>
  )
}
