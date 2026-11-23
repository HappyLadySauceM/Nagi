import { memo, useRef, useState } from 'react'
import { Badge } from '../../components/ui/badge'
import { Button } from '../../components/ui/button'
import { FeedbackState } from '../../components/feedback-state'
import { Input } from '../../components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../components/ui/table'
import { formatDateTime } from '../../lib/format'
import { DecisionDetailSheet } from './flow/decision-detail-sheet'
import { FlowRunView } from './flow/flow-run-view'
import { useDecision } from './queries'
import { reasonText } from './flow/reason-text'
import { recordedFlow, rowKey, stageLabel, type RunRow } from './live-decision-data'
import { useLiveDecisions } from './use-live-decisions'

const states: Record<string, string> = { running: '判断中', completed: '已结束', failed: '失败', interrupted: '中断', unknown: '历史记录 · 实时证据未知' }
const decisions: Record<string, string> = { reply: '回复', silent: '沉默', defer: '等待' }
const branches: Record<string, string> = {
  blocked: '直接拦截', deferred: '候选话题暂缓', bypassed: '合格续接，绕过冷却', restored: '恢复冷却并拦截',
  equal: '间隔等于阈值，放行', disabled: '冷却已关闭', input_missing: '间隔输入不可用', passed: '间隔超过阈值，放行',
  decision_persistence_failed: '决策记录写入失败', decision_failed: '判断失败', decision_cancelled: '判断取消', claim_lost: '任务领取已失效', semantic_result_unknown: '中断，语义结果未知',
}

const RunTableRow = memo(function RunTableRow({ row, setSelected }: { row: RunRow; setSelected: (key: string) => void }) {
  const events = row.events
  const last = events.at(-1)
  const key = rowKey(row)
  return <TableRow data-run-id={row.runId || undefined} data-decision-id={row.decisionId || undefined} data-run-state={row.state} data-run-sequences={events.map(event => event.sequence).join(',')}>
    <TableCell><Button className="h-auto min-h-9 px-2 text-left" variant="ghost" onClick={() => setSelected(key)} aria-label={row.decisionId ? `查看决策 ${row.decisionId}` : `查看运行 ${row.runId}`}>
      <span className="max-w-40 truncate font-mono sm:max-w-56">{row.conversationId}</span>
    </Button></TableCell>
    <TableCell><Badge variant={row.state === 'running' ? 'warning' : 'secondary'}>{states[row.state] ?? '未知'}</Badge></TableCell>
    <TableCell>{last ? stageLabel(last) : '—'}</TableCell>
    <TableCell>{decisions[row.decision] ?? '—'}</TableCell>
    <TableCell className="whitespace-nowrap text-muted-foreground">{formatDateTime(row.startedAt)}</TableCell>
  </TableRow>
})

function RunStages({ row, position, seek, onClose, onDetail }: {
  row: RunRow; position: number | undefined; seek: (step: number | undefined) => void; onClose: () => void; onDetail: () => void
}) {
  const formal = useDecision(row.decisionId || null)
  const final = formal.data?.run
  const recording = recordedFlow(row, position)
  const current = position ?? row.events.at(-1)?.sequence
  return <section className="space-y-3 rounded-lg border p-4" aria-label="运行步骤">
    <div className="flex items-center justify-between gap-2">
      <h2 className="font-medium">运行步骤</h2>
      <div className="flex gap-2">
        {row.decisionId > 0 && <Button variant="outline" size="sm" onClick={onDetail}>查看正式决策</Button>}
        <Button variant="ghost" size="sm" onClick={onClose}>关闭步骤</Button>
      </div>
    </div>
    <FlowRunView run={final} recording={position === undefined && final ? undefined : recording} showDelivery={false} showReplay={false} />
    {position !== undefined && <Button size="sm" variant="outline" onClick={() => seek(undefined)}>跟随最新步骤</Button>}
    {row.reason && <p className="text-sm">{branches[row.reason] ?? reasonText(row.reason)}</p>}
    {row.events.length === 0 ? <p className="text-sm text-muted-foreground">这条历史记录的实时步骤未知。</p> : <ol className="space-y-2">
      {row.events.map(event => <li key={event.sequence} className="flex flex-wrap gap-x-3 text-sm" data-run-sequence={event.sequence}>
        <button type="button" aria-current={event.sequence === current ? 'step' : undefined} className="underline-offset-4 hover:underline" onClick={() => seek(event.sequence)}>{stageLabel(event)}</button><time dateTime={event.at} className="text-muted-foreground">{formatDateTime(event.at)}</time>
        {event.ruleId && <span>{event.ruleId}</span>}
        {event.branch && <span>{branches[event.branch] ?? event.branch}</span>}
        {event.elapsed !== undefined && <span>已间隔 {event.elapsed} ms</span>}
        {event.threshold !== undefined && <span>阈值 {event.threshold} ms</span>}
        {event.latency !== undefined && <span>模型耗时 {event.latency} ms</span>}
      </li>)}
    </ol>}
  </section>
}

export function LiveDecisionLog({ botId }: { botId: number }) {
  return <AccountDecisionLog key={botId} botId={botId} />
}

function AccountDecisionLog({ botId }: { botId: number }) {
  const selectedKey = useRef('')
  const [selected, setSelected] = useState('')
  const [position, setPosition] = useState<number>()
  const live = useLiveDecisions(botId, selectedKey)
  const select = (key: string) => {
    if (selectedKey.current !== key) setPosition(undefined)
    selectedKey.current = key
    setSelected(key)
  }
  return live.view ? <DecisionRows rows={live.view.rows} history={live.view.history} selected={selected} setSelected={select} position={position} seek={setPosition} canPage={Boolean(live.view.next)} paging={live.paging} older={live.older} reset={live.reset} />
    : live.error ? <FeedbackState variant="error" title="暂无法读取决策记录" error={live.error} onRetry={live.reset} />
      : <p role="status" className="text-sm text-muted-foreground">正在核验实时连接…</p>
}

function DecisionRows({ rows, history, selected, setSelected, position, seek, canPage, paging, older, reset }: {
  rows: RunRow[]; history: RunRow[] | null; selected: string; setSelected: (key: string) => void
  position: number | undefined; seek: (sequence: number | undefined) => void
  canPage: boolean; paging: boolean; older: () => void; reset: () => void
}) {
  const [detail, setDetail] = useState<number | null>(null)
  const [conversation, setConversation] = useState('')
  const [reason, setReason] = useState('')
  const [offset, setOffset] = useState(0)
  const pageSize = 24
  const latestByKey = new Map(rows.map(row => [rowKey(row), row]))
  const all = history ? history.map(row => latestByKey.get(rowKey(row)) ?? row) : rows
  const shown = all.filter(row => row.conversationId.includes(conversation.trim()) && row.reason.includes(reason.trim()))
  const current = latestByKey.get(selected)
  const first = Math.min(offset, Math.max(0, Math.ceil(shown.length / pageSize) - 1) * pageSize)
  const visible = shown.slice(first, first + pageSize)
  return <section className="space-y-4" aria-label="实时决策记录">
    <div className="flex flex-wrap items-center gap-2">
      <Input className="w-full sm:w-56" aria-label="会话 ID" placeholder="按会话 ID 筛选" value={conversation} onChange={event => { setConversation(event.target.value); setDetail(null); setOffset(0) }} />
      <Input className="w-full sm:w-56" aria-label="原因码筛选" placeholder="按原因码筛选" value={reason} onChange={event => { setReason(event.target.value); setDetail(null); setOffset(0) }} />
      <Button variant="outline" size="sm" onClick={reset}>刷新</Button>
      <span className="ml-auto text-xs text-muted-foreground">实时连接已授权</span>
    </div>
    {current && <RunStages key={rowKey(current)} row={current} position={position} seek={seek} onClose={() => { setSelected(''); setDetail(null) }} onDetail={() => setDetail(current.decisionId)} />}
    {shown.length ? <div className="overflow-hidden rounded-lg border border-border bg-card">
      <Table label="意图决策记录"><TableHeader><TableRow><TableHead>会话</TableHead><TableHead>运行状态</TableHead><TableHead>最近步骤</TableHead><TableHead>结论</TableHead><TableHead>开始时间</TableHead></TableRow></TableHeader>
        <TableBody>{visible.map(row => <RunTableRow key={rowKey(row)} row={row} setSelected={setSelected} />)}</TableBody>
      </Table>
    </div> : <FeedbackState variant="empty" title={conversation || reason ? '已加载记录没有匹配项' : '还没有决策记录'} />}
    <div className="flex flex-wrap gap-2">
      {first > 0 && <Button variant="outline" size="sm" onClick={() => setOffset(Math.max(0, first - pageSize))}>上一页</Button>}
      {first + pageSize < shown.length && <Button variant="outline" size="sm" onClick={() => setOffset(first + pageSize)}>下一页</Button>}
      {canPage && <Button variant="outline" size="sm" onClick={() => { setOffset(0); older() }} disabled={paging}>{paging ? '读取中…' : '更早记录'}</Button>}
    </div>
    {history && <Button variant="ghost" size="sm" onClick={reset}>最新记录</Button>}
    {detail !== null && current?.decisionId === detail && <DecisionDetailSheet decisionId={detail} onClose={() => setDetail(null)} />}
  </section>
}
