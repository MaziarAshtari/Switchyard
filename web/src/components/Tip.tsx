import type { ComponentProps, ReactElement, ReactNode } from 'react'
import { Loader2, type LucideIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'

/** Tooltip around a single element; renders the element alone when there is no content. */
export function Tip({ content, children }: { content: ReactNode; children: ReactElement }) {
  if (!content) return children
  return (
    <Tooltip>
      <TooltipTrigger asChild>{children}</TooltipTrigger>
      <TooltipContent className="max-w-sm break-words">{content}</TooltipContent>
    </Tooltip>
  )
}

interface IconButtonProps extends Omit<ComponentProps<typeof Button>, 'children' | 'size'> {
  label: string
  icon: LucideIcon
  pending?: boolean
}

/** Small icon-only button with a tooltip and an accessible label. */
export function IconButton({ label, icon: Icon, pending, disabled, variant = 'ghost', className, ...props }: IconButtonProps) {
  return (
    <Tip content={label}>
      <Button
        type="button"
        variant={variant}
        size="icon-sm"
        aria-label={label}
        disabled={disabled || pending}
        className={cn('text-muted-foreground hover:text-foreground', className)}
        {...props}
      >
        {pending ? <Loader2 className="animate-spin" /> : <Icon />}
      </Button>
    </Tip>
  )
}
