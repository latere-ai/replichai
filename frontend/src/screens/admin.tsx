// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// The platform console: who may sign in and at what tier, each account's
// headroom, and the deployment's policy, namely the start reserve, the overdraft
// floor, the rate card and the drafting model.

import { useCallback, useEffect, useRef, useState } from 'react'
import {
  getPlatformSettings,
  getSolvency,
  grantAccess,
  type GrantView,
  listGrants,
  type PlatformSettings,
  type PriceList,
  type ModelRates,
  type RateCard,
  refreezeAccount,
  revokeAccess,
  setPlatformSettings,
  topUp,
  unfreezeAccount,
} from '../api'
import { type I18n } from '../i18n'
import { formatUSD, parseUSD, toDollars } from '../money'
import { Ico } from '../icons'
import { followsList, listName, listedKeys } from '../price-list'
import { absoluteTime, relativeTime } from '../when'
import { GlassCheckbox, GlassSelect } from 'latere-ui/react'
import { NumberField, Pill, PromptDialog, Seams, Tip } from '../ui'
import { CardHead, Empty, Initials, TierPill } from './shared'

// AdminScreen is the console: who may sign in, at what tier, with how much
// headroom, and the platform-wide default they draw from.
export function AdminScreen({ i18n, onCreditsChanged }: { i18n: I18n; onCreditsChanged: () => void }) {
  const { t } = i18n
  const [people, setPeople] = useState<GrantView[] | null>(null)
  const [settings, setSettings] = useState<PlatformSettings | null>(null)
  const [solvency, setSolvency] = useState<number | null>(null)
  const [err, setErr] = useState('')

  const refresh = useCallback(() => {
    listGrants()
      .then((g) => setPeople(g ?? []))
      .catch(() => setPeople([]))
    getPlatformSettings()
      .then(setSettings)
      .catch(() => setSettings(null))
    getSolvency()
      .then((s) => setSolvency(s.outstanding_micro))
      .catch(() => setSolvency(null))
  }, [])
  useEffect(refresh, [refresh])

  const run = async (fn: () => Promise<unknown>) => {
    setErr('')
    try {
      await fn()
      refresh()
    } catch (e) {
      setErr((e as Error).message === '403' ? t('adminNotAdmin') : t('adminError'))
    }
  }

  return (
    <div className="screen measured">
      <div className="screen-head">
        <h1 className="screen-title">
          <Seams i18n={i18n} k="navAdmin" />
        </h1>
        <p className="screen-desc">{t('adminIntro')}</p>
      </div>
      {err && <p className="err small">{err}</p>}
      <div className="stack">
        <PlatformPolicy i18n={i18n} settings={settings} onSave={(next) => run(() => setPlatformSettings(next))} />
        {solvency !== null && (
          <div className="card card-pad" data-tip={t('solvencyTitle')}>
            <div className="usage-line">
              {t('solvency')}
              <span className="v tabular">{formatUSD(i18n.lang, solvency)}</span>
            </div>
          </div>
        )}
        <PeopleCard
          i18n={i18n}
          people={people}
          onGrant={(email, tier) => run(() => grantAccess(email, tier))}
          onRevoke={(email) => run(() => revokeAccess(email))}
          onTopup={(email, amount) =>
            run(async () => {
              await topUp(email, amount)
              onCreditsChanged()
            })
          }
          onUnfreeze={(email) => run(() => unfreezeAccount(email))}
          onRefreeze={(email) => run(() => refreezeAccount(email))}
        />
      </div>
    </div>
  )
}

// environmentLabel names an environment for a person: what it holds, not the
// catalog's word for it. One this console does not know yet is shown by its
// name.
function environmentLabel(t: I18n['t'], name: string): string {
  switch (name) {
    case 'base':
      return t('environmentBase')
    case 'science':
      return t('environmentScience')
    case 'gui':
      return t('environmentGui')
    default:
      return name
  }
}

// PlatformPolicy edits what this deployment charges and what it will risk: two
// numbers and a price list, all micro-USD, typed in dollars. The two numbers
// fail closed in opposite directions: a reserve that vanished would start
// sessions on empty projects; a floor that grew would stop bounding a runaway.
function PlatformPolicy({
  i18n,
  settings,
  onSave,
}: {
  i18n: I18n
  settings: PlatformSettings | null
  onSave: (next: Pick<PlatformSettings, 'start_reserve' | 'overdraft_cap' | 'rates' | 'draft' | 'latere_environments'>) => void
}) {
  const { t } = i18n
  const [reserve, setReserve] = useState('')
  const [floor, setFloor] = useState('')
  // The drafting model: a protocol, an endpoint and a model id. There is no
  // key field; the key belongs to the deployment.
  const [provider, setProvider] = useState('')
  const [baseURL, setBaseURL] = useState('')
  const [model, setModel] = useState('')
  // The price list, as the rows the admin is editing (rateDrafts below).
  const [rates, setRates] = useState<RateDraft[]>([])
  // The environments a Latere session may name. base is always offered,
  // because a session that names none runs on it.
  const [environments, setEnvironments] = useState<string[]>([])
  useEffect(() => {
    if (!settings) return
    setReserve(toDollars(settings.start_reserve))
    setFloor(toDollars(settings.overdraft_cap))
    setProvider(settings.draft?.provider ?? '')
    setBaseURL(settings.draft?.base_url ?? '')
    setModel(settings.draft?.model ?? '')
    setRates((settings.rates?.models ?? []).map((m) => draftOf(m)))
    setEnvironments(settings.latere_environments ?? ['base'])
  }, [settings])

  const nextReserve = parseUSD(reserve)
  const nextFloor = parseUSD(floor)
  const valid = nextReserve !== null && nextReserve >= 0 && nextFloor !== null && nextFloor >= 0
  const draftCfg = { provider: provider.trim(), base_url: baseURL.trim(), model: model.trim() }
  const draftChanged =
    !!settings &&
    (draftCfg.provider !== (settings.draft?.provider ?? '') ||
      draftCfg.base_url !== (settings.draft?.base_url ?? '') ||
      draftCfg.model !== (settings.draft?.model ?? ''))
  // Only a card the admin edited is checked and rebuilt. One they left alone is
  // sent back as the server gave it, so a row this form would refuse, such as
  // an empty card or a row priced at zero, never blocks saving the reserve.
  const serverCard = settings?.rates ?? { models: [] }
  const edited = !!settings && JSON.stringify(rates.map(shownOf)) !== JSON.stringify(serverCard.models.map((m) => shownOf(draftOf(m, -1))))
  const card = edited ? cardOf(rates, t) : { card: serverCard, problem: '' }
  const cardChanged = edited && !!card.card && JSON.stringify(card.card) !== JSON.stringify(serverCard)
  const known = settings?.known_latere_environments ?? []
  const environmentsChanged = !!settings && [...environments].sort().join(',') !== [...(settings.latere_environments ?? ['base'])].sort().join(',')
  const toggleEnvironment = (name: string, on: boolean) =>
    setEnvironments((current) => (on ? [...current.filter((n) => n !== name), name] : current.filter((n) => n !== name)))
  const dirty =
    !!settings &&
    valid &&
    !card.problem &&
    (nextReserve !== settings.start_reserve || nextFloor !== settings.overdraft_cap || draftChanged || cardChanged || environmentsChanged)
  const save = () => {
    if (settings && valid && card.card)
      onSave({
        start_reserve: nextReserve,
        overdraft_cap: nextFloor,
        rates: card.card,
        draft: draftCfg,
        // Sent only when changed, so a console that predates a known
        // environment never narrows the list in force.
        ...(environmentsChanged ? { latere_environments: environments } : {}),
      })
  }

  return (
    <div className="card card-pad stack">
      <span className="eyebrow eyebrow-ico" style={{ color: 'var(--warn)' }}>
        <Ico name="wallet" size={12} /> {t('platformPolicy')}
      </span>
      <div className="policy-row">
        <div className="policy-copy">
          <label className="admin-label" htmlFor="start-reserve">
            {t('startReserve')}
          </label>
          <p className="policy-hint">{t('startReserveHint')}</p>
        </div>
        <NumberField i18n={i18n} id="start-reserve" value={reserve} onChange={setReserve} min={0} ariaLabel={t('startReserve')} />
      </div>
      <div className="policy-row">
        <div className="policy-copy">
          <label className="admin-label" htmlFor="overdraft-cap">
            {t('overdraftCap')}
          </label>
          <p className="policy-hint">{t('overdraftHint')}</p>
        </div>
        <NumberField i18n={i18n} id="overdraft-cap" value={floor} onChange={setFloor} min={0} ariaLabel={t('overdraftCap')} />
      </div>
      {/* Which model writes a project's description. No key field: the
          credential comes from the deployment's environment, because a
          settings row is read by every admin and lands in every backup. The
          model id must also be in the rate card below, or drafting is refused
          rather than guessing a price. */}
      <div className="policy-row">
        <div className="policy-copy">
          <label className="admin-label" htmlFor="draft-provider">
            {t('draftProvider')}
          </label>
          <p className="policy-hint">{t('draftModelHint')}</p>
        </div>
        <div style={{ width: 260 }}>
          <GlassSelect
            value={provider}
            options={[
              { value: '', label: t('draftProviderNone') },
              { value: 'anthropic', label: 'Anthropic' },
              { value: 'openai', label: t('draftProviderOpenAI') },
            ]}
            onChange={setProvider}
            ariaLabel={t('draftProvider')}
          />
        </div>
      </div>
      {provider !== '' && (
        <>
          <div className="policy-row">
            <div className="policy-copy">
              <label className="admin-label" htmlFor="draft-model">
                {t('draftModel')}
              </label>
              {provider === 'anthropic' && <p className="policy-hint">{t('draftUsesAgentKey')}</p>}
            </div>
            <input
              id="draft-model"
              className="text policy-input"
              value={model}
              placeholder={t('draftModelPlaceholder')}
              onChange={(e) => setModel(e.target.value)}
              data-test="draft-model"
            />
          </div>
          {/* Only another vendor needs an address and a key. Anthropic is the
              one this deployment already runs its sessions on, so choosing it
              is a model id and nothing else. */}
          {provider !== 'anthropic' && (
            <div className="policy-row">
              <div className="policy-copy">
                <label className="admin-label" htmlFor="draft-base-url">
                  {t('draftBaseURL')}
                </label>
                {/* The key note sits on the address row, the one that says
                    this is another vendor's endpoint. */}
                <p className="policy-hint">{t('draftNeedsOwnKey')}</p>
              </div>
              <input
                id="draft-base-url"
                className="text policy-input"
                value={baseURL}
                placeholder={t('draftBaseURLPlaceholder')}
                onChange={(e) => setBaseURL(e.target.value)}
                data-test="draft-base-url"
              />
            </div>
          )}
        </>
      )}
      {/* Which of the Latere platform's images a session may name. base
          cannot be turned off: a session that names none runs on it. */}
      {known.length > 0 && (
        <div className="policy-row">
          <div className="policy-copy">
            <span className="admin-label">{t('latereEnvironments')}</span>
            <p className="policy-hint">{t('latereEnvironmentsHint')}</p>
          </div>
          <div className="policy-checks" data-test="latere-environments">
            {known.map((name) => (
              <GlassCheckbox
                key={name}
                value={name === 'base' || environments.includes(name)}
                disabled={name === 'base'}
                onChange={(on) => toggleEnvironment(name, on)}
                label={environmentLabel(t, name)}
              />
            ))}
          </div>
        </div>
      )}
      {settings && <RateCardEditor i18n={i18n} rows={rates} onChange={setRates} problem={card.problem} priceList={settings.price_list} />}
      {/* One Save at the foot of the card: one request writes the whole
          policy, so the button sits where it reads as saving all of it. */}
      <div className="card-foot">
        {settings?.updated_by && <span className="muted small">{t('policyUpdatedBy', { who: settings.updated_by })}</span>}
        <button className="btn" data-test="save-policy" disabled={!dirty} onClick={save}>
          <Ico name="check" size={13} w={2.2} /> {t('savePolicy')}
        </button>
      </div>
    </div>
  )
}

// The prices a row carries, in the order the card's columns show them. Every one
// is micro-USD per million tokens on the wire and dollars per million in the
// field, the unit vendors quote, so an admin copying a price list types the
// number they read.
const PRICES = ['input', 'output', 'cache_read', 'cache_write_5m', 'cache_write_1h'] as const
type PriceField = (typeof PRICES)[number]

// RateDraft is one row of the price list as the admin is editing it: the model
// id and the prices as typed, beside the row the server sent. A save starts
// from that row, so it changes only what was edited and carries every field
// this form does not show, a provider-qualified ref or a meter's price, through
// untouched.
type RateDraft = { key: number; base: Partial<ModelRates> & Record<string, unknown>; model: string } & Record<PriceField, string>

let draftKey = 0
const draftOf = (m: ModelRates, key = draftKey++): RateDraft => ({
  key,
  base: m,
  model: m.model,
  input: toDollars(m.input),
  output: toDollars(m.output),
  cache_read: toDollars(m.cache_read),
  cache_write_5m: toDollars(m.cache_write_5m),
  cache_write_1h: toDollars(m.cache_write_1h),
})
// shownOf is what a row shows the admin, the part an edit can change.
const shownOf = (r: RateDraft) => [r.model, ...PRICES.map((f) => r[f])]
// A new row is blank, prices included: the console never picks a price for the
// admin, and a row that cannot be saved until it is filled in says so.
const blankDraft = (): RateDraft => ({ key: draftKey++, base: {}, model: '', input: '', output: '', cache_read: '', cache_write_5m: '', cache_write_1h: '' })

// cardOf turns the rows back into the card a save sends, or names the first
// thing that keeps it from being one. The rules are the server's own
// (credit.Card.Validate), checked here so the admin learns which row is wrong
// before they press Save rather than from a refusal after it, plus one the
// server tolerates: a row priced at zero for everything is ignored when a
// session is priced, so it would read as a price and charge as the dearest.
function cardOf(rows: RateDraft[], t: I18n['t']): { card: RateCard | null; problem: string } {
  if (rows.length === 0) return { card: null, problem: t('rateEmpty') }
  const seen = new Set<string>()
  const models: ModelRates[] = []
  for (const r of rows) {
    const model = r.model.trim()
    if (!model) return { card: null, problem: t('rateNoModel') }
    if (seen.has(model)) return { card: null, problem: t('rateTwice', { model }) }
    seen.add(model)
    const prices = PRICES.map((f) => parseUSD(r[f]))
    if (prices.some((p) => p === null || p < 0)) return { card: null, problem: t('ratePrice', { model }) }
    if (prices.every((p) => p === 0) && !r.base.free && !r.base.cache_write && !(Array.isArray(r.base.extra) && r.base.extra.length)) {
      return { card: null, problem: t('rateAllZero', { model }) }
    }
    const next: Record<string, unknown> = { ...r.base, model }
    // A rename carries the row's ref with it, keeping its provider: the ref is
    // what pricing matches, and one left behind would price the row under its
    // old name.
    const ref = r.base.model_ref
    if (ref && typeof ref === 'object') next.model_ref = { ...ref, id: model }
    PRICES.forEach((f, i) => {
      next[f] = prices[i]
    })
    models.push(next as ModelRates)
  }
  return { card: { models }, problem: '' }
}

// RateCardEditor is the price list as a table edited in place: a row per
// model, its five prices, and controls to remove a row or add one. A model id
// with no row is charged the card's dearest rates.
//
// When the server follows a price list, it rewrites the rows the list prices
// every hour, so those rows are shown first as read-only text and the admin's
// own rows follow as fields. The grouping is display only; Save sends the rows
// in the order the server holds them.
function RateCardEditor({
  i18n,
  rows,
  onChange,
  problem,
  priceList,
}: {
  i18n: I18n
  rows: RateDraft[]
  onChange: (next: RateDraft[]) => void
  problem: string
  priceList?: PriceList
}) {
  const { t, lang } = i18n
  const added = useRef<number | null>(null)
  const grid = useRef<HTMLDivElement | null>(null)
  // The new row's model id takes the cursor, since naming the model is the
  // first thing a new row needs.
  useEffect(() => {
    if (added.current === null) return
    grid.current?.querySelector<HTMLInputElement>(`[data-key="${added.current}"] input[data-field="model"]`)?.focus()
    added.current = null
  }, [rows])
  const set = (key: number, field: 'model' | PriceField, value: string) => onChange(rows.map((r) => (r.key === key ? { ...r, [field]: value } : r)))
  const label: Record<PriceField, string> = {
    input: t('rateInput'),
    output: t('rateOutput'),
    cache_read: t('rateCacheRead'),
    cache_write_5m: t('rateCacheWrite5m'),
    cache_write_1h: t('rateCacheWrite1h'),
  }
  const listed = listedKeys(priceList)
  const theirs = rows.filter((r) => followsList(r.base, listed))
  const own = theirs.length > 0 ? rows.filter((r) => !followsList(r.base, listed)) : rows
  const list = listName(priceList?.url)
  return (
    <div className="stack" style={{ gap: 8 }}>
      <span className="muted small">{t('rateCardHint')}</span>
      {priceList && !priceList.applied_at && (
        <span className="muted small" data-test="rate-list-pending">
          {t('rateListPending', { list })}
        </span>
      )}
      <div className="rate-card" ref={grid}>
        <div className="rate-row rate-head" aria-hidden="true">
          <span>{t('rateModel')}</span>
          {PRICES.map((f) => (
            <span key={f}>{label[f]}</span>
          ))}
          <span />
        </div>
        {theirs.length > 0 && (
          <>
            <div className="rate-group" data-test="rate-group-list">
              <span className="rate-group-name">{t('rateFromList', { list })}</span>
              <span className="rate-group-note" data-tip={absoluteTime(lang, priceList?.applied_at)} data-test="rate-list-applied">
                {t('rateFromListNote', { list, when: relativeTime(t, lang, priceList?.applied_at) })}
              </span>
            </div>
            {theirs.map((r) => (
              <div className="rate-row rate-listed" key={r.key} data-key={r.key} data-test="rate-row" data-source="list">
                <span className="rate-cell rate-model">
                  <span className="rate-label">{t('rateModel')}</span>
                  <span className="rate-value mono">{r.model}</span>
                </span>
                {PRICES.map((f) => (
                  <span className="rate-cell" key={f}>
                    <span className="rate-label">{label[f]}</span>
                    <span className="rate-value tabular">{r[f]}</span>
                  </span>
                ))}
                <span />
              </div>
            ))}
            {own.length > 0 && (
              <div className="rate-group" data-test="rate-group-own">
                <span className="rate-group-name">{t('rateOwn')}</span>
                <span className="rate-group-note">{t('rateOwnNote', { list })}</span>
              </div>
            )}
          </>
        )}
        {own.map((r) => (
          <div className="rate-row" key={r.key} data-key={r.key} data-test="rate-row" data-source="admin">
            <label className="rate-cell rate-model">
              <span className="rate-label">{t('rateModel')}</span>
              <input
                className="text mono"
                data-field="model"
                value={r.model}
                placeholder={t('rateModelPlaceholder')}
                spellCheck={false}
                onChange={(e) => set(r.key, 'model', e.target.value)}
              />
            </label>
            {PRICES.map((f) => (
              <label className="rate-cell" key={f}>
                <span className="rate-label">{label[f]}</span>
                <input
                  className="text tabular"
                  data-field={f}
                  inputMode="decimal"
                  value={r[f]}
                  placeholder="0.00"
                  aria-label={`${r.model || t('rateModel')}: ${label[f]}`}
                  onChange={(e) => set(r.key, f, e.target.value)}
                />
              </label>
            ))}
            <button
              type="button"
              className="icon-btn danger rate-remove"
              data-test="rate-remove"
              disabled={rows.length === 1}
              aria-label={t('rateRemove', { model: r.model || t('rateModel') })}
              data-tip={t('rateRemove', { model: r.model || t('rateModel') })}
              onClick={() => onChange(rows.filter((x) => x.key !== r.key))}
            >
              <Ico name="trash" size={14} />
            </button>
          </div>
        ))}
      </div>
      <div className="rate-foot">
        <button
          type="button"
          className="btn ghost sm"
          data-test="rate-add"
          onClick={() => {
            const row = blankDraft()
            added.current = row.key
            onChange([...rows, row])
          }}
        >
          <Ico name="plus" size={13} /> {t('rateAdd')}
        </button>
        {problem && (
          <span className="err small" role="status" data-test="rate-error">
            {problem}
          </span>
        )}
      </div>
    </div>
  )
}

// PeopleCard is the user list: who has signed in, at what tier, with what
// headroom. Everything here is seeded by email. Nobody needs to have signed in
// first, and a grant binds to them when they do.
function PeopleCard({
  i18n,
  people,
  onGrant,
  onRevoke,
  onTopup,
  onUnfreeze,
  onRefreeze,
}: {
  i18n: I18n
  people: GrantView[] | null
  onGrant: (email: string, tier: 'user' | 'admin') => void
  onRevoke: (email: string) => void
  onTopup: (email: string, amount: number) => void
  onUnfreeze: (email: string) => void
  onRefreeze: (email: string) => void
}) {
  const { t } = i18n
  const [email, setEmail] = useState('')
  const [tier, setTier] = useState<'user' | 'admin'>('user')
  // Show only the accounts that owe, which are the debts an operator acts on.
  const [owingOnly, setOwingOnly] = useState(false)
  // Which row's top-up is open in the app's own dialog.
  const [topping, setTopping] = useState<GrantView | null>(null)

  const invite = (e: React.FormEvent) => {
    e.preventDefault()
    const target = email.trim()
    if (!target) return
    onGrant(target, tier)
    setEmail('')
  }

  const shown = people && owingOnly ? people.filter((p) => p.wallet < 0) : people

  return (
    <div className="card">
      <CardHead label={t('people')} icon="users" tone="var(--brand)">
        <button className={'btn ghost sm' + (owingOnly ? ' active' : '')} style={{ marginLeft: 'auto' }} aria-pressed={owingOnly} onClick={() => setOwingOnly((v) => !v)}>
          <Ico name="alert" size={12} /> {t('onlyOwing')}
        </button>
      </CardHead>
      {shown === null ? (
        <div className="empty">{t('loading')}</div>
      ) : shown.length === 0 ? (
        <Empty icon="userPlus" text={owingOnly ? t('noOwing') : t('noGrants')} />
      ) : (
        shown.map((p) => (
          <div key={p.email} className="member-row">
            <Initials email={p.email} />
            <span className="member-email">{p.email}</span>
            <TierPill i18n={i18n} tier={p.tier} />
            {!p.bound && (
              <Pill tone="warn" icon="clock">
                {t('pending')}
              </Pill>
            )}
            {p.frozen && (
              <Pill tone="err" icon="alert">
                {t('frozen')}
              </Pill>
            )}
            <Tip text={t('walletOf', { email: p.email })}>
              <span className="muted small tabular member-wallet">{formatUSD(i18n.lang, p.wallet)}</span>
            </Tip>
            {/* One group, so the controls stay on one line on a phone and end
                where every other row's do. */}
            <span className="row-actions">
              {p.wallet < 0 &&
                (p.frozen ? (
                  <button className="btn ghost sm" data-tip={t('unfreezeTitle')} onClick={() => onUnfreeze(p.email)}>
                    <Ico name="check" size={12} /> {t('unfreeze')}
                  </button>
                ) : (
                  <button className="btn ghost sm" data-tip={t('refreezeTitle')} onClick={() => onRefreeze(p.email)}>
                    <Ico name="alert" size={12} /> {t('refreeze')}
                  </button>
                ))}
              <button className="btn ghost sm" onClick={() => setTopping(p)}>
                <Ico name="wallet" size={12} /> {t('topup')}
              </button>
              <button className="btn ghost sm danger" onClick={() => onRevoke(p.email)}>
                <Ico name="x" size={12} w={2} /> {t('revoke')}
              </button>
            </span>
          </div>
        ))
      )}
      <form className="field-row" onSubmit={invite}>
        <input
          className="text"
          style={{ flex: 1 }}
          type="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder={t('inviteEmail')}
          spellCheck={false}
        />
        <div style={{ width: 132 }}>
          <GlassSelect
            value={tier}
            onChange={(v) => setTier(v === 'admin' ? 'admin' : 'user')}
            ariaLabel={t('access')}
            options={[
              { value: 'user', label: t('tierUser') },
              { value: 'admin', label: t('tierAdmin') },
            ]}
          />
        </div>
        <button className="btn">
          <Ico name="userPlus" size={13} /> {t('grant')}
        </button>
      </form>
      <div style={{ padding: '0 14px 12px' }}>
        <span className="muted small">{t('grantHint')}</span>
      </div>
      {topping && (
        <PromptDialog
          i18n={i18n}
          title={t('topup')}
          body={t('topupPrompt', { email: topping.email })}
          initial="10"
          parse={parseUSD}
          confirmLabel={t('save')}
          cancelLabel={t('cancel')}
          onCancel={() => setTopping(null)}
          onConfirm={(n) => {
            onTopup(topping.email, n)
            setTopping(null)
          }}
        />
      )}
    </div>
  )
}
