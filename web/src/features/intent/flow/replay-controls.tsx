import { useState } from 'react'
import { Calculator, Cpu, TriangleAlert } from 'lucide-react'
import { Button } from '../../../components/ui/button'
import { Badge } from '../../../components/ui/badge'
import { Card } from '../../../components/ui/card'
import { ConfirmDialog } from '../../../components/confirm-dialog'
import { describeError } from '../../../lib/errors'
import type { IntentRun } from '../../../lib/api/contracts'
import { ReplaySourcePanel } from './replay-source-panel'
import { useRecalculateDecision, useReplayDecisionModel } from './queries'

const DECISION_LABELS: Record<string, string> = { reply: '回复', silent: '沉默', defer: '延后' }

function summaryLabel(run: IntentRun) {
  return `${DECISION_LABELS[run.summary.decision] ?? run.summary.decision} · ${run.summary.reasonText || run.summary.reasonCode}`
}

/** Recalculate (free) and replay-model (paid, never sends) stay separate actions. */
export function ReplayControls({ decisionId }: { decisionId: number }) {
  const recalc = useRecalculateDecision(decisionId)
  const replay = useReplayDecisionModel(decisionId)
  const [confirmReplay, setConfirmReplay] = useState(false)

  return (
    <Card className="space-y-4 p-5">
      <div>
        <h3 className="text-sm font-semibold">结果核对</h3>
        <p className="mt-1 text-meta text-muted-foreground">
          两个入口都不会发送消息。重算只用已保存快照；重新调用模型会产生费用。
        </p>
      </div>
      <div className="flex flex-wrap gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={recalc.isPending}
          onClick={() => recalc.mutate()}
        >
          <Calculator className="size-3.5" aria-hidden />
          {recalc.isPending ? '重算中…' : '按当前策略重算'}
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={replay.isPending}
          onClick={() => setConfirmReplay(true)}
          className="text-muted-foreground"
        >
          <Cpu className="size-3.5" aria-hidden />
          重新调用模型（可能产生费用）
        </Button>
      </div>
      {recalc.isError ? (
        <p role="alert" className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          {recalc.error.message.includes('snapshot_missing')
            ? '该历史记录缺少可重算快照，无法按当前策略重算。'
            : describeError(recalc.error).description}
        </p>
      ) : null}
      {recalc.data ? (
        <div className="space-y-2 rounded-lg border border-border p-3 text-sm">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium">策略重算结果</span>
            <Badge variant={recalc.data.changed ? 'warning' : 'success'}>{recalc.data.changed ? '结论有变化' : '结论一致'}</Badge>
            <span className="text-xs text-muted-foreground">不调用模型 · 不写库 · 不发送</span>
          </div>
          <div className="grid gap-2 sm:grid-cols-2">
            <div><div className="text-xs text-muted-foreground">原结论</div><div className="mt-1">{summaryLabel(recalc.data.before)}</div></div>
            <div><div className="text-xs text-muted-foreground">重算后</div><div className="mt-1">{summaryLabel(recalc.data.after)}</div></div>
          </div>
          <ReplaySourcePanel replay={recalc.data.after.replay} />
        </div>
      ) : null}
      {replay.isError ? (
        <p role="alert" className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          {replay.error.message.includes('snapshot_missing')
            ? '该历史记录缺少固定输入快照，无法重新调用模型。'
            : describeError(replay.error).description}
        </p>
      ) : null}
      {replay.data ? (
        <div className="space-y-2 rounded-lg border border-warning/30 bg-warning/5 p-3 text-sm">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium">临时模型重放</span>
            <Badge variant="warning">不保存 · 不发送</Badge>
          </div>
          <p>{summaryLabel(replay.data.run)}</p>
          <ReplaySourcePanel replay={replay.data.run.replay} />
          <p className="text-xs text-muted-foreground">
            费用提示：{replay.data.costEstimate || '一次语义模型请求'}
          </p>
        </div>
      ) : null}
      <ConfirmDialog
        open={confirmReplay}
        onOpenChange={setConfirmReplay}
        title="重新调用模型？"
        description="会使用已保存的固定输入调用当前语义模型，可能产生模型调用费用。结果只用于预览，不写入决策记录，也不会发送 QQ 消息。"
        confirmLabel="确认调用"
        pending={replay.isPending}
        onConfirm={() => {
          replay.mutate(undefined, { onSettled: () => setConfirmReplay(false) })
        }}
      />
      {recalc.isPending || replay.isPending ? (
        <p role="status" className="flex items-center gap-2 text-xs text-muted-foreground">
          <TriangleAlert className="size-3.5" aria-hidden />
          正在执行本地核对，请稍候…
        </p>
      ) : null}
    </Card>
  )
}
