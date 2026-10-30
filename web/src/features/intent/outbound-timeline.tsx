import { useQuery } from '@tanstack/react-query'
import { apiRequest } from '../../lib/api/client'
import { asArray, asRecord, readNumber, readString } from '../../lib/api/adapters/guard'
import { FeedbackState } from '../../components/feedback-state'

const endpoint = '/api/v1/agent/outbound'
const labels: Record<string, string> = { prepared:'待发送', sending:'发送中', sent:'已送达', failed:'发送失败', unknown:'结果不明，请人工核对，勿重复发送', cancelled:'已取消', denied:'被拒绝' }

export function OutboundTimeline({ botId, jobId }: { botId: number; jobId: number }) {
  const query = useQuery({
    queryKey: ['agent', 'outbound', botId, jobId],
    queryFn: async ({ signal }) => {
      const raw = asRecord(await apiRequest(`${endpoint}?bot_id=${botId}&job_id=${jobId}`, { signal }), endpoint, 'outbound')
      const summary = asRecord(raw.Summary, endpoint, 'Summary')
      const rows = asArray(raw.Items, endpoint, 'Items').map(value => {
        const row = asRecord(value, endpoint, 'record')
        return { id:readString(row, 'ID', endpoint), sequence:readNumber(row, 'Sequence', endpoint), status:readString(row, 'Status', endpoint), text:readString(row, 'Text', endpoint), receipt:readNumber(row, 'PlatformMessageID', endpoint, 0), error:readString(row, 'ErrorCode', endpoint, '') }
      })
      return { rows, outcome:readString(summary,'Outcome',endpoint), reason:readString(summary,'Reason',endpoint), denied:readNumber(summary,'Denied',endpoint) }
    },
  })
  if (query.isError) return <FeedbackState variant="error" title="读取投递记录失败" error={query.error} onRetry={()=>void query.refetch()} />
  if (query.isLoading) return <p role="status">正在读取投递记录…</p>
  const rows = query.data?.rows ?? []
  const sent = rows.filter(row=>row.status==='sent').length
  const outcomes: Record<string,string> = { completed:'完整送达', partial:'部分完成', failed:'投递失败', unknown:'存在结果不明的投递', blocked:'已被阻止', no_output:'没有输出', processing:'处理中，投递尚未结束' }
  return <section className="mt-5 space-y-3"><p>{outcomes[query.data?.outcome ?? ''] ?? ''} · 拒绝 {query.data?.denied ?? 0} 次</p>{query.data?.reason ? <p role="alert">终止原因：{query.data.reason}</p>:null}<h3 className="font-semibold">消息投递 · {sent}/{rows.length} 条已确认送达</h3>{rows.length===0 ? <p className="text-muted-foreground">本任务没有消息投递记录。</p> : rows.map(row=><article key={row.id} className="rounded-lg border p-3 text-sm"><p>第 {row.sequence} 条 · {labels[row.status] ?? row.status}</p><p className="whitespace-pre-wrap break-words">{row.text}</p>{row.receipt ? <p>平台回执：{row.receipt}</p>:null}{row.error ? <p>原因：{row.error}</p>:null}</article>)}</section>
}
