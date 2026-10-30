// Runs before the app loads so the page never flashes the wrong theme.
// Keep in sync with src/lib/theme.ts (same storage key and values).
try {
  var t = localStorage.getItem('switchyard-theme')
  var dark = t === 'dark' || (t !== 'light' && matchMedia('(prefers-color-scheme: dark)').matches)
  if (dark) document.documentElement.classList.add('dark')
} catch {}
