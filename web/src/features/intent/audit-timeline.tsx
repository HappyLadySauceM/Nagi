import { useQuery } from '@tanstack/react-query'
import { apiRequest } from '../../lib/api/client'
import { asArray, asRecord, readNumber, readString } from '../../lib/api/adapters/guard'
import { FeedbackState } from '../../components/feedback-state'

const endpoint = '/api/v1/audit/runs'

type Artifact = { id: string; kind: string; status: string; contentType: string }

export function AuditTimeline({ botId, jobId }: { botId: number; jobId: number }) {
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
            return { id: readString(event, 'ID', endpoint), stage: readString(event, 'Stage', endpoint), status: readString(event, 'Status', endpoint), error: readString(event, 'ErrorCode', endpoint, '') }
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
  const labels: Record<string, string> = { running: '运行中', started: '已开始', done: '已完成', failed: '失败', unknown: '待核查' }
  return <section className="mt-5 space-y-3" aria-label="本地运行审计">
    <h3 className="font-semibold">本地运行审计</h3>
    <p className="text-sm text-muted-foreground">任务完成与消息送达分别记录，投递结果以下方投递记录为准。</p>
    {query.data?.length === 0 ? <p className="text-sm text-muted-foreground">此任务没有本地运行审计记录，历史任务不会补造记录。</p> : query.data?.map(run => <article key={run.id} className="space-y-2 rounded-lg border p-3 text-sm">
      <p>{labels[run.status] ?? run.status} · 配置修订 {run.version}</p>
      <p className="break-all font-mono text-xs">运行：{run.id}</p>
      {run.traceId && <p className="break-all font-mono text-xs">Trace：{run.traceId}</p>}
      {run.error && <p role="alert">原因：{run.error}</p>}
      <ol className="space-y-1">{run.events.map(event => <li key={event.id}>{event.stage} · {labels[event.status] ?? event.status}{event.error ? ` · ${event.error}` : ''}</li>)}</ol>
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
