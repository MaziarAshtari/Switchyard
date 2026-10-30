import { useState, type ReactNode } from 'react'
import { Link } from 'react-router'
import { AlertCircle, Check, ChevronLeft, Copy, type LucideIcon } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { IconButton } from '@/components/Tip'
import { errorMessage } from '@/lib/api'
import { fullTime, relativeTime } from '@/lib/format'
import { cn, copyText } from '@/lib/utils'

// Small building blocks shared by all pages.

export function PageHeader({
  title,
  description,
  actions,
  back,
  children,
}: {
  title: ReactNode
  description?: ReactNode
  actions?: ReactNode
  back?: { to: string; label: string }
  children?: ReactNode
}) {
  return (
    <div className="mb-6 grid gap-1">
      {back && (
        <Link
          to={back.to}
          className="mb-1 inline-flex w-fit items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
        >
          <ChevronLeft className="size-4" />
          {back.label}
        </Link>
      )}
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="flex flex-wrap items-center gap-2 text-2xl font-semibold tracking-tight">{title}</h1>
          {description && <p className="mt-1 text-sm text-muted-foreground">{description}</p>}
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>
      {children}
    </div>
  )
}

export function EmptyState({
  icon: Icon,
  title,
  hint,
  action,
}: {
  icon?: LucideIcon
  title: string
  hint?: ReactNode
  action?: ReactNode
}) {
  return (
    <div className="flex flex-col items-center gap-2 rounded-lg border border-dashed px-6 py-12 text-center">
      {Icon && (
        <div className="mb-1 rounded-full bg-muted p-3">
          <Icon className="size-5 text-muted-foreground" />
        </div>
      )}
      <h3 className="font-medium">{title}</h3>
      {hint && <p className="max-w-md text-sm text-muted-foreground">{hint}</p>}
      {action && <div className="mt-2">{action}</div>}
    </div>
  )
}

export function Loading({ rows = 3 }: { rows?: number }) {
  return (
    <div className="grid gap-2" aria-busy="true" aria-label="Loading">
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="h-10 w-full" />
      ))}
    </div>
  )
}

export function ErrorState({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  return (
    <div role="alert" className="flex flex-col items-start gap-2 rounded-lg border border-destructive/30 bg-destructive/5 p-4 text-sm">
      <div className="flex items-center gap-2 font-medium text-destructive">
        <AlertCircle className="size-4" />
        Could not load data
      </div>
      <p className="text-muted-foreground">{errorMessage(error)}</p>
      {onRetry && (
        <Button size="sm" variant="outline" onClick={() => onRetry()}>
          Try again
        </Button>
      )}
    </div>
  )
}

/** Label + input + optional help text + validation message. */
export function Field({
  label,
  htmlFor,
  help,
  error,
  className,
  children,
}: {
  label?: ReactNode
  htmlFor?: string
  help?: ReactNode
  error?: string
  className?: string
  children: ReactNode
}) {
  return (
    <div className={cn('grid content-start gap-1.5', className)}>
      {label && <Label htmlFor={htmlFor}>{label}</Label>}
      {children}
      {help && <p className="text-xs text-muted-foreground">{help}</p>}
      {error && <p className="text-xs font-medium text-destructive">{error}</p>}
    </div>
  )
}

/** General (non-field) error of a form or dialog. */
export function FormError({ message }: { message?: string }) {
  if (!message) return null
  return (
    <div role="alert" className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
      {message}
    </div>
  )
}

/** Relative time with the exact local time as tooltip. */
export function RelativeTime({ value }: { value: string | null | undefined }) {
  if (!value) return <span className="text-muted-foreground">—</span>
  return (
    <time dateTime={value} title={fullTime(value)}>
      {relativeTime(value)}
    </time>
  )
}

export function CopyButton({ value, label = 'Copy', className }: { value: string; label?: string; className?: string }) {
  const [copied, setCopied] = useState(false)
  async function copy() {
    try {
      await copyText(value)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      toast.error('Could not copy to the clipboard. Select the text and copy it manually.')
    }
  }
  return <IconButton label={copied ? 'Copied' : label} icon={copied ? Check : Copy} onClick={copy} className={className} />
}

/** Monospace value with a copy button, e.g. the client base URL. */
export function CopyField({ value, label }: { value: string; label?: string }) {
  return (
    <span className="inline-flex max-w-full items-center gap-1 rounded-md border bg-muted/40 py-0.5 pr-0.5 pl-2.5">
      <code className="truncate font-mono text-xs">{value}</code>
      <CopyButton value={value} label={label} className="size-7" />
    </span>
  )
}
