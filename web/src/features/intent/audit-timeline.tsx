import { useQuery } from '@tanstack/react-query'
import { apiRequest } from '../../lib/api/client'
import { asArray, asRecord, readNumber, readString } from '../../lib/api/adapters/guard'
import { FeedbackState } from '../../components/feedback-state'

const endpoint = '/api/v1/audit/runs'

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
    </article>)}
  </section>
}
