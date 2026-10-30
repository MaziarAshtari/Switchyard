import { useState, type DragEvent } from 'react'
import { Link } from 'react-router'
import { ChevronDown, ChevronUp, GripVertical, Plus, X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { IconButton } from '@/components/Tip'
import type { Errors } from '@/lib/form'
import type { ModelType, Provider, Strategy } from '@/lib/types'
import { cn } from '@/lib/utils'

export interface TargetRow {
  /** Local id for React keys while rows move around. */
  key: number
  provider_id: number | null
  upstream_model: string
  weight: string
  enabled: boolean
}

interface TargetsEditorProps {
  targets: TargetRow[]
  onChange: (targets: TargetRow[]) => void
  onAdd: () => void
  providers: Provider[]
  modelType: ModelType
  strategy: Strategy
  errors: Errors
}

/** Ordered target list; the order is the priority. Reorder by dragging the handle or with the arrows. */
export function TargetsEditor({ targets, onChange, onAdd, providers, modelType, strategy, errors }: TargetsEditorProps) {
  const [dragIndex, setDragIndex] = useState<number | null>(null)
  const [overIndex, setOverIndex] = useState<number | null>(null)
  const weighted = strategy === 'weighted'
  const compatible = (p: Provider) => modelType === 'chat' || p.type === 'openai'

  function move(from: number, to: number) {
    if (to < 0 || to >= targets.length || from === to) return
    const next = [...targets]
    const [row] = next.splice(from, 1)
    next.splice(to, 0, row)
    onChange(next)
  }

  function update(index: number, patch: Partial<TargetRow>) {
    onChange(targets.map((t, i) => (i === index ? { ...t, ...patch } : t)))
  }

  function startDrag(e: DragEvent<HTMLDivElement>, index: number) {
    setDragIndex(index)
    e.dataTransfer.effectAllowed = 'move'
    e.dataTransfer.setData('text/plain', String(index)) // Firefox only starts a drag with data set
    const row = e.currentTarget.closest('[data-target-row]')
    if (row instanceof HTMLElement) e.dataTransfer.setDragImage(row, 16, 16)
  }

  function endDrag() {
    setDragIndex(null)
    setOverIndex(null)
  }

  return (
    <div className="grid gap-2">
      {targets.length === 0 ? (
        <div className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
          No targets yet. Add at least one provider model this public model should route to.
          {providers.length === 0 && (
            <>
              {' '}
              First{' '}
              <Link to="/providers" className="underline hover:text-foreground">
                add a provider
              </Link>
              .
            </>
          )}
        </div>
      ) : (
        <>
          <div className="hidden gap-2 px-3 text-xs font-medium text-muted-foreground md:flex">
            <span className="w-14" />
            <span className="w-52">Provider</span>
            <span className="flex-1">Upstream model</span>
            {weighted && <span className="w-20">Weight</span>}
            <span className="w-[7.5rem]">Enabled</span>
          </div>
          {targets.map((t, i) => {
            const provider = providers.find((p) => p.id === t.provider_id)
            const incompatible = provider && !compatible(provider)
            const options = providers.filter((p) => compatible(p) || p.id === t.provider_id)
            const err = (field: string) => errors[`targets.${i}.${field}`]
            const rowErrors = [
              err('provider_id'),
              incompatible && !err('provider_id') ? 'Non-chat models can only use OpenAI-compatible providers' : '',
              err('upstream_model'),
              err('weight'),
              err('enabled'),
            ].filter(Boolean)

            return (
              <div
                key={t.key}
                data-target-row
                onDragOver={(e) => {
                  if (dragIndex === null) return
                  e.preventDefault()
                  e.dataTransfer.dropEffect = 'move'
                  if (overIndex !== i) setOverIndex(i)
                }}
                onDrop={(e) => {
                  e.preventDefault()
                  if (dragIndex !== null) move(dragIndex, i)
                  endDrag()
                }}
                className={cn(
                  'rounded-md border bg-card p-2 transition-colors',
                  dragIndex === i && 'opacity-50',
                  dragIndex !== null && overIndex === i && dragIndex !== i && 'border-primary',
                  (rowErrors.length > 0 || !t.enabled) && 'bg-muted/40',
                )}
              >
                <div className="flex flex-wrap items-center gap-2">
                  <div className="flex w-14 items-center">
                    {/* Mouse-only handle; the arrow buttons are the keyboard alternative. */}
                    <div
                      draggable
                      onDragStart={(e) => startDrag(e, i)}
                      onDragEnd={endDrag}
                      className="cursor-grab rounded p-1 text-muted-foreground hover:text-foreground active:cursor-grabbing"
                      title="Drag to reorder"
                      aria-hidden="true"
                    >
                      <GripVertical className="size-4" />
                    </div>
                    <span className="w-6 text-center text-sm font-medium text-muted-foreground tabular-nums">{i + 1}</span>
                  </div>
                  <Select
                    value={t.provider_id === null ? '' : String(t.provider_id)}
                    onValueChange={(v) => update(i, { provider_id: Number(v) })}
                  >
                    <SelectTrigger
                      className="w-full md:w-52"
                      aria-label={`Target ${i + 1} provider`}
                      aria-invalid={!!err('provider_id') || !!incompatible}
                    >
                      <SelectValue placeholder={providers.length ? 'Choose provider' : 'No providers yet'} />
                    </SelectTrigger>
                    <SelectContent>
                      {options.map((p) => (
                        <SelectItem key={p.id} value={String(p.id)}>
                          {p.name}
                          {!p.enabled && ' (disabled)'}
                          {!compatible(p) && ' (not OpenAI-compatible)'}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <Input
                    value={t.upstream_model}
                    onChange={(e) => update(i, { upstream_model: e.target.value })}
                    placeholder="Upstream model, e.g. gpt-4o"
                    aria-label={`Target ${i + 1} upstream model`}
                    aria-invalid={!!err('upstream_model')}
                    className="min-w-40 flex-1 font-mono text-sm"
                  />
                  {weighted && (
                    <Input
                      value={t.weight}
                      onChange={(e) => update(i, { weight: e.target.value })}
                      inputMode="numeric"
                      aria-label={`Target ${i + 1} weight`}
                      aria-invalid={!!err('weight')}
                      className="w-20"
                      title="Weight"
                    />
                  )}
                  <div className="flex items-center gap-0.5">
                    <Switch
                      checked={t.enabled}
                      onCheckedChange={(c) => update(i, { enabled: c })}
                      aria-label={`Target ${i + 1} enabled`}
                      className="mr-2"
                    />
                    <IconButton label="Move up" icon={ChevronUp} disabled={i === 0} onClick={() => move(i, i - 1)} />
                    <IconButton
                      label="Move down"
                      icon={ChevronDown}
                      disabled={i === targets.length - 1}
                      onClick={() => move(i, i + 1)}
                    />
                    <IconButton
                      label="Remove target"
                      icon={X}
                      onClick={() => onChange(targets.filter((_, j) => j !== i))}
                    />
                  </div>
                </div>
                {rowErrors.length > 0 && (
                  <p className="mt-1.5 pl-14 text-xs font-medium text-destructive">{rowErrors.join(' · ')}</p>
                )}
              </div>
            )
          })}
        </>
      )}
      {errors.targets && <p className="text-xs font-medium text-destructive">{errors.targets}</p>}
      <Button type="button" variant="outline" size="sm" className="w-fit" onClick={onAdd}>
        <Plus />
        Add target
      </Button>
    </div>
  )
}
