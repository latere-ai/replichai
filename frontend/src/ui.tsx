import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { Ico, type IconName } from './icons'

// The control set. Nothing here delegates to a browser-drawn widget: no native
// checkbox, radio, select, number stepper, disclosure triangle, tooltip, or
// prompt(). Each one is rebuilt so it carries the app's own tokens and reads
// the same on every platform — and each one keeps the role, state, and keyboard
// behaviour the native element would have given us for free.

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

// Radio renders one exclusive choice. The group wrapper carries the role so
// arrow-key navigation lands on the right set.
export function RadioGroup({ label, children }: { label?: string; children: ReactNode }) {
  return (
    <div className="radio-group" role="radiogroup" aria-label={label}>
      {children}
    </div>
  )
}

export function Radio({ on, onChange, children }: { on: boolean; onChange: () => void; children: ReactNode }) {
  return (
    <button type="button" role="radio" aria-checked={on} className={'rd' + (on ? ' on' : '')} onClick={onChange}>
      <span className="rd-box">{on && <span className="rd-dot" />}</span>
      <span className="ck-label">{children}</span>
    </button>
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
    window.addEventListener('resize', place)
    // A scroll moves the anchor out from under a fixed list, so it dismisses.
    window.addEventListener('scroll', () => close(false), { capture: true, once: true })
    return () => window.removeEventListener('resize', place)
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
  const n = Number.parseInt(value, 10)
  const step = (by: number) => {
    const base = Number.isInteger(n) ? n : min
    const next = Math.min(max ?? Number.MAX_SAFE_INTEGER, Math.max(min, base + by))
    onChange(String(next))
  }
  return (
    <div className="numf" style={{ width }}>
      <button type="button" className="numf-step" aria-label="−" onClick={() => step(-1)} disabled={Number.isInteger(n) && n <= min}>
        <Ico name="minus" size={12} w={2.2} />
      </button>
      <input
        id={id}
        aria-label={ariaLabel}
        autoFocus={autoFocus}
        className="numf-input tabular"
        inputMode="numeric"
        value={value}
        // Digits only: a text box that accepts letters would need a second
        // validation story the field does not have.
        onChange={(e) => onChange(e.target.value.replace(/[^0-9]/g, ''))}
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
}: {
  title: string
  body: string
  initial: string
  confirmLabel: string
  cancelLabel: string
  onCancel: () => void
  onConfirm: (value: number) => void
}) {
  const [draft, setDraft] = useState(initial)
  const parsed = Number.parseInt(draft, 10)
  const valid = Number.isInteger(parsed) && parsed >= 0
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
          <button className="btn" disabled={!valid} onClick={() => valid && onConfirm(parsed)}>
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
// `align` pins the bubble to one edge instead of centring it, for a trigger
// close enough to the viewport edge that a centred bubble would run off it.
export type TipSide = 'top' | 'bottom'
export type TipAlign = 'center' | 'end'

// tipAttrs applies the tooltip to an element directly, for the cases where a
// wrapper element would break an ARIA relationship.
export function tipAttrs(text: string, side: TipSide = 'bottom', align: TipAlign = 'center') {
  return { 'data-tip': text, 'data-tip-align': align === 'end' ? 'end' : undefined, className: `tip tip-${side}` }
}

export function Tip({ text, children, side = 'bottom', align = 'center' }: { text?: string; children: ReactNode; side?: TipSide; align?: TipAlign }) {
  if (!text) return <>{children}</>
  return (
    <span className={'tip tip-' + side} data-tip={text} data-tip-align={align === 'end' ? 'end' : undefined}>
      {children}
    </span>
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
