import { useCallback, useEffect, useState } from 'react'

// Whether the navigation rail is folded down to its icons.
//
// This is a preference about the app's chrome, not a place, so it lives in
// localStorage rather than the address bar: two people opening the same shared
// link should each see the rail the width they left it at, and folding the rail
// is not something Back should undo.
const KEY = 'sidebar'

// The value stored when the rail is folded. Anything else — including the absent
// value of a first visit — reads as open, so the rail a new operator meets is the
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

// useDrawer holds whether the rail is showing as an overlay, which is what it
// does below the phone breakpoint (030).
//
// This is deliberately not the fold above. The fold is a preference — a width
// the operator chose, kept across reloads — and a drawer standing open is a
// moment, not a preference: a phone that reopened the navigation on every load
// would hide the screen the link pointed at. So it lives in memory and starts
// closed, and nothing here reads or writes `sidebar`.
//
// There is no media query here either. Above the breakpoint the rail is a
// column in the flow and the class this state adds paints nothing, so the state
// is free to be open and mean nothing — which is what keeps the two widths one
// component rather than two.
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
