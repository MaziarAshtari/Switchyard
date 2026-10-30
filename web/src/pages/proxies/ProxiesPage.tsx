import { useState } from 'react'
import { Link } from 'react-router'
import { Network, Pencil, Plus, Trash2, Upload, Users, Zap } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ProxyTypeBadge } from '@/components/Badges'
import { EmptyState, ErrorState, Loading, PageHeader } from '@/components/common'
import { ConfirmDialog, type ConfirmOptions } from '@/components/ConfirmDialog'
import { IconButton, Tip } from '@/components/Tip'
import { api } from '@/lib/api'
import { latency, plural } from '@/lib/format'
import { notifyTest, useAction, useProxies, useSettings } from '@/lib/queries'
import type { Proxy, ProxyInput, TestResult } from '@/lib/types'
import { AssignDialog } from './AssignDialog'
import { ImportDialog } from './ImportDialog'
import { ProxyDialog } from './ProxyDialog'

type ProxyTestResult = TestResult & { proxy_id: number }

// Test results are not stored on the server; keep them for this browser session.
const sessionTests = new Map<number, TestResult>()

const plProxy = (n: number) => plural(n, 'proxy', 'proxies')

export default function ProxiesPage() {
  const proxies = useProxies()
  const settings = useSettings()
  const [tests, setTests] = useState(() => new Map(sessionTests))
  const [selected, setSelected] = useState<Set<number>>(() => new Set())
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<Proxy | undefined>()
  const [importOpen, setImportOpen] = useState(false)
  const [assignOpen, setAssignOpen] = useState(false)
  const [confirm, setConfirm] = useState<ConfirmOptions | null>(null)

  function recordTests(results: ProxyTestResult[]) {
    for (const r of results) sessionTests.set(r.proxy_id, r)
    setTests(new Map(sessionTests))
  }

  const testAll = useAction(() => api.post<{ results: ProxyTestResult[] }>('/api/proxies/test', { ids: [] }), {
    success: ({ results }) => {
      recordTests(results)
      const failed = results.filter((r) => !r.ok).length
      if (failed) toast.warning(`Tested ${plProxy(results.length)}: ${results.length - failed} OK, ${failed} failed`)
      else toast.success(`Tested ${plProxy(results.length)}: all OK`)
    },
  })
  const remove = useAction((p: Proxy) => api.del(`/api/proxies/${p.id}`), { success: 'Proxy deleted' })

  const list = proxies.data ?? []
  const selectedProxies = list.filter((p) => selected.has(p.id))
  const allChecked =
    list.length > 0 && selectedProxies.length === list.length ? true : selectedProxies.length > 0 ? 'indeterminate' : false

  function select(id: number, checked: boolean) {
    setSelected((current) => {
      const next = new Set(current)
      if (checked) next.add(id)
      else next.delete(id)
      return next
    })
  }

  function openDialog(p?: Proxy) {
    setEditing(p)
    setDialogOpen(true)
  }

  function confirmDelete(p: Proxy) {
    const effects: string[] = []
    if (p.used_by_keys) effects.push(`${plural(p.used_by_keys, 'key')} will switch to “Inherit provider default”`)
    if (p.used_by_providers) {
      effects.push(`${plural(p.used_by_providers, 'provider')} will lose this default proxy (direct connection)`)
    }
    setConfirm({
      title: `Delete proxy ${p.name}?`,
      description: effects.length ? `${effects.join(' and ')}.` : 'No keys or providers use this proxy.',
      onConfirm: () => remove.mutateAsync(p),
    })
  }

  const addButtons = (
    <>
      <Button variant="outline" onClick={() => setImportOpen(true)}>
        <Upload />
        Bulk import
      </Button>
      <Button onClick={() => openDialog()}>
        <Plus />
        Add proxy
      </Button>
    </>
  )

  return (
    <>
      <PageHeader
        title="Proxies"
        description="Outgoing HTTP, HTTPS or SOCKS5 proxies for upstream requests."
        actions={
          list.length > 0 && (
            <>
              <Button variant="outline" onClick={() => setAssignOpen(true)} disabled={selectedProxies.length === 0}>
                <Users />
                Assign to keys
              </Button>
              <Button variant="outline" onClick={() => testAll.mutate()} disabled={testAll.isPending}>
                <Zap />
                {testAll.isPending ? 'Testing…' : 'Test all'}
              </Button>
              {addButtons}
            </>
          )
        }
      >
        {settings.data && (
          <p className="text-xs text-muted-foreground">
            Tests fetch <code className="font-mono break-all">{settings.data.proxy_test_url}</code> (
            <Link to="/settings" className="underline hover:text-foreground">
              change in Settings
            </Link>
            ).
          </p>
        )}
      </PageHeader>

      {proxies.isPending ? (
        <Loading />
      ) : proxies.isError ? (
        <ErrorState error={proxies.error} onRetry={proxies.refetch} />
      ) : list.length === 0 ? (
        <EmptyState
          icon={Network}
          title="No proxies yet"
          hint="Add proxies one by one or paste a whole list. Then use them as a provider default or assign them to keys."
          action={<div className="flex gap-2">{addButtons}</div>}
        />
      ) : (
        <Card className="overflow-hidden py-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-10 pl-4">
                  <Checkbox
                    checked={allChecked}
                    onCheckedChange={(c) => setSelected(c === true ? new Set(list.map((p) => p.id)) : new Set())}
                    aria-label="Select all proxies"
                  />
                </TableHead>
                <TableHead>Name</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Address</TableHead>
                <TableHead>Auth</TableHead>
                <TableHead>Used by</TableHead>
                <TableHead>Last test</TableHead>
                <TableHead>Enabled</TableHead>
                <TableHead className="pr-4 text-right">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.map((p) => (
                <ProxyRow
                  key={p.id}
                  proxy={p}
                  selected={selected.has(p.id)}
                  onSelect={(checked) => select(p.id, checked)}
                  lastTest={tests.get(p.id)}
                  onTested={(r) => recordTests([{ ...r, proxy_id: p.id }])}
                  onEdit={() => openDialog(p)}
                  onDelete={() => confirmDelete(p)}
                />
              ))}
            </TableBody>
          </Table>
        </Card>
      )}

      <ProxyDialog open={dialogOpen} onOpenChange={setDialogOpen} proxy={editing} />
      <ImportDialog open={importOpen} onOpenChange={setImportOpen} />
      <AssignDialog open={assignOpen} onOpenChange={setAssignOpen} proxies={selectedProxies} />
      <ConfirmDialog options={confirm} onClose={() => setConfirm(null)} />
    </>
  )
}

interface ProxyRowProps {
  proxy: Proxy
  selected: boolean
  onSelect: (checked: boolean) => void
  lastTest?: TestResult
  onTested: (result: TestResult) => void
  onEdit: () => void
  onDelete: () => void
}

function ProxyRow({ proxy: p, selected, onSelect, lastTest, onTested, onEdit, onDelete }: ProxyRowProps) {
  const toggle = useAction(
    () => {
      const input: ProxyInput = {
        name: p.name,
        type: p.type,
        host: p.host,
        port: p.port,
        username: p.username,
        enabled: !p.enabled,
      }
      return api.put<Proxy>(`/api/proxies/${p.id}`, input)
    },
    { success: (r) => `${r.name} ${r.enabled ? 'enabled' : 'disabled'}` },
  )
  const test = useAction(() => api.post<TestResult>(`/api/proxies/${p.id}/test`), {
    success: (r) => {
      onTested(r)
      notifyTest('Proxy', r)
    },
  })
  const usedBy = [
    p.used_by_keys ? plural(p.used_by_keys, 'key') : '',
    p.used_by_providers ? plural(p.used_by_providers, 'provider') : '',
  ].filter(Boolean)

  return (
    <TableRow data-state={selected ? 'selected' : undefined}>
      <TableCell className="pl-4">
        <Checkbox checked={selected} onCheckedChange={(c) => onSelect(c === true)} aria-label={`Select ${p.name}`} />
      </TableCell>
      <TableCell className="font-medium">{p.name}</TableCell>
      <TableCell>
        <ProxyTypeBadge type={p.type} />
      </TableCell>
      <TableCell>
        <code className="font-mono text-xs">
          {p.host}:{p.port}
        </code>
      </TableCell>
      <TableCell>{p.username || <span className="text-muted-foreground">—</span>}</TableCell>
      <TableCell>{usedBy.length ? usedBy.join(' · ') : <span className="text-muted-foreground">—</span>}</TableCell>
      <TableCell>
        {!lastTest ? (
          <span className="text-muted-foreground">—</span>
        ) : lastTest.ok ? (
          <span className="text-emerald-600 dark:text-emerald-400">OK · {latency(lastTest.latency_ms)}</span>
        ) : (
          <Tip content={lastTest.error || `HTTP ${lastTest.status_code}`}>
            <span tabIndex={0} className="cursor-help text-red-600 underline decoration-dotted dark:text-red-400">
              Failed
            </span>
          </Tip>
        )}
      </TableCell>
      <TableCell>
        <Switch
          checked={p.enabled}
          disabled={toggle.isPending}
          onCheckedChange={() => toggle.mutate()}
          aria-label={`${p.enabled ? 'Disable' : 'Enable'} ${p.name}`}
        />
      </TableCell>
      <TableCell className="pr-4 text-right">
        <IconButton label="Test proxy" icon={Zap} pending={test.isPending} onClick={() => test.mutate()} />
        <IconButton label="Edit" icon={Pencil} onClick={onEdit} />
        <IconButton label="Delete" icon={Trash2} onClick={onDelete} />
      </TableCell>
    </TableRow>
  )
}
