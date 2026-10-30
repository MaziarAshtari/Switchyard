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
import { Switch } from '@/components/ui/switch'
import { Field, FormError } from '@/components/common'
import { api } from '@/lib/api'
import { intInRange, toErrors, useFormState, type Errors } from '@/lib/form'
import { proxyTypes } from '@/lib/labels'
import { useAction } from '@/lib/queries'
import type { Proxy, ProxyInput, ProxyType } from '@/lib/types'

interface ProxyDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Omit to create a new proxy. */
  proxy?: Proxy
}

export function ProxyDialog({ open, onOpenChange, proxy }: ProxyDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{proxy ? `Edit ${proxy.name}` : 'Add proxy'}</DialogTitle>
          <DialogDescription>Upstream requests can be sent through this proxy.</DialogDescription>
        </DialogHeader>
        <ProxyForm proxy={proxy} onDone={() => onOpenChange(false)} />
      </DialogContent>
    </Dialog>
  )
}

function ProxyForm({ proxy, onDone }: { proxy?: Proxy; onDone: () => void }) {
  const [values, set] = useFormState({
    name: proxy?.name ?? '',
    type: proxy?.type ?? ('http' as ProxyType),
    host: proxy?.host ?? '',
    port: proxy ? String(proxy.port) : '',
    username: proxy?.username ?? '',
    password: '',
    enabled: proxy?.enabled ?? true,
  })
  const [errors, setErrors] = useState<Errors>({})
  const save = useAction(
    (input: ProxyInput) =>
      proxy ? api.put<Proxy>(`/api/proxies/${proxy.id}`, input) : api.post<Proxy>('/api/proxies', input),
    { success: proxy ? 'Proxy saved' : 'Proxy added', toastErrors: false },
  )

  function submit(e: FormEvent) {
    e.preventDefault()
    const errs: Errors = {}
    const host = values.host.trim()
    if (!host) errs.host = 'Host is required'
    else if (/[\s/]/.test(host)) errs.host = 'Enter only the host name or IP address (no scheme or path)'
    const port = intInRange(values.port, 1, 65535)
    if (port === null) errs.port = 'Enter a port from 1 to 65535'
    setErrors(errs)
    if (Object.keys(errs).length > 0 || port === null) return

    const input: ProxyInput = {
      name: values.name.trim(),
      type: values.type,
      host,
      port,
      username: values.username.trim(),
      enabled: values.enabled,
    }
    // An empty password field keeps the stored password.
    if (values.password) input.password = values.password
    save.mutate(input, { onSuccess: onDone, onError: (err) => setErrors(toErrors(err)) })
  }

  return (
    <form onSubmit={submit} className="grid gap-4" noValidate>
      <FormError message={errors._} />
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Name" htmlFor="proxy-name" error={errors.name} help="Optional.">
          <Input
            id="proxy-name"
            value={values.name}
            onChange={(e) => set('name', e.target.value)}
            placeholder="defaults to host:port"
            autoFocus
          />
        </Field>
        <Field label="Type" htmlFor="proxy-type" error={errors.type}>
          <Select value={values.type} onValueChange={(t) => set('type', t as ProxyType)}>
            <SelectTrigger id="proxy-type" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {proxyTypes.map((t) => (
                <SelectItem key={t.value} value={t.value}>
                  {t.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
      </div>
      <div className="grid gap-4 sm:grid-cols-[1fr_8rem]">
        <Field label="Host" htmlFor="proxy-host" error={errors.host}>
          <Input
            id="proxy-host"
            value={values.host}
            onChange={(e) => set('host', e.target.value)}
            placeholder="1.2.3.4 or proxy.example.com"
            className="font-mono"
            aria-invalid={!!errors.host}
          />
        </Field>
        <Field label="Port" htmlFor="proxy-port" error={errors.port}>
          <Input
            id="proxy-port"
            inputMode="numeric"
            value={values.port}
            onChange={(e) => set('port', e.target.value)}
            placeholder="8080"
            className="font-mono"
            aria-invalid={!!errors.port}
          />
        </Field>
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Username" htmlFor="proxy-user" error={errors.username} help="Optional.">
          <Input
            id="proxy-user"
            value={values.username}
            onChange={(e) => set('username', e.target.value)}
            autoComplete="off"
          />
        </Field>
        <Field label="Password" htmlFor="proxy-password" error={errors.password}>
          <Input
            id="proxy-password"
            type="password"
            autoComplete="new-password"
            value={values.password}
            onChange={(e) => set('password', e.target.value)}
            placeholder={proxy?.has_password ? 'Leave empty to keep the current password' : 'Optional'}
          />
        </Field>
      </div>
      <label className="flex items-center justify-between gap-4 rounded-md border p-3">
        <span className="grid gap-0.5">
          <span className="text-sm font-medium">Enabled</span>
          <span className="text-xs text-muted-foreground">Keys that use a disabled proxy are skipped.</span>
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
          {save.isPending ? 'Saving…' : proxy ? 'Save' : 'Add proxy'}
        </Button>
      </DialogFooter>
    </form>
  )
}
