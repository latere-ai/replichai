// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// The New-project form (012, 036): a title, the papers it is about, each
// looked up before it is submitted, and the collaborators to invite. Creating
// a project spends nothing, so the form knows nothing about credits.

import { useState } from 'react'
import { createProject, type ResolvedPaper, resolvePaper } from '../api'
import { invalidateProjects } from '../projects-store'
import { type I18n } from '../i18n'
import { createOutcome, generic, refusal } from '../errors'
import { Ico } from '../icons'
import { Seams, Tip } from '../ui'

// A paper row in the New-project form: the link, and its looked-up preview
// title or error, filled by the Look-up button.
type PaperRow = { url: string; preview?: ResolvedPaper; error?: string; resolving?: boolean }

// Creating a project spends nothing (012), so this screen needs to know
// nothing about anybody's credits.
export function SubmitScreen({ i18n, onSubmitted }: { i18n: I18n; onSubmitted: (projectId: string) => void }) {
  const { t } = i18n
  const [title, setTitle] = useState('')
  const [rows, setRows] = useState<PaperRow[]>([{ url: '' }])
  const [collaborators, setCollaborators] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  const setRow = (i: number, patch: Partial<PaperRow>) => setRows((rs) => rs.map((r, k) => (k === i ? { ...r, ...patch } : r)))
  const addRow = () => setRows((rs) => [...rs, { url: '' }])
  const removeRow = (i: number) => setRows((rs) => (rs.length > 1 ? rs.filter((_, k) => k !== i) : rs))

  // Look one row's link up and show the title it found, or why it could not.
  const resolveRow = async (i: number) => {
    const url = rows[i].url.trim()
    if (!url) return
    setRow(i, { resolving: true, preview: undefined, error: undefined })
    try {
      const preview = await resolvePaper(url)
      setRow(i, { preview, resolving: false })
    } catch (e) {
      const { status, offline } = refusal(e)
      const msg = offline ? t('errOffline') : status === 400 ? t('unresolvablePaper') : t('resolveFailed')
      setRow(i, { error: msg, resolving: false })
    }
  }

  const submit = async () => {
    const papers = rows.map((r) => r.url.trim()).filter(Boolean)
    // A project may start empty (036), since it records papers and runs nothing,
    // but it may not start nameless, because the title falls back to the first
    // paper's reference and with no paper there is no fallback.
    if (papers.length === 0 && !title.trim()) {
      setErr(t('nameOrPaper'))
      return
    }
    if (busy) return
    setBusy(true)
    setErr('')
    const collabs = collaborators.split(/[,\s]+/).map((s) => s.trim()).filter(Boolean)
    try {
      const { status, data } = await createProject(title.trim(), papers, collabs)
      const outcome = createOutcome(t, status, data)
      setRows((rs) => rs.map((r) => (outcome.rows.has(r.url.trim()) ? { ...r, error: outcome.rows.get(r.url.trim()) } : r)))
      setErr(outcome.banner)
      // The project exists either way. It records papers and runs nothing, so
      // there is no partial state to explain and nothing to keep the person here
      // for. A reference the server did not recognise is reported on its row and
      // can be added again from inside the project.
      if (outcome.ok) {
        invalidateProjects()
        onSubmitted(data.project_id!)
      }
    } catch (e) {
      setErr(generic(t, e, 'createFailed'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="screen measured form">
      <div className="submit-form">
        <div className="screen-head">
          <h1 className="screen-title">
          <Seams i18n={i18n} k="newProject" />
        </h1>
          <p className="screen-desc">{t('newProjectDesc')}</p>
        </div>

        <label className="field">
          {t('projectNameLabel')}
          <input
            className="text"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder={t('projectNamePlaceholder')}
            spellCheck={false}
          />
        </label>

        <div className="field">
          {t('papersLabel')}
          <span className="attestation">{t('papersOptional')}</span>
          <div className="stack" style={{ gap: 10 }}>
            {rows.map((row, i) => (
              <div key={i} className="paper-row">
                <div className="paper-row-input">
                  <input
                    className="text"
                    value={row.url}
                    onChange={(e) => setRow(i, { url: e.target.value, preview: undefined, error: undefined })}
                    placeholder={t('paperPlaceholder')}
                    spellCheck={false}
                    onKeyDown={(e) => e.key === 'Enter' && resolveRow(i)}
                          />
                  <button className="btn ghost sm" disabled={!row.url.trim() || row.resolving } onClick={() => resolveRow(i)}>
                    <Ico name="search" size={12} w={2} />
                    {row.resolving ? t('resolvingPaper') : t('resolve')}
                  </button>
                  {rows.length > 1 && (
                    <Tip text={t('removePaper')}>
                      <button className="icon-btn danger" aria-label={t('removePaper')} onClick={() => removeRow(i)}>
                        <Ico name="x" size={13} w={1.9} />
                      </button>
                    </Tip>
                  )}
                </div>
                {row.preview && (
                  <div className="paper-preview">
                    <Ico name="check" size={12} w={2.2} style={{ color: 'var(--ok)' }} /> {row.preview.title || row.preview.key}
                  </div>
                )}
                {row.error && (
                  <div className="paper-preview err">
                    <Ico name="alert" size={12} /> {row.error}
                  </div>
                )}
              </div>
            ))}
          </div>
          <button className="btn ghost sm" onClick={addRow} style={{ alignSelf: 'flex-start', marginTop: 4 }}>
            <Ico name="plus" size={12} w={2.2} /> {t('addPaper')}
          </button>
        </div>

        <label className="field">
          {t('collaboratorsLabel')}
          <input
            className="text"
            value={collaborators}
            onChange={(e) => setCollaborators(e.target.value)}
            placeholder={t('collaboratorsPlaceholder')}
            spellCheck={false}
          />
          <span className="attestation">{t('collaboratorsHint')}</span>
        </label>

        <div className="submit-input-row">
          <button className="btn lg" disabled={busy} onClick={submit}>
            <Ico name="sparkles" size={14} /> {busy ? t('creatingProject') : t('createProjectBtn')}
          </button>
          <span className="attestation" style={{ alignSelf: 'center' }}>{t('submitNoSpend')}</span>
        </div>
        {err && <p className="err small">{err}</p>}
      </div>
    </div>
  )
}
