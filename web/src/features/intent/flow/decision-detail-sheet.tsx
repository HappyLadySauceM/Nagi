import { FeedbackState } from '../../../components/feedback-state'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '../../../components/ui/sheet'
import { Skeleton } from '../../../components/ui/skeleton'
import { AuditTimeline } from '../audit-timeline'
import { OutboundTimeline } from '../outbound-timeline'
import { useDecision } from '../queries'
import { DecisionConclusion } from './decision-conclusion'
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
      <SheetContent side="right" className="w-full overflow-y-auto bg-background p-6 sm:max-w-6xl">
        <div className="border-b border-border pb-5 pr-10">
          <SheetTitle className="text-lg font-semibold">决策详情</SheetTitle>
          <SheetDescription className="mt-2 leading-relaxed">
            还原最终判定、流程、实际发送、结果核对与回放以及审计时间线；点击流程节点查看节点证据。
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
          detail.data.run && detail.data.run.snapshotStatus !== 'legacy' ? (
            <>
              <FlowRunView run={detail.data.run} />
              <AuditTimeline botId={detail.data.botId} jobId={detail.data.jobId} />
            </>
          ) : (
            <>
              {detail.data.run ? <DecisionConclusion run={detail.data.run} /> : null}
              <LegacyStepTimeline detail={detail.data} />
              <OutboundTimeline botId={detail.data.botId} jobId={detail.data.jobId} />
              <AuditTimeline botId={detail.data.botId} jobId={detail.data.jobId} />
            </>
          )
        ) : null}
      </SheetContent>
    </Sheet>
  )
}
