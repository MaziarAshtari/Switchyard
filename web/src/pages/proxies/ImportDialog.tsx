import { useState, type FormEvent } from 'react'
import { useMutation } from '@tanstack/react-query'
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
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { ToneBadge } from '@/components/Badges'
import { Field, FormError } from '@/components/common'
import { api, describeError } from '@/lib/api'
import { plural } from '@/lib/format'
import { proxyTypes } from '@/lib/labels'
import { useAction } from '@/lib/queries'
import type { ImportLine, ImportResult, ProxyType } from '@/lib/types'

const placeholder = [
  'socks5://user:pass@1.2.3.4:1080',
  'http://1.2.3.4:8080',
  '1.2.3.4:8080',
  '1.2.3.4:8080:user:pass',
].join('\n')

const plProxy = (n: number) => plural(n, 'proxy', 'proxies')

export function ImportDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90svh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Bulk import proxies</DialogTitle>
          <DialogDescription>Paste one proxy per line, preview the result, then import. Duplicates are skipped.</DialogDescription>
        </DialogHeader>
        <ImportForm onDone={() => onOpenChange(false)} />
      </DialogContent>
    </Dialog>
  )
}

function ImportForm({ onDone }: { onDone: () => void }) {
  const [defaultType, setDefaultType] = useState<ProxyType>('http')
  const [text, setText] = useState('')
  const [preview, setPreview] = useState<ImportResult | null>(null)
  const [error, setError] = useState('')

  const body = (dryRun: boolean) => ({ text, default_type: defaultType, dry_run: dryRun })
  const check = useMutation({ mutationFn: () => api.post<ImportResult>('/api/proxies/import', body(true)) })
  const save = useAction(() => api.post<ImportResult>('/api/proxies/import', body(false)), {
    success: (r) => `Imported ${plProxy(r.created)}`,
    toastErrors: false,
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    setError('')
    if (!preview) {
      if (!text.trim()) {
        setError('Paste at least one proxy.')
        return
      }
      check.mutate(undefined, { onSuccess: setPreview, onError: (err) => setError(describeError(err)) })
      return
    }
    save.mutate(undefined, { onSuccess: onDone, onError: (err) => setError(describeError(err)) })
  }

  // Any edit makes the preview outdated.
  function edit(update: () => void) {
    update()
    setPreview(null)
  }

  const busy = check.isPending || save.isPending

  return (
    <form onSubmit={submit} className="grid gap-4" noValidate>
      <FormError message={error} />
      <Field label="Scheme for lines without one" htmlFor="import-scheme" className="sm:w-64">
        <Select value={defaultType} onValueChange={(t) => edit(() => setDefaultType(t as ProxyType))}>
          <SelectTrigger id="import-scheme" className="w-full">
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
      <Field label="Proxies" htmlFor="import-text" help="Accepted: scheme://user:pass@host:port, scheme://host:port, host:port, host:port:user:pass">
        <Textarea
          id="import-text"
          rows={7}
          value={text}
          onChange={(e) => {
            const value = e.target.value
            edit(() => setText(value))
          }}
          readOnly={busy}
          placeholder={placeholder}
          className="max-h-72 font-mono text-xs"
          spellCheck={false}
          autoFocus
        />
      </Field>

      {preview && <Preview result={preview} />}

      <DialogFooter>
        <DialogClose asChild>
          <Button type="button" variant="outline">
            Cancel
          </Button>
        </DialogClose>
        {preview ? (
          <Button type="submit" disabled={busy || preview.valid === 0}>
            {save.isPending ? 'Importing…' : `Import ${plProxy(preview.valid)}`}
          </Button>
        ) : (
          <Button type="submit" disabled={busy || !text.trim()}>
            {check.isPending ? 'Checking…' : 'Preview'}
          </Button>
        )}
      </DialogFooter>
    </form>
  )
}

function Preview({ result }: { result: ImportResult }) {
  return (
    <div className="grid gap-2">
      <p className="text-sm">
        <span className="font-medium text-emerald-600 dark:text-emerald-400">{result.valid} valid</span>
        {' · '}
        <span className="text-muted-foreground">{plural(result.duplicates, 'duplicate')}</span>
        {' · '}
        <span className={result.invalid ? 'text-destructive' : 'text-muted-foreground'}>{result.invalid} invalid</span>
      </p>
      <div className="max-h-64 overflow-y-auto rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-10 pl-3">#</TableHead>
              <TableHead>Input</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Parsed</TableHead>
              <TableHead className="pr-3">Username</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {result.lines.map((line) => (
              <TableRow key={line.line}>
                <TableCell className="pl-3 text-muted-foreground tabular-nums">{line.line}</TableCell>
                <TableCell className="max-w-[14rem] truncate font-mono text-xs" title={line.input}>
                  {line.input}
                </TableCell>
                <TableCell className="whitespace-normal">
                  <LineStatus line={line} />
                </TableCell>
                <TableCell className="font-mono text-xs">
                  {line.status === 'invalid' ? '—' : `${line.type}://${line.host}:${line.port}`}
                </TableCell>
                <TableCell className="pr-3">{line.username || '—'}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}

function LineStatus({ line }: { line: ImportLine }) {
  if (line.status === 'valid') return <ToneBadge tone="green">Valid</ToneBadge>
  if (line.status === 'duplicate') return <ToneBadge tone="grey">Duplicate</ToneBadge>
  return (
    <span className="flex flex-wrap items-center gap-1.5">
      <ToneBadge tone="red">Invalid</ToneBadge>
      <span className="text-xs text-destructive">{line.error}</span>
    </span>
  )
}
