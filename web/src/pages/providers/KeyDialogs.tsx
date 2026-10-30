import { useState, type FormEvent } from 'react'
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
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { Field, FormError } from '@/components/common'
import { api, describeError } from '@/lib/api'
import { plural } from '@/lib/format'
import { toErrors, useFormState, type Errors } from '@/lib/form'
import { useAction, useProxies } from '@/lib/queries'
import type { ApiKey, BulkKeysResult, KeyInput, Provider, ProxyMode } from '@/lib/types'

interface DialogProps {
  provider: Provider
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function AddKeyDialog({ provider, open, onOpenChange }: DialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add API key</DialogTitle>
          <DialogDescription>The key is stored encrypted and is never shown again.</DialogDescription>
        </DialogHeader>
        <AddKeyForm provider={provider} onDone={() => onOpenChange(false)} />
      </DialogContent>
    </Dialog>
  )
}

export function EditKeyDialog({ provider, apiKey, open, onOpenChange }: DialogProps & { apiKey: ApiKey }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Edit key</DialogTitle>
          <DialogDescription>
            <code className="font-mono">{apiKey.secret_hint}</code> · the secret itself cannot be changed; add a new key
            instead.
          </DialogDescription>
        </DialogHeader>
        <EditKeyForm provider={provider} apiKey={apiKey} onDone={() => onOpenChange(false)} />
      </DialogContent>
    </Dialog>
  )
}

export function BulkAddDialog({ provider, open, onOpenChange }: DialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Bulk add API keys</DialogTitle>
          <DialogDescription>Keys that already exist for {provider.name} are skipped.</DialogDescription>
        </DialogHeader>
        <BulkAddForm provider={provider} />
      </DialogContent>
    </Dialog>
  )
}

interface ProxyValues {
  proxy_mode: ProxyMode
  proxy_id: number | null
}

function validateProxy(v: ProxyValues, errors: Errors) {
  if (v.proxy_mode === 'specific' && v.proxy_id === null) errors.proxy_id = 'Choose a proxy'
}

function ProxyFields({
  provider,
  values,
  onChange,
  errors,
}: {
  provider: Provider
  values: ProxyValues
  onChange: (patch: Partial<ProxyValues>) => void
  errors: Errors
}) {
  const proxies = useProxies()
  const inheritLabel = `Inherit provider default (${provider.proxy_id ? provider.proxy_name : 'direct'})`

  return (
    <>
      <Field label="Proxy" htmlFor="key-proxy-mode" error={errors.proxy_mode}>
        <Select value={values.proxy_mode} onValueChange={(mode) => onChange({ proxy_mode: mode as ProxyMode })}>
          <SelectTrigger id="key-proxy-mode" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="inherit">{inheritLabel}</SelectItem>
            <SelectItem value="direct">Direct (no proxy)</SelectItem>
            <SelectItem value="specific">Specific proxy</SelectItem>
          </SelectContent>
        </Select>
      </Field>
      {values.proxy_mode === 'specific' && (
        <Field label="Specific proxy" htmlFor="key-proxy" error={errors.proxy_id}>
          <Select
            value={values.proxy_id === null ? '' : String(values.proxy_id)}
            onValueChange={(id) => onChange({ proxy_id: Number(id) })}
          >
            <SelectTrigger id="key-proxy" className="w-full" aria-invalid={!!errors.proxy_id}>
              <SelectValue placeholder={proxies.data?.length ? 'Choose a proxy' : 'No proxies yet (add them on the Proxies page)'} />
            </SelectTrigger>
            <SelectContent>
              {proxies.data?.map((p) => (
                <SelectItem key={p.id} value={String(p.id)}>
                  {p.name}
                  {!p.enabled && ' (disabled)'}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      )}
    </>
  )
}

function AddKeyForm({ provider, onDone }: { provider: Provider; onDone: () => void }) {
  const [values, set, setValues] = useFormState({
    label: '',
    secret: '',
    proxy_mode: 'inherit' as ProxyMode,
    proxy_id: null as number | null,
  })
  const [errors, setErrors] = useState<Errors>({})
  const add = useAction((body: typeof values) => api.post<ApiKey>(`/api/providers/${provider.id}/keys`, body), {
    success: 'Key added',
    toastErrors: false,
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    const errs: Errors = {}
    if (!values.secret.trim()) errs.secret = 'API key is required'
    validateProxy(values, errs)
    setErrors(errs)
    if (Object.keys(errs).length > 0) return
    const body = {
      label: values.label.trim(),
      secret: values.secret.trim(),
      proxy_mode: values.proxy_mode,
      proxy_id: values.proxy_mode === 'specific' ? values.proxy_id : null,
    }
    add.mutate(body, { onSuccess: onDone, onError: (err) => setErrors(toErrors(err)) })
  }

  return (
    <form onSubmit={submit} className="grid gap-4" noValidate>
      <FormError message={errors._} />
      <Field label="Label" htmlFor="key-label" error={errors.label} help="Optional. Helps you recognise the key.">
        <Input
          id="key-label"
          value={values.label}
          onChange={(e) => set('label', e.target.value)}
          placeholder="e.g. main account"
          autoFocus
        />
      </Field>
      <Field label="API key" htmlFor="key-secret" error={errors.secret}>
        <Input
          id="key-secret"
          type="password"
          autoComplete="new-password"
          value={values.secret}
          onChange={(e) => set('secret', e.target.value)}
          className="font-mono"
          aria-invalid={!!errors.secret}
        />
      </Field>
      <ProxyFields
        provider={provider}
        values={values}
        onChange={(patch) => setValues((v) => ({ ...v, ...patch }))}
        errors={errors}
      />
      <DialogFooter>
        <DialogClose asChild>
          <Button type="button" variant="outline">
            Cancel
          </Button>
        </DialogClose>
        <Button type="submit" disabled={add.isPending}>
          {add.isPending ? 'Adding…' : 'Add key'}
        </Button>
      </DialogFooter>
    </form>
  )
}

function EditKeyForm({ provider, apiKey, onDone }: { provider: Provider; apiKey: ApiKey; onDone: () => void }) {
  const [values, set, setValues] = useFormState({
    label: apiKey.label,
    proxy_mode: apiKey.proxy_mode,
    proxy_id: apiKey.proxy_id,
  })
  const [errors, setErrors] = useState<Errors>({})
  const save = useAction((body: KeyInput) => api.put<ApiKey>(`/api/keys/${apiKey.id}`, body), {
    success: 'Key saved',
    toastErrors: false,
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    const errs: Errors = {}
    validateProxy(values, errs)
    setErrors(errs)
    if (Object.keys(errs).length > 0) return
    const body: KeyInput = {
      label: values.label.trim(),
      enabled: apiKey.enabled,
      proxy_mode: values.proxy_mode,
      proxy_id: values.proxy_mode === 'specific' ? values.proxy_id : null,
    }
    save.mutate(body, { onSuccess: onDone, onError: (err) => setErrors(toErrors(err)) })
  }

  return (
    <form onSubmit={submit} className="grid gap-4" noValidate>
      <FormError message={errors._} />
      <Field label="Label" htmlFor="key-label" error={errors.label}>
        <Input id="key-label" value={values.label} onChange={(e) => set('label', e.target.value)} autoFocus />
      </Field>
      <ProxyFields
        provider={provider}
        values={values}
        onChange={(patch) => setValues((v) => ({ ...v, ...patch }))}
        errors={errors}
      />
      <DialogFooter>
        <DialogClose asChild>
          <Button type="button" variant="outline">
            Cancel
          </Button>
        </DialogClose>
        <Button type="submit" disabled={save.isPending}>
          {save.isPending ? 'Saving…' : 'Save'}
        </Button>
      </DialogFooter>
    </form>
  )
}

function BulkAddForm({ provider }: { provider: Provider }) {
  const [text, setText] = useState('')
  const [error, setError] = useState('')
  const [result, setResult] = useState<BulkKeysResult | null>(null)
  const add = useAction(
    (body: { text: string }) => api.post<BulkKeysResult>(`/api/providers/${provider.id}/keys/bulk`, body),
    { toastErrors: false },
  )
  const count = text.split('\n').filter((line) => line.trim()).length

  function submit(e: FormEvent) {
    e.preventDefault()
    if (count === 0) {
      setError('Paste at least one key.')
      return
    }
    setError('')
    add.mutate({ text }, { onSuccess: setResult, onError: (err) => setError(describeError(err)) })
  }

  return (
    <form onSubmit={submit} className="grid gap-4" noValidate>
      <FormError message={error} />
      <Field
        label="API keys"
        htmlFor="bulk-keys"
        help={
          <>
            One key per line. Optional label: <code className="font-mono">key|label</code> · {plural(count, 'key')}
          </>
        }
      >
        <Textarea
          id="bulk-keys"
          rows={8}
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={'sk-...\nsk-...|backup account'}
          className="max-h-80 font-mono text-xs"
          spellCheck={false}
          autoComplete="off"
          autoFocus
        />
      </Field>

      {result && (
        <div role="status" className="rounded-md border bg-muted/40 p-3 text-sm">
          <p className="font-medium">
            Added {result.added}
            {result.skipped > 0 && `, skipped ${plural(result.skipped, 'duplicate')}`}
            {result.errors.length > 0 && `, ${plural(result.errors.length, 'invalid line')}`}
          </p>
          {result.errors.length > 0 && (
            <ul className="mt-2 grid max-h-40 gap-1 overflow-y-auto text-xs text-destructive">
              {result.errors.map((e) => (
                <li key={e.line}>
                  Line {e.line}: {e.error}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      <DialogFooter>
        <DialogClose asChild>
          <Button type="button" variant="outline">
            {result ? 'Close' : 'Cancel'}
          </Button>
        </DialogClose>
        <Button type="submit" disabled={add.isPending || count === 0}>
          {add.isPending ? 'Adding…' : `Add ${plural(count, 'key')}`}
        </Button>
      </DialogFooter>
    </form>
  )
}
