// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest'
import { repoSuffix, runLabel } from './repo'

// Five reproductions of one paper differ by four characters. A row that labels
// the link "GitHub" renders all five identically, which is exactly the case a
// repeat study produces.
describe('repoSuffix', () => {
  it('names the run by the part the registry allocated', () => {
    expect(repoSuffix('open-hai/progressive-autonomy-as-preference-learning-a-k46m')).toBe('k46m')
    expect(repoSuffix('open-hai/progressive-autonomy-as-preference-learning-a-2zjw')).toBe('2zjw')
  })

  it('keeps a name that carries no suffix whole', () => {
    expect(repoSuffix('open-hai/replichai-spike-mount')).toBe('replichai-spike-mount')
    expect(repoSuffix('open-hai/posture')).toBe('posture')
  })

  it('reads a bare name with no owner', () => {
    expect(repoSuffix('gender-in-image-search-a3f9')).toBe('a3f9')
  })
})

// A row under a paper card says what the run ran on. The paper's own reference
// is on the card above it, so repeating it per row told the reader nothing
// about which of five sessions they were looking at.
describe('runLabel', () => {
  it('names the model the session ran on', () => {
    expect(runLabel({ ref: 'https://doi.org/10.1145/2702123.2702520', status: { State: 'done', Model: 'gpt-6-astra' } as never })).toBe('gpt-6-astra')
    expect(runLabel({ ref: 'x', status: { Model: 'claude-opus-5' } as never })).toBe('claude-opus-5')
  })

  it('falls back to the reference before a worker has reported one', () => {
    expect(runLabel({ ref: 'https://doi.org/10.1145/2702123.2702520' })).toBe('https://doi.org/10.1145/2702123.2702520')
    expect(runLabel({ ref: 'x', status: { Model: '  ' } as never })).toBe('x')
  })
})
