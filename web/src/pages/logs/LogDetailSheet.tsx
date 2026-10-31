import { useState, type ReactNode } from 'react'
import { Link } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { AttemptsTable } from '@/components/AttemptsTable'
import { HttpStatusBadge, ModelTypeBadge } from '@/components/Badges'
import { CopyButton, ErrorState, Loading } from '@/components/common'
import { api } from '@/lib/api'
import { fullTime, latency, plural } from '@/lib/format'
import type { LogDetail } from '@/lib/types'

/** Side panel with every detail of one request. Pass null to close it. */
export function LogDetailSheet({ id, onClose }: { id: number | null; onClose: () => void }) {
  // Keep the last id while the sheet animates closed.
  const [shownId, setShownId] = useState(id)
  if (id !== null && id !== shownId) setShownId(id)

  const detail = useQuery({
    queryKey: ['logs', 'detail', shownId],
    queryFn: () => api.get<LogDetail>(`/api/logs/${shownId}`),
    enabled: shownId !== null,
  })

  return (
    <Sheet open={id !== null} onOpenChange={(open) => !open && onClose()}>
      <SheetContent className="w-full gap-0 overflow-y-auto sm:max-w-2xl">
        <SheetHeader className="border-b">
          <SheetTitle>Request #{shownId}</SheetTitle>
          <SheetDescription>{detail.data ? fullTime(detail.data.created_at) : 'Request details'}</SheetDescription>
        </SheetHeader>
        <div className="grid grid-cols-[minmax(0,1fr)] gap-6 p-4">
          {detail.isPending ? (
            <Loading rows={6} />
          ) : detail.isError ? (
            <ErrorState error={detail.error} onRetry={detail.refetch} />
          ) : (
            <Details log={detail.data} />
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}

function Details({ log }: { log: LogDetail }) {
  const tokens = (n: number | null) => (n === null ? '—' : n.toLocaleString())
  const noBodies = log.request_body === null && log.response_body === null
  const bridge = (log.attempt_details ?? []).filter((a) => a.vision_bridge)
  const upstream = (log.attempt_details ?? []).filter((a) => !a.vision_bridge)

  return (
    <>
      <dl className="grid grid-cols-2 gap-x-6 gap-y-3 text-sm sm:grid-cols-3">
        <Item label="Status">
          <HttpStatusBadge code={log.status_code} />
        </Item>
        <Item label="Latency">{latency(log.latency_ms)}</Item>
        <Item label="Attempts">{log.attempts}</Item>
        <Item label="Model">
          <span className="flex flex-wrap items-center gap-2">
            <span className="font-mono text-xs">{log.model || '—'}</span>
            {log.model_type && <ModelTypeBadge type={log.model_type} />}
          </span>
        </Item>
        <Item label="Endpoint">
          <code className="font-mono text-xs">{log.endpoint || '—'}</code>
        </Item>
        <Item label="Stream">{log.stream ? 'Yes' : 'No'}</Item>
        <Item label="Provider">{log.provider_name || '—'}</Item>
        <Item label="Upstream model">
          <span className="font-mono text-xs">{log.upstream_model || '—'}</span>
        </Item>
        <Item label="Key">
          <code className="font-mono text-xs">{log.key_hint || '—'}</code>
        </Item>
        <Item label="Input tokens">{tokens(log.input_tokens)}</Item>
        <Item label="Output tokens">{tokens(log.output_tokens)}</Item>
        <Item label="Time">{fullTime(log.created_at)}</Item>
      </dl>

      {log.error && (
        <section className="grid gap-1.5">
          <h3 className="text-sm font-medium">Error</h3>
          <p className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm break-words whitespace-pre-wrap text-destructive">
            {log.error}
          </p>
        </section>
      )}

      {bridge.length > 0 && (
        <section className="grid gap-1.5">
          <h3 className="text-sm font-medium">Vision bridge</h3>
          <p className="text-xs text-muted-foreground">
            {plural(bridge.length, 'image')} sent to <span className="font-mono">{bridge[0].vision_bridge}</span> for a
            description before routing. Each call is also logged as its own request.
          </p>
          <AttemptsTable attempts={bridge} />
        </section>
      )}

      <section className="grid gap-1.5">
        <h3 className="text-sm font-medium">Attempts</h3>
        <AttemptsTable attempts={upstream} />
      </section>

      {log.request_body !== null && <Body title="Request body" body={log.request_body} />}
      {log.response_body !== null && <Body title="Response body" body={log.response_body} />}
      {noBodies && (
        <p className="text-sm text-muted-foreground">
          Request and response bodies were not stored. Turn on “Store request/response bodies” in{' '}
          <Link to="/settings" className="underline hover:text-foreground">
            Settings
          </Link>{' '}
          to keep them for new requests.
        </p>
      )}
    </>
  )
}

function Item({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 break-words">{children}</dd>
    </div>
  )
}

function Body({ title, body }: { title: string; body: string }) {
  let text = body
  try {
    text = JSON.stringify(JSON.parse(body), null, 2)
  } catch {
    // Not JSON (stream, truncated body or plain text): show as stored.
  }
  return (
    <section className="grid gap-1.5">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium">{title}</h3>
        <CopyButton value={body} label={`Copy ${title.toLowerCase()}`} />
      </div>
      <pre className="max-h-80 overflow-auto rounded-md border bg-muted/50 p-3 font-mono text-xs break-words whitespace-pre-wrap">
        {text || '(empty)'}
      </pre>
    </section>
  )
}
