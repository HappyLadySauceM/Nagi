import { useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { Badge } from '../../components/ui/badge'
import { Button } from '../../components/ui/button'
import { Card } from '../../components/ui/card'
import { FeedbackState } from '../../components/feedback-state'
import { PageHeader } from '../../components/page-header'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '../../components/ui/sheet'
import { Skeleton } from '../../components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '../../components/ui/table'
import {
  DECISION_KIND_LABELS,
  DECISION_PHASE_LABELS,
  type DecisionDetailEntry,
  type DecisionEntry,
} from '../../lib/api/contracts'
import { formatDateTime, formatRelativeTime } from '../../lib/format'
import { useAccountContext } from '../accounts/account-context'
import { useDecision, useDecisions } from './queries'

const KIND_VARIANTS: Record<DecisionEntry['decision'], 'success' | 'secondary' | 'warning'> = {
  reply: 'success',
  silent: 'secondary',
  defer: 'warning',
}

export function IntentPage() {
  const { selectedBotId, dbConfigured } = useAccountContext()
  const [selected, setSelected] = useState<number | null>(null)
  const decisions = useDecisions(selectedBotId, '')
  const detail = useDecision(selected)

  return (
    <div className="space-y-4">
      <PageHeader
        title="决策记录"
        description="每条消息为什么回复、为什么沉默，都能在这里逐条还原；数据来自意图流水线落库的决策与步骤。"
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={() => void decisions.refetch()}
            disabled={decisions.isFetching || !dbConfigured}
          >
            <RefreshCw className={decisions.isFetching ? 'animate-spin' : undefined} />
            刷新
          </Button>
        }
      />

      {!dbConfigured ? (
        <FeedbackState
          variant="unconfigured"
          title="未配置数据库"
          description="决策记录依赖数据库；配置 database.dsn 后自动开启。"
        />
      ) : decisions.isError ? (
        <FeedbackState
          variant="error"
          title="读取决策记录失败"
          error={decisions.error}
          onRetry={() => void decisions.refetch()}
        />
      ) : decisions.isLoading ? (
        <Skeleton className="h-64 w-full" />
      ) : decisions.data && decisions.data.items.length === 0 ? (
        <FeedbackState
          variant="empty"
          title="还没有决策记录"
          description="机器人收到消息并完成一次意图判定后，记录会出现在这里。"
        />
      ) : (
        <Card className="overflow-hidden">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>时间</TableHead>
                <TableHead>决策</TableHead>
                <TableHead>原因码</TableHead>
                <TableHead>模型</TableHead>
                <TableHead className="text-right">耗时</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {decisions.data?.items.map((entry) => (
                <TableRow
                  key={entry.id}
                  className="cursor-pointer"
                  onClick={() => setSelected(entry.id)}
                >
                  <TableCell className="whitespace-nowrap text-meta text-muted-foreground">
                    {entry.createdAt ? formatRelativeTime(entry.createdAt) : '—'}
                  </TableCell>
                  <TableCell>
                    <Badge variant={KIND_VARIANTS[entry.decision]}>
                      {DECISION_KIND_LABELS[entry.decision]}
                    </Badge>
                  </TableCell>
                  <TableCell className="font-mono text-meta">{entry.reasonCode || '—'}</TableCell>
                  <TableCell className="text-meta text-muted-foreground">
                    {entry.model || '—'}
                  </TableCell>
                  <TableCell className="text-right text-meta text-muted-foreground">
                    {entry.latencyMs} ms
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}

      <Sheet open={selected !== null} onOpenChange={(open) => !open && setSelected(null)}>
        <SheetContent className="w-full sm:max-w-lg">
          <SheetTitle>决策详情</SheetTitle>
          <SheetDescription>
            按判定顺序列出策略、规则、语义与结论；语义步骤只在调用 Jev 时出现。
          </SheetDescription>
          {detail.isLoading ? (
            <Skeleton className="mt-4 h-40 w-full" />
          ) : detail.isError ? (
            <FeedbackState
              variant="error"
              title="读取决策详情失败"
              error={detail.error}
              onRetry={() => void detail.refetch()}
            />
          ) : detail.data ? (
            <StepTimeline detail={detail.data} />
          ) : null}
        </SheetContent>
      </Sheet>
    </div>
  )
}

function StepTimeline({ detail }: { detail: DecisionDetailEntry }) {
  return (
    <div className="mt-4 space-y-3">
      <div className="text-meta text-muted-foreground">
        {detail.createdAt ? formatDateTime(detail.createdAt) : '时间未知'} · job {detail.jobId}
        {detail.rulesetVersion ? ` · 规则版本 ${detail.rulesetVersion}` : ''}
      </div>
      {detail.steps.length === 0 ? (
        <FeedbackState
          variant="empty"
          title="该记录没有步骤"
          description="步骤落库上线前的历史决策只保存了最终结论。"
        />
      ) : (
        <ol className="space-y-3">
          {detail.steps.map((step) => (
            <li key={step.index} className="rounded-md border border-border/60 p-3">
              <div className="flex items-center justify-between gap-2">
                <span className="text-sm font-medium">
                  {DECISION_PHASE_LABELS[step.phase] ?? step.phase}
                </span>
                {step.reasonCode ? (
                  <span className="font-mono text-meta text-muted-foreground">
                    {step.reasonCode}
                  </span>
                ) : null}
              </div>
              <dl className="mt-2 grid grid-cols-2 gap-x-3 gap-y-1 text-meta">
                {Object.entries(step.detail).map(([key, value]) => (
                  <div key={key} className="contents">
                    <dt className="truncate text-muted-foreground">{key}</dt>
                    <dd className="truncate font-mono">{formatDetail(value)}</dd>
                  </div>
                ))}
              </dl>
            </li>
          ))}
        </ol>
      )}
      {Object.keys(detail.scores).length > 0 ? (
        <div className="rounded-md bg-muted/40 p-3 text-meta">
          <div className="mb-1 font-medium">语义分数</div>
          <div className="grid grid-cols-2 gap-x-3 gap-y-1">
            {Object.entries(detail.scores).map(([key, value]) => (
              <div key={key} className="contents">
                <span className="truncate text-muted-foreground">{key}</span>
                <span className="font-mono">{value.toFixed(2)}</span>
              </div>
            ))}
          </div>
        </div>
      ) : null}
    </div>
  )
}

function formatDetail(value: unknown): string {
  if (typeof value === 'string') return value || '—'
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  if (value == null) return '—'
  return JSON.stringify(value)
}
