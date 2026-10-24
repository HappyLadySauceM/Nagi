import { BrainCircuit, Database, MessagesSquare, Workflow, type LucideIcon } from 'lucide-react'
import { Card, CardContent } from '../../../components/ui/card'
import { Skeleton } from '../../../components/ui/skeleton'
import { NumberTicker } from '../../../components/magicui/number-ticker'
import { AnimatedGroup } from '../../../components/motion-primitives/animated-group'
import { UNAVAILABLE_TEXT, formatCount } from '../../../lib/format'
import { sumCounts, summarizeMemories } from '../../../lib/api/adapters/metrics'
import type { MetricsSnapshot } from '../../../lib/api/contracts'

interface KpiCardProps {
  label: string
  hint: string
  value?: number
  isLoading: boolean
  icon: LucideIcon
}

function KpiCard({ label, hint, value, isLoading, icon: Icon }: KpiCardProps) {
  return (
    <Card className="group overflow-hidden transition-[border-color,transform] duration-150 hover:-translate-y-px hover:border-primary/30">
      <CardContent className="flex items-start justify-between gap-3 p-5">
        <div className="min-w-0">
          <p className="text-xs text-muted-foreground">{label}</p>
          <div className="mt-2.5 flex h-8 items-baseline text-[30px] font-semibold leading-none tracking-tight text-foreground">
            {isLoading ? (
              <Skeleton className="h-7 w-20" />
            ) : value === undefined ? (
              <span className="text-base font-medium text-muted-foreground">
                {UNAVAILABLE_TEXT}
              </span>
            ) : (
              <NumberTicker value={value} />
            )}
          </div>
          <p className="mt-2 truncate text-[11px] text-muted-foreground">{hint}</p>
        </div>
        <span className="flex size-8 shrink-0 items-center justify-center rounded-lg border border-border bg-secondary/50 text-muted-foreground transition-colors group-hover:border-primary/30 group-hover:text-primary">
          <Icon className="size-4" aria-hidden />
        </span>
      </CardContent>
    </Card>
  )
}

interface KpiCardsProps {
  metrics?: MetricsSnapshot
  isLoading: boolean
  isError: boolean
}

export function KpiCards({ metrics, isLoading, isError }: KpiCardsProps) {
  const decisionsTotal = metrics
    ? metrics.intentDecisions24h.reduce((total, item) => total + item.count, 0)
    : undefined
  const jobsTotal = sumCounts(metrics?.messageJobs)
  const memories = summarizeMemories(metrics?.memories)
  const pendingJobs = metrics?.messageJobs.find((row) => row.key === 'pending')?.count
  const activeMemories = metrics?.memories.find((row) => row.key === 'active')?.count

  const memoriesHint =
    memories === undefined
      ? '按状态统计的条目'
      : memories.excludedDeleted > 0
        ? `已启用 ${formatCount(activeMemories)} 条 · 不含已删除 ${formatCount(memories.excludedDeleted)} 条`
        : `已启用 ${formatCount(activeMemories)} 条`

  const resolve = (value: number | undefined) => (isError ? undefined : value)

  return (
    <AnimatedGroup
      className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4"
      childClassName="min-w-0"
    >
      <KpiCard
        label="近 24 小时消息"
        hint="messages_24h 聚合计数"
        value={resolve(metrics?.messages24h)}
        isLoading={isLoading}
        icon={MessagesSquare}
      />
      <KpiCard
        label="意图决策（24h）"
        hint={
          decisionsTotal === undefined
            ? '决策记录不可获取'
            : `共 ${formatCount(decisionsTotal)} 次判断`
        }
        value={resolve(decisionsTotal)}
        isLoading={isLoading}
        icon={BrainCircuit}
      />
      <KpiCard
        label="消息任务"
        hint={pendingJobs === undefined ? '累计任务状态计数' : `等待中 ${formatCount(pendingJobs)} 个`}
        value={resolve(jobsTotal)}
        isLoading={isLoading}
        icon={Workflow}
      />
      <KpiCard
        label="长期记忆"
        hint={memoriesHint}
        value={resolve(memories?.total)}
        isLoading={isLoading}
        icon={Database}
      />
    </AnimatedGroup>
  )
}
