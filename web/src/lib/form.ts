import { useCallback, useState } from 'react'
import { ApiError, errorMessage } from './api'

/** Field messages keyed like the API ("name", "targets.0.upstream_model"); "_" holds the general message. */
export type Errors = Record<string, string>

export function toErrors(err: unknown): Errors {
  if (err instanceof ApiError) return { ...err.fields, _: err.message }
  return { _: errorMessage(err) }
}

/** Parses a whole number typed into an input; null when invalid or outside [min, max]. */
export function intInRange(value: string, min: number, max = Number.MAX_SAFE_INTEGER): number | null {
  if (!/^\s*\d+\s*$/.test(value)) return null
  const n = Number(value)
  return n >= min && n <= max ? n : null
}

/** useState for a flat form object, with a typed per-field setter. */
export function useFormState<T extends object>(initial: T | (() => T)) {
  const [values, setValues] = useState<T>(initial)
  const set = useCallback(<K extends keyof T>(key: K, value: T[K]) => {
    setValues((current) => ({ ...current, [key]: value }))
  }, [])
  return [values, set, setValues] as const
}
