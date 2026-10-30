import { useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { Boxes, Pencil, Plus, Trash2, TriangleAlert, Zap } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ModelTypeBadge } from '@/components/Badges'
import { EmptyState, ErrorState, Loading, PageHeader } from '@/components/common'
import { ConfirmDialog, type ConfirmOptions } from '@/components/ConfirmDialog'
import { IconButton } from '@/components/Tip'
import { api } from '@/lib/api'
import { plural } from '@/lib/format'
import { labelOf, strategies } from '@/lib/labels'
import { useAction, useModels, useModelTest } from '@/lib/queries'
import type { Model } from '@/lib/types'
import { ModelTestDialog } from './ModelTest'

export default function ModelsPage() {
  const models = useModels()
  const navigate = useNavigate()
  const [confirm, setConfirm] = useState<ConfirmOptions | null>(null)
  const [testOpen, setTestOpen] = useState(false)
  const [tested, setTested] = useState<Model | null>(null)
  const { test, run } = useModelTest(setConfirm)
  const remove = useAction((m: Model) => api.del(`/api/models/${m.id}`), { success: 'Model deleted' })

  const startTest = (m: Model) =>
    run(m, () => {
      setTested(m)
      setTestOpen(true)
    })

  function confirmDelete(m: Model) {
    setConfirm({
      title: `Delete model ${m.name}?`,
      description: `Clients requesting “${m.name}” will get an error. Providers and keys are not affected.`,
      onConfirm: () => remove.mutateAsync(m),
    })
  }

  const newButton = (
    <Button asChild>
      <Link to="/models/new">
        <Plus />
        New model
      </Link>
    </Button>
  )

  return (
    <>
      <PageHeader
        title="Models"
        description="Public model names your clients use, and where each one is routed."
        actions={models.data?.length ? newButton : undefined}
      />

      {models.isPending ? (
        <Loading />
      ) : models.isError ? (
        <ErrorState error={models.error} onRetry={models.refetch} />
      ) : models.data.length === 0 ? (
        <EmptyState
          icon={Boxes}
          title="No models yet"
          hint="A model is the name clients send (e.g. “gpt-best”). It routes to one or more provider models, with failover and retries."
          action={newButton}
        />
      ) : (
        <Card className="overflow-hidden py-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="pl-4">Name</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Strategy</TableHead>
                <TableHead>Targets</TableHead>
                <TableHead>Retry</TableHead>
                <TableHead className="pr-4 text-right">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {models.data.map((m) => (
                <TableRow key={m.id}>
                  <TableCell className="pl-4">
                    <Link to={`/models/${m.id}`} className="font-mono text-sm font-medium hover:underline">
                      {m.name}
                    </Link>
                  </TableCell>
                  <TableCell>
                    <ModelTypeBadge type={m.type} />
                  </TableCell>
                  <TableCell>{labelOf(strategies, m.strategy)}</TableCell>
                  <TableCell className="max-w-[28rem]">
                    <TargetSummary model={m} />
                  </TableCell>
                  <TableCell>{plural(m.max_total_attempts, 'attempt')}</TableCell>
                  <TableCell className="pr-4 text-right">
                    {m.type !== 'video' && (
                      <IconButton
                        label="Test"
                        icon={Zap}
                        pending={test.isPending && test.variables?.id === m.id}
                        onClick={() => startTest(m)}
                      />
                    )}
                    <IconButton label="Edit" icon={Pencil} onClick={() => navigate(`/models/${m.id}`)} />
                    <IconButton label="Delete" icon={Trash2} onClick={() => confirmDelete(m)} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}

      <ModelTestDialog
        modelName={tested?.name ?? ''}
        open={testOpen}
        onOpenChange={setTestOpen}
        pending={test.isPending}
        result={test.data}
        error={test.error}
      />
      <ConfirmDialog options={confirm} onClose={() => setConfirm(null)} />
    </>
  )
}

function TargetSummary({ model }: { model: Model }) {
  if (model.targets.length === 0) {
    return (
      <span className="inline-flex items-center gap-1.5 text-amber-600 dark:text-amber-400">
        <TriangleAlert className="size-4" />
        No targets
      </span>
    )
  }
  const shown = model.targets.slice(0, 2).map((t) => `${t.provider_name} · ${t.upstream_model}`)
  const more = model.targets.length - shown.length
  return (
    <span className="block truncate text-muted-foreground" title={model.targets.map((t) => `${t.provider_name} · ${t.upstream_model}`).join('\n')}>
      {shown.join(' → ')}
      {more > 0 && ` +${more} more`}
    </span>
  )
}
