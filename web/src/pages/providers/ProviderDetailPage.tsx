import { useState, type ReactNode } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { Pencil, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { ProviderTypeBadge, ToneBadge } from '@/components/Badges'
import { EmptyState, ErrorState, Loading, PageHeader } from '@/components/common'
import { ConfirmDialog, type ConfirmOptions } from '@/components/ConfirmDialog'
import { api, ApiError } from '@/lib/api'
import { plural } from '@/lib/format'
import { chatEndpoint, keyRecoveryText, keyStrategies, labelOf } from '@/lib/labels'
import { useAction, useProvider, useSettings } from '@/lib/queries'
import type { Provider } from '@/lib/types'
import { KeysCard } from './KeysCard'
import { ProviderDialog } from './ProviderDialog'

const back = { to: '/providers', label: 'Providers' }

export default function ProviderDetailPage() {
  const id = Number(useParams().id)
  const provider = useProvider(id)

  if (provider.isPending) {
    return (
      <>
        <PageHeader back={back} title="Provider" />
        <Loading rows={5} />
      </>
    )
  }
  if (provider.isError) {
    const notFound = provider.error instanceof ApiError && provider.error.status === 404
    return (
      <>
        <PageHeader back={back} title="Provider" />
        {notFound ? (
          <EmptyState
            title="Provider not found"
            hint="It may have been deleted."
            action={
              <Button asChild variant="outline">
                <Link to="/providers">All providers</Link>
              </Button>
            }
          />
        ) : (
          <ErrorState error={provider.error} onRetry={provider.refetch} />
        )}
      </>
    )
  }
  return <ProviderDetail provider={provider.data} />
}

function ProviderDetail({ provider: p }: { provider: Provider }) {
  const navigate = useNavigate()
  const settings = useSettings()
  const [editOpen, setEditOpen] = useState(false)
  const [confirm, setConfirm] = useState<ConfirmOptions | null>(null)
  const remove = useAction(() => api.del(`/api/providers/${p.id}`), { success: 'Provider deleted' })
  const headerCount = Object.keys(p.headers ?? {}).length
  const recovery =
    p.disabled_key_recovery_minutes !== null
      ? keyRecoveryText(p.disabled_key_recovery_minutes)
      : `Default${settings.data ? ` · ${keyRecoveryText(settings.data.disabled_key_recovery_minutes).toLowerCase()}` : ''}`

  function confirmDelete() {
    setConfirm({
      title: `Delete provider ${p.name}?`,
      description: `This also deletes its ${plural(p.keys.total, 'API key')} and removes it from ${plural(p.model_count, 'model')}.`,
      onConfirm: async () => {
        await remove.mutateAsync()
        navigate('/providers')
      },
    })
  }

  return (
    <>
      <PageHeader
        back={back}
        title={
          <>
            {p.name}
            <ProviderTypeBadge type={p.type} />
            {!p.enabled && <ToneBadge tone="grey">Disabled</ToneBadge>}
          </>
        }
        actions={
          <>
            <Button variant="outline" onClick={() => setEditOpen(true)}>
              <Pencil />
              Edit
            </Button>
            <Button variant="outline" onClick={confirmDelete}>
              <Trash2 />
              Delete
            </Button>
          </>
        }
      />

      <Card className="mb-6 py-0">
        <dl className="grid gap-x-8 gap-y-4 p-5 text-sm sm:grid-cols-2 lg:grid-cols-3">
          <Item label="Chat endpoint" className="sm:col-span-2 lg:col-span-3">
            <code className="font-mono text-xs break-all">{chatEndpoint(p.type, p.base_url)}</code>
          </Item>
          <Item label="Request timeout">{p.timeout_seconds} s</Item>
          <Item label="Default proxy">{p.proxy_id ? p.proxy_name : 'None (direct)'}</Item>
          <Item label="Key selection">{labelOf(keyStrategies, p.key_strategy)}</Item>
          <Item label="Extra headers">{headerCount ? plural(headerCount, 'header') : 'None'}</Item>
          <Item label="Used by">{plural(p.model_count, 'model')}</Item>
          <Item label="Disabled key recovery">{recovery}</Item>
        </dl>
      </Card>

      <KeysCard provider={p} />

      <ProviderDialog open={editOpen} onOpenChange={setEditOpen} provider={p} />
      <ConfirmDialog options={confirm} onClose={() => setConfirm(null)} />
    </>
  )
}

function Item({ label, className, children }: { label: string; className?: string; children: ReactNode }) {
  return (
    <div className={className}>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-0.5">{children}</dd>
    </div>
  )
}
