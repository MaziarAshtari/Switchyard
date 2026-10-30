import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { api, describeError } from './api'
import { latency } from './format'
import type { ConfirmOptions } from '@/components/ConfirmDialog'
import type { ApiKey, GatewayKey, Me, Model, ModelTestResult, Provider, Proxy, Settings, TestResult } from './types'

// Shared queries. Query keys start with the resource name, so invalidating
// ["providers"] also covers ["providers", id] and ["providers", id, "keys"].

/** GET a list; tolerates `null` (how Go encodes an empty nil slice). */
const list = async <T>(path: string) => (await api.get<T[] | null>(path)) ?? []

export const useMe = () =>
  useQuery({ queryKey: ['me'], queryFn: () => api.get<Me>('/api/auth/me'), retry: false, staleTime: Infinity })

export const useProviders = () =>
  useQuery({ queryKey: ['providers'], queryFn: () => list<Provider>('/api/providers') })

export const useProvider = (id: number) =>
  useQuery({ queryKey: ['providers', id], queryFn: () => api.get<Provider>(`/api/providers/${id}`) })

export const useProviderKeys = (id: number) =>
  useQuery({
    queryKey: ['providers', id, 'keys'],
    queryFn: () => list<ApiKey>(`/api/providers/${id}/keys`),
    refetchInterval: 10_000,
  })

export const useAllKeys = () => useQuery({ queryKey: ['keys'], queryFn: () => list<ApiKey>('/api/keys') })

export const useProxies = () => useQuery({ queryKey: ['proxies'], queryFn: () => list<Proxy>('/api/proxies') })

export const useModels = () => useQuery({ queryKey: ['models'], queryFn: () => list<Model>('/api/models') })

export const useSettings = () => useQuery({ queryKey: ['settings'], queryFn: () => api.get<Settings>('/api/settings') })

export const useGatewayKeys = () =>
  useQuery({ queryKey: ['gateway-keys'], queryFn: () => list<GatewayKey>('/api/gateway-keys') })

/**
 * Marks all server data (except the session) as stale. Resources reference each
 * other (key counts, proxy usage, model targets...), so after any change we simply
 * refetch whatever is on screen instead of tracking every dependency.
 */
function useInvalidateAll() {
  const queryClient = useQueryClient()
  return () => queryClient.invalidateQueries({ predicate: (query) => query.queryKey[0] !== 'me' })
}

interface ActionOptions<TData, TVars> {
  /** Toast shown on success; a function may return nothing and show its own toast. */
  success?: string | ((data: TData, vars: TVars) => string | void)
  /** Set to false when the caller shows errors itself (inline form errors). */
  toastErrors?: boolean
}

/** A mutation that refreshes all data on success and reports the outcome with a toast. */
export function useAction<TVars = void, TData = unknown>(
  fn: (vars: TVars) => Promise<TData>,
  { success, toastErrors = true }: ActionOptions<TData, TVars> = {},
) {
  const invalidateAll = useInvalidateAll()
  return useMutation({
    mutationFn: fn,
    onSuccess: (data, vars) => {
      void invalidateAll()
      const message = typeof success === 'function' ? success(data, vars) : success
      if (message) toast.success(message)
    },
    onError: (err) => {
      if (toastErrors) toast.error(describeError(err))
    },
  })
}

/** Toast for a key/proxy connectivity test, e.g. "Key test OK · 200 · 340 ms". */
export function notifyTest(subject: string, result: TestResult) {
  const summary = [result.status_code ? String(result.status_code) : '', latency(result.latency_ms)].filter(Boolean).join(' · ')
  if (result.ok) toast.success(`${subject} test OK · ${summary}`)
  else toast.error(`${subject} test failed · ${summary}`, { description: result.error || undefined })
}

/** Sends a sample request to a saved model. Image tests cost money, so they ask first. */
export function useModelTest(confirm: (options: ConfirmOptions) => void) {
  const invalidateAll = useInvalidateAll()
  const test = useMutation({
    mutationFn: (model: Model) => api.post<ModelTestResult>(`/api/models/${model.id}/test`),
    // A test updates key states and adds a log entry.
    onSettled: () => void invalidateAll(),
  })

  /** onStart runs when the request is actually sent (after confirmation for image models). */
  function run(model: Model, onStart?: () => void) {
    test.reset()
    const start = () => {
      onStart?.()
      test.mutate(model)
    }
    if (model.type !== 'image') {
      start()
      return
    }
    confirm({
      title: `Test ${model.name}?`,
      description: 'Sends a real image generation request; this may cost money.',
      confirmLabel: 'Send request',
      destructive: false,
      onConfirm: async () => start(),
    })
  }

  return { test, run }
}
