import { useState } from 'react'
import { ArrowRight, RefreshCw } from 'lucide-react'
import { Badge } from '../../../components/ui/badge'
import { Button } from '../../../components/ui/button'
import { Input } from '../../../components/ui/input'
import { Card } from '../../../components/ui/card'
import { FeedbackState } from '../../../components/feedback-state'
import { Skeleton } from '../../../components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../../components/ui/table'
import { DECISION_KIND_LABELS, type DecisionEntry } from '../../../lib/api/contracts'
import { formatRelativeTime } from '../../../lib/format'
import { cn } from '../../../lib/utils'
import { useAccountContext } from '../../accounts/account-context'
import { useInbound } from '../queries'
import { DecisionDetailSheet } from './decision-detail-sheet'
import { jobStatus, segmentLabels } from './inbound-labels'

const KIND_VARIANTS: Record<DecisionEntry['decision'], 'success' | 'secondary' | 'warning'> = {
  reply: 'success',
  silent: 'secondary',
  defer: 'warning',
}

/** Flow-first overview: latest decisions open a right-side detail drawer. */
export function FlowWorkbench(props: { botId?: number; onViewAll?: () => void; history?: boolean }) {
  return <InboundWorkbench key={props.botId} {...props} />
}

function InboundWorkbench({ botId, onViewAll, history }: { botId?: number; onViewAll?: () => void; history?: boolean }) {
  const { dbConfigured } = useAccountContext()
  const [conversation, setConversation] = useState('')
  const [conversationId, setConversationId] = useState('')
  const [cursor, setCursor] = useState(0)
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const limit = history ? 20 : 7
  const decisions = useInbound(botId, conversationId, limit, cursor)
  const rows = decisions.data?.items ?? []

  if (botId === undefined) {
    return <FeedbackState variant="empty" title="先选择机器人账号" description="选择账号后查看它的判定流程。" />
  }
  if (!dbConfigured) {
    return <FeedbackState variant="unconfigured" title="未配置数据库" description="配置数据库后即可查看判定流程。" />
  }
  return (
    <div className="space-y-4">
      <Card className="overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-3">
          <div>
            <h3 className="text-sm font-semibold">{history ? '入站记录' : '最近决策'}</h3>
            <p className="mt-1 text-meta text-muted-foreground">打开入站消息查看处理状态、参与判断和 QQ 回执。</p>
            {decisions.data && <p className="mt-1 text-meta text-muted-foreground">本进程中因作用域策略未入库 {decisions.data.blocked} 条（全账号会话）</p>}
          </div>
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => void decisions.refetch()} disabled={decisions.isFetching}>
              <RefreshCw className={cn('size-3.5', decisions.isFetching && 'animate-spin')} aria-hidden />
              刷新
            </Button>
            {onViewAll ? <Button variant="ghost" size="sm" onClick={onViewAll}>查看全部<ArrowRight aria-hidden /></Button> : null}
          </div>
        </div>
        {history && (
          <form className="flex flex-wrap gap-2 px-4 py-3" onSubmit={event => {
            event.preventDefault()
            setCursor(0)
            setConversationId(conversation.trim())
          }}>
            <Input className="max-w-96" aria-label="会话 ID" placeholder="按会话 ID 筛选" value={conversation} onChange={event => setConversation(event.target.value)} />
            <Button type="submit" variant="outline">筛选</Button>
          </form>
        )}
        {decisions.isError ? (
          <FeedbackState variant="error" title="读取入站记录失败" error={decisions.error} onRetry={() => void decisions.refetch()} />
        ) : decisions.isLoading ? <Skeleton className="h-72 w-full" /> : rows.length === 0 ? (
          <FeedbackState variant="empty" title={cursor ? '没有更早的入站记录' : '没有匹配的入站记录'} />
        ) : <Table label={history ? '入站记录' : '最近决策'}>
          <TableHeader>
            <TableRow>
              <TableHead>消息 / 会话</TableHead>
              <TableHead>处理状态</TableHead>
              <TableHead>原因</TableHead>
              <TableHead>模型</TableHead>
              <TableHead className="text-right">时间</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row) => (
              <TableRow
                key={row.messageId}
                tabIndex={0}
                aria-haspopup="dialog"
                aria-label={`查看入站消息 ${row.messageId}`}
                className={cn('cursor-pointer focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-ring', selectedId === row.messageId && 'bg-secondary/60')}
                onClick={() => setSelectedId(row.messageId)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' || event.key === ' ') {
                    event.preventDefault()
                    setSelectedId(row.messageId)
                  }
                }}
              >
                <TableCell>
                  <div className="flex flex-col items-start gap-1.5 text-left">
                    <span className="max-w-72 truncate">{row.text || row.segmentTypes.map(type => segmentLabels[type] ?? type).join(' · ') || '无文本'}</span>
                    <span className="max-w-56 truncate font-mono text-caption text-muted-foreground">#{row.messageId} · {row.conversationId}</span>
                  </div>
                </TableCell>
                <TableCell>
                  <div className="flex flex-wrap items-center gap-1.5">
                    {row.decision && <Badge variant={KIND_VARIANTS[row.decision]}>{DECISION_KIND_LABELS[row.decision]}</Badge>}
                    <span className="text-meta text-muted-foreground">{jobStatus[row.jobStatus] ?? (row.jobStatus || '无任务')}</span>
                  </div>
                </TableCell>
                <TableCell className="font-mono text-meta">{row.reasonCode || '—'}</TableCell>
                <TableCell className="text-meta text-muted-foreground">{row.model || '未调用语义模型'}</TableCell>
                <TableCell className="whitespace-nowrap text-right text-meta text-muted-foreground">
                  {row.receivedAt ? formatRelativeTime(row.receivedAt) : '—'}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>}
        {history && (
          <div className="flex justify-end gap-2 border-t px-4 py-3">
            <Button variant="outline" size="sm" disabled={!cursor || decisions.isFetching} onClick={() => setCursor(0)}>最新记录</Button>
            <Button variant="outline" size="sm" disabled={rows.length < limit || decisions.isFetching} onClick={() => setCursor(rows.at(-1)?.messageId ?? 0)}>更早记录</Button>
          </div>
        )}
      </Card>
      <DecisionDetailSheet inbound={selectedId === null ? undefined : { botId, messageId: selectedId }} onClose={() => setSelectedId(null)} />
    </div>
  )
}
