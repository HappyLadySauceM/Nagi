import { FeedbackState } from '../../../components/feedback-state'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '../../../components/ui/sheet'
import { Skeleton } from '../../../components/ui/skeleton'
import { AuditTimeline } from '../audit-timeline'
import { OutboundTimeline } from '../outbound-timeline'
import { useDecision } from '../queries'
import { DecisionConclusion } from './decision-conclusion'
import { formatDateTime } from '../../../lib/format'
import { Card } from '../../../components/ui/card'
import type { DecisionDetailEntry } from '../../../lib/api/contracts'
import { DeliveryPanel } from './delivery-panel'
import { ReplayControls } from './replay-controls'
import { FlowRunView } from './flow-run-view'
import { LegacyStepTimeline } from './legacy-step-timeline'

/**
 * First-level decision drawer shared by the overview and the decision log.
 * 运行概览与决策记录共用的一级决策详情抽屉；点击流程节点会在其上方再开二级抽屉。
 */
export function DecisionDetailSheet({
  decisionId,
  onClose,
}: {
  decisionId: number | null
  onClose: () => void
}) {
  const detail = useDecision(decisionId)
  const open = decisionId !== null
  return (
    <Sheet
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      <SheetContent side="right" className="w-[calc(100vw-1rem)] max-w-none overflow-y-auto bg-background p-6 sm:max-w-none">
        <div className="border-b border-border pb-5 pr-10">
          <SheetTitle className="text-lg font-semibold">决策详情</SheetTitle>
          <SheetDescription className="mt-2 leading-relaxed">
            从 QQ 入站消息查看参与判定、回复生成与最终回执。
          </SheetDescription>
        </div>
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
          <div className="space-y-4">
            <IngressPanel detail={detail.data} />
            {detail.data.run && detail.data.run.snapshotStatus !== 'legacy' ? (
              <FlowRunView run={detail.data.run} showDelivery={false} showReplay={false} />
            ) : (
              <>
                {detail.data.run ? <DecisionConclusion run={detail.data.run} /> : null}
                <LegacyStepTimeline detail={detail.data} />
              </>
            )}
            <AuditTimeline botId={detail.data.botId} jobId={detail.data.jobId} decisionRunId={detail.data.run?.runId} />
            {detail.data.run && detail.data.run.snapshotStatus !== 'legacy'
              ? <DeliveryPanel run={detail.data.run} />
              : <OutboundTimeline botId={detail.data.botId} jobId={detail.data.jobId} />}
            {detail.data.run && detail.data.run.snapshotStatus !== 'legacy'
              ? <ReplayControls decisionId={detail.data.id} modelReplay={detail.data.provider !== 'agent_attention'} /> : null}
          </div>
        ) : null}
      </SheetContent>
    </Sheet>
  )
}

const jobStatus: Record<string, string> = {
  pending: '等待处理', processing: '处理中', done: '任务完成', failed: '任务失败', cancelled: '已取消',
}
const segmentLabels: Record<string, string> = {
  image: '图片', face: '表情', at: '@', reply: '引用', record: '语音', video: '视频', file: '文件',
}

function IngressPanel({ detail }: { detail: DecisionDetailEntry }) {
  const ingress = detail.ingress
  return <Card className="space-y-2 p-5 text-sm">
    <h3 className="font-semibold">QQ 入站消息</h3>
    {ingress ? <>
      <p className="text-muted-foreground">
        发送人 {ingress.senderId}
        {ingress.receivedAt ? ` · ${formatDateTime(ingress.receivedAt)}` : ''}
        {ingress.platformMessageId ? ` · QQ 消息 ${ingress.platformMessageId}` : ''}
      </p>
      {ingress.text && <p className="whitespace-pre-wrap break-words">{ingress.text}</p>}
      {ingress.segmentTypes.length > 0 && <p>{ingress.segmentTypes.map(type => segmentLabels[type] ?? type).join(' · ')}</p>}
      <p className="text-muted-foreground">
        已入库 · 消息 {ingress.messageId} · 任务 {detail.jobId} · {jobStatus[ingress.jobStatus] ?? ingress.jobStatus}
      </p>
      {ingress.traceId && <p className="break-all font-mono text-xs">入站 Trace {ingress.traceId}</p>}
    </> : <p className="text-muted-foreground">入站记录不可用 · 任务 {detail.jobId}</p>}
  </Card>
}
