import { useState, type FormEvent } from 'react'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Badge } from '@/components/ui/badge'
import { ErrorState, FormError, Loading } from '@/components/common'
import { api, describeError } from '@/lib/api'
import { plural } from '@/lib/format'
import { keyProxyText } from '@/lib/labels'
import { useAction, useAllKeys } from '@/lib/queries'
import type { ApiKey, Proxy } from '@/lib/types'

interface AssignDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The proxies to assign (selected on the Proxies page). */
  proxies: Proxy[]
}

export function AssignDialog({ open, onOpenChange, proxies }: AssignDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Assign {plural(proxies.length, 'proxy', 'proxies')} to keys</DialogTitle>
          <DialogDescription>
            One proxy: all selected keys use it. Several proxies: they are distributed round-robin across the selected
            keys.
          </DialogDescription>
        </DialogHeader>
        <AssignForm proxies={proxies} onDone={() => onOpenChange(false)} />
      </DialogContent>
    </Dialog>
  )
}

function groupByProvider(keys: ApiKey[]) {
  const groups = new Map<number, { id: number; name: string; keys: ApiKey[] }>()
  for (const key of keys) {
    const group = groups.get(key.provider_id) ?? { id: key.provider_id, name: key.provider_name, keys: [] }
    group.keys.push(key)
    groups.set(key.provider_id, group)
  }
  return [...groups.values()].sort((a, b) => a.name.localeCompare(b.name))
}

function checkState(total: number, selected: number) {
  return total > 0 && selected === total ? true : selected > 0 ? 'indeterminate' : false
}

function AssignForm({ proxies, onDone }: { proxies: Proxy[]; onDone: () => void }) {
  const keys = useAllKeys()
  const [selected, setSelected] = useState<Set<number>>(() => new Set())
  const [error, setError] = useState('')
  const assign = useAction(
    (body: { proxy_ids: number[]; key_ids: number[] }) => api.post<{ updated: number }>('/api/proxies/assign', body),
    { success: (r) => `Assigned proxies to ${plural(r.updated, 'key')}`, toastErrors: false },
  )

  const all = keys.data ?? []
  const groups = groupByProvider(all)
  const disabledProxies = proxies.filter((p) => !p.enabled)

  function setMany(ids: number[], checked: boolean) {
    setSelected((current) => {
      const next = new Set(current)
      for (const id of ids) {
        if (checked) next.add(id)
        else next.delete(id)
      }
      return next
    })
  }

  function submit(e: FormEvent) {
    e.preventDefault()
    if (selected.size === 0) {
      setError('Select at least one key.')
      return
    }
    setError('')
    assign.mutate(
      { proxy_ids: proxies.map((p) => p.id), key_ids: [...selected] },
      { onSuccess: onDone, onError: (err) => setError(describeError(err)) },
    )
  }

  return (
    <form onSubmit={submit} className="grid gap-4" noValidate>
      <FormError message={error} />
      <div className="flex flex-wrap items-center gap-1.5 text-sm">
        <span className="text-muted-foreground">Proxies:</span>
        {proxies.map((p) => (
          <Badge key={p.id} variant="outline">
            {p.name}
            {!p.enabled && ' (disabled)'}
          </Badge>
        ))}
      </div>
      {disabledProxies.length > 0 && (
        <p className="text-sm text-amber-600 dark:text-amber-400">
          Keys that get a disabled proxy are skipped until the proxy is enabled again.
        </p>
      )}

      {keys.isPending ? (
        <Loading />
      ) : keys.isError ? (
        <ErrorState error={keys.error} onRetry={keys.refetch} />
      ) : all.length === 0 ? (
        <p className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
          No API keys yet. Add keys on a provider page first.
        </p>
      ) : (
        <div className="rounded-md border">
          <label className="flex items-center gap-2 border-b bg-muted/40 px-3 py-2 text-sm font-medium">
            <Checkbox
              checked={checkState(all.length, selected.size)}
              onCheckedChange={(c) => setMany(all.map((k) => k.id), c === true)}
            />
            All keys ({all.length})
          </label>
          <div className="max-h-[45vh] overflow-y-auto">
            {groups.map((group) => {
              const ids = group.keys.map((k) => k.id)
              const count = ids.filter((id) => selected.has(id)).length
              return (
                <div key={group.id} className="border-b last:border-b-0">
                  <label className="flex items-center gap-2 px-3 py-2 text-sm font-medium">
                    <Checkbox checked={checkState(ids.length, count)} onCheckedChange={(c) => setMany(ids, c === true)} />
                    {group.name}
                    <span className="font-normal text-muted-foreground">
                      {count}/{ids.length}
                    </span>
                  </label>
                  {group.keys.map((k) => (
                    <label
                      key={k.id}
                      className="flex flex-wrap items-center gap-x-3 gap-y-0.5 py-1.5 pr-3 pl-9 text-sm hover:bg-muted/40"
                    >
                      <Checkbox checked={selected.has(k.id)} onCheckedChange={(c) => setMany([k.id], c === true)} />
                      <span>{k.label || '—'}</span>
                      <code className="font-mono text-xs text-muted-foreground">{k.secret_hint}</code>
                      <span className="ml-auto text-xs text-muted-foreground">{keyProxyText(k)}</span>
                    </label>
                  ))}
                </div>
              )
            })}
          </div>
        </div>
      )}

      <DialogFooter>
        <DialogClose asChild>
          <Button type="button" variant="outline">
            Cancel
          </Button>
        </DialogClose>
        <Button type="submit" disabled={assign.isPending || selected.size === 0}>
          {assign.isPending ? 'Assigning…' : `Assign to ${plural(selected.size, 'key')}`}
        </Button>
      </DialogFooter>
    </form>
  )
}
