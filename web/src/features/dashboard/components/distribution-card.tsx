import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '../../../components/ui/card'
import { FeedbackState, shortErrorReason } from '../../../components/feedback-state'
import { Skeleton } from '../../../components/ui/skeleton'
import { formatCount } from '../../../lib/format'
import { cn } from '../../../lib/utils'

export interface DistributionRow {
  key: string
  label: string
  count: number
}

interface DistributionCardProps {
  title: string
  description: string
  rows?: DistributionRow[]
  isLoading: boolean
  error?: unknown
  onRetry: () => void
  emptyText: string
  className?: string
}

/** Real aggregate counters rendered as proportional bars; no invented trend. */
export function DistributionCard({
  title,
  description,
  rows,
  isLoading,
  error,
  onRetry,
  emptyText,
  className,
}: DistributionCardProps) {
  const total = rows?.reduce((sum, row) => sum + row.count, 0)
  const max = rows?.reduce((peak, row) => Math.max(peak, row.count), 0) ?? 0

  return (
    <Card className={cn('h-full', className)}>
      <CardHeader className="flex-row items-start justify-between gap-3 space-y-0">
        <div className="min-w-0">
          <CardTitle>{title}</CardTitle>
          <CardDescription className="mt-1">{description}</CardDescription>
        </div>
        {rows ? (
          <span className="shrink-0 font-mono text-[11px] text-muted-foreground tabular-nums">
            合计 {formatCount(total)}
          </span>
        ) : null}
      </CardHeader>
      <CardContent className="pt-3">
        {isLoading ? (
          <div className="space-y-3 py-1">
            {Array.from({ length: 4 }).map((_, index) => (
              <div key={index} className="space-y-1.5">
                <Skeleton className="h-3 w-24" />
                <Skeleton className="h-2 w-full" />
              </div>
            ))}
          </div>
        ) : error !== undefined ? (
          <FeedbackState
            variant="error"
            title="指标不可获取"
            description={shortErrorReason(error)}
            onRetry={onRetry}
            compact
          />
        ) : rows ? (
          <div className="space-y-3">
            {rows.map((row) => {
              const width = max > 0 && row.count > 0 ? Math.max(2, Math.round((row.count / max) * 100)) : 0
              return (
                <div key={row.key} className="space-y-1.5">
                  <div className="flex items-baseline justify-between gap-3 text-xs">
                    <span className="min-w-0 truncate text-muted-foreground">{row.label}</span>
                    <span className="shrink-0 font-mono text-foreground tabular-nums">
                      {formatCount(row.count)}
                    </span>
                  </div>
                  <div className="h-1.5 w-full overflow-hidden rounded-full bg-secondary/80">
                    <div
                      className="h-full rounded-full bg-gradient-to-r from-primary/50 to-primary transition-[width] duration-300 ease-quiet"
                      style={{ width: `${width}%` }}
                      aria-hidden
                    />
                  </div>
                </div>
              )
            })}
            {total === 0 ? (
              <p className="pt-1 text-[11px] text-muted-foreground">{emptyText}</p>
            ) : null}
          </div>
        ) : null}
      </CardContent>
    </Card>
  )
}
