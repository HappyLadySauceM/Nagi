import { AlertTriangle, CheckCircle2, CircleHelp, MinusCircle } from 'lucide-react'
import { Badge } from '../../../components/ui/badge'
import { Card } from '../../../components/ui/card'
import { cn } from '../../../lib/utils'
import type { IntentRun, IntentSnapshotStatus } from '../../../lib/api/contracts'
import { reasonText } from './reason-text'

const DECISION_STYLE = {
  reply: {
    label: '回复',
    badge: 'success' as const,
    icon: CheckCircle2,
    className: 'border-success/30 bg-success/10 text-success',
  },
  silent: {
    label: '沉默',
    badge: 'secondary' as const,
    icon: MinusCircle,
    className: 'border-border bg-muted/50 text-foreground',
  },
  defer: {
    label: '延后',
    badge: 'warning' as const,
    icon: CircleHelp,
    className: 'border-warning/30 bg-warning/10 text-warning',
  },
}

const SNAPSHOT_LABELS: Record<IntentSnapshotStatus, string> = {
  complete: '快照完整',
  partial: '快照部分缺失',
  missing: '无快照',
  legacy: '历史记录无快照',
}

/** First-screen answer: why the bot replied or stayed silent, plus node counts. */
export function DecisionConclusion({ run, showSnapshotStatus = true }: { run?: IntentRun; showSnapshotStatus?: boolean }) {
  if (!run) {
    return (
      <Card className="border-dashed p-6 text-sm text-muted-foreground">
        选择一条决策后查看“为什么回复/沉默”。
      </Card>
    )
  }

  const style = DECISION_STYLE[run.summary.decision]
  const Icon = style.icon
  const summaryText = !run.summary.reasonCode && run.summary.decision === 'defer'
    ? '规则验证完成，尚未进行语义判定'
    : reasonText(run.summary.reasonCode, run.summary.reasonText)
  const counts = run.nodes.reduce(
    (acc, node) => {
      acc[node.status] += 1
      return acc
    },
    { executed: 0, skipped: 0, failed: 0, degraded: 0, unknown: 0 },
  )
  const snapshotIncomplete = showSnapshotStatus && run.snapshotStatus !== 'complete'

  return (
    <Card className={cn('overflow-hidden border p-5 sm:p-6', style.className)}>
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="flex items-start gap-3">
          <span className="mt-0.5 flex size-10 shrink-0 items-center justify-center rounded-full bg-background/70">
            <Icon className="size-5" aria-hidden />
          </span>
          <div>
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-meta font-medium opacity-80">最终判定</span>
              <Badge variant={style.badge}>{style.label}</Badge>
            </div>
            <h2 className="mt-2 text-xl font-semibold tracking-tight">
              {summaryText}
            </h2>
            <p className="mt-2 text-sm leading-relaxed opacity-80">
              原因码 <span className="font-mono">{run.summary.reasonCode || '—'}</span>
              {run.rulesetVersion ? <> · 规则版本 <span className="font-mono">{run.rulesetVersion}</span></> : null}
              {run.model ? <> · 模型 <span className="font-mono">{run.model}</span></> : null}
            </p>
          </div>
        </div>
        <div className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm sm:grid-cols-4">
          {[
            ['执行', counts.executed],
            ['跳过', counts.skipped],
            ['降级', counts.degraded],
            ['失败', counts.failed],
          ].map(([label, value]) => (
            <div key={label}>
              <div className="text-meta opacity-70">{label}</div>
              <div className="mt-1 font-mono text-lg font-semibold tabular-nums">{value}</div>
            </div>
          ))}
        </div>
      </div>
      {snapshotIncomplete ? (
        <p
          role="status"
          className="mt-4 flex items-start gap-2 rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-xs leading-relaxed text-warning"
        >
          <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden />
          <span>
            {SNAPSHOT_LABELS[run.snapshotStatus]}
            {run.missingFields.length > 0 ? `：${run.missingFields.join('、')}` : ''}。仅展示已记录的数据，不补造流程节点或分支。
          </span>
        </p>
      ) : null}
    </Card>
  )
}

export { SNAPSHOT_LABELS }
