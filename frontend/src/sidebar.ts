// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState } from 'react'

// Whether the navigation rail is folded down to its icons.
//
// This is a preference about the app's chrome, not a place, so it lives in
// localStorage rather than the address bar: two people opening the same shared
// link should each see the rail the width they left it at, and folding the rail
// is not something Back should undo.
const KEY = 'sidebar'

// The value stored when the rail is folded. Anything else, including the absent
// value of a first visit, reads as open, so the rail a new operator meets is the
// one that names its destinations.
const FOLDED = 'folded'

// readFolded accepts what localStorage held.
export function readFolded(raw: string | null): boolean {
  return raw === FOLDED
}

// useSidebar persists the fold and hands back a toggle. There is no media query
// here on purpose: the rail folds when it is asked to and at no other time, so a
// width the operator chose is never overridden by one the window happened to be.
export function useSidebar(): { folded: boolean; toggle: () => void } {
  const [folded, setFolded] = useState<boolean>(() => readFolded(localStorage.getItem(KEY)))

  useEffect(() => {
    localStorage.setItem(KEY, folded ? FOLDED : 'open')
  }, [folded])

  const toggle = useCallback(() => setFolded((f) => !f), [])
  return { folded, toggle }
}

// useDrawer holds whether the rail is showing as an overlay, which is how it
// appears below the phone breakpoint. Unlike the fold, an open drawer is
// transient: it lives in memory, starts closed and never touches `sidebar`, so a
// phone does not reopen the navigation over the linked screen on every load.
// Above the breakpoint the class this state adds paints nothing, so one
// component serves both widths without a media query.
export function useDrawer(): { open: boolean; show: () => void; hide: () => void } {
  const [open, setOpen] = useState(false)

  // Escape closes it, the same key that closes every other overlay in the app.
  useEffect(() => {
    if (!open) return
    const esc = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('keydown', esc)
    return () => document.removeEventListener('keydown', esc)
  }, [open])

  const show = useCallback(() => setOpen(true), [])
  const hide = useCallback(() => setOpen(false), [])
  return { open, show, hide }
}
