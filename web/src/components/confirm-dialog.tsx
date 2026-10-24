import type { ReactNode } from 'react'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from './ui/alert-dialog'
import { cn } from '../lib/utils'

interface ConfirmDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description?: ReactNode
  /** Extra bullet list, e.g. the capabilities that will be granted. */
  details?: string[]
  confirmLabel?: string
  cancelLabel?: string
  destructive?: boolean
  pending?: boolean
  onConfirm: () => void
}

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  details,
  confirmLabel = '确认',
  cancelLabel = '取消',
  destructive = false,
  pending = false,
  onConfirm,
}: ConfirmDialogProps) {
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          {description ? <AlertDialogDescription>{description}</AlertDialogDescription> : null}
        </AlertDialogHeader>
        {details && details.length > 0 ? (
          <ul className="mt-3 space-y-1.5 rounded-lg border border-warning/25 bg-warning/5 p-3">
            {details.map((item) => (
              <li key={item} className="flex gap-2 text-[11px] leading-relaxed text-warning">
                <span aria-hidden>•</span>
                <span className="text-foreground/90">{item}</span>
              </li>
            ))}
          </ul>
        ) : null}
        <AlertDialogFooter>
          <AlertDialogCancel asChild disabled={pending}>
            <button
              type="button"
              className="inline-flex h-8 cursor-pointer items-center justify-center rounded-lg border border-border px-3 text-xs text-foreground transition-colors hover:bg-secondary/60 disabled:cursor-not-allowed disabled:opacity-50"
            >
              {cancelLabel}
            </button>
          </AlertDialogCancel>
          <AlertDialogAction asChild disabled={pending}>
            <button
              type="button"
              onClick={(event) => {
                event.preventDefault()
                onConfirm()
              }}
              disabled={pending}
              className={cn(
                'inline-flex h-8 cursor-pointer items-center justify-center rounded-lg px-3 text-xs font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-60',
                destructive
                  ? 'bg-destructive text-destructive-foreground hover:bg-destructive/90'
                  : 'bg-primary text-primary-foreground hover:bg-primary/90',
              )}
            >
              {pending ? '处理中…' : confirmLabel}
            </button>
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
