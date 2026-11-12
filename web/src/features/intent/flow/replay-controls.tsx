import { useState } from 'react'
import { Calculator, Cpu, TriangleAlert } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '../../../components/ui/button'
import { Badge } from '../../../components/ui/badge'
import { Card } from '../../../components/ui/card'
import { ConfirmDialog } from '../../../components/confirm-dialog'
import { describeError } from '../../../lib/errors'
import type { IntentCost, IntentRun, ReplayEstimate } from '../../../lib/api/contracts'
import { ReplaySourcePanel } from './replay-source-panel'
import { ReplayEvaluationsPanel } from './replay-evaluations-panel'
import {
  useRecalculateDecision,
  useReplayDecisionModel,
  useReplayEstimate,
  useSaveReplayEvaluation,
} from './queries'

const DECISION_LABELS: Record<string, string> = { reply: '回复', silent: '沉默', defer: '延后' }

function summaryLabel(run: IntentRun) {
  return `${DECISION_LABELS[run.summary.decision] ?? run.summary.decision} · ${run.summary.reasonText || run.summary.reasonCode}`
}

function costText(cost?: IntentCost): string {
  if (!cost || cost.known === false) {
    return '费用未知'
  }
  if (cost.text || cost.costEstimate) {
    return cost.text ?? cost.costEstimate ?? '费用未知'
  }
  if (cost.amount !== undefined) {
    return `${cost.amount}${cost.currency ? ` ${cost.currency}` : ''}`
  }
  return '费用未知'
}

function estimateDetails(estimate: ReplayEstimate): string[] {
  const details = [
    `模型：${estimate.provider} / ${estimate.model}`,
    `样本数：${estimate.sampleCount}`,
    `预计外部请求：${estimate.estimatedExternalRequests} 次`,
  ]
  if (estimate.tokenEstimate) {
    details.push(
      `预计 token：输入 ${estimate.tokenEstimate.inputTokens} / 输出 ${estimate.tokenEstimate.outputTokens} / 合计 ${estimate.tokenEstimate.totalTokens}（${estimate.tokenEstimate.method}）`,
    )
  } else {
    details.push('预计 token：未知')
  }
  details.push(`费用：${costText(estimate.cost)}`)
  return details
}

/** Recalculate (free) and replay-model (paid, never sends) stay separate actions. */
export function ReplayControls({ decisionId }: { decisionId: number }) {
  const recalc = useRecalculateDecision(decisionId)
  const replay = useReplayDecisionModel(decisionId)
  const estimate = useReplayEstimate(decisionId)
  const saveEvaluation = useSaveReplayEvaluation(decisionId)
  const [pendingEstimate, setPendingEstimate] = useState<ReplayEstimate | null>(null)
  const [confirmReplay, setConfirmReplay] = useState(false)

  const startReplay = () => {
    estimate.mutate(undefined, {
      onSuccess: (data) => {
        setPendingEstimate(data)
        setConfirmReplay(true)
      },
    })
  }

  return (
    <div className="space-y-4">
      <Card className="space-y-4 p-5">
        <div>
          <h3 className="text-sm font-semibold">结果核对</h3>
          <p className="mt-1 text-meta text-muted-foreground">
            两个入口都不会发送消息。重算只用已保存快照；重新调用模型会先由服务端给出费用估算，确认后才执行。
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
            disabled={estimate.isPending || replay.isPending}
            onClick={startReplay}
            className="text-muted-foreground"
          >
            <Cpu className="size-3.5" aria-hidden />
            {estimate.isPending ? '获取费用估算…' : '重新调用模型（可能产生费用）'}
          </Button>
        </div>
        {estimate.isError ? (
          <p role="alert" className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
            {describeError(estimate.error).description}
          </p>
        ) : null}
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
              <Badge variant={recalc.data.changed ? 'warning' : 'success'}>
                {recalc.data.changed ? '结论有变化' : '结论一致'}
              </Badge>
              <span className="text-xs text-muted-foreground">不调用模型 · 不写库 · 不发送</span>
              <Button
                size="sm"
                variant="outline"
                disabled={saveEvaluation.isPending}
                onClick={() =>
                  saveEvaluation.mutate(
                    { mode: 'recalculate' },
                    {
                      onSuccess: (saved) => toast.success(`评估已保存 #${saved.id}`),
                      onError: (error) => toast.error(describeError(error).title),
                    },
                  )
                }
              >
                保存评估
              </Button>
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
              <Badge variant="warning">不发送消息</Badge>
              {replay.data.evaluationId ? (
                <Badge variant="outline">已保存评估 #{replay.data.evaluationId}</Badge>
              ) : null}
            </div>
            <p>{summaryLabel(replay.data.run)}</p>
            <ReplaySourcePanel replay={replay.data.run.replay} />
            <p className="text-xs text-muted-foreground">
              费用提示：{replay.data.costEstimate || '费用未知'}
            </p>
          </div>
        ) : null}
        {recalc.isPending || replay.isPending || estimate.isPending ? (
          <p role="status" className="flex items-center gap-2 text-xs text-muted-foreground">
            <TriangleAlert className="size-3.5" aria-hidden />
            正在执行本地核对，请稍候…
          </p>
        ) : null}
      </Card>
      <ReplayEvaluationsPanel decisionId={decisionId} />
      <ConfirmDialog
        open={confirmReplay && pendingEstimate !== null}
        onOpenChange={(open) => {
          setConfirmReplay(open)
          if (!open) setPendingEstimate(null)
        }}
        title="重新调用模型？"
        description="服务端已完成费用估算；确认后才会使用固定输入调用当前语义模型。结果只用于评估，不发送 QQ 消息。"
        details={pendingEstimate ? estimateDetails(pendingEstimate) : undefined}
        confirmLabel="确认调用"
        pending={replay.isPending}
        onConfirm={() => {
          if (!pendingEstimate) return
          replay.mutate(
            { confirmationToken: pendingEstimate.confirmationToken },
            {
              onSuccess: () => {
                setPendingEstimate(null)
              },
              onError: (error) => {
                toast.error(describeError(error).title)
              },
              onSettled: () => setConfirmReplay(false),
            },
          )
        }}
      />
    </div>
  )
}
