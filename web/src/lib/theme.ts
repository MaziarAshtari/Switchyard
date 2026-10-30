import { useSyncExternalStore } from 'react'

// Light/dark mode: the "dark" class on <html>. public/theme.js applies the saved
// choice before the app loads; this module keeps it in sync afterwards.

export type Theme = 'light' | 'dark' | 'system'

const STORAGE_KEY = 'switchyard-theme'
const media = window.matchMedia('(prefers-color-scheme: dark)')
const listeners = new Set<() => void>()

let theme: Theme = readTheme()

function readTheme(): Theme {
  try {
    const value = localStorage.getItem(STORAGE_KEY)
    return value === 'light' || value === 'dark' ? value : 'system'
  } catch {
    return 'system'
  }
}

function resolvedTheme(): 'light' | 'dark' {
  if (theme === 'system') return media.matches ? 'dark' : 'light'
  return theme
}

function update() {
  document.documentElement.classList.toggle('dark', resolvedTheme() === 'dark')
  listeners.forEach((listener) => listener())
}

function setTheme(next: Theme) {
  theme = next
  try {
    localStorage.setItem(STORAGE_KEY, next)
  } catch {
    // Storage may be unavailable (private mode); the choice then lasts for this page only.
  }
  update()
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

media.addEventListener('change', update)
update()

export function useTheme() {
  const current = useSyncExternalStore(subscribe, () => theme)
  const resolved = useSyncExternalStore(subscribe, resolvedTheme)
  return { theme: current, resolved, setTheme }
}
