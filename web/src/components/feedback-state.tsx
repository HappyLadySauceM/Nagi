import {
  AlertTriangle,
  Inbox,
  Loader2,
  Lock,
  PlugZap,
  RefreshCw,
  ServerCrash,
  ShieldAlert,
} from 'lucide-react'
import { describeError, type FeedbackVariant } from '../lib/errors'
import { Button } from './ui/button'
import { Skeleton } from './ui/skeleton'
import { cn } from '../lib/utils'

const VARIANT_ICONS = {
  error: AlertTriangle,
  unauthorized: Lock,
  unconfigured: ShieldAlert,
  offline: PlugZap,
  empty: Inbox,
  loading: Loader2,
} as const

interface FeedbackStateProps {
  variant: FeedbackVariant
  title?: string
  description?: string
  error?: unknown
  onRetry?: () => void
  compact?: boolean
  className?: string
}

export function FeedbackState({
  variant,
  title,
  description,
  error,
  onRetry,
  compact = false,
  className,
}: FeedbackStateProps) {
  if (variant === 'loading') {
    return (
      <div className={cn('space-y-2', compact ? 'py-2' : 'py-6', className)} aria-busy="true">
        <Skeleton className="h-4 w-1/3" />
        <Skeleton className="h-4 w-2/3" />
        <Skeleton className="h-4 w-1/2" />
      </div>
    )
  }

  const presentation = error !== undefined ? describeError(error) : undefined
  const Icon = VARIANT_ICONS[variant]
  const resolvedTitle =
    title ??
    presentation?.title ??
    (variant === 'empty' ? '暂无数据' : '加载失败')
  const resolvedDescription = description ?? presentation?.description
  const showRetry =
    onRetry !== undefined && variant !== 'empty' && presentation?.canRetry !== false

  return (
    <div
      className={cn(
        'flex flex-col items-center justify-center gap-2 text-center',
        compact ? 'py-6' : 'py-12',
        className,
      )}
      role={variant === 'empty' ? 'status' : 'alert'}
    >
      <span
        className={cn(
          'flex size-9 items-center justify-center rounded-full border',
          variant === 'unauthorized' || variant === 'unconfigured'
            ? 'border-warning/30 bg-warning/10 text-warning'
            : variant === 'empty'
              ? 'border-border bg-secondary/60 text-muted-foreground'
              : 'border-destructive/30 bg-destructive/10 text-destructive',
        )}
      >
        {variant === 'empty' ? (
          <Icon className="size-4" aria-hidden />
        ) : variant === 'offline' ? (
          <ServerCrash className="size-4" aria-hidden />
        ) : (
          <Icon className="size-4" aria-hidden />
        )}
      </span>
      <p className="text-sm font-medium text-foreground">{resolvedTitle}</p>
      {resolvedDescription ? (
        <p className="max-w-md text-xs leading-relaxed text-muted-foreground">
          {resolvedDescription}
        </p>
      ) : null}
      {showRetry ? (
        <Button variant="outline" size="sm" onClick={onRetry} className="mt-1">
          <RefreshCw aria-hidden />
          重试
        </Button>
      ) : null}
    </div>
  )
}
