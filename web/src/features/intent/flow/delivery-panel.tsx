import { Card } from '../../../components/ui/card'
import { Badge } from '../../../components/ui/badge'
import type { IntentRun } from '../../../lib/api/contracts'

const OUTCOME_LABELS: Record<string, string> = {
  completed: '完整送达',
  partial: '部分送达',
  failed: '投递失败',
  unknown: '存在结果不明的投递',
  blocked: '已被阻止',
  no_output: '没有输出',
  processing: '处理中，投递尚未结束',
}

const STATUS_LABELS: Record<string, { label: string; variant: 'success' | 'warning' | 'destructive' | 'secondary' | 'outline' }> = {
  prepared: { label: '待发送', variant: 'secondary' },
  sending: { label: '发送中', variant: 'warning' },
  sent: { label: '已送达', variant: 'success' },
  failed: { label: '发送失败', variant: 'destructive' },
  unknown: { label: '结果不明，请人工核对，勿重复发送', variant: 'warning' },
  cancelled: { label: '已取消', variant: 'secondary' },
  denied: { label: '被拒绝', variant: 'destructive' },
}

/** Delivery is intentionally separate from the decision and is the only source for "sent". */
export function DeliveryPanel({ run }: { run?: IntentRun }) {
  if (!run) return null
  const { delivery, summary } = run
  const sent = delivery.items.filter((item) => item.status === 'sent').length
  const isReply = summary.decision === 'reply'
  return (
    <Card className="p-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 className="text-sm font-semibold">实际发送</h3>
          <p className="mt-1 text-meta text-muted-foreground">
            这里汇总这条入站消息在各次执行中的 QQ 投递结果。
          </p>
        </div>
        <Badge variant={isReply && delivery.items.length === 0 ? 'warning' : delivery.outcome === 'completed' ? 'success' : 'secondary'}>
          {isReply && delivery.items.length === 0
            ? '任务尚无实际发送记录'
            : OUTCOME_LABELS[delivery.outcome] ?? (delivery.items.length ? '有发送记录' : '未发送')}
        </Badge>
      </div>
      <p className="mt-3 text-xs text-muted-foreground">
        {delivery.items.length > 0
          ? `共 ${delivery.items.length} 条记录，${sent} 条确认送达${delivery.denied ? `，拒绝 ${delivery.denied} 次` : ''}`
          : isReply
            ? '投递台账暂未记录本任务的发送。'
            : '任务没有实际发送。'}
        {delivery.reason ? ` · 终止原因：${delivery.reason}` : ''}
      </p>
      {delivery.items.length > 0 ? (
        <div className="mt-4 space-y-2">
          {delivery.items.map((item) => {
            const meta = STATUS_LABELS[item.status] ?? { label: item.status || '未知', variant: 'outline' as const }
            return (
              <article key={item.id || `${item.sequence}`} className="rounded-lg border border-border p-3 text-xs">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-muted-foreground">第 {item.sequence} 条</span>
                  <Badge variant={meta.variant}>{meta.label}</Badge>
                  {item.platformMessageId ? <span className="font-mono text-muted-foreground">回执 {item.platformMessageId}</span> : null}
                </div>
                {item.text ? <p className="mt-2 whitespace-pre-wrap break-words text-sm">{item.text}</p> : null}
                {item.errorCode ? <p className="mt-2 font-mono text-destructive">{item.errorCode}</p> : null}
              </article>
            )
          })}
        </div>
      ) : null}
    </Card>
  )
}
