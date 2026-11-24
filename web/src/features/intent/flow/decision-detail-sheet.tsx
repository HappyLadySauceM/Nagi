import { useState } from 'react'
import { Button } from '../../../components/ui/button'
import { recordedFlow, runReasonText, stageLabel, type RunRow } from '../live-decision-data'
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
import type { toInboundMessage } from '../../../lib/api/adapters/intent'
import type { DecisionDetailEntry, ModelMediaEvidence, PictureEvidence } from '../../../lib/api/contracts'
import { DeliveryPanel } from './delivery-panel'
import { ReplayControls } from './replay-controls'
import { FlowRunView } from './flow-run-view'
import { jobStatus, segmentLabels } from './inbound-labels'
import { LegacyStepTimeline } from './legacy-step-timeline'

export function DecisionDetailSheet({
  decisionId,
  row,
  botId,
  inbound,
  onClose,
  onCloseAutoFocus,
}: {
  decisionId?: number | null
  row?: RunRow
  botId?: number
  inbound?: { botId: number; messageId: number }
  onClose: () => void
  onCloseAutoFocus?: (event: Event) => void
}) {
  const message = useInboundMessage(inbound?.botId, inbound?.messageId ?? null)
  const [selected, setSelected] = useState<{ messageId: number; botId: number; decisionId: number }>()
  const choice = selected && inbound && selected.messageId === inbound.messageId && selected.botId === inbound.botId
    ? selected.decisionId : undefined
  const current = choice ?? message.data?.decisionId
  const formalId = inbound ? current : row?.decisionId ?? decisionId
  const detail = useDecision(formalId || null)
  const open = inbound !== undefined || row !== undefined || decisionId != null
  const expectedBot = inbound?.botId ?? botId
  const loadError = message.error ?? detail.error
  const formal = !loadError && (expectedBot === undefined || detail.data?.botId === expectedBot) ? detail.data : undefined
  const [position, seek] = useState<number>()
  const run = formal?.run
  const hasFlow = Boolean(row?.events.length || (run && run.snapshotStatus !== 'legacy'))
  const recording = row?.events.length && (position !== undefined || !run || run.snapshotStatus === 'legacy')
    ? recordedFlow(row, position)
    : undefined
  return (
    <Sheet
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      <SheetContent side="right" onCloseAutoFocus={onCloseAutoFocus} className="w-full overflow-y-auto bg-background p-4 sm:w-[76vw] sm:max-w-6xl sm:p-6">
        <div className="border-b border-border pb-5 pr-10">
          <SheetTitle className="text-lg font-semibold">决策详情</SheetTitle>
          <SheetDescription className="mt-2 leading-relaxed">
            {row ? `${row.decisionId ? `决策 #${row.decisionId}` : '意图运行'} · ${row.conversationId} · ${formatDateTime(row.startedAt)}` : '从 QQ 入站消息查看参与判定、回复生成与最终回执。'}
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
        <div className="space-y-4">
          {message.isLoading || detail.isLoading ? (
            <div role="status" aria-label="正在读取决策详情"><Skeleton className="mt-4 h-20 w-full" /></div>
          ) : loadError ? (
            <FeedbackState variant="error" title="读取决策详情失败" error={loadError}
              onRetry={() => void (message.isError ? message.refetch() : detail.refetch())} />
          ) : formal ? (
            <IngressPanel ingress={message.data ?? formal.ingress} jobId={formal.jobId} botId={formal.botId} decisionId={formal.id} />
          ) : message.data ? (
            <>
              <IngressPanel ingress={message.data} jobId={message.data.jobId} botId={inbound?.botId} />
              <Card className="p-5 text-sm">尚无参与决策记录</Card>
              {inbound && message.data.jobId > 0 && (
                <>
                  <AuditTimeline botId={inbound.botId} jobId={message.data.jobId} />
                  <OutboundTimeline botId={inbound.botId} jobId={message.data.jobId} />
                </>
              )}
            </>
          ) : null}
          {hasFlow && <FlowRunView run={run} recording={recording} showDelivery={false} showReplay={false} />}
          {row && <RunSteps row={row} position={position} seek={seek} />}
          {formal && <>
            {run?.snapshotStatus === 'legacy' && <DecisionConclusion run={run} />}
            {(!run || run.snapshotStatus === 'legacy') && <LegacyStepTimeline detail={formal} />}
            <AuditTimeline botId={formal.botId} jobId={formal.jobId} decisionRunId={run?.runId} />
            {!inbound && run && run.snapshotStatus !== 'legacy'
              ? <DeliveryPanel run={run} />
              : <OutboundTimeline botId={formal.botId} jobId={formal.jobId} />}
            {run && run.snapshotStatus !== 'legacy' && <ReplayControls decisionId={formal.id} />}
          </>}
        </div>
      </SheetContent>
    </Sheet>
  )
}

function RunSteps({ row, position, seek }: {
  row: RunRow
  position: number | undefined
  seek: (step: number | undefined) => void
}) {
  const current = position ?? row.events.at(-1)?.sequence
  return <Card role="region" className="space-y-3 p-4" aria-label="运行步骤">
    <div className="flex flex-wrap items-center justify-between gap-2">
      <h3 className="text-sm font-semibold">运行步骤</h3>
      {position !== undefined && <Button size="sm" variant="outline" onClick={() => seek(undefined)}>跟随最新步骤</Button>}
    </div>
    {row.reason && <p className="text-sm text-muted-foreground">{runReasonText(row.reason)}</p>}
    {row.events.length === 0 ? <p className="text-sm text-muted-foreground">这条历史记录的实时步骤未知。</p> : <ol className="space-y-2">
      {row.events.map(event => <li key={event.sequence} className="flex flex-wrap items-baseline gap-x-3 text-sm" data-run-sequence={event.sequence}>
        <button type="button" aria-current={event.sequence === current ? 'step' : undefined}
          className="cursor-pointer underline-offset-4 hover:underline aria-[current=step]:font-semibold"
          onClick={() => seek(event.sequence)}>{stageLabel(event)}</button>
        <time dateTime={event.at} className="text-xs text-muted-foreground">{formatDateTime(event.at)}</time>
        {event.ruleId && <span>{event.ruleId}</span>}
        {event.branch && <span>{runReasonText(event.branch)}</span>}
        {event.elapsed !== undefined && <span>已间隔 {event.elapsed} ms</span>}
        {event.threshold !== undefined && <span>阈值 {event.threshold} ms</span>}
        {event.latency !== undefined && <span>模型耗时 {event.latency} ms</span>}
      </li>)}
    </ol>}
  </Card>
}

const attachmentStatuses: Record<string, string> = {
  pending: '等待归档', processing: '正在归档', uploaded: '已归档', expired: '已过期',
  unavailable: '无法归档', failed: '归档失败', purging: '待清理',
}

function IngressPanel({ ingress, jobId, botId, decisionId }: {
  ingress: (NonNullable<DecisionDetailEntry['ingress']> & Partial<Pick<ReturnType<typeof toInboundMessage>, 'attachments' | 'jobError'>>) | undefined
  jobId: number
  botId?: number
  decisionId?: number
}) {
  const media = ingress?.media
  const decision = media?.decisions.find(item => item.decisionId === decisionId)
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
        {jobId > 0 ? ` · 任务 ${jobId} · ${ingress.jobError === 'archive_only' ? '仅归档' : jobStatus[ingress.jobStatus] ?? ingress.jobStatus}` : ' · 无任务记录'}
        {ingress.jobError && ingress.jobError !== 'archive_only' ? ` · ${ingress.jobError === 'model_media_audit_start_failed' ? '模型请求因审计启动失败被拦截' : ingress.jobError}` : ''}
      </p>
      {media ? <div className="space-y-1" aria-label="图片与模型输入">
        <p>图片段 {media.current.imageCount} 个</p>
        <PictureStates label="入站时" evidence={media.atIngress} />
        <PictureStates label="当前附件" evidence={media.current} />
        {decision && <>
          <PictureStates label="决策时" evidence={decision.atDecision} />
          <ModelMediaStates label="意图模型" evidence={decision.intent} />
          <ModelMediaStates label="生成模型" evidence={decision.generation} />
        </>}
      </div> : ingress.segmentTypes.includes('image') && <p>历史图片状态未知</p>}
      {botId && ingress.attachments && ingress.attachments.length > 0 && (
        <ul className="space-y-1" aria-label="已登记的附件">
          {ingress.attachments.map(attachment => (
            <li key={attachment.id} className="flex flex-wrap items-center gap-x-3">
              <span>{attachment.filename || segmentLabels[attachment.kind] || attachment.kind}</span>
              <span>{attachmentStatuses[attachment.status] ?? attachment.status}</span>
              {attachment.failureCode && <span className="text-muted-foreground">{attachment.failureCode}</span>}
              {attachment.status === 'uploaded' && <a className="underline" download
                href={`/api/v1/intent/inbound/${ingress.messageId}/attachments/${encodeURIComponent(attachment.id)}?bot_id=${botId}`}
              >下载附件</a>}
            </li>
          ))}
        </ul>
      )}
      <p className="break-all font-mono text-xs">{ingress.traceId ? `入站 Trace ${ingress.traceId}` : '无入站 Trace 记录'}</p>
    </> : <p className="text-muted-foreground">入站记录不可用 · 任务 {jobId}</p>}
  </Card>
}

const pictureStates: Record<string, string> = {
  unknown: '证据未知', archive_disabled: '归档关闭', unregistered: '未注册',
  pending: '待处理', unavailable: '无法归档', no_download_source: '缺少下载来源',
  uploaded: '已上传', expired: '已过期',
}

function PictureStates({ label, evidence }: { label: string; evidence?: PictureEvidence }) {
  return <p>{label}{' · '}{evidence ? evidence.images.map(image =>
    `第 ${image.segmentIndex + 1} 段 ${pictureStates[image.status] ?? '证据未知'}`,
  ).join('、') || '无图片' : '证据未知'}</p>
}

const modelMediaLabels: Record<string, string> = {
  unknown: '证据未知', not_called: '未调用', started: '进行中', text_only: '仅文字',
  presence_hint: '仅图片存在性提示', image_submitted: '已提交图像',
}

const responseBodyLabels: Record<string, string> = {
  unknown: '响应体结束状态未知', complete: '响应体完整读取',
  response_body_read_failed: '响应体读取失败', closed_unknown: '响应体已关闭，完整性未知',
  closed_early: '响应体提前关闭',
}

function ModelMediaStates({ label, evidence }: { label: string; evidence: ModelMediaEvidence }) {
  return <div>
    <span>{label}{' · '}{modelMediaLabels[evidence.state] ?? (evidence.calls.length ? `${evidence.calls.length} 次调用` : '证据未知')}</span>
    {evidence.startFailed && <p>{evidence.calls.length ? '有一次请求因审计启动失败未派发' : '审计启动失败，未调用模型'}</p>}
    {evidence.calls.map((call, index) => {
      const count = call.attempts.filter(attempt => attempt.state === 'written' && attempt.shape === 'image_submitted').length
      const result = call.outcome === 'failed' ? '调用失败' : call.outcome === 'succeeded' ? '调用成功' : modelMediaLabels[call.outcome] ?? '证据未知'
      return <div key={index} className="pl-4">
        <p>{result}
          {call.httpStatus ? ` · HTTP ${call.httpStatus}` : ''}
          {call.attemptCountUnknown ? ' · 物理尝试总数未知' : ''}
          {call.reason === 'transport_attempt_unattributed' ? ' · 最终错误的逐笔归属未知' : ''}
          {call.reason === 'failed_before_write' ? ' · 写出前失败' : ''}
          {call.responseBody ? ` · ${responseBodyLabels[call.responseBody] ?? responseBodyLabels.unknown}` : ''}
          {call.attemptCountUnknown && count > 0 ? ` · 至少 ${count} 笔已提交图像` : ''}
        </p>
        {call.attempts.length > 0 && <ul className="pl-4">
          {call.attempts.map((attempt, attemptIndex) => <li key={attemptIndex}>
            {!call.attemptCountUnknown ? `第 ${attemptIndex + 1} 笔 · ` : ''}
            {attempt.state === 'written' ? modelMediaLabels[attempt.shape] ?? '证据未知' : attempt.state === 'partial_write' ? '部分写出' : '写出未知'}
            {attempt.images.length > 0 ? `（${attempt.images.map(index => `第 ${index + 1} 段`).join('、')}）` : ''}
            {attempt.hints.length > 0 ? ` · 图片提示 ${attempt.hints.map(index => `第 ${index + 1} 段`).join('、')}` : ''}
            {attempt.httpStatus ? ` · HTTP ${attempt.httpStatus}` : ''}
            {attempt.responseBody ? ` · ${responseBodyLabels[attempt.responseBody] ?? responseBodyLabels.unknown}` : ''}
          </li>)}
        </ul>}
      </div>
    })}
  </div>
}
