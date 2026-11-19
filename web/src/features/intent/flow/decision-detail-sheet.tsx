import { useState } from 'react'
import { FeedbackState } from '../../../components/feedback-state'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '../../../components/ui/sheet'
import { Skeleton } from '../../../components/ui/skeleton'
import { AuditTimeline } from '../audit-timeline'
import { OutboundTimeline } from '../outbound-timeline'
import { useDecision, useInboundMessage } from '../queries'
import { DecisionConclusion } from './decision-conclusion'
import { formatDateTime } from '../../../lib/format'
import { Select } from '../../../components/ui/select'
import { Card } from '../../../components/ui/card'
import type { DecisionDetailEntry } from '../../../lib/api/contracts'
import { DeliveryPanel } from './delivery-panel'
import { ReplayControls } from './replay-controls'
import { FlowRunView } from './flow-run-view'
import { jobStatus, segmentLabels } from './inbound-labels'
import { LegacyStepTimeline } from './legacy-step-timeline'

/**
 * First-level decision drawer shared by the overview and the decision log.
 * 运行概览与决策记录共用的一级决策详情抽屉；点击流程节点会在其上方再开二级抽屉。
 */
export function DecisionDetailSheet({
  decisionId,
  inbound,
  onClose,
}: {
  decisionId?: number | null
  inbound?: { botId: number; messageId: number }
  onClose: () => void
}) {
  const message = useInboundMessage(inbound?.botId, inbound?.messageId ?? null)
  const [selected, setSelected] = useState<{ messageId: number; botId: number; decisionId: number }>()
  const choice = selected && inbound && selected.messageId === inbound.messageId && selected.botId === inbound.botId
    ? selected.decisionId : undefined
  const current = choice ?? message.data?.decisionId
  const detail = useDecision(inbound ? current ?? null : decisionId ?? null)
  const open = inbound !== undefined || decisionId != null
  const loadError = message.error ?? detail.error
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
        {inbound && message.data && (
          <div className="flex flex-wrap items-center gap-4 pt-3">
            <a className="text-sm underline" href={`/decisions/inbound/${inbound.botId}/${inbound.messageId}`}>入站详情链接</a>
            {message.data.decisionIds.length > 1 && (
              <Select className="max-w-64" aria-label="查看哪次参与决策" value={current} onChange={event => {
                setSelected({ ...inbound, decisionId: Number(event.target.value) })
              }}>
                {message.data.decisionIds.map(id => <option key={id} value={id}>决策 #{id}</option>)}
              </Select>
            )}
          </div>
        )}
        {message.isLoading || detail.isLoading ? (
          <Skeleton className="mt-4 h-40 w-full" />
        ) : loadError ? (
          <FeedbackState
            variant="error"
            title="读取决策详情失败"
            error={loadError}
            onRetry={() => void (message.isError ? message.refetch() : detail.refetch())}
          />
        ) : detail.data ? (
          <div className="space-y-4">
            <IngressPanel ingress={message.data ?? detail.data.ingress} jobId={detail.data.jobId} />
            {detail.data.run && detail.data.run.snapshotStatus !== 'legacy' ? (
              <FlowRunView run={detail.data.run} showDelivery={false} showReplay={false} />
            ) : (
              <>
                {detail.data.run ? <DecisionConclusion run={detail.data.run} /> : null}
                <LegacyStepTimeline detail={detail.data} />
              </>
            )}
            <AuditTimeline botId={detail.data.botId} jobId={detail.data.jobId} decisionRunId={detail.data.run?.runId} />
            {!inbound && detail.data.run && detail.data.run.snapshotStatus !== 'legacy'
              ? <DeliveryPanel run={detail.data.run} />
              : <OutboundTimeline botId={detail.data.botId} jobId={detail.data.jobId} />}
            {detail.data.run && detail.data.run.snapshotStatus !== 'legacy'
              ? <ReplayControls decisionId={detail.data.id} /> : null}
          </div>
        ) : message.data ? (
          <div className="space-y-4">
            <IngressPanel ingress={message.data} jobId={message.data.jobId} />
            <Card className="p-5 text-sm">尚无参与决策记录</Card>
            {inbound && message.data.jobId > 0 && (
              <>
                <AuditTimeline botId={inbound.botId} jobId={message.data.jobId} />
                <OutboundTimeline botId={inbound.botId} jobId={message.data.jobId} />
              </>
            )}
          </div>
        ) : null}
      </SheetContent>
    </Sheet>
  )
}

function IngressPanel({ ingress, jobId }: {
  ingress: (NonNullable<DecisionDetailEntry['ingress']> & { jobError?: string }) | undefined
  jobId: number
}) {
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
        已入库 · 消息 {ingress.messageId}
        {jobId > 0 ? ` · 任务 ${jobId} · ${jobStatus[ingress.jobStatus] ?? ingress.jobStatus}` : ' · 无任务记录'}
        {ingress.jobError ? ` · ${ingress.jobError}` : ''}
      </p>
      <p className="break-all font-mono text-xs">{ingress.traceId ? `入站 Trace ${ingress.traceId}` : '无入站 Trace 记录'}</p>
    </> : <p className="text-muted-foreground">入站记录不可用 · 任务 {jobId}</p>}
  </Card>
}
