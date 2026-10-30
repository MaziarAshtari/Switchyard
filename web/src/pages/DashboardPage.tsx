import { Link } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { ArrowRight, CheckCircle2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { KeyStatusBadges, ToneBadge } from '@/components/Badges'
import { CopyField, ErrorState, Loading, PageHeader } from '@/components/common'
import { api, qs } from '@/lib/api'
import { compact, latency, plural, startOfToday } from '@/lib/format'
import type { ApiKey, CountRow, Dashboard, KeyCounts } from '@/lib/types'
import { cn } from '@/lib/utils'

export default function DashboardPage() {
  const since = startOfToday()
  const dashboard = useQuery({
    queryKey: ['dashboard', since],
    queryFn: () => api.get<Dashboard>(`/api/dashboard${qs({ since })}`),
    refetchInterval: 30_000,
  })

  return (
    <>
      <PageHeader
        title="Dashboard"
        description="Today's traffic and the health of your upstream keys."
        actions={
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            Client base URL
            <CopyField value={`${window.location.origin}/v1`} label="Copy base URL" />
          </div>
        }
      />
      {dashboard.isPending ? (
        <Loading rows={6} />
      ) : dashboard.isError ? (
        <ErrorState error={dashboard.error} onRetry={dashboard.refetch} />
      ) : (
        <Overview data={dashboard.data} />
      )}
    </>
  )
}

function Overview({ data }: { data: Dashboard }) {
  const { setup } = data
  const needsSetup = setup.providers === 0 || setup.keys === 0 || setup.models === 0 || setup.gateway_keys === 0
  const hasRequests = data.requests > 0
  const successRate = data.success_rate >= 1 ? '100%' : `${(data.success_rate * 100).toFixed(1)}%`

  return (
    <div className="grid gap-4">
      {needsSetup && <GettingStarted setup={setup} />}

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Stat
          label="Requests today"
          value={compact(data.requests)}
          sub={data.errors ? `${compact(data.errors)} failed` : 'No failed requests'}
        />
        <Stat label="Success rate" value={hasRequests ? successRate : '—'} sub="Of today's requests" />
        <Stat label="Avg latency" value={hasRequests ? latency(data.avg_latency_ms) : '—'} sub="Per request" />
        <Stat
          label="Tokens"
          value={`${compact(data.input_tokens)} / ${compact(data.output_tokens)}`}
          sub="Input / output"
        />
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Requests per hour</CardTitle>
          <CardDescription>Today, local time. Failed requests are shown in red.</CardDescription>
        </CardHeader>
        <CardContent>
          <HourlyChart hourly={data.hourly} />
        </CardContent>
      </Card>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>By model</CardTitle>
          </CardHeader>
          <CardContent>
            <BarList rows={data.by_model} />
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>By provider</CardTitle>
          </CardHeader>
          <CardContent>
            <BarList rows={data.by_provider} />
          </CardContent>
        </Card>
      </div>

      <KeyHealth counts={data.keys} unhealthy={data.unhealthy_keys} />
    </div>
  )
}

function Stat({ label, value, sub }: { label: string; value: string; sub: string }) {
  return (
    <Card className="gap-1 px-5 py-4">
      <p className="text-sm text-muted-foreground">{label}</p>
      <p className="text-2xl font-semibold tabular-nums">{value}</p>
      <p className="text-xs text-muted-foreground">{sub}</p>
    </Card>
  )
}

const pad = (n: number) => String(n).padStart(2, '0')

function HourlyChart({ hourly }: { hourly: Dashboard['hourly'] }) {
  const hours = Array.from(
    { length: 24 },
    (_, hour) => hourly.find((h) => h.hour === hour) ?? { hour, requests: 0, errors: 0 },
  )
  const max = Math.max(1, ...hours.map((h) => h.requests))

  return (
    <div>
      <div className="flex h-40 items-end gap-1">
        {hours.map((h) => (
          <div
            key={h.hour}
            className="flex h-full flex-1 flex-col justify-end rounded-sm bg-muted/50"
            title={`${pad(h.hour)}:00–${pad(h.hour + 1)}:00 · ${plural(h.requests, 'request')} · ${plural(h.errors, 'error')}`}
          >
            {h.requests > 0 && (
              <div
                className="flex min-h-0.5 flex-col overflow-hidden rounded-sm"
                style={{ height: `${(h.requests / max) * 100}%` }}
              >
                <div className="flex-1 bg-primary/70" />
                <div className="bg-destructive" style={{ height: `${(h.errors / h.requests) * 100}%` }} />
              </div>
            )}
          </div>
        ))}
      </div>
      <div className="mt-1.5 flex gap-1 text-[10px] text-muted-foreground tabular-nums">
        {hours.map((h) => (
          <div key={h.hour} className="flex-1 text-center">
            {h.hour % 3 === 0 ? pad(h.hour) : ''}
          </div>
        ))}
      </div>
    </div>
  )
}

function BarList({ rows }: { rows: CountRow[] }) {
  if (rows.length === 0) return <p className="text-sm text-muted-foreground">No requests yet today.</p>
  const sorted = [...rows].sort((a, b) => b.requests - a.requests)
  const max = Math.max(1, ...sorted.map((r) => r.requests))

  return (
    <ul className="grid gap-2.5">
      {sorted.map((r) => (
        <li key={r.name} className="grid grid-cols-[minmax(0,9rem)_1fr_auto] items-center gap-3 text-sm">
          <span className="truncate" title={r.name}>
            {r.name || '(none)'}
          </span>
          <div className="h-2 overflow-hidden rounded-full bg-muted">
            <div className="h-full rounded-full bg-primary/70" style={{ width: `${(r.requests / max) * 100}%` }} />
          </div>
          <span className="text-right tabular-nums">
            {compact(r.requests)}
            {r.errors > 0 && <span className="ml-2 text-destructive">{compact(r.errors)} failed</span>}
          </span>
        </li>
      ))}
    </ul>
  )
}

function KeyHealth({ counts, unhealthy }: { counts: KeyCounts; unhealthy: ApiKey[] }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Key health</CardTitle>
        <CardDescription>{plural(counts.total, 'upstream API key')} across all providers.</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-4">
        <div className="flex flex-wrap gap-2">
          <ToneBadge tone="green">Active · {counts.active}</ToneBadge>
          <ToneBadge tone="amber">Cooling down · {counts.cooling_down}</ToneBadge>
          <ToneBadge tone="red">Disabled · {counts.disabled}</ToneBadge>
          <ToneBadge tone="grey">Off · {counts.off}</ToneBadge>
        </div>
        {unhealthy.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            {counts.total === 0 ? 'No API keys yet.' : 'All enabled keys are usable.'}
          </p>
        ) : (
          <ul className="divide-y rounded-md border">
            {unhealthy.map((k) => (
              <li key={k.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-sm">
                <Link to={`/providers/${k.provider_id}`} className="font-medium hover:underline">
                  {k.provider_name}
                </Link>
                {k.label && <span>{k.label}</span>}
                <code className="font-mono text-xs text-muted-foreground">{k.secret_hint}</code>
                <KeyStatusBadges apiKey={k} />
                {k.effective_proxy && !k.effective_proxy.enabled && (
                  <ToneBadge tone="amber">Proxy disabled</ToneBadge>
                )}
                {k.last_error && (
                  <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground" title={k.last_error}>
                    {k.last_error}
                  </span>
                )}
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}

function GettingStarted({ setup }: { setup: Dashboard['setup'] }) {
  const steps = [
    {
      done: setup.providers > 0,
      title: 'Add a provider',
      hint: 'An upstream service such as OpenAI, Anthropic or Gemini (or any compatible API).',
      to: '/providers',
      cta: 'Providers',
    },
    {
      done: setup.keys > 0,
      title: 'Add API keys',
      hint: "Open a provider and add one or more of its API keys.",
      to: '/providers',
      cta: 'Providers',
    },
    {
      done: setup.models > 0,
      title: 'Create a model',
      hint: 'The model name your clients send, routed to one or more provider models.',
      to: '/models',
      cta: 'Models',
    },
    {
      done: setup.gateway_keys > 0,
      title: 'Create a gateway key',
      hint: 'The API key your clients use to call Switchyard.',
      to: '/settings',
      cta: 'Settings',
    },
  ]

  return (
    <Card>
      <CardHeader>
        <CardTitle>Getting started</CardTitle>
        <CardDescription>Four steps until your clients can send requests through Switchyard.</CardDescription>
      </CardHeader>
      <CardContent>
        <ol className="grid gap-3">
          {steps.map((step, i) => (
            <li key={step.title} className="flex items-center gap-3">
              {step.done ? (
                <CheckCircle2 className="size-5 shrink-0 text-emerald-600 dark:text-emerald-400" aria-label="Done" />
              ) : (
                <span className="flex size-5 shrink-0 items-center justify-center rounded-full border text-xs text-muted-foreground">
                  {i + 1}
                </span>
              )}
              <div className="min-w-0 flex-1">
                <p className={cn('text-sm font-medium', step.done && 'text-muted-foreground line-through')}>{step.title}</p>
                <p className="text-xs text-muted-foreground">{step.hint}</p>
              </div>
              <Button asChild size="sm" variant={step.done ? 'ghost' : 'outline'}>
                <Link to={step.to}>
                  {step.cta}
                  <ArrowRight />
                </Link>
              </Button>
            </li>
          ))}
        </ol>
      </CardContent>
    </Card>
  )
}
