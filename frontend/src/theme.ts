import { useEffect, useState } from 'react'

// The appearance the operator chose. "auto" defers to the operating system;
// the other two pin it. The choice is what persists — never the resolved
// value — so a machine that switches at sunset follows along.
export type ThemeMode = 'auto' | 'light' | 'dark'

// The value actually stamped on <html>. Every dark rule hangs off
// [data-theme='dark'], so "auto" must be resolved here rather than written out.
export type Theme = 'light' | 'dark'

export const THEME_MODES: ThemeMode[] = ['auto', 'light', 'dark']

const KEY = 'theme'
const DARK_QUERY = '(prefers-color-scheme: dark)'

// readMode accepts what localStorage held. Anything unrecognised — and the
// absent value of a first visit — reads as "auto"; the two legacy values this
// app used to store are already valid modes, so a returning operator keeps the
// appearance they picked.
export function readMode(raw: string | null): ThemeMode {
  return raw === 'light' || raw === 'dark' || raw === 'auto' ? raw : 'auto'
}

// resolveTheme turns a mode plus the system preference into the one value the
// stylesheet understands.
export function resolveTheme(mode: ThemeMode, prefersDark: boolean): Theme {
  if (mode === 'auto') return prefersDark ? 'dark' : 'light'
  return mode
}

// useTheme persists the mode, resolves it against the system preference, and
// stamps the result on <html>. While the mode is "auto" it stays subscribed to
// the media query, so the appearance follows the OS without a reload.
export function useTheme(): { mode: ThemeMode; theme: Theme; setMode: (m: ThemeMode) => void } {
  const [mode, setMode] = useState<ThemeMode>(() => readMode(localStorage.getItem(KEY)))
  const [prefersDark, setPrefersDark] = useState(() => window.matchMedia?.(DARK_QUERY).matches ?? false)

  useEffect(() => {
    const mq = window.matchMedia?.(DARK_QUERY)
    if (!mq) return
    const sync = () => setPrefersDark(mq.matches)
    sync()
    mq.addEventListener('change', sync)
    return () => mq.removeEventListener('change', sync)
  }, [])

  const theme = resolveTheme(mode, prefersDark)
  useEffect(() => {
    document.documentElement.dataset.theme = theme
    localStorage.setItem(KEY, mode)
  }, [mode, theme])

  return { mode, theme, setMode }
}
