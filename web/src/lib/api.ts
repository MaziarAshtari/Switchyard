// Small fetch wrapper for the admin API. Errors are thrown as ApiError.

export class ApiError extends Error {
  readonly status: number
  readonly fields: Record<string, string>

  constructor(status: number, message: string, fields: Record<string, string> = {}) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.fields = fields
  }
}

interface RequestOptions {
  /** Set to false when a 401 means "wrong input" rather than "session expired". */
  authRedirect?: boolean
}

let onUnauthorized = () => {}

/** Registers what happens when the session has expired (a 401 from any call except login/me). */
export function setUnauthorizedHandler(handler: () => void) {
  onUnauthorized = handler
}

const authEndpoints = new Set(['/api/auth/login', '/api/auth/me'])

async function request<T>(method: string, path: string, body?: unknown, opts: RequestOptions = {}): Promise<T> {
  let res: Response
  try {
    res = await fetch(path, {
      method,
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch {
    throw new ApiError(0, 'Cannot reach the Switchyard server. Check that it is running.')
  }

  if (res.status === 401 && opts.authRedirect !== false && !authEndpoints.has(path)) onUnauthorized()

  const text = await res.text()
  let data: unknown
  if (text) {
    try {
      data = JSON.parse(text)
    } catch {
      data = undefined // e.g. an HTML error page from a reverse proxy
    }
  }

  if (!res.ok) {
    const err = (data ?? {}) as { error?: unknown; fields?: unknown }
    const message =
      typeof err.error === 'string' && err.error ? err.error : `HTTP ${res.status}${res.statusText ? ` ${res.statusText}` : ''}`
    const fields = err.fields && typeof err.fields === 'object' ? (err.fields as Record<string, string>) : {}
    throw new ApiError(res.status, message, fields)
  }
  return data as T
}

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T = void>(path: string, body?: unknown, opts?: RequestOptions) => request<T>('POST', path, body, opts),
  put: <T>(path: string, body: unknown) => request<T>('PUT', path, body),
  del: (path: string) => request<void>('DELETE', path),
}

/** Human-readable message for any thrown value (never "[object Object]"). */
export function errorMessage(err: unknown): string {
  if (err instanceof Error && err.message) return err.message
  if (typeof err === 'string' && err) return err
  return 'Something went wrong.'
}

/** Like errorMessage, but spells out field errors for places that cannot highlight fields (toasts). */
export function describeError(err: unknown): string {
  if (err instanceof ApiError && Object.keys(err.fields).length > 0) return Object.values(err.fields).join(' ')
  return errorMessage(err)
}

/** Builds "?a=1&b=2", skipping empty values. */
export function qs(params: Record<string, string | number | null | undefined>): string {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== null && value !== '') search.set(key, String(value))
  }
  const s = search.toString()
  return s ? `?${s}` : ''
}
