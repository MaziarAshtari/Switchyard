import { useState } from 'react'
import { Link } from 'react-router'
import { Pencil, Plus, Server, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ProviderTypeBadge } from '@/components/Badges'
import { EmptyState, ErrorState, Loading, PageHeader } from '@/components/common'
import { ConfirmDialog, type ConfirmOptions } from '@/components/ConfirmDialog'
import { IconButton } from '@/components/Tip'
import { api } from '@/lib/api'
import { plural } from '@/lib/format'
import { keyStrategies, labelOf } from '@/lib/labels'
import { useAction, useProviders } from '@/lib/queries'
import type { Provider, ProviderInput } from '@/lib/types'
import { ProviderDialog } from './ProviderDialog'

function toInput(p: Provider, patch: Partial<ProviderInput> = {}): ProviderInput {
  const { name, type, base_url, headers, timeout_seconds, enabled, proxy_id, key_strategy } = p
  return { name, type, base_url, headers, timeout_seconds, enabled, proxy_id, key_strategy, ...patch }
}

export default function ProvidersPage() {
  const providers = useProviders()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<Provider | undefined>()
  const [confirm, setConfirm] = useState<ConfirmOptions | null>(null)

  const toggle = useAction(
    (p: Provider) => api.put<Provider>(`/api/providers/${p.id}`, toInput(p, { enabled: !p.enabled })),
    { success: (p) => `${p.name} ${p.enabled ? 'enabled' : 'disabled'}` },
  )
  const remove = useAction((p: Provider) => api.del(`/api/providers/${p.id}`), { success: 'Provider deleted' })

  function openDialog(p?: Provider) {
    setEditing(p)
    setDialogOpen(true)
  }

  function confirmDelete(p: Provider) {
    setConfirm({
      title: `Delete provider ${p.name}?`,
      description: `This also deletes its ${plural(p.keys.total, 'API key')} and removes it from ${plural(p.model_count, 'model')}.`,
      onConfirm: () => remove.mutateAsync(p),
    })
  }

  const addButton = (
    <Button onClick={() => openDialog()}>
      <Plus />
      Add provider
    </Button>
  )

  return (
    <>
      <PageHeader
        title="Providers"
        description="Upstream AI services, their API keys and connection settings."
        actions={providers.data?.length ? addButton : undefined}
      />

      {providers.isPending ? (
        <Loading />
      ) : providers.isError ? (
        <ErrorState error={providers.error} onRetry={providers.refetch} />
      ) : providers.data.length === 0 ? (
        <EmptyState
          icon={Server}
          title="No providers yet"
          hint="Add an upstream service such as OpenAI, Anthropic or Gemini, or any API compatible with one of them."
          action={addButton}
        />
      ) : (
        <Card className="overflow-hidden py-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="pl-4">Name</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Base URL</TableHead>
                <TableHead>Keys</TableHead>
                <TableHead>Default proxy</TableHead>
                <TableHead>Key selection</TableHead>
                <TableHead>Enabled</TableHead>
                <TableHead className="pr-4 text-right">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {providers.data.map((p) => (
                <TableRow key={p.id}>
                  <TableCell className="pl-4 font-medium">
                    <Link to={`/providers/${p.id}`} className="hover:underline">
                      {p.name}
                    </Link>
                  </TableCell>
                  <TableCell>
                    <ProviderTypeBadge type={p.type} />
                  </TableCell>
                  <TableCell className="max-w-[16rem] truncate text-muted-foreground" title={p.base_url}>
                    {p.base_url}
                  </TableCell>
                  <TableCell>
                    <KeySummary provider={p} />
                  </TableCell>
                  <TableCell>{p.proxy_id ? p.proxy_name : <span className="text-muted-foreground">Direct</span>}</TableCell>
                  <TableCell>{labelOf(keyStrategies, p.key_strategy)}</TableCell>
                  <TableCell>
                    <Switch
                      checked={p.enabled}
                      disabled={toggle.isPending && toggle.variables?.id === p.id}
                      onCheckedChange={() => toggle.mutate(p)}
                      aria-label={`${p.enabled ? 'Disable' : 'Enable'} ${p.name}`}
                    />
                  </TableCell>
                  <TableCell className="pr-4 text-right">
                    <IconButton label="Edit" icon={Pencil} onClick={() => openDialog(p)} />
                    <IconButton label="Delete" icon={Trash2} onClick={() => confirmDelete(p)} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}

      <ProviderDialog open={dialogOpen} onOpenChange={setDialogOpen} provider={editing} />
      <ConfirmDialog options={confirm} onClose={() => setConfirm(null)} />
    </>
  )
}

function KeySummary({ provider }: { provider: Provider }) {
  const { keys } = provider
  if (keys.total === 0) {
    return (
      <Link to={`/providers/${provider.id}`} className="text-amber-600 hover:underline dark:text-amber-400">
        No keys
      </Link>
    )
  }
  return (
    <span className="flex flex-wrap items-baseline gap-x-2">
      <span className="tabular-nums">
        {keys.active} active / {keys.total}
      </span>
      {keys.cooling_down > 0 && (
        <span className="text-xs text-amber-600 dark:text-amber-400">{keys.cooling_down} cooling down</span>
      )}
      {keys.disabled > 0 && <span className="text-xs text-red-600 dark:text-red-400">{keys.disabled} disabled</span>}
      {keys.off > 0 && <span className="text-xs text-muted-foreground">{keys.off} off</span>}
    </span>
  )
}
