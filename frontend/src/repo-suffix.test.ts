// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest'
import { repoSuffix } from './App'

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
