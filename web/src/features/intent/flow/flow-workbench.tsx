import { useMemo, useState } from 'react'
import { ArrowRight, RefreshCw } from 'lucide-react'
import { Badge } from '../../../components/ui/badge'
import { Button } from '../../../components/ui/button'
import { Card } from '../../../components/ui/card'
import { FeedbackState } from '../../../components/feedback-state'
import { Skeleton } from '../../../components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../../components/ui/table'
import { DECISION_KIND_LABELS, type DecisionEntry } from '../../../lib/api/contracts'
import { formatRelativeTime } from '../../../lib/format'
import { cn } from '../../../lib/utils'
import { useAccountContext } from '../../accounts/account-context'
import { useDecisions } from '../queries'
import { DecisionDetailSheet } from './decision-detail-sheet'

const KIND_VARIANTS: Record<DecisionEntry['decision'], 'success' | 'secondary' | 'warning'> = {
  reply: 'success',
  silent: 'secondary',
  defer: 'warning',
}

/** Flow-first overview: latest decisions open a right-side detail drawer. */
export function FlowWorkbench({ botId, onViewAll }: { botId?: number; onViewAll?: () => void }) {
  const { dbConfigured } = useAccountContext()
  const decisions = useDecisions(botId, '', 7, 0, '')
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const rows = useMemo(() => decisions.data?.items ?? [], [decisions.data])

  if (botId === undefined) {
    return <FeedbackState variant="empty" title="先选择机器人账号" description="选择账号后查看它的判定流程。" />
  }
  if (!dbConfigured) {
    return <FeedbackState variant="unconfigured" title="未配置数据库" description="配置数据库后即可查看判定流程。" />
  }
  if (decisions.isError) {
    return <FeedbackState variant="error" title="读取决策记录失败" error={decisions.error} onRetry={() => void decisions.refetch()} />
  }
  if (decisions.isLoading) {
    return <Skeleton className="h-72 w-full" />
  }
  if (rows.length === 0) {
    return (
      <Card>
        <FeedbackState
          variant="empty"
          title="还没有决策记录"
          description="机器人完成一次意图判定后，这里会按固定流程展示“为什么回复/沉默”。"
        />
      </Card>
    )
  }

  return (
    <div className="space-y-4">
      <Card className="overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-3">
          <div>
            <h3 className="text-sm font-semibold">最近决策</h3>
            <p className="mt-1 text-meta text-muted-foreground">点击任意决策行，在右侧抽屉查看完整详情；点击流程节点再开二级抽屉。</p>
          </div>
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => void decisions.refetch()} disabled={decisions.isFetching}>
              <RefreshCw className={cn('size-3.5', decisions.isFetching && 'animate-spin')} aria-hidden />
              刷新
            </Button>
            {onViewAll ? <Button variant="ghost" size="sm" onClick={onViewAll}>查看全部<ArrowRight aria-hidden /></Button> : null}
          </div>
        </div>
        <Table label="最近决策">
          <TableHeader>
            <TableRow>
              <TableHead>决策 / 会话</TableHead>
              <TableHead>原因</TableHead>
              <TableHead>模型</TableHead>
              <TableHead className="text-right">时间</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row) => (
              <TableRow
                key={row.id}
                tabIndex={0}
                aria-haspopup="dialog"
                aria-label={`查看决策 ${row.id}`}
                className={cn('cursor-pointer focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-ring', selectedId === row.id && 'bg-secondary/60')}
                onClick={() => setSelectedId(row.id)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' || event.key === ' ') {
                    event.preventDefault()
                    setSelectedId(row.id)
                  }
                }}
              >
                <TableCell>
                  <div className="flex flex-col items-start gap-1.5 text-left">
                    <Badge variant={KIND_VARIANTS[row.decision]}>{DECISION_KIND_LABELS[row.decision]}</Badge>
                    <span className="max-w-56 truncate font-mono text-caption text-muted-foreground">{row.conversationId || `#${row.id}`}</span>
                  </div>
                </TableCell>
                <TableCell className="font-mono text-meta">{row.reasonCode || '—'}</TableCell>
                <TableCell className="text-meta text-muted-foreground">{row.model || '未调用语义模型'}</TableCell>
                <TableCell className="whitespace-nowrap text-right text-meta text-muted-foreground">
                  {row.createdAt ? formatRelativeTime(row.createdAt) : '—'}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Card>
      <DecisionDetailSheet decisionId={selectedId} onClose={() => setSelectedId(null)} />
    </div>
  )
}
