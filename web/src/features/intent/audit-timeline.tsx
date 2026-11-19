import { useQuery } from '@tanstack/react-query'
import { apiRequest } from '../../lib/api/client'
import { asArray, asRecord, readNumber, readOptionalString, readString } from '../../lib/api/adapters/guard'
import { formatDateTime } from '../../lib/format'
import { FeedbackState } from '../../components/feedback-state'

const endpoint = '/api/v1/audit/runs'

const statuses: Record<string, string> = {
  running: '运行中', started: '已开始', succeeded: '成功', done: '已完成',
  failed: '失败', unknown: '待核查', skipped: '跳过', blocked: '已拦截',
}
const stages: Record<string, string> = {
  'worker.process': '任务执行', 'intent.decision': '参与决策', 'agent.context': '准备上下文与媒体', 'agent.generation': '生成回复',
  'tool.execute': '调用工具', 'outbound.deliver': '尝试投递到 QQ',
  'media.voice.transcribe': '转写语音', 'media.voice.synthesize': '合成语音',
}

type Artifact = { id: string; kind: string; status: string; contentType: string }

function eventMetadata(raw: unknown): Record<string, unknown> {
  // Audit payloads are JSON bytes encoded as base64 by the API.
  // 审计接口用 base64 编码 JSON 字节。
  if (typeof raw !== 'string' || raw === '') return {}
  try {
    const json = new TextDecoder().decode(Uint8Array.from(atob(raw), character => character.charCodeAt(0)))
    const value: unknown = JSON.parse(json)
    return asRecord(value, endpoint, 'event metadata')
  } catch {
    return {}
  }
}

export function AuditTimeline({ botId, jobId, decisionRunId }: { botId: number; jobId: number; decisionRunId?: string }) {
  const query = useQuery({
    queryKey: ['audit', 'runs', botId, jobId],
    queryFn: async ({ signal }) => {
      const page = asRecord(await apiRequest(`${endpoint}?bot_id=${botId}&job_id=${jobId}`, { signal }), endpoint, 'page')
      const runs = asArray(page.items, endpoint, 'items')
      return Promise.all(runs.map(async value => {
        const summary = asRecord(value, endpoint, 'run')
        const id = readString(summary, 'ID', endpoint)
        const run = asRecord(await apiRequest(`${endpoint}/${encodeURIComponent(id)}`, { signal }), endpoint, 'run')
        return {
          id, status: readString(run, 'Status', endpoint), error: readString(run, 'ErrorCode', endpoint, ''),
          traceId: readString(run, 'TraceID', endpoint, ''), version: readNumber(run, 'ConfigVersion', endpoint, 0),
          events: asArray(run.Events ?? [], endpoint, 'Events').map(raw => {
            const event = asRecord(raw, endpoint, 'event')
            const output = eventMetadata(event.Output)
            const input = eventMetadata(event.Input)
            const startedAt = readOptionalString(event, 'StartedAt', endpoint)
            const finishedAt = readOptionalString(event, 'FinishedAt', endpoint)
            const elapsed = startedAt && finishedAt ? Date.parse(finishedAt) - Date.parse(startedAt) : NaN
            return {
              id: readString(event, 'ID', endpoint), stage: readString(event, 'Stage', endpoint),
              status: readString(event, 'Status', endpoint), error: readString(event, 'ErrorCode', endpoint, ''),
              startedAt, durationMs: Number.isFinite(elapsed) && elapsed >= 0 ? elapsed : undefined,
              decisionRunId: typeof output.decision === 'object' && output.decision !== null && 'RunID' in output.decision && typeof output.decision.RunID === 'string' ? output.decision.RunID : '',
              historyIds: Array.isArray(output.history_message_ids) ? output.history_message_ids.filter((id): id is number => typeof id === 'number') : [],
              model: typeof output.model === 'string' ? output.model : '',
              tool: typeof input.tool === 'string' ? input.tool : '',
              inputUsage: typeof output.input_usage === 'number' ? output.input_usage : undefined,
              outputUsage: typeof output.output_usage === 'number' ? output.output_usage : undefined,
            }
          }),
          artifacts: asArray(run.Artifacts ?? [], endpoint, 'Artifacts').map(raw => {
            const artifact = asRecord(raw, endpoint, 'artifact')
            return {
              id: readString(artifact, 'ID', endpoint), kind: readString(artifact, 'Kind', endpoint),
              status: readString(artifact, 'Status', endpoint), contentType: readString(artifact, 'ContentType', endpoint),
            }
          }),
        }
      }))
    },
  })
  if (query.isError) return <FeedbackState variant="error" title="读取运行审计失败" error={query.error} onRetry={() => void query.refetch()} />
  if (query.isLoading) return <p role="status">正在读取运行审计…</p>
  return <section className="mt-5 space-y-3" aria-label="回复执行记录">
    <h3 className="font-semibold">回复执行记录</h3>
    <p className="text-sm text-muted-foreground">同一入站消息可以有多次执行。QQ 回执汇总于下方的任务投递记录。</p>
    {query.data?.length === 0 ? <p className="text-sm text-muted-foreground">此任务没有回复执行记录记录，历史任务不会补造记录。</p> : query.data?.map(run => <article key={run.id} className="space-y-2 rounded-lg border p-3 text-sm">
      <p>{statuses[run.status] ?? run.status} · 配置修订 {run.version}
        {decisionRunId && (run.events.some(event => event.decisionRunId === decisionRunId)
          ? ' · 当前选中决策所属批次'
          : ' · 同一入站消息的执行批次')}
      </p>
      <p className="break-all font-mono text-xs">运行：{run.id}</p>
      {run.traceId && <p className="break-all font-mono text-xs">Trace：{run.traceId}</p>}
      {run.error && <p role="alert">原因：{run.error}</p>}
      <ol className="space-y-2">{run.events.map(event => <li key={event.id} className="rounded-md border p-2">
        <p>{stages[event.stage] ?? event.stage} · {statuses[event.status] ?? event.status}{event.error ? ` · ${event.error}` : ''}</p>
        {event.startedAt && <time className="text-xs text-muted-foreground" dateTime={event.startedAt}>{formatDateTime(event.startedAt)}</time>}
        {event.durationMs !== undefined && <span className="text-xs text-muted-foreground"> · {event.durationMs} ms</span>}
        {event.stage === 'agent.generation' && (event.model || event.inputUsage !== undefined || event.outputUsage !== undefined) &&
          <p>{[event.model, event.inputUsage === undefined ? '' : `输入 ${event.inputUsage} token`, event.outputUsage === undefined ? '' : `输出 ${event.outputUsage} token`].filter(Boolean).join(' · ')}</p>}
        {event.stage === 'agent.context' && event.historyIds.length > 0 && <p>历史消息 ID {event.historyIds.join('、')}</p>}
        {event.stage === 'tool.execute'  && event.tool && <p>{event.tool}</p>}
      </li>)}</ol>
      {run.artifacts.length > 0 ? <section className="space-y-2 border-t pt-3" aria-label="审计媒体回放">
        <h4 className="font-medium">媒体回放</h4>
        <p className="text-xs text-muted-foreground">链接按需签发，有效期为 5 分钟；媒体到期后仍保留处理记录。</p>
        <div className="grid gap-3 sm:grid-cols-2">{run.artifacts.map(artifact => <ArtifactReplay key={artifact.id} artifact={artifact} />)}</div>
      </section> : null}
    </article>)}
  </section>
}

function ArtifactReplay({ artifact }: { artifact: Artifact }) {
  const query = useQuery({
    queryKey: ['audit', 'artifact', artifact.id],
    enabled: artifact.status === 'uploaded',
    refetchOnMount: 'always',
    queryFn: async ({ signal }) => {
      const value = asRecord(await apiRequest(`/api/v1/audit/artifacts/${encodeURIComponent(artifact.id)}`, { signal }), endpoint, 'artifact replay')
      return readString(value, 'url', endpoint, '')
    },
  })
  const title = artifact.kind || artifact.contentType || '媒体'
  if (artifact.status !== 'uploaded') return <div className="rounded-md border p-3 text-xs text-muted-foreground">{title} · {artifact.status === 'expired' ? '已过期' : artifact.status === 'redacted' ? '已脱敏' : artifact.status === 'failed' ? '归档失败' : '等待归档'}</div>
  if (query.isLoading) return <div className="rounded-md border p-3 text-xs text-muted-foreground">{title} · 正在签发回放链接…</div>
  if (query.isError || !query.data) return <div className="rounded-md border p-3 text-xs text-muted-foreground">{title} · 回放链接暂不可用</div>
  return <figure className="rounded-md border p-3">
    <figcaption className="mb-2 truncate text-xs text-muted-foreground">{title}</figcaption>
    {artifact.contentType.startsWith('image/')
      ? <img className="max-h-64 w-full rounded object-contain" src={query.data} alt={title} loading="lazy" referrerPolicy="no-referrer" />
      : <audio className="w-full" controls preload="metadata" src={query.data}>浏览器不支持音频回放。</audio>}
  </figure>
}
