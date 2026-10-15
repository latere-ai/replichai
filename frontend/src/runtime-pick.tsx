// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// The choice of where the next session runs and on what (048): the runtime,
// when the deployment offers more than one, then the Effort level, the
// family's word for the tier, and on the Latere platform one specific model
// below the levels. One trigger on the papers card opens a panel; the panel
// holds the whole choice so Start stays one tap.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import type { ModelTier, RuntimeOption } from './api'
import { type I18n } from './i18n'
import { Ico } from './icons'
import { formatUSD } from './money'
import './runtime-pick.css'

// Choice is what the next Start sends. tier is the stored preference (best,
// balanced, fast); model is one exact model as provider/id, and empty when the
// person picked a level.
export type Choice = { runtime: string; tier: string; model: string }

// The levels in the order every Latere picker shows them, cheapest first.
const levels = ['fast', 'balanced', 'best'] as const

// runtimesFrom reads the server's answer. A deployment that predates runtime
// choice answers tiers only, which reads as one unnamed runtime.
export function runtimesFrom(answer: { tiers?: ModelTier[]; runtimes?: RuntimeOption[] }): RuntimeOption[] {
  if (answer.runtimes?.length) return answer.runtimes
  return [
    {
      name: '',
      label: '',
      default: true,
      payer: 'project',
      efforts: (answer.tiers ?? []).map((t) => ({ tier: t.tier, effort: '', model: t.model, input: t.input, output: t.output, error: t.error })),
    },
  ]
}

export function RuntimePick({
  i18n,
  runtimes,
  value,
  onChange,
}: {
  i18n: I18n
  runtimes: RuntimeOption[]
  value: Choice
  onChange: (c: Choice) => void
}) {
  const { t } = i18n
  const effortName = (tier: string) =>
    tier === 'fast' ? t('effortQuick') : tier === 'balanced' ? t('effortThorough') : t('effortSustained')
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [at, setAt] = useState({ left: 0, top: 0 })
  const btn = useRef<HTMLButtonElement>(null)
  const pop = useRef<HTMLDivElement>(null)
  const runtime = runtimes.find((r) => r.name === value.runtime) ?? runtimes.find((r) => r.default) ?? runtimes[0]
  const tier = value.tier || 'best'

  const close = useCallback((refocus = true) => {
    setOpen(false)
    setQuery('')
    if (refocus) btn.current?.focus()
  }, [])

  // Portalled to <body> for the same reason the shared Select is: every card
  // clips its corners. It opens under the trigger, aligned to its left edge
  // and kept on screen, and above it when there is no room below.
  useLayoutEffect(() => {
    if (!open) return
    const place = () => {
      const b = btn.current?.getBoundingClientRect()
      const p = pop.current
      if (!b || !p) return
      const width = p.offsetWidth
      const height = p.offsetHeight
      const below = window.innerHeight - b.bottom
      const top = below < height + 12 && b.top > height + 12 ? b.top - height - 6 : b.bottom + 6
      const left = Math.max(12, Math.min(b.left, window.innerWidth - width - 12))
      setAt({ left, top })
    }
    place()
    const dismiss = () => close(false)
    window.addEventListener('resize', place)
    window.addEventListener('scroll', dismiss, { capture: true, once: true })
    return () => {
      window.removeEventListener('resize', place)
      window.removeEventListener('scroll', dismiss, { capture: true })
    }
  }, [open, close, runtime?.name, value.model, query])

  useEffect(() => {
    if (!open) return
    const away = (e: MouseEvent) => {
      const target = e.target as Node
      if (!btn.current?.contains(target) && !pop.current?.contains(target)) close(false)
    }
    document.addEventListener('mousedown', away)
    return () => document.removeEventListener('mousedown', away)
  }, [open, close])

  const matches = useMemo(() => {
    const models = runtime?.models ?? []
    const q = query.trim().toLowerCase()
    return (q ? models.filter((m) => m.toLowerCase().includes(q)) : models).slice(0, 50)
  }, [runtime, query])

  if (!runtime) return null

  const summary = [runtimes.length > 1 ? runtime.label : '', value.model || effortName(tier)].filter(Boolean).join(' · ')

  const price = (input?: number, output?: number) =>
    input && output ? t('tierPrice', { in: formatUSD(i18n.lang, input), out: formatUSD(i18n.lang, output) }) : ''

  return (
    <>
      <button
        ref={btn}
        type="button"
        className={'rp-btn' + (open ? ' open' : '')}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-label={t('runtimePickLabel', { choice: summary })}
        onClick={() => (open ? close() : setOpen(true))}
        onKeyDown={(e) => {
          if (e.key === 'Escape' && open) close()
        }}
      >
        <span className="rp-btn-value">{summary}</span>
        <Ico name="chevronDown" size={13} className="rp-caret" />
      </button>
      {open &&
        createPortal(
          <div
            ref={pop}
            className="rp-pop"
            role="dialog"
            aria-label={t('runtimePickTitle')}
            style={{ left: at.left, top: at.top }}
            onKeyDown={(e) => {
              if (e.key === 'Escape') {
                e.stopPropagation()
                close()
              }
            }}
          >
            {runtimes.length > 1 && (
              <div className="rp-section">
                <div className="rp-head">{t('runsOn')}</div>
                <div className="rp-seg" role="radiogroup" aria-label={t('runsOn')}>
                  {runtimes.map((r) => (
                    <button
                      key={r.name}
                      type="button"
                      role="radio"
                      aria-checked={r.name === runtime.name}
                      className={'rp-seg-opt' + (r.name === runtime.name ? ' on' : '')}
                      onClick={() => onChange({ runtime: r.name, tier, model: '' })}
                    >
                      {r.label}
                    </button>
                  ))}
                </div>
              </div>
            )}
            <div className="rp-section">
              <div className="rp-head">{t('effort')}</div>
              <div className="rp-levels" role="radiogroup" aria-label={t('effort')}>
                {levels.map((level) => {
                  const offer = runtime.efforts.find((e) => e.tier === level)
                  const on = !value.model && tier === level
                  const unavailable = !!offer?.error
                  return (
                    <button
                      key={level}
                      type="button"
                      role="radio"
                      aria-checked={on}
                      disabled={unavailable}
                      className={'rp-level' + (on ? ' on' : '')}
                      onClick={() => {
                        onChange({ runtime: runtime.name, tier: level, model: '' })
                        close()
                      }}
                    >
                      <span className="rp-level-name">{effortName(level)}</span>
                      <span className="rp-level-detail">
                        {unavailable
                          ? t('effortUnavailable')
                          : [offer?.model, price(offer?.input, offer?.output)].filter(Boolean).join(' · ')}
                      </span>
                      {on && <Ico name="check" size={13} w={2.2} className="rp-tick" />}
                    </button>
                  )
                })}
              </div>
            </div>
            {!!runtime.models?.length && (
              <div className="rp-section">
                <div className="rp-head">{t('specificModel')}</div>
                <input
                  className="text rp-search"
                  type="search"
                  value={query}
                  placeholder={t('searchModels', { count: String(runtime.models.length) })}
                  aria-label={t('specificModel')}
                  onChange={(e) => setQuery(e.target.value)}
                  spellCheck={false}
                />
                <ul className="rp-models" role="listbox" aria-label={t('specificModel')}>
                  {matches.length === 0 && <li className="rp-empty">{t('noModelMatch')}</li>}
                  {matches.map((m) => (
                    <li key={m}>
                      <button
                        type="button"
                        role="option"
                        aria-selected={m === value.model}
                        className={'rp-model' + (m === value.model ? ' on' : '')}
                        onClick={() => {
                          onChange({ runtime: runtime.name, tier, model: m })
                          close()
                        }}
                      >
                        <span>{m}</span>
                        {m === value.model && <Ico name="check" size={13} w={2.2} className="rp-tick" />}
                      </button>
                    </li>
                  ))}
                </ul>
              </div>
            )}
            <p className="rp-note">{runtime.payer === 'person' ? t('paidByPerson') : t('paidByProject')}</p>
          </div>,
          document.body,
        )}
    </>
  )
}
