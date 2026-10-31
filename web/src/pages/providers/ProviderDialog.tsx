import { useState, type FormEvent } from 'react'
import { Plus, X } from 'lucide-react'
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
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Field, FormError } from '@/components/common'
import { api } from '@/lib/api'
import { intInRange, toErrors, useFormState, type Errors } from '@/lib/form'
import { chatEndpoint, defaultBaseUrls, keyRecoveryText, keyStrategies, maxKeyRecoveryMinutes, providerTypes } from '@/lib/labels'
import { useAction, useProxies, useSettings } from '@/lib/queries'
import type { KeyStrategy, Provider, ProviderInput, ProviderType } from '@/lib/types'

interface ProviderDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Omit to create a new provider. */
  provider?: Provider
}

export function ProviderDialog({ open, onOpenChange, provider }: ProviderDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{provider ? `Edit ${provider.name}` : 'Add provider'}</DialogTitle>
          <DialogDescription>An upstream AI service. Its API keys are managed on the provider page.</DialogDescription>
        </DialogHeader>
        <ProviderForm provider={provider} onDone={() => onOpenChange(false)} />
      </DialogContent>
    </Dialog>
  )
}

interface FormValues {
  name: string
  type: ProviderType
  base_url: string
  headers: { name: string; value: string }[]
  timeout_seconds: string
  enabled: boolean
  proxy_id: number | null
  key_strategy: KeyStrategy
  /** "" = Settings default */
  disabled_key_recovery_minutes: string
}

function initialValues(p?: Provider): FormValues {
  return {
    name: p?.name ?? '',
    type: p?.type ?? 'openai',
    base_url: p?.base_url ?? defaultBaseUrls.openai,
    headers: Object.entries(p?.headers ?? {}).map(([name, value]) => ({ name, value })),
    timeout_seconds: String(p?.timeout_seconds ?? 600),
    enabled: p?.enabled ?? true,
    proxy_id: p?.proxy_id ?? null,
    key_strategy: p?.key_strategy ?? 'round_robin',
    disabled_key_recovery_minutes: p?.disabled_key_recovery_minutes == null ? '' : String(p.disabled_key_recovery_minutes),
  }
}

function validate(v: FormValues): { input?: ProviderInput; errors: Errors } {
  const errors: Errors = {}
  if (!v.name.trim()) errors.name = 'Name is required'
  const baseUrl = v.base_url.trim()
  if (!baseUrl) errors.base_url = 'Base URL is required'
  else if (!/^https?:\/\/\S+$/i.test(baseUrl)) errors.base_url = 'Must start with http:// or https://'
  const timeout = intInRange(v.timeout_seconds, 1, 3600)
  if (timeout === null) errors.timeout_seconds = 'Enter a number of seconds from 1 to 3600'
  const recoveryText = v.disabled_key_recovery_minutes.trim()
  const recovery = recoveryText ? intInRange(recoveryText, 0, maxKeyRecoveryMinutes) : null
  if (recoveryText && recovery === null) {
    errors.disabled_key_recovery_minutes = `Enter minutes from 0 to ${maxKeyRecoveryMinutes}, or leave empty for the default`
  }

  const headers: Record<string, string> = {}
  for (const row of v.headers) {
    const name = row.name.trim()
    if (!name && !row.value.trim()) continue
    if (!name) {
      errors.headers = 'Every header needs a name'
      break
    }
    if (Object.keys(headers).some((h) => h.toLowerCase() === name.toLowerCase())) {
      errors.headers = `Duplicate header: ${name}`
      break
    }
    headers[name] = row.value
  }

  if (Object.keys(errors).length > 0 || timeout === null) return { errors }
  return {
    errors,
    input: {
      name: v.name.trim(),
      type: v.type,
      base_url: baseUrl,
      headers,
      timeout_seconds: timeout,
      enabled: v.enabled,
      proxy_id: v.proxy_id,
      key_strategy: v.key_strategy,
      disabled_key_recovery_minutes: recovery,
    },
  }
}

function ProviderForm({ provider, onDone }: { provider?: Provider; onDone: () => void }) {
  const proxies = useProxies()
  const settings = useSettings()
  const [values, set, setValues] = useFormState(() => initialValues(provider))
  const [errors, setErrors] = useState<Errors>({})
  const save = useAction(
    (input: ProviderInput) =>
      provider ? api.put<Provider>(`/api/providers/${provider.id}`, input) : api.post<Provider>('/api/providers', input),
    { success: provider ? 'Provider saved' : 'Provider added', toastErrors: false },
  )

  function changeType(type: ProviderType) {
    setValues((v) => {
      const url = v.base_url.trim()
      const isDefault = !url || Object.values(defaultBaseUrls).includes(url)
      return { ...v, type, base_url: isDefault ? defaultBaseUrls[type] : v.base_url }
    })
  }

  function setHeader(index: number, patch: Partial<FormValues['headers'][number]>) {
    set(
      'headers',
      values.headers.map((h, i) => (i === index ? { ...h, ...patch } : h)),
    )
  }

  function submit(e: FormEvent) {
    e.preventDefault()
    const { input, errors } = validate(values)
    setErrors(errors)
    if (!input) return
    save.mutate(input, { onSuccess: onDone, onError: (err) => setErrors(toErrors(err)) })
  }

  const endpoint = chatEndpoint(values.type, values.base_url)
  const strategyHelp = keyStrategies.find((s) => s.value === values.key_strategy)?.help
  const defaultRecovery = settings.data ? keyRecoveryText(settings.data.disabled_key_recovery_minutes).toLowerCase() : ''

  return (
    <form onSubmit={submit} className="grid gap-4" noValidate>
      <FormError message={errors._} />

      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Name" htmlFor="provider-name" error={errors.name}>
          <Input
            id="provider-name"
            value={values.name}
            onChange={(e) => set('name', e.target.value)}
            placeholder="e.g. OpenAI"
            aria-invalid={!!errors.name}
            autoFocus
          />
        </Field>
        <Field label="Type" htmlFor="provider-type" error={errors.type}>
          <Select value={values.type} onValueChange={(t) => changeType(t as ProviderType)}>
            <SelectTrigger id="provider-type" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {providerTypes.map((t) => (
                <SelectItem key={t.value} value={t.value}>
                  {t.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      </div>

      <Field
        label="Base URL"
        htmlFor="provider-url"
        error={errors.base_url}
        help={
          endpoint && (
            <>
              Chat requests go to <code className="font-mono break-all">{endpoint}</code>
            </>
          )
        }
      >
        <Input
          id="provider-url"
          value={values.base_url}
          onChange={(e) => set('base_url', e.target.value)}
          placeholder={defaultBaseUrls[values.type]}
          aria-invalid={!!errors.base_url}
        />
      </Field>

      <div className="grid gap-1.5">
        <Label>Extra headers</Label>
        {values.headers.length === 0 && (
          <p className="text-xs text-muted-foreground">None. Added to every request sent to this provider.</p>
        )}
        {values.headers.map((h, i) => (
          <div key={i} className="flex gap-2">
            <Input
              value={h.name}
              onChange={(e) => setHeader(i, { name: e.target.value })}
              placeholder="Header name"
              aria-label="Header name"
              className="font-mono text-xs"
            />
            <Input
              value={h.value}
              onChange={(e) => setHeader(i, { value: e.target.value })}
              placeholder="Value"
              aria-label="Header value"
              className="font-mono text-xs"
            />
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label="Remove header"
              onClick={() =>
                set(
                  'headers',
                  values.headers.filter((_, j) => j !== i),
                )
              }
            >
              <X />
            </Button>
          </div>
        ))}
        {errors.headers && <p className="text-xs font-medium text-destructive">{errors.headers}</p>}
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="w-fit"
          onClick={() => set('headers', [...values.headers, { name: '', value: '' }])}
        >
          <Plus />
          Add header
        </Button>
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <Field
          label="Request timeout (seconds)"
          htmlFor="provider-timeout"
          error={errors.timeout_seconds}
          help="Maximum total time for one upstream request, including streaming."
        >
          <Input
            id="provider-timeout"
            inputMode="numeric"
            value={values.timeout_seconds}
            onChange={(e) => set('timeout_seconds', e.target.value)}
            aria-invalid={!!errors.timeout_seconds}
          />
        </Field>
        <Field
          label="Default proxy"
          htmlFor="provider-proxy"
          error={errors.proxy_id}
          help="Used by keys set to “Inherit provider default”."
        >
          <Select
            value={values.proxy_id === null ? 'none' : String(values.proxy_id)}
            onValueChange={(v) => set('proxy_id', v === 'none' ? null : Number(v))}
          >
            <SelectTrigger id="provider-proxy" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="none">None (direct)</SelectItem>
              {proxies.data?.map((p) => (
                <SelectItem key={p.id} value={String(p.id)}>
                  {p.name}
                  {!p.enabled && ' (disabled)'}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Key selection" htmlFor="provider-strategy" error={errors.key_strategy} help={strategyHelp}>
          <Select value={values.key_strategy} onValueChange={(v) => set('key_strategy', v as KeyStrategy)}>
            <SelectTrigger id="provider-strategy" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {keyStrategies.map((s) => (
                <SelectItem key={s.value} value={s.value}>
                  {s.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field
          label="Disabled key recovery (minutes)"
          htmlFor="provider-recovery"
          error={errors.disabled_key_recovery_minutes}
          help="Re-activates keys disabled by 401/402/403. Empty = Settings default, 0 = never."
        >
          <Input
            id="provider-recovery"
            inputMode="numeric"
            value={values.disabled_key_recovery_minutes}
            onChange={(e) => set('disabled_key_recovery_minutes', e.target.value)}
            placeholder={defaultRecovery ? `Default (${defaultRecovery})` : 'Default'}
            aria-invalid={!!errors.disabled_key_recovery_minutes}
          />
        </Field>
      </div>

      <label className="flex items-center justify-between gap-4 rounded-md border p-3">
        <span className="grid gap-0.5">
          <span className="text-sm font-medium">Enabled</span>
          <span className="text-xs text-muted-foreground">Disabled providers are skipped by every model.</span>
        </span>
        <Switch checked={values.enabled} onCheckedChange={(c) => set('enabled', c)} />
      </label>

      <DialogFooter>
        <DialogClose asChild>
          <Button type="button" variant="outline">
            Cancel
          </Button>
        </DialogClose>
        <Button type="submit" disabled={save.isPending}>
          {save.isPending ? 'Saving…' : provider ? 'Save' : 'Add provider'}
        </Button>
      </DialogFooter>
    </form>
  )
}
