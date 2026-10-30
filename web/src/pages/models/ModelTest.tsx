import { Loader2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { AttemptsTable } from '@/components/AttemptsTable'
import { HttpStatusBadge, ToneBadge } from '@/components/Badges'
import { errorMessage } from '@/lib/api'
import { latency } from '@/lib/format'
import type { ModelTestResult } from '@/lib/types'

interface TestState {
  pending: boolean
  result?: ModelTestResult
  error: unknown
}

/** Progress, result or error of a model test. */
export function TestOutcome({ pending, result, error }: TestState) {
  if (pending) {
    return (
      <p className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" />
        Sending a sample request…
      </p>
    )
  }
  if (error) return <p className="text-sm text-destructive">{errorMessage(error)}</p>
  if (!result) return null

  return (
    <div className="grid gap-3">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        {result.ok ? <ToneBadge tone="green">OK</ToneBadge> : <ToneBadge tone="red">Failed</ToneBadge>}
        <HttpStatusBadge code={result.status_code} />
        <span className="text-muted-foreground tabular-nums">{latency(result.latency_ms)}</span>
      </div>
      {result.output && (
        <pre className="max-h-48 overflow-auto rounded-md bg-muted p-3 font-mono text-xs break-words whitespace-pre-wrap">
          {result.output}
        </pre>
      )}
      {result.error && <p className="text-sm break-words text-destructive">{result.error}</p>}
      <div className="grid gap-1.5">
        <h3 className="text-sm font-medium">Attempts</h3>
        <AttemptsTable attempts={result.attempts} />
      </div>
    </div>
  )
}

/** Shows a model test started from the models list. */
export function ModelTestDialog({
  modelName,
  open,
  onOpenChange,
  ...state
}: TestState & { modelName: string; open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Test {modelName}</DialogTitle>
          <DialogDescription>A small real request routed exactly like a client request.</DialogDescription>
        </DialogHeader>
        <TestOutcome {...state} />
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline">Close</Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
