import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { ErrorState, Field, FormError, Loading, PageHeader } from '@/components/common'
import { api, ApiError } from '@/lib/api'
import { intInRange, toErrors, useFormState, type Errors } from '@/lib/form'
import { maxKeyRecoveryMinutes } from '@/lib/labels'
import { useAction, useSettings } from '@/lib/queries'
import type { Settings } from '@/lib/types'
import { GatewayKeysCard } from './GatewayKeysCard'

export default function SettingsPage() {
  const settings = useSettings()

  return (
    <div className="max-w-4xl">
      <PageHeader title="Settings" description="Client access, logging and the admin password." />
      <div className="grid gap-4">
        <GatewayKeysCard />
        {settings.isPending ? (
          <Loading rows={4} />
        ) : settings.isError ? (
          <ErrorState error={settings.error} onRetry={settings.refetch} />
        ) : (
          <GeneralCard settings={settings.data} />
        )}
        <PasswordCard />
      </div>
    </div>
  )
}

// The form is initialised once; background refetches never overwrite unsaved edits.
function GeneralCard({ settings }: { settings: Settings }) {
  const [values, set, setValues] = useFormState(() => ({
    log_retention_days: String(settings.log_retention_days),
    log_bodies: settings.log_bodies,
    body_kb: String(Math.max(1, Math.round(settings.log_body_max_bytes / 1024))),
    default_cooldown_seconds: String(settings.default_cooldown_seconds),
    disabled_key_recovery_minutes: String(settings.disabled_key_recovery_minutes),
    proxy_test_url: settings.proxy_test_url,
  }))
  const [errors, setErrors] = useState<Errors>({})
  const save = useAction((body: Settings) => api.put<Settings>('/api/settings', body), {
    success: 'Settings saved',
    toastErrors: false,
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    const errs: Errors = {}
    const retention = intInRange(values.log_retention_days, 1, 3650)
    if (retention === null) errs.log_retention_days = 'Enter a number of days from 1 to 3650'
    const kb = intInRange(values.body_kb, 1, 10240)
    if (kb === null) errs.log_body_max_bytes = 'Enter a size from 1 to 10240 KB (10 MB)'
    const cooldown = intInRange(values.default_cooldown_seconds, 1, 86400)
    if (cooldown === null) errs.default_cooldown_seconds = 'Enter a number of seconds from 1 to 86400'
    const recovery = intInRange(values.disabled_key_recovery_minutes, 0, maxKeyRecoveryMinutes)
    if (recovery === null) errs.disabled_key_recovery_minutes = `Enter a number of minutes from 0 to ${maxKeyRecoveryMinutes}`
    const url = values.proxy_test_url.trim()
    if (!/^https?:\/\/\S+$/i.test(url)) errs.proxy_test_url = 'Enter a URL starting with http:// or https://'
    setErrors(errs)
    if (Object.keys(errs).length > 0 || retention === null || kb === null || cooldown === null || recovery === null) return

    save.mutate(
      {
        log_retention_days: retention,
        log_bodies: values.log_bodies,
        log_body_max_bytes: kb * 1024,
        default_cooldown_seconds: cooldown,
        disabled_key_recovery_minutes: recovery,
        proxy_test_url: url,
      },
      {
        onSuccess: (saved) =>
          setValues((v) => ({ ...v, body_kb: String(Math.max(1, Math.round(saved.log_body_max_bytes / 1024))) })),
        onError: (err) => setErrors(toErrors(err)),
      },
    )
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>General</CardTitle>
        <CardDescription>Request logging, key cooldowns and recovery, proxy tests.</CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={submit} className="grid gap-4" noValidate>
          <FormError message={errors._} />
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              label="Log retention (days)"
              htmlFor="settings-retention"
              error={errors.log_retention_days}
              help="Older request logs are deleted automatically."
            >
              <Input
                id="settings-retention"
                inputMode="numeric"
                value={values.log_retention_days}
                onChange={(e) => set('log_retention_days', e.target.value)}
                aria-invalid={!!errors.log_retention_days}
              />
            </Field>
            <Field
              label="Default cooldown (seconds)"
              htmlFor="settings-cooldown"
              error={errors.default_cooldown_seconds}
              help="Used when a 429 response has no Retry-After header."
            >
              <Input
                id="settings-cooldown"
                inputMode="numeric"
                value={values.default_cooldown_seconds}
                onChange={(e) => set('default_cooldown_seconds', e.target.value)}
                aria-invalid={!!errors.default_cooldown_seconds}
              />
            </Field>
            <Field
              label="Disabled key recovery (minutes)"
              htmlFor="settings-recovery"
              error={errors.disabled_key_recovery_minutes}
              help="Keys disabled by a 401/402/403 response become active again after this long. 0 = never. Providers can override it."
            >
              <Input
                id="settings-recovery"
                inputMode="numeric"
                value={values.disabled_key_recovery_minutes}
                onChange={(e) => set('disabled_key_recovery_minutes', e.target.value)}
                aria-invalid={!!errors.disabled_key_recovery_minutes}
              />
            </Field>
          </div>

          <div className="grid gap-4 rounded-md border p-3 sm:grid-cols-[1fr_12rem] sm:items-start">
            <label className="flex items-start justify-between gap-4">
              <span className="grid gap-0.5">
                <span className="text-sm font-medium">Store request/response bodies</span>
                <span className="text-xs text-muted-foreground">
                  Useful for debugging. Bodies may contain sensitive data and use disk space.
                </span>
              </span>
              <Switch checked={values.log_bodies} onCheckedChange={(c) => set('log_bodies', c)} />
            </label>
            <Field
              label="Body size limit (KB)"
              htmlFor="settings-body-kb"
              error={errors.log_body_max_bytes}
              help="Longer bodies are truncated."
            >
              <Input
                id="settings-body-kb"
                inputMode="numeric"
                value={values.body_kb}
                onChange={(e) => set('body_kb', e.target.value)}
                disabled={!values.log_bodies}
                aria-invalid={!!errors.log_body_max_bytes}
              />
            </Field>
          </div>

          <Field
            label="Proxy test URL"
            htmlFor="settings-test-url"
            error={errors.proxy_test_url}
            help="Fetched through a proxy when you test it on the Proxies page."
          >
            <Input
              id="settings-test-url"
              value={values.proxy_test_url}
              onChange={(e) => set('proxy_test_url', e.target.value)}
              className="font-mono text-sm"
              aria-invalid={!!errors.proxy_test_url}
            />
          </Field>

          <div className="flex justify-end">
            <Button type="submit" disabled={save.isPending}>
              {save.isPending ? 'Saving…' : 'Save settings'}
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}

const emptyPasswords = { current: '', next: '', confirm: '' }

function PasswordCard() {
  const [values, set, setValues] = useFormState(emptyPasswords)
  const [errors, setErrors] = useState<Errors>({})
  const [pending, setPending] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    const errs: Errors = {}
    if (!values.current) errs.current_password = 'Enter your current password'
    if (values.next.length < 8) errs.new_password = 'Use at least 8 characters'
    if (values.confirm !== values.next) errs.confirm = 'Passwords do not match'
    setErrors(errs)
    if (Object.keys(errs).length > 0) return

    setPending(true)
    try {
      // A 401 here means "wrong current password", not an expired session.
      await api.post(
        '/api/settings/password',
        { current_password: values.current, new_password: values.next },
        { authRedirect: false },
      )
      setValues(emptyPasswords)
      toast.success('Password changed')
    } catch (err) {
      const next = toErrors(err)
      if (err instanceof ApiError && err.status === 401 && !next.current_password) {
        setErrors({ current_password: err.message || 'Current password is wrong' })
      } else {
        setErrors(next)
      }
    } finally {
      setPending(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Admin password</CardTitle>
        <CardDescription>Other signed-in sessions are signed out when the password changes.</CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={submit} className="grid gap-4" noValidate>
          <FormError message={errors._} />
          <div className="grid gap-4 sm:grid-cols-3">
            <Field label="Current password" htmlFor="password-current" error={errors.current_password}>
              <Input
                id="password-current"
                type="password"
                autoComplete="current-password"
                value={values.current}
                onChange={(e) => set('current', e.target.value)}
                aria-invalid={!!errors.current_password}
              />
            </Field>
            <Field label="New password" htmlFor="password-new" error={errors.new_password} help="At least 8 characters.">
              <Input
                id="password-new"
                type="password"
                autoComplete="new-password"
                value={values.next}
                onChange={(e) => set('next', e.target.value)}
                aria-invalid={!!errors.new_password}
              />
            </Field>
            <Field label="Confirm new password" htmlFor="password-confirm" error={errors.confirm}>
              <Input
                id="password-confirm"
                type="password"
                autoComplete="new-password"
                value={values.confirm}
                onChange={(e) => set('confirm', e.target.value)}
                aria-invalid={!!errors.confirm}
              />
            </Field>
          </div>
          <div className="flex justify-end">
            <Button type="submit" disabled={pending}>
              {pending ? 'Changing…' : 'Change password'}
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}
