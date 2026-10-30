import { useState, type FormEvent } from 'react'
import { KeySquare, Plus, Trash2, TriangleAlert } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { CopyButton, CopyField, EmptyState, ErrorState, Field, FormError, Loading, RelativeTime } from '@/components/common'
import { ConfirmDialog, type ConfirmOptions } from '@/components/ConfirmDialog'
import { IconButton } from '@/components/Tip'
import { api } from '@/lib/api'
import { toErrors, type Errors } from '@/lib/form'
import { useAction, useGatewayKeys } from '@/lib/queries'
import type { CreatedGatewayKey, GatewayKey } from '@/lib/types'

export function GatewayKeysCard() {
  const keys = useGatewayKeys()
  const [createOpen, setCreateOpen] = useState(false)
  const [created, setCreated] = useState<CreatedGatewayKey | null>(null)
  const [confirm, setConfirm] = useState<ConfirmOptions | null>(null)
  const toggle = useAction(
    (k: GatewayKey) => api.put<GatewayKey>(`/api/gateway-keys/${k.id}`, { name: k.name, enabled: !k.enabled }),
    { success: (k) => `${k.name} ${k.enabled ? 'enabled' : 'disabled'}` },
  )
  const remove = useAction((k: GatewayKey) => api.del(`/api/gateway-keys/${k.id}`), { success: 'Gateway key deleted' })

  function confirmDelete(k: GatewayKey) {
    setConfirm({
      title: `Delete gateway key ${k.name}?`,
      description: 'Clients using this key will immediately get 401 errors. This cannot be undone.',
      onConfirm: () => remove.mutateAsync(k),
    })
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Gateway keys</CardTitle>
        <CardDescription className="grid gap-2">
          <span>
            Clients call this base URL with one of these keys (<code className="font-mono">Authorization: Bearer</code>,{' '}
            <code className="font-mono">x-api-key</code> or <code className="font-mono">x-goog-api-key</code>).
          </span>
          <CopyField value={`${window.location.origin}/v1`} label="Copy base URL" />
        </CardDescription>
        <CardAction>
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus />
            Create key
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent>
        {keys.isPending ? (
          <Loading rows={2} />
        ) : keys.isError ? (
          <ErrorState error={keys.error} onRetry={keys.refetch} />
        ) : keys.data.length === 0 ? (
          <EmptyState
            icon={KeySquare}
            title="No gateway keys yet"
            hint="Create a key for your clients. Without one, nobody can use the gateway."
          />
        ) : (
          <div className="rounded-md border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="pl-3">Name</TableHead>
                  <TableHead>Key</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead>Enabled</TableHead>
                  <TableHead className="pr-3 text-right">
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {keys.data.map((k) => (
                  <TableRow key={k.id}>
                    <TableCell className="pl-3 font-medium">{k.name}</TableCell>
                    <TableCell>
                      <code className="font-mono text-xs">{k.secret_hint}</code>
                    </TableCell>
                    <TableCell>
                      <RelativeTime value={k.created_at} />
                    </TableCell>
                    <TableCell>
                      <Switch
                        checked={k.enabled}
                        disabled={toggle.isPending && toggle.variables?.id === k.id}
                        onCheckedChange={() => toggle.mutate(k)}
                        aria-label={`${k.enabled ? 'Disable' : 'Enable'} ${k.name}`}
                      />
                    </TableCell>
                    <TableCell className="pr-3 text-right">
                      <IconButton label="Delete" icon={Trash2} onClick={() => confirmDelete(k)} />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>

      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create gateway key</DialogTitle>
            <DialogDescription>A key for one client or app, so you can disable it on its own later.</DialogDescription>
          </DialogHeader>
          <CreateKeyForm
            onCreated={(key) => {
              setCreateOpen(false)
              setCreated(key)
            }}
          />
        </DialogContent>
      </Dialog>

      <Dialog open={created !== null} onOpenChange={(open) => !open && setCreated(null)}>
        {/* Closing by clicking outside would lose the secret for good. */}
        <DialogContent onInteractOutside={(e) => e.preventDefault()}>
          <DialogHeader>
            <DialogTitle>Gateway key created</DialogTitle>
            <DialogDescription>{created?.name}</DialogDescription>
          </DialogHeader>
          <div className="flex items-center gap-2">
            <Input readOnly value={created?.secret ?? ''} className="font-mono text-xs" onFocus={(e) => e.target.select()} />
            <CopyButton value={created?.secret ?? ''} label="Copy key" />
          </div>
          <p className="flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 p-3 text-sm text-amber-700 dark:text-amber-400">
            <TriangleAlert className="mt-0.5 size-4 shrink-0" />
            Copy this key now. It will not be shown again.
          </p>
          <DialogFooter>
            <DialogClose asChild>
              <Button>Done</Button>
            </DialogClose>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ConfirmDialog options={confirm} onClose={() => setConfirm(null)} />
    </Card>
  )
}

function CreateKeyForm({ onCreated }: { onCreated: (key: CreatedGatewayKey) => void }) {
  const [name, setName] = useState('')
  const [errors, setErrors] = useState<Errors>({})
  const create = useAction((body: { name: string }) => api.post<CreatedGatewayKey>('/api/gateway-keys', body), {
    toastErrors: false,
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    if (!name.trim()) {
      setErrors({ name: 'Name is required' })
      return
    }
    setErrors({})
    create.mutate({ name: name.trim() }, { onSuccess: onCreated, onError: (err) => setErrors(toErrors(err)) })
  }

  return (
    <form onSubmit={submit} className="grid gap-4" noValidate>
      <FormError message={errors._} />
      <Field label="Name" htmlFor="gateway-key-name" error={errors.name}>
        <Input
          id="gateway-key-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. Claude Code on laptop"
          aria-invalid={!!errors.name}
          autoFocus
        />
      </Field>
      <DialogFooter>
        <DialogClose asChild>
          <Button type="button" variant="outline">
            Cancel
          </Button>
        </DialogClose>
        <Button type="submit" disabled={create.isPending}>
          {create.isPending ? 'Creating…' : 'Create key'}
        </Button>
      </DialogFooter>
    </form>
  )
}
