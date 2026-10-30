import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { HttpStatusBadge } from '@/components/Badges'
import { latency } from '@/lib/format'
import type { Attempt } from '@/lib/types'

/** Every upstream attempt of one request, in the order they were tried. */
export function AttemptsTable({ attempts }: { attempts: Attempt[] | null }) {
  if (!attempts?.length) return <p className="text-sm text-muted-foreground">No upstream attempts were made.</p>
  return (
    <div className="overflow-x-auto rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-8 pl-3">#</TableHead>
            <TableHead>Provider</TableHead>
            <TableHead>Key</TableHead>
            <TableHead>Upstream model</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Latency</TableHead>
            <TableHead className="pr-3">Error</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {attempts.map((a, i) => (
            <TableRow key={i}>
              <TableCell className="pl-3 text-muted-foreground tabular-nums">{i + 1}</TableCell>
              <TableCell>{a.provider_name || '—'}</TableCell>
              <TableCell>
                <code className="font-mono text-xs">{a.key_hint || '—'}</code>
              </TableCell>
              <TableCell className="font-mono text-xs">{a.upstream_model || '—'}</TableCell>
              <TableCell>
                <HttpStatusBadge code={a.status_code} />
              </TableCell>
              <TableCell className="tabular-nums">{latency(a.latency_ms)}</TableCell>
              <TableCell className="min-w-[12rem] pr-3 text-xs break-words whitespace-normal text-muted-foreground">
                {a.error || '—'}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}
