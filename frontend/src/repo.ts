// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// How one run is named on a row: by its repository's suffix, and by what it
// ran on. Pure, so the test reads both without a browser.

// repoSuffix is the part of a reproduction's repository name that identifies
// this run: the registry gives every reproduction of one paper the same slug
// and a short random suffix, so the suffix is the only part that differs
// between five runs of the same paper. A name with no suffix, an owner and a
// bare slug, is shown whole rather than sliced into something misleading.
export function repoSuffix(repo: string): string {
  const name = repo.slice(repo.lastIndexOf('/') + 1)
  const cut = name.lastIndexOf('-')
  const tail = cut < 0 ? '' : name.slice(cut + 1)
  return /^[a-z0-9]{4}$/.test(tail) ? tail : name
}

// runLabel says what one session ran on, for a row listed under the paper it
// reproduced. The model id is the answer, because comparing models is what a
// repeat study is for. A session whose worker has not reported a model yet
// falls back to the reference.
export function runLabel(j: { ref: string; status?: { Model?: string } }): string {
  return j.status?.Model?.trim() || j.ref
}
