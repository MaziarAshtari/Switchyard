import { useState } from 'react'
import { useInfiniteQuery } from '@tanstack/react-query'
import { Radio, RefreshCw, ScrollText } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { HttpStatusBadge, ModelTypeBadge } from '@/components/Badges'
import { EmptyState, ErrorState, Field, Loading, PageHeader, RelativeTime } from '@/components/common'
import { api, qs } from '@/lib/api'
import { compact, latency, localInputToIso } from '@/lib/format'
import { useModels, useProviders } from '@/lib/queries'
import type { LogPage } from '@/lib/types'
import { cn } from '@/lib/utils'
import { LogDetailSheet } from './LogDetailSheet'

interface Filters {
  model: string
  provider_id: string
  status: string
  from: string
  to: string
}

const ALL = 'all'
const noFilters: Filters = { model: ALL, provider_id: ALL, status: ALL, from: '', to: '' }
const orEmpty = (value: string) => (value === ALL ? '' : value)

export default function LogsPage() {
  const [filters, setFilters] = useState<Filters>(noFilters)
  const [openId, setOpenId] = useState<number | null>(null)
  const models = useModels()
  const providers = useProviders()

  const params = {
    model: orEmpty(filters.model),
    provider_id: orEmpty(filters.provider_id),
    status: orEmpty(filters.status),
    from: localInputToIso(filters.from),
    to: localInputToIso(filters.to),
    limit: 50,
  }
  const logs = useInfiniteQuery({
    queryKey: ['logs', params],
    queryFn: ({ pageParam }) => api.get<LogPage>(`/api/logs${qs({ ...params, before_id: pageParam })}`),
    initialPageParam: null as number | null,
    getNextPageParam: (last) => last.next_before_id,
  })
  const items = logs.data?.pages.flatMap((page) => page.items) ?? []
  const filtered = JSON.stringify(filters) !== JSON.stringify(noFilters)
  const setFilter = (key: keyof Filters, value: string) => setFilters((f) => ({ ...f, [key]: value }))

  return (
    <>
      <PageHeader
        title="Logs"
        description="Every request sent to the gateway, with each upstream attempt."
        actions={
          <Button variant="outline" onClick={() => logs.refetch()} disabled={logs.isFetching}>
            <RefreshCw className={cn(logs.isFetching && !logs.isFetchingNextPage && 'animate-spin')} />
            Refresh
          </Button>
        }
      />

      <Card className="mb-4 py-0">
        <div className="flex flex-wrap items-end gap-3 p-4">
          <Field label="Model" htmlFor="filter-model" className="w-full sm:w-48">
            <Select value={filters.model} onValueChange={(v) => setFilter('model', v)}>
              <SelectTrigger id="filter-model" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={ALL}>All models</SelectItem>
                {models.data?.map((m) => (
                  <SelectItem key={m.id} value={m.name}>
                    {m.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field label="Provider" htmlFor="filter-provider" className="w-full sm:w-48">
            <Select value={filters.provider_id} onValueChange={(v) => setFilter('provider_id', v)}>
              <SelectTrigger id="filter-provider" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={ALL}>All providers</SelectItem>
                {providers.data?.map((p) => (
                  <SelectItem key={p.id} value={String(p.id)}>
                    {p.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field label="Status" htmlFor="filter-status" className="w-full sm:w-36">
            <Select value={filters.status} onValueChange={(v) => setFilter('status', v)}>
              <SelectTrigger id="filter-status" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={ALL}>All</SelectItem>
                <SelectItem value="success">Success</SelectItem>
                <SelectItem value="error">Errors</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field label="From" htmlFor="filter-from" className="w-full sm:w-auto">
            <Input
              id="filter-from"
              type="datetime-local"
              value={filters.from}
              onChange={(e) => setFilter('from', e.target.value)}
            />
          </Field>
          <Field label="To" htmlFor="filter-to" className="w-full sm:w-auto">
            <Input id="filter-to" type="datetime-local" value={filters.to} onChange={(e) => setFilter('to', e.target.value)} />
          </Field>
          <Button variant="ghost" onClick={() => setFilters(noFilters)} disabled={!filtered}>
            Reset
          </Button>
        </div>
      </Card>

      {logs.isPending ? (
        <Loading rows={8} />
      ) : logs.isError ? (
        <ErrorState error={logs.error} onRetry={logs.refetch} />
      ) : items.length === 0 ? (
        <EmptyState
          icon={ScrollText}
          title={filtered ? 'No requests match these filters' : 'No requests logged yet'}
          hint={
            filtered
              ? 'Try a wider date range or reset the filters.'
              : 'Requests that clients send to /v1 with a gateway key appear here.'
          }
          action={
            filtered && (
              <Button variant="outline" onClick={() => setFilters(noFilters)}>
                Reset filters
              </Button>
            )
          }
        />
      ) : (
        <>
          <Card className="overflow-hidden py-0">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="pl-4">Time</TableHead>
                  <TableHead>Model</TableHead>
                  <TableHead>Provider · upstream model</TableHead>
                  <TableHead>Key</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Latency</TableHead>
                  <TableHead>Tokens in / out</TableHead>
                  <TableHead>Attempts</TableHead>
                  <TableHead>
                    <span className="sr-only">Stream</span>
                  </TableHead>
                  <TableHead className="pr-4">Error</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((e) => (
                  <TableRow
                    key={e.id}
                    tabIndex={0}
                    onClick={() => setOpenId(e.id)}
                    onKeyDown={(ev) => ev.key === 'Enter' && setOpenId(e.id)}
                    className="cursor-pointer"
                  >
                    <TableCell className="pl-4">
                      <RelativeTime value={e.created_at} />
                    </TableCell>
                    <TableCell>
                      <span className="flex items-center gap-2">
                        <span className="font-mono text-xs">{e.model || '—'}</span>
                        {e.model_type && <ModelTypeBadge type={e.model_type} className="px-1.5 py-0 text-[10px]" />}
                      </span>
                    </TableCell>
                    <TableCell className="max-w-[16rem] truncate">
                      {e.provider_name ? (
                        <>
                          {e.provider_name}
                          <span className="text-muted-foreground"> · {e.upstream_model}</span>
                        </>
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </TableCell>
                    <TableCell>
                      <code className="font-mono text-xs">{e.key_hint || '—'}</code>
                    </TableCell>
                    <TableCell>
                      <HttpStatusBadge code={e.status_code} />
                    </TableCell>
                    <TableCell className="tabular-nums">{latency(e.latency_ms)}</TableCell>
                    <TableCell className="tabular-nums">
                      {e.input_tokens === null && e.output_tokens === null ? (
                        <span className="text-muted-foreground">—</span>
                      ) : (
                        `${e.input_tokens === null ? '—' : compact(e.input_tokens)} / ${e.output_tokens === null ? '—' : compact(e.output_tokens)}`
                      )}
                    </TableCell>
                    <TableCell className={cn('tabular-nums', e.attempts > 1 && 'text-amber-600 dark:text-amber-400')}>
                      {e.attempts}
                    </TableCell>
                    <TableCell>
                      {e.stream && (
                        <span title="Streamed response" aria-label="Streamed response">
                          <Radio className="size-4 text-muted-foreground" />
                        </span>
                      )}
                    </TableCell>
                    <TableCell className="max-w-[18rem] truncate pr-4 text-xs text-muted-foreground" title={e.error}>
                      {e.error}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </Card>
          {logs.hasNextPage && (
            <div className="mt-4 flex justify-center">
              <Button variant="outline" onClick={() => logs.fetchNextPage()} disabled={logs.isFetchingNextPage}>
                {logs.isFetchingNextPage ? 'Loading…' : 'Load more'}
              </Button>
            </div>
          )}
        </>
      )}

      <LogDetailSheet id={openId} onClose={() => setOpenId(null)} />
    </>
  )
}
