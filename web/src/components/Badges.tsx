import { useEffect, useState, type ComponentProps } from 'react'
import { Badge } from '@/components/ui/badge'
import { Tip } from '@/components/Tip'
import { duration, fullTime } from '@/lib/format'
import { labelOf, modelTypes, providerTypes, proxyTypes } from '@/lib/labels'
import type { ApiKey, ModelType, ProviderType, ProxyType } from '@/lib/types'
import { cn } from '@/lib/utils'

const tones = {
  green: 'border-emerald-500/25 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400',
  amber: 'border-amber-500/25 bg-amber-500/10 text-amber-700 dark:text-amber-400',
  red: 'border-red-500/25 bg-red-500/10 text-red-700 dark:text-red-400',
  grey: 'border-border bg-muted text-muted-foreground',
  blue: 'border-blue-500/25 bg-blue-500/10 text-blue-700 dark:text-blue-300',
  violet: 'border-violet-500/25 bg-violet-500/10 text-violet-700 dark:text-violet-300',
  pink: 'border-pink-500/25 bg-pink-500/10 text-pink-700 dark:text-pink-300',
  orange: 'border-orange-500/25 bg-orange-500/10 text-orange-700 dark:text-orange-300',
  teal: 'border-teal-500/25 bg-teal-500/10 text-teal-700 dark:text-teal-300',
  lime: 'border-lime-500/25 bg-lime-500/10 text-lime-700 dark:text-lime-300',
}

type Tone = keyof typeof tones

export function ToneBadge({ tone, className, ...props }: ComponentProps<typeof Badge> & { tone: Tone }) {
  return <Badge variant="outline" className={cn(tones[tone], className)} {...props} />
}

const modelTypeTones: Record<ModelType, Tone> = {
  chat: 'blue',
  embedding: 'violet',
  image: 'pink',
  video: 'orange',
  tts: 'teal',
  asr: 'lime',
}

export function ModelTypeBadge({ type, className }: { type: string; className?: string }) {
  const tone = modelTypeTones[type as ModelType] ?? 'grey'
  return (
    <ToneBadge tone={tone} className={className}>
      {labelOf(modelTypes, type as ModelType)}
    </ToneBadge>
  )
}

export function ProviderTypeBadge({ type }: { type: ProviderType }) {
  return <Badge variant="outline">{providerTypes.find((t) => t.value === type)?.short ?? type}</Badge>
}

export function ProxyTypeBadge({ type }: { type: ProxyType }) {
  return <Badge variant="outline">{labelOf(proxyTypes, type)}</Badge>
}

/** HTTP status: 2xx green, 4xx amber, 5xx red, 0 (no response) grey. */
export function HttpStatusBadge({ code }: { code: number }) {
  if (!code) {
    return (
      <Tip content="No response (connection error or timeout)">
        <ToneBadge tone="grey">—</ToneBadge>
      </Tip>
    )
  }
  const tone: Tone = code >= 200 && code < 300 ? 'green' : code >= 500 ? 'red' : code >= 400 ? 'amber' : 'grey'
  return (
    <ToneBadge tone={tone} className="font-mono">
      {code}
    </ToneBadge>
  )
}

/** Runtime status of an upstream key, plus "Off" when it is switched off. */
export function KeyStatusBadges({ apiKey }: { apiKey: ApiKey }) {
  return (
    <div className="flex flex-wrap items-center gap-1">
      {apiKey.status === 'active' && <ToneBadge tone="green">Active</ToneBadge>}
      {apiKey.status === 'cooling_down' && <CoolingDownBadge until={apiKey.cooldown_until} />}
      {apiKey.status === 'disabled' &&
        (apiKey.disabled_until ? (
          <RecoveringBadge until={apiKey.disabled_until} reason={apiKey.last_error} />
        ) : (
          <Tip content={apiKey.last_error || 'Disabled after an authentication error'}>
            <ToneBadge tone="red">Disabled</ToneBadge>
          </Tip>
        ))}
      {!apiKey.enabled && (
        <Tip content="Switched off: this key is never used">
          <ToneBadge tone="grey">Off</ToneBadge>
        </Tip>
      )}
    </div>
  )
}

function CoolingDownBadge({ until }: { until: string | null }) {
  const now = useNow(1000)
  const remaining = until ? (new Date(until).getTime() - now) / 1000 : 0
  return (
    <Tip content={until ? `Cooling down until ${fullTime(until)}` : 'Cooling down after a 429 response'}>
      <ToneBadge tone="amber">Cooling down{remaining > 0 ? ` · ${duration(remaining)}` : ''}</ToneBadge>
    </Tip>
  )
}

function RecoveringBadge({ until, reason }: { until: string; reason: string }) {
  const now = useNow(1000)
  const remaining = (new Date(until).getTime() - now) / 1000
  return (
    <Tip
      content={
        <>
          {reason || 'Disabled after an authentication error'}
          <br />
          Re-activated automatically at {fullTime(until)}
        </>
      }
    >
      <ToneBadge tone="red">Disabled{remaining > 0 ? ` · ${duration(remaining)}` : ''}</ToneBadge>
    </Tip>
  )
}

function useNow(intervalMs: number) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])
  return now
}
