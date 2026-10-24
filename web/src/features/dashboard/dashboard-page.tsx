import { useQueryClient } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { Button } from '../../components/ui/button'
import { PageHeader } from '../../components/page-header'
import { formatUpdatedAt } from '../../lib/format'
import { cn } from '../../lib/utils'
import { DECISION_LABELS, JOB_STATUS_LABELS, MEMORY_STATUS_LABELS } from '../../lib/api/contracts'
import { SYSTEM_QUERY_KEYS, useAccounts, useMetrics, useStatus } from '../system/queries'
import { ConnectionCard } from './components/connection-card'
import { DistributionCard, type DistributionRow } from './components/distribution-card'
import { KpiCards } from './components/kpi-cards'

export function DashboardPage() {
  const queryClient = useQueryClient()
  const metrics = useMetrics()
  const status = useStatus()
  const accounts = useAccounts()

  const refreshedAt = Math.max(
    metrics.dataUpdatedAt,
    status.dataUpdatedAt,
    accounts.dataUpdatedAt,
  )
  const isRefreshing = metrics.isFetching || status.isFetching || accounts.isFetching

  const refreshAll = () => {
    for (const queryKey of SYSTEM_QUERY_KEYS) {
      void queryClient.invalidateQueries({ queryKey })
    }
  }

  const jobRows: DistributionRow[] | undefined = metrics.data?.messageJobs.map((row) => ({
    key: row.key,
    label: JOB_STATUS_LABELS[row.key] ?? row.key,
    count: row.count,
  }))

  const memoryRows: DistributionRow[] | undefined = metrics.data?.memories.map((row) => ({
    key: row.key,
    label: MEMORY_STATUS_LABELS[row.key] ?? row.key,
    count: row.count,
  }))

  const decisionRows: DistributionRow[] | undefined = metrics.data?.intentDecisions24h.map(
    (item) => ({
      key: `${item.decision}:${item.reasonCode}`,
      label: `${DECISION_LABELS[item.decision] ?? item.decision} · ${item.reasonCode}`,
      count: item.count,
    }),
  )

  return (
    <div className="space-y-4">
      <PageHeader
        title="概览"
        description="机器人连接、消息与意图决策的实时聚合。所有数字均来自后端聚合计数，不使用推测值。"
        actions={
          <div className="flex items-center gap-2">
            <span className="hidden text-[11px] text-muted-foreground sm:inline">
              更新于 {formatUpdatedAt(refreshedAt)}
            </span>
            <Button variant="outline" size="sm" onClick={refreshAll} disabled={isRefreshing}>
              <RefreshCw className={cn('size-3.5', isRefreshing && 'animate-spin')} aria-hidden />
              刷新
            </Button>
          </div>
        }
      />

      <KpiCards metrics={metrics.data} isLoading={metrics.isLoading} isError={metrics.isError} />

      <div className="grid gap-3 lg:grid-cols-2">
        <ConnectionCard />
        <DistributionCard
          title="消息任务状态"
          description="message_jobs 累积计数，异常任务会在此处暴露"
          rows={jobRows}
          isLoading={metrics.isLoading}
          error={metrics.error ?? undefined}
          onRetry={() => void metrics.refetch()}
          emptyText="任务队列为空：尚未收到需要处理的消息。"
        />
      </div>

      <div className="grid gap-3 lg:grid-cols-2">
        <DistributionCard
          title="意图决策原因（24h）"
          description="Jev 判定分布，近 24 小时内按原因聚合"
          rows={decisionRows}
          isLoading={metrics.isLoading}
          error={metrics.error ?? undefined}
          onRetry={() => void metrics.refetch()}
          emptyText="近 24 小时没有意图决策记录。"
        />
        <DistributionCard
          title="长期记忆状态"
          description="按生命周期状态统计的全部记忆条目"
          rows={memoryRows}
          isLoading={metrics.isLoading}
          error={metrics.error ?? undefined}
          onRetry={() => void metrics.refetch()}
          emptyText="尚无长期记忆条目。"
        />
      </div>
    </div>
  )
}
