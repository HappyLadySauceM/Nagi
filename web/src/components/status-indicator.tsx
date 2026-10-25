import { AlertTriangle, CircleHelp, CircleCheck, CircleX } from 'lucide-react'
import { cn } from '../lib/utils'

export type StatusTone = 'ok' | 'warn' | 'error' | 'unknown'

const TONE_STYLES: Record<StatusTone, { dot: string; text: string; border: string }> = {
  ok: { dot: 'bg-success', text: 'text-success', border: 'border-success/30' },
  warn: { dot: 'bg-warning', text: 'text-warning', border: 'border-warning/30' },
  error: { dot: 'bg-destructive', text: 'text-destructive', border: 'border-destructive/30' },
  unknown: {
    dot: 'bg-muted-foreground',
    text: 'text-muted-foreground',
    border: 'border-border',
  },
}

const TONE_ICONS = {
  ok: CircleCheck,
  warn: AlertTriangle,
  error: CircleX,
  unknown: CircleHelp,
} as const

interface StatusIndicatorProps {
  tone: StatusTone
  label: string
  description?: string
  /** Announce tone changes politely; numbers are never announced. */
  live?: boolean
  className?: string
}

/** Full status row: icon, label and an optional explanation. */
export function StatusIndicator({
  tone,
  label,
  description,
  live = false,
  className,
}: StatusIndicatorProps) {
  const styles = TONE_STYLES[tone]
  const Icon = TONE_ICONS[tone]

  return (
    <div
      className={cn('flex items-start gap-2', className)}
      role="status"
      aria-live={live ? 'polite' : 'off'}
    >
      <Icon className={cn('mt-0.5 size-4 shrink-0', styles.text)} aria-hidden />
      <div className="min-w-0">
        <p className={cn('text-sm font-medium', styles.text)}>{label}</p>
        {description ? (
          <p className="mt-0.5 text-xs leading-relaxed text-muted-foreground">{description}</p>
        ) : null}
      </div>
    </div>
  )
}

interface StatusPillProps {
  tone: StatusTone
  label: string
  description?: string
  live?: boolean
  className?: string
}

/** Compact chip for the top bar: the dot plus a short label, text not colour alone. */
export function StatusPill({ tone, label, description, live, className }: StatusPillProps) {
  const styles = TONE_STYLES[tone]

  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 rounded-full border bg-surface/60 px-2.5 py-1 text-meta font-medium',
        styles.border,
        styles.text,
        className,
      )}
      role="status"
      aria-live={live ? 'polite' : 'off'}
      title={description}
    >
      <span className={cn('size-1.5 rounded-full', styles.dot)} aria-hidden />
      {label}
    </span>
  )
}
