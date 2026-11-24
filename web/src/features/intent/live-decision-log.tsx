import { memo, useCallback, useRef, useState, type ReactNode } from 'react'
import { ArrowRight, ChevronRight } from 'lucide-react'
import { Link } from '@tanstack/react-router'
import { Badge } from '../../components/ui/badge'
import { Button } from '../../components/ui/button'
import { FeedbackState } from '../../components/feedback-state'
import { Input } from '../../components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../components/ui/table'
import { formatDateTime } from '../../lib/format'
import { DecisionDetailSheet } from './flow/decision-detail-sheet'
import { compareRunRows, rowKey, runReasonText, stageLabel, type RunRow } from './live-decision-data'
import { useLiveDecisions } from './use-live-decisions'

const states: Record<string, string> = { running: '判断中', completed: '已结束', failed: '失败', interrupted: '中断', unknown: '历史记录 · 实时证据未知' }
const decisions: Record<string, string> = { reply: '回复', silent: '沉默', defer: '等待' }
const pageSize = 24
const recentSize = 8

type SelectRow = (key: string, trigger: HTMLButtonElement | null) => void
type LeaveRow = (key: string) => void

const RunTableRow = memo(function RunTableRow({ row, recent, onSelect, onLeave }: { row: RunRow; recent: boolean; onSelect: SelectRow; onLeave: LeaveRow }) {
  const trigger = useRef<HTMLButtonElement>(null)
  const events = row.events
  const last = events.at(-1)
  const key = rowKey(row)
  return <TableRow
    className="cursor-pointer focus-within:bg-secondary/40"
    data-run-id={row.runId || undefined} data-decision-id={row.decisionId || undefined}
    data-run-state={row.state} data-run-sequences={events.map(event => event.sequence).join(',')}
    onClick={() => onSelect(key, trigger.current)}
  >
    <TableCell className="py-1 pl-2">
      <Button ref={trigger} className="h-auto min-h-9 max-w-56 justify-start px-2 text-left" variant="ghost" type="button"
        onBlur={() => onLeave(key)} aria-haspopup="dialog" aria-label={row.decisionId ? `查看决策 ${row.decisionId} · ${row.conversationId}` : `查看运行 ${row.conversationId}`}
      >
        <span className="truncate font-mono">{row.conversationId}</span>
      </Button>
    </TableCell>
    {!recent && <>
      <TableCell className="whitespace-nowrap py-2"><Badge variant={row.state === 'running' ? 'warning' : 'secondary'}>{states[row.state] ?? '未知'}</Badge></TableCell>
      <TableCell className="whitespace-nowrap py-2">{last ? stageLabel(last) : '—'}</TableCell>
    </>}
    <TableCell className="whitespace-nowrap py-2">
      {row.decision ? <Badge variant={row.decision === 'reply' ? 'success' : 'secondary'}>{decisions[row.decision] ?? row.decision}</Badge> : '—'}
    </TableCell>
    <TableCell className="min-w-40 py-2 text-muted-foreground">{row.reason ? runReasonText(row.reason) : '—'}</TableCell>
    <TableCell className="whitespace-nowrap py-2 text-muted-foreground">
      <time dateTime={row.startedAt}>{formatDateTime(row.startedAt)}</time>
    </TableCell>
    <TableCell className="w-9 py-2 pl-0"><ChevronRight className="size-4 text-muted-foreground" aria-hidden /></TableCell>
  </TableRow>
})

type Props = {
  botId: number
  recent?: boolean
  renderSummary?: (live: ReturnType<typeof useLiveDecisions>) => ReactNode
}

export function LiveDecisionLog(props: Props) {
  return <AccountDecisionLog key={props.botId} {...props} />
}

function AccountDecisionLog({ botId, recent = false, renderSummary }: Props) {
  const selectedKey = useRef('')
  const trigger = useRef<HTMLButtonElement>(null)
  const heading = useRef<HTMLHeadingElement>(null)
  const [selection, setSelection] = useState({ key: '', open: false })
  const selected = selection.open ? selection.key : ''
  const [conversation, setConversation] = useState('')
  const [reason, setReason] = useState('')
  const [offset, setOffset] = useState(0)
  const live = useLiveDecisions(botId, selectedKey)
  const title = recent ? '最近决策' : '实时决策记录'

  const select = useCallback((key: string, button: HTMLButtonElement | null) => {
    selectedKey.current = key
    trigger.current = button
    setSelection({ key, open: true })
  }, [])

  const leave = useCallback((key: string) => {
    setSelection(current => current.key === key && !current.open ? { key: '', open: false } : current)
  }, [])

  const byKey = new Map(live.view?.rows.map(row => [rowKey(row), row]))
  const current = byKey.get(selected)
  const dated = live.view ? [...live.view.rows].sort(compareRunRows) : []
  const all = live.view?.history?.map(row => byKey.get(rowKey(row)) ?? row) ?? dated
  const shown = recent
    ? all.filter(row => row.decisionId > 0)
    : all.filter(row => row.conversationId.includes(conversation.trim()) && row.reason.includes(reason.trim()))
  const first = Math.min(offset, Math.max(0, Math.ceil(shown.length / pageSize) - 1) * pageSize)
  const page = recent ? shown.slice(0, recentSize) : shown.slice(first, first + pageSize)
  const anchor = byKey.get(selection.key)
  const visible = anchor && shown.includes(anchor) && !page.includes(anchor)
    ? [...page, anchor].sort(compareRunRows)
    : page
  const hasFilters = Boolean(conversation.trim() || reason.trim())
  return <>
    {renderSummary?.(live)}
    <section className="space-y-3" aria-label={title}>
      <header className="flex items-center justify-between gap-3">
        <h2 ref={heading} tabIndex={-1} className={recent ? 'text-sm font-medium' : 'sr-only'}>{title}</h2>
        {recent && <Button asChild variant="outline" size="sm"><Link to="/decisions/history">查看全部<ArrowRight aria-hidden /></Link></Button>}
      </header>
      {!live.view ? (live.error ? <FeedbackState compact={recent} variant="error" title={`暂无法读取${recent ? title : '决策记录'}`} error={live.error} onRetry={live.reset} />
        : <p role="status" className="py-3 text-sm text-muted-foreground">{recent ? '正在读取最近决策…' : '正在核验实时连接…'}</p>) : <>
        {!recent && <div className="flex flex-wrap items-center gap-2">
          <Input className="w-full sm:w-56" aria-label="会话 ID" placeholder="按会话 ID 筛选" value={conversation}
            onChange={event => { setConversation(event.target.value); setOffset(0); setSelection({ key: '', open: false }) }} />
          <Input className="w-full sm:w-56" aria-label="原因码筛选" placeholder="按原因码筛选" value={reason}
            onChange={event => { setReason(event.target.value); setOffset(0); setSelection({ key: '', open: false }) }} />
          <Button variant="outline" size="sm" onClick={live.reset}>刷新</Button>
          <span className="ml-auto text-xs text-muted-foreground">实时连接已授权</span>
        </div>}
        {visible.length ? <div className="overflow-hidden rounded-lg border border-border bg-card">
          <Table label={recent ? '最近意图决策' : '意图决策记录'}>
            <TableHeader><TableRow>
              <TableHead>会话</TableHead>
              {!recent && <><TableHead>运行状态</TableHead><TableHead>最近步骤</TableHead></>}
              <TableHead>结论</TableHead><TableHead>原因</TableHead><TableHead>时间</TableHead>
              <TableHead><span className="sr-only">详情</span></TableHead>
            </TableRow></TableHeader>
            <TableBody>{visible.map(row => <RunTableRow key={rowKey(row)} row={row} recent={recent} onSelect={select} onLeave={leave} />)}</TableBody>
          </Table>
        </div> : <FeedbackState compact={recent} variant="empty" title={hasFilters ? '已加载记录没有匹配项' : '还没有决策记录'} />}
        {!recent && <div className="flex flex-wrap gap-2">
          {first > 0 && <Button variant="outline" size="sm" onClick={() => { setOffset(Math.max(0, first - pageSize)); setSelection({ key: '', open: false }) }}>上一页</Button>}
          {first + pageSize < shown.length && <Button variant="outline" size="sm" onClick={() => { setOffset(first + pageSize); setSelection({ key: '', open: false }) }}>下一页</Button>}
          {live.view.next && <Button variant="outline" size="sm" onClick={() => { setOffset(0); setSelection({ key: '', open: false }); live.older() }} disabled={live.paging}>{live.paging ? '读取中…' : '更早记录'}</Button>}
          {live.view.history && <Button variant="ghost" size="sm" onClick={live.reset}>最新记录</Button>}
        </div>}
      </>}
      <DecisionDetailSheet key={selection.key} row={current} botId={botId} onClose={() => {
        selectedKey.current = ''
        setSelection(previous => ({ ...previous, open: false }))
      }} onCloseAutoFocus={event => {
        event.preventDefault()
        const target = trigger.current?.isConnected ? trigger.current : heading.current
        target?.focus({ preventScroll: true })
      }} />
    </section>
  </>
}
