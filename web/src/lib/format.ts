// Display helpers. All functions are pure apart from reading the current time.

/** "just now", "3 min ago", "2 h ago", "5 days ago"; older dates as a local date. */
export function relativeTime(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return '—'
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return '—'
  const sec = Math.round((now - t) / 1000)
  if (sec < 0) return `in ${duration(-sec)}`
  if (sec < 45) return 'just now'
  const min = Math.round(sec / 60)
  if (min < 60) return `${min} min ago`
  const hours = Math.round(min / 60)
  if (hours < 24) return `${hours} h ago`
  const days = Math.round(hours / 24)
  if (days < 30) return days === 1 ? '1 day ago' : `${days} days ago`
  return new Date(t).toLocaleDateString()
}

/** Full local date and time, e.g. for title attributes. */
export function fullTime(iso: string | null | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString()
}

/** "340 ms" / "2.1 s" */
export function latency(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)} ms`
  return `${(ms / 1000).toFixed(1)} s`
}

/** "45s", "3m 20s", "1h 5m" */
export function duration(totalSeconds: number): string {
  const s = Math.max(0, Math.round(totalSeconds))
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return s % 60 ? `${m}m ${s % 60}s` : `${m}m`
  const h = Math.floor(m / 60)
  return m % 60 ? `${h}h ${m % 60}m` : `${h}h`
}

/** 950, 1.2k, 3.4M, 1.1B */
export function compact(n: number): string {
  const abs = Math.abs(n)
  const fmt = (v: number, unit: string) => `${v.toFixed(1).replace(/\.0$/, '')}${unit}`
  if (abs < 1000) return String(Math.round(n))
  if (abs < 999_950) return fmt(n / 1e3, 'k')
  if (abs < 999_950_000) return fmt(n / 1e6, 'M')
  return fmt(n / 1e9, 'B')
}

/** "1 key", "3 keys"; pass the plural form for irregular words. */
export function plural(n: number, word: string, pluralWord = `${word}s`): string {
  return `${n} ${n === 1 ? word : pluralWord}`
}

/** ISO timestamp of today's local midnight. */
export function startOfToday(): string {
  const d = new Date()
  d.setHours(0, 0, 0, 0)
  return d.toISOString()
}

/** Converts a datetime-local input value ("2026-10-30T14:05") to ISO, or "" when empty/invalid. */
export function localInputToIso(value: string): string {
  if (!value) return ''
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString()
}
