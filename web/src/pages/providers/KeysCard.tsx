import { useState } from 'react'
import { KeyRound, Pencil, Plus, RotateCcw, Trash2, TriangleAlert, Upload, Zap } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { KeyStatusBadges } from '@/components/Badges'
import { EmptyState, ErrorState, Loading, RelativeTime } from '@/components/common'
import { ConfirmDialog, type ConfirmOptions } from '@/components/ConfirmDialog'
import { IconButton, Tip } from '@/components/Tip'
import { api } from '@/lib/api'
import { compact, plural } from '@/lib/format'
import { keyProxyText } from '@/lib/labels'
import { notifyTest, useAction, useProviderKeys } from '@/lib/queries'
import type { ApiKey, KeyBatchAction, KeyInput, Provider, TestResult } from '@/lib/types'
import { AddKeyDialog, BulkAddDialog, EditKeyDialog } from './KeyDialogs'

const batchDone: Record<KeyBatchAction, string> = {
  enable: 'Enabled',
  disable: 'Disabled',
  reset: 'Reset',
  delete: 'Deleted',
}

export function KeysCard({ provider }: { provider: Provider }) {
  const keys = useProviderKeys(provider.id)
  const [selected, setSelected] = useState<Set<number>>(() => new Set())
  const [addOpen, setAddOpen] = useState(false)
  const [bulkOpen, setBulkOpen] = useState(false)
  const [editOpen, setEditOpen] = useState(false)
  const [editing, setEditing] = useState<ApiKey | undefined>()
  const [confirm, setConfirm] = useState<ConfirmOptions | null>(null)

  const batch = useAction(
    (body: { ids: number[]; action: KeyBatchAction }) => api.post<{ updated: number }>('/api/keys/batch', body),
    { success: (r, body) => `${batchDone[body.action]} ${plural(r.updated, 'key')}` },
  )
  const remove = useAction((k: ApiKey) => api.del(`/api/keys/${k.id}`), { success: 'Key deleted' })
  const resetAll = useAction(() => api.post<{ updated: number }>(`/api/providers/${provider.id}/keys/reset`), {
    success: (r) => (r.updated ? `Re-activated ${plural(r.updated, 'key')}` : 'No keys needed re-activation'),
  })

  const list = keys.data ?? []
  const inactive = list.filter((k) => k.status !== 'active').length
  // Ignore selected ids of keys that no longer exist.
  const selectedIds = list.filter((k) => selected.has(k.id)).map((k) => k.id)
  const allChecked =
    list.length > 0 && selectedIds.length === list.length ? true : selectedIds.length > 0 ? 'indeterminate' : false

  function select(id: number, checked: boolean) {
    setSelected((current) => {
      const next = new Set(current)
      if (checked) next.add(id)
      else next.delete(id)
      return next
    })
  }

  function runBatch(action: KeyBatchAction) {
    if (action !== 'delete') {
      batch.mutate({ ids: selectedIds, action })
      return
    }
    setConfirm({
      title: `Delete ${plural(selectedIds.length, 'key')}?`,
      description: 'The selected keys are removed from this provider and can no longer be used. This cannot be undone.',
      onConfirm: async () => {
        await batch.mutateAsync({ ids: selectedIds, action })
        setSelected(new Set())
      },
    })
  }

  function confirmDelete(k: ApiKey) {
    setConfirm({
      title: `Delete key ${k.label || k.secret_hint}?`,
      description: 'Requests will no longer use this key. This cannot be undone.',
      onConfirm: () => remove.mutateAsync(k),
    })
  }

  const addButtons = (
    <>
      <Button size="sm" variant="outline" onClick={() => setBulkOpen(true)}>
        <Upload />
        Bulk add
      </Button>
      <Button size="sm" onClick={() => setAddOpen(true)}>
        <Plus />
        Add key
      </Button>
    </>
  )

  return (
    <Card className="gap-0 overflow-hidden py-0">
      <div className="flex flex-wrap items-start justify-between gap-3 border-b p-4">
        <div>
          <h2 className="font-semibold">API keys</h2>
          <p className="text-sm text-muted-foreground">
            Rate-limited keys cool down and rejected keys are disabled automatically; requests then use the next key.
          </p>
        </div>
        {list.length > 0 && (
          <div className="flex flex-wrap gap-2">
            <Button
              size="sm"
              variant="outline"
              disabled={inactive === 0 || resetAll.isPending}
              onClick={() => resetAll.mutate()}
              title="Re-activate all disabled keys and clear cooldowns now"
            >
              <RotateCcw />
              Reset disabled keys
            </Button>
            {addButtons}
          </div>
        )}
      </div>

      {selectedIds.length > 0 && (
        <div className="flex flex-wrap items-center gap-2 border-b bg-muted/40 px-4 py-2">
          <span className="mr-1 text-sm font-medium">{selectedIds.length} selected</span>
          <Button size="sm" variant="outline" disabled={batch.isPending} onClick={() => runBatch('enable')}>
            Enable
          </Button>
          <Button size="sm" variant="outline" disabled={batch.isPending} onClick={() => runBatch('disable')}>
            Disable
          </Button>
          <Button size="sm" variant="outline" disabled={batch.isPending} onClick={() => runBatch('reset')}>
            Reset
          </Button>
          <Button size="sm" variant="outline" disabled={batch.isPending} onClick={() => runBatch('delete')}>
            Delete
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>
            Clear selection
          </Button>
        </div>
      )}

      {keys.isPending ? (
        <div className="p-4">
          <Loading />
        </div>
      ) : keys.isError ? (
        <div className="p-4">
          <ErrorState error={keys.error} onRetry={keys.refetch} />
        </div>
      ) : list.length === 0 ? (
        <div className="p-4">
          <EmptyState
            icon={KeyRound}
            title="No API keys yet"
            hint="Add this provider's API keys. Requests are spread across them, and failing keys are skipped automatically."
            action={<div className="flex gap-2">{addButtons}</div>}
          />
        </div>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-10 pl-4">
                <Checkbox
                  checked={allChecked}
                  onCheckedChange={(c) => setSelected(c === true ? new Set(list.map((k) => k.id)) : new Set())}
                  aria-label="Select all keys"
                />
              </TableHead>
              <TableHead>Label</TableHead>
              <TableHead>Key</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Proxy</TableHead>
              <TableHead>Last used</TableHead>
              <TableHead>Success / Fail</TableHead>
              <TableHead>Last error</TableHead>
              <TableHead>Enabled</TableHead>
              <TableHead className="pr-4 text-right">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {list.map((k) => (
              <KeyRow
                key={k.id}
                apiKey={k}
                selected={selected.has(k.id)}
                onSelect={(checked) => select(k.id, checked)}
                onEdit={() => {
                  setEditing(k)
                  setEditOpen(true)
                }}
                onDelete={() => confirmDelete(k)}
              />
            ))}
          </TableBody>
        </Table>
      )}

      <AddKeyDialog provider={provider} open={addOpen} onOpenChange={setAddOpen} />
      <BulkAddDialog provider={provider} open={bulkOpen} onOpenChange={setBulkOpen} />
      {editing && <EditKeyDialog provider={provider} apiKey={editing} open={editOpen} onOpenChange={setEditOpen} />}
      <ConfirmDialog options={confirm} onClose={() => setConfirm(null)} />
    </Card>
  )
}

interface KeyRowProps {
  apiKey: ApiKey
  selected: boolean
  onSelect: (checked: boolean) => void
  onEdit: () => void
  onDelete: () => void
}

// Each row owns its mutations so several keys can be tested or toggled at once.
function KeyRow({ apiKey: k, selected, onSelect, onEdit, onDelete }: KeyRowProps) {
  const name = k.label || k.secret_hint
  const toggle = useAction(
    () =>
      api.put<ApiKey>(`/api/keys/${k.id}`, {
        label: k.label,
        enabled: !k.enabled,
        proxy_mode: k.proxy_mode,
        proxy_id: k.proxy_id,
      } satisfies KeyInput),
    { success: (r) => `Key ${r.enabled ? 'enabled' : 'disabled'}` },
  )
  const reset = useAction(() => api.post<ApiKey>(`/api/keys/${k.id}/reset`), { success: 'Key re-activated' })
  const test = useAction(() => api.post<TestResult>(`/api/keys/${k.id}/test`), {
    success: (r) => notifyTest('Key', r),
  })
  const proxyOff = k.effective_proxy && !k.effective_proxy.enabled

  return (
    <TableRow data-state={selected ? 'selected' : undefined}>
      <TableCell className="pl-4">
        <Checkbox checked={selected} onCheckedChange={(c) => onSelect(c === true)} aria-label={`Select ${name}`} />
      </TableCell>
      <TableCell className="font-medium">{k.label || <span className="text-muted-foreground">—</span>}</TableCell>
      <TableCell>
        <code className="font-mono text-xs">{k.secret_hint}</code>
      </TableCell>
      <TableCell>
        <KeyStatusBadges apiKey={k} />
      </TableCell>
      <TableCell>
        <span className="inline-flex items-center gap-1.5">
          {keyProxyText(k)}
          {proxyOff && (
            <Tip content="Proxy disabled: this key is skipped">
              <span tabIndex={0} aria-label="Proxy disabled: this key is skipped">
                <TriangleAlert className="size-4 text-amber-500" />
              </span>
            </Tip>
          )}
        </span>
      </TableCell>
      <TableCell>
        <RelativeTime value={k.last_used_at} />
      </TableCell>
      <TableCell className="tabular-nums">
        <span className="text-emerald-600 dark:text-emerald-400">{compact(k.success_count)}</span>
        <span className="text-muted-foreground"> / </span>
        <span className={k.failure_count ? 'text-red-600 dark:text-red-400' : undefined}>{compact(k.failure_count)}</span>
      </TableCell>
      <TableCell className="max-w-[14rem]">
        {k.last_error ? (
          <Tip content={k.last_error}>
            <span className="block truncate text-xs text-muted-foreground">{k.last_error}</span>
          </Tip>
        ) : (
          <span className="text-muted-foreground">—</span>
        )}
      </TableCell>
      <TableCell>
        <Switch
          checked={k.enabled}
          disabled={toggle.isPending}
          onCheckedChange={() => toggle.mutate()}
          aria-label={`${k.enabled ? 'Disable' : 'Enable'} ${name}`}
        />
      </TableCell>
      <TableCell className="pr-4 text-right">
        <IconButton label="Test key" icon={Zap} pending={test.isPending} onClick={() => test.mutate()} />
        {k.status !== 'active' && (
          <IconButton
            label="Reset: clear cooldown / re-activate"
            icon={RotateCcw}
            pending={reset.isPending}
            onClick={() => reset.mutate()}
          />
        )}
        <IconButton label="Edit" icon={Pencil} onClick={onEdit} />
        <IconButton label="Delete" icon={Trash2} onClick={onDelete} />
      </TableCell>
    </TableRow>
  )
}
