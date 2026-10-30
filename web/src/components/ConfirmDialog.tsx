import { useState, type ReactNode } from 'react'
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'

export interface ConfirmOptions {
  title: string
  /** Inline content only (rendered inside a paragraph). */
  description: ReactNode
  confirmLabel?: string
  /** Defaults to true (red confirm button). */
  destructive?: boolean
  /** Errors are reported by the caller (mutations toast them); the dialog then stays open. */
  onConfirm: () => Promise<unknown>
}

/** Confirmation for destructive actions. Open it by passing options; null closes it. */
export function ConfirmDialog({ options, onClose }: { options: ConfirmOptions | null; onClose: () => void }) {
  const [pending, setPending] = useState(false)
  // Keep showing the last options while the dialog animates closed.
  const [shown, setShown] = useState(options)
  if (options && options !== shown) setShown(options)

  async function confirm() {
    if (!shown) return
    setPending(true)
    try {
      await shown.onConfirm()
      onClose()
    } catch {
      // Already reported by the caller; keep the dialog open so the user can retry or cancel.
    } finally {
      setPending(false)
    }
  }

  return (
    <AlertDialog open={options !== null} onOpenChange={(open) => !open && !pending && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{shown?.title}</AlertDialogTitle>
          <AlertDialogDescription>{shown?.description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending}>Cancel</AlertDialogCancel>
          <Button variant={shown?.destructive === false ? 'default' : 'destructive'} onClick={confirm} disabled={pending}>
            {pending ? 'Working…' : (shown?.confirmLabel ?? 'Delete')}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
