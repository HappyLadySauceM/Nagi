import { useState } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { ArrowRight, RefreshCw } from 'lucide-react'
import { apiRequest } from '../../lib/api/client'
import { asRecord, readNumber } from '../../lib/api/adapters/guard'
import { Select } from '../../components/ui/select'
import { RulesPage, SimulationPanel } from './rules-page'
import { Input } from '../../components/ui/input'
import { OutboundTimeline } from './outbound-timeline'
import { Badge } from '../../components/ui/badge'
import { Button } from '../../components/ui/button'
import { FeedbackState } from '../../components/feedback-state'
import { PageHeader } from '../../components/page-header'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '../../components/ui/sheet'
import { Skeleton } from '../../components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../components/ui/table'
import {
  DECISION_KIND_LABELS,
  DECISION_PHASE_LABELS,
  type DecisionDetailEntry,
  type DecisionEntry,
} from '../../lib/api/contracts'
import { formatCount, formatDateTime, formatRelativeTime } from '../../lib/format'
import { cn } from '../../lib/utils'
import { useAccountContext } from '../accounts/account-context'
import { useDecision, useDecisions } from './queries'

const KIND_VARIANTS: Record<DecisionEntry['decision'], 'success' | 'secondary' | 'warning'> = {
  reply: 'success', silent: 'secondary', defer: 'warning',
}
const TABS = [
  ['overview', '运行概览'], ['rules', '规则管理'],
  ['simulate', '模拟验证'],
] as const
type IntentTab = typeof TABS[number][0]

export function IntentPage() {
  const { selectedBotId } = useAccountContext()
  return <IntentWorkspace key={selectedBotId ?? 'none'} />
}

function IntentWorkspace() {
  const navigate = useNavigate()
  const [tab, setTab] = useState<IntentTab>('overview')
  function changeTab(next: IntentTab) {
    if (next !== tab && !window.dispatchEvent(new Event('nagi:account-changing', { cancelable: true }))) return false
    setTab(next)
    return true
  }
  return (
    <div className="space-y-6">
      <PageHeader title="意图识别" description="了解每一次回复与沉默背后的判断，持续调整机器人的参与方式。" />
      <div role="tablist" aria-label="意图识别模块" className="flex gap-6 overflow-x-auto border-b border-border">
        {TABS.map(([key, label], index) => (
          <button key={key} type="button" role="tab" id={`intent-tab-${key}`}
            aria-selected={tab === key} aria-controls={tab === key ? `intent-panel-${key}` : undefined} tabIndex={tab === key ? 0 : -1}
            className={cn('shrink-0 border-b-2 px-0.5 pb-3 text-sm font-medium transition-colors', tab === key ? 'border-foreground text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground')}
            onClick={() => changeTab(key)}
            onKeyDown={event => {
              const next = event.key === 'ArrowRight' ? (index + 1) % TABS.length : event.key === 'ArrowLeft' ? (index + TABS.length - 1) % TABS.length : event.key === 'Home' ? 0 : event.key === 'End' ? TABS.length - 1 : null
              if (next === null) return
              event.preventDefault()
              if (changeTab(TABS[next][0])) document.getElementById(`intent-tab-${TABS[next][0]}`)?.focus()
            }}>
            {label}
          </button>
        ))}
      </div>
      <div role="tabpanel" id={`intent-panel-${tab}`} aria-labelledby={`intent-tab-${tab}`}>
        {tab === 'overview' ? <IntentOverview onViewAll={() => { void navigate({ to: '/decisions/history' }) }} /> : null}
        {tab === 'rules' ? <RulesPage embedded /> : null}
        {tab === 'simulate' ? <SimulationPanel /> : null}
      </div>
    </div>
  )
}

function IntentOverview({ onViewAll }: { onViewAll: () => void }) {
  const { selectedBotId, dbConfigured } = useAccountContext()
  const [hours, setHours] = useState('24')
  const query = useQuery({
    queryKey: ['intent', 'overview', selectedBotId, hours],
    enabled: selectedBotId !== undefined,
    queryFn: async ({ signal }) => {
      const raw = await apiRequest(`/api/v1/intent/overview?bot_id=${selectedBotId}&hours=${hours}`, { signal })
      const row = asRecord(raw, '/api/v1/intent/overview', 'overview')
      return Object.fromEntries(['Total', 'SemanticCalls', 'SemanticSuccess', 'Degraded', 'Replies', 'Silent', 'AverageLatencyMS'].map(key => [key, readNumber(row, key, '/api/v1/intent/overview', 0)]))
    },
  })
  const value = query.data
  if (selectedBotId === undefined) return <FeedbackState variant="empty" title="先选择机器人账号" description="选择账号后查看它的意图识别数据。" action={<Button asChild variant="outline"><Link to="/accounts">查看账号连接</Link></Button>} />
  if (!dbConfigured) return <FeedbackState variant="unconfigured" title="未配置数据库" description="配置数据库后即可查看意图识别统计与决策记录。" />
  return (
    <div className="space-y-7">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-sm font-medium">决策摘要</h2>
        <div className="flex items-center gap-2">
          <Select aria-label="统计时间范围" containerClassName="w-36" value={hours} onChange={event => setHours(event.target.value)}>
            <option value="24">最近 24 小时</option><option value="168">最近 7 天</option><option value="720">最近 30 天</option>
          </Select>
          <Button variant="outline" size="icon" aria-label="刷新统计" disabled={query.isFetching} onClick={() => void query.refetch()}><RefreshCw className={cn('size-4', query.isFetching && 'animate-spin')} /></Button>
        </div>
      </div>
      {query.isError ? <FeedbackState variant="error" error={query.error} onRetry={() => void query.refetch()} /> : query.isLoading ? (
        <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">{Array.from({ length: 4 }, (_, index) => <Skeleton key={index} className="h-28" />)}</div>
      ) : value ? (
        <>
          <dl className="grid grid-cols-2 overflow-hidden rounded-lg border border-border bg-card lg:grid-cols-4">
            {[
              ['决策总数', formatCount(value.Total), '完成意图判断的消息'],
              ['已回复', formatCount(value.Replies), '决策结果为回复'],
              ['已沉默', formatCount(value.Silent), '决策结果为沉默'],
              ['平均语义耗时', `${value.AverageLatencyMS.toFixed(0)} ms`, '语义识别平均用时'],
            ].map(([label, count, hint], index) => (
              <div key={label} className={cn('min-w-0 p-5', index % 2 === 1 && 'border-l border-border', index > 1 && 'border-t border-border lg:border-t-0', index === 2 && 'lg:border-l')}>
                <dt className="text-meta text-muted-foreground">{label}</dt>
                <dd className="mt-3 text-2xl font-semibold tracking-tight tabular-nums">{count}</dd>
                <p className="mt-2 text-caption text-muted-foreground">{hint}</p>
              </div>
            ))}
          </dl>
          <section className="flex flex-col justify-between gap-4 border-b border-border pb-6 sm:flex-row sm:items-start">
            <div><h2 className="text-sm font-medium">语义服务</h2><p className="mt-1 text-meta text-muted-foreground">调用成功率表示服务返回可用结果，不代表判断准确率。</p></div>
            <dl className="flex shrink-0 flex-wrap gap-x-8 gap-y-3 text-sm">
              {[['语义调用', formatCount(value.SemanticCalls)], ['调用成功率', value.SemanticCalls ? `${(value.SemanticSuccess / value.SemanticCalls * 100).toFixed(1)}%` : '—'], ['降级率', value.SemanticCalls ? `${(value.Degraded / value.SemanticCalls * 100).toFixed(1)}%` : '—']].map(([label, count]) => <div key={label}><dt className="text-meta text-muted-foreground">{label}</dt><dd className="mt-1 font-medium tabular-nums">{count}</dd></div>)}
            </dl>
          </section>
        </>
      ) : null}
      <DecisionLog recent onViewAll={onViewAll} />
    </div>
  )
}

function DecisionLog({ recent = false, onViewAll }: { recent?: boolean; onViewAll?: () => void }) {
  const { selectedBotId, dbConfigured } = useAccountContext()
  const [selected, setSelected] = useState<number | null>(null)
  const [conversation, setConversation] = useState('')
  const [reason, setReason] = useState('')
  const [cursor, setCursor] = useState(0)
  const decisions = useDecisions(selectedBotId, conversation, 20, cursor, reason)
  const detail = useDecision(selected)
  const rows = decisions.data?.items ?? []
  const visibleRows = recent ? rows.slice(0, 7) : rows
  return (
    <section className="space-y-4" aria-label={recent ? '最近决策' : '决策记录'}>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div><h2 className="text-sm font-medium">{recent ? '最近决策' : '决策记录'}</h2><p className="mt-1 text-meta text-muted-foreground">{recent ? '最新记录，不受上方统计时间范围影响。' : '按会话和原因筛选，查看实际判断过程与投递结果。'}</p></div>
        {recent ? <Button variant="ghost" size="sm" onClick={onViewAll}>查看全部<ArrowRight aria-hidden /></Button> : <Button variant="outline" size="sm" disabled={decisions.isFetching || !dbConfigured || selectedBotId === undefined} onClick={() => void decisions.refetch()}><RefreshCw className={cn(decisions.isFetching && 'animate-spin')} aria-hidden />刷新</Button>}
      </div>
      {!recent ? (
        <div className="flex flex-wrap items-center gap-2">
          <Input className="w-full sm:w-56" aria-label="会话 ID" placeholder="按会话 ID 筛选" value={conversation} onChange={event => { setConversation(event.target.value); setCursor(0); setSelected(null) }} />
          <Input className="w-full sm:w-56" aria-label="原因码筛选" placeholder="按原因码筛选" value={reason} onChange={event => { setReason(event.target.value); setCursor(0); setSelected(null) }} />
        </div>
      ) : null}
      {selectedBotId === undefined ? <FeedbackState variant="empty" title="先选择机器人账号" description="决策记录属于具体账号。" /> : !dbConfigured ? <FeedbackState variant="unconfigured" title="未配置数据库" description="配置数据库后即可查看决策记录。" /> : decisions.isError ? <FeedbackState variant="error" title="读取决策记录失败" error={decisions.error} onRetry={() => void decisions.refetch()} /> : decisions.isLoading ? <Skeleton className="h-64 w-full" /> : rows.length === 0 ? <FeedbackState variant="empty" title={conversation || reason ? '没有匹配的决策记录' : cursor ? '没有更早的决策记录' : '还没有决策记录'} description="机器人完成意图判定后，可在这里查看它的判断过程。" /> : (
        <div className="overflow-hidden rounded-lg border border-border bg-card">
          <Table label="意图决策记录">
            <TableHeader><TableRow><TableHead>决策 / 会话</TableHead><TableHead>原因</TableHead><TableHead>模型</TableHead><TableHead>时间</TableHead><TableHead className="text-right">耗时</TableHead></TableRow></TableHeader>
            <TableBody>{visibleRows.map(entry => (
              <TableRow key={entry.id} className="cursor-pointer" onClick={() => setSelected(entry.id)}>
                <TableCell><button type="button" className="flex flex-col items-start gap-1.5 text-left" aria-label={`查看决策 ${entry.id}`} onClick={event => { event.stopPropagation(); setSelected(entry.id) }}><Badge variant={KIND_VARIANTS[entry.decision]}>{DECISION_KIND_LABELS[entry.decision]}</Badge><span className="max-w-56 truncate font-mono text-caption text-muted-foreground">{entry.conversationId || `#${entry.id}`}</span></button></TableCell>
                <TableCell className="font-mono text-meta">{entry.reasonCode || '—'}</TableCell>
                <TableCell className="text-meta text-muted-foreground">{entry.model || '未调用语义模型'}</TableCell>
                <TableCell className="whitespace-nowrap text-meta text-muted-foreground" title={entry.createdAt ? formatDateTime(entry.createdAt) : undefined}>{entry.createdAt ? formatRelativeTime(entry.createdAt) : '—'}</TableCell>
                <TableCell className="text-right text-meta text-muted-foreground tabular-nums">{entry.latencyMs} ms</TableCell>
              </TableRow>
            ))}</TableBody>
          </Table>
        </div>
      )}
      {!recent ? <div className="flex items-center justify-between gap-2 border-t border-border pt-3"><span className="text-meta text-muted-foreground">{cursor ? '正在查看历史记录' : '最新记录'}</span><div className="flex gap-2"><Button variant="outline" size="sm" disabled={!cursor || decisions.isFetching} onClick={() => { setSelected(null); setCursor(0) }}>最新记录</Button><Button variant="outline" size="sm" disabled={!rows.length || decisions.isFetching} onClick={() => { setSelected(null); setCursor(rows.at(-1)!.id) }}>更早记录</Button></div></div> : null}
      <Sheet open={selected !== null} onOpenChange={open => { if (!open) setSelected(null) }}>
        <SheetContent side="right" className="w-full overflow-y-auto bg-background p-6 sm:max-w-xl">
          <div className="border-b border-border pb-5 pr-6"><SheetTitle className="text-lg font-semibold">决策详情</SheetTitle><SheetDescription className="mt-2 leading-relaxed">还原策略、规则、语义与最终投递的实际过程。</SheetDescription></div>
          {detail.isLoading ? <Skeleton className="mt-4 h-40 w-full" /> : detail.isError ? <FeedbackState variant="error" title="读取决策详情失败" error={detail.error} onRetry={() => void detail.refetch()} /> : detail.data ? <><StepTimeline detail={detail.data} /><OutboundTimeline botId={detail.data.botId} jobId={detail.data.jobId} /></> : null}
        </SheetContent>
      </Sheet>
    </section>
  )
}

function StepTimeline({ detail }: { detail: DecisionDetailEntry }) {
  const rawThresholds = detail.steps.find(step => step.phase === 'final')?.detail.thresholds
  const thresholds = rawThresholds && typeof rawThresholds === 'object' ? rawThresholds as Record<string, unknown> : undefined
  const thresholdFor = (key: string) => thresholds?.[key === 'appropriate_to_join' ? 'interjection' : key === 'addressed_to_other' ? 'addressed_other_max' : 'continuation']
  return (
    <div className="mt-4 space-y-3">
 {detail.steps.some(step=>step.phase==='semantic' && step.detail.available===false) ? <p role="alert" className="rounded-md border border-warning p-3 text-sm">语义服务不可用，已按既有规则降级。以下零值不代表真实语义分数。</p>:null}
      <div className="text-meta text-muted-foreground">
        {detail.createdAt ? formatDateTime(detail.createdAt) : '时间未知'} · job {detail.jobId}
        {detail.rulesetVersion ? ` · 规则版本 ${detail.rulesetVersion}` : ''}
      </div>
      {!thresholds ? <p className="text-xs text-muted-foreground">此历史记录未保存阈值快照，无法还原当时的阈值比较。</p> : null}
      {detail.steps.length === 0 ? (
        <FeedbackState
          variant="empty"
          title="该记录没有步骤"
          description="步骤落库上线前的历史决策只保存了最终结论。"
        />
      ) : (
        <ol className="space-y-4 border-l-2 border-primary/30 pl-4">
          {detail.steps.map((step) => (
            <li key={step.index} className="relative rounded-xl border border-primary/25 bg-gradient-to-br from-primary/10 to-transparent p-4 before:absolute before:-left-[23px] before:top-5 before:size-3 before:rounded-full before:bg-primary before:shadow-[0_0_12px_hsl(var(--primary)/0.5)]">
              <div className="flex items-center justify-between gap-2">
                <span className="text-sm font-medium">
                  {DECISION_PHASE_LABELS[step.phase] ?? step.phase}
                </span>
                {step.reasonCode ? (
                  <span className="font-mono text-meta text-muted-foreground">
                    {step.reasonCode}
                  </span>
                ) : null}
              </div>
              {Array.isArray(step.detail.evaluations) ? <ol className="mt-3 space-y-2">{step.detail.evaluations.map((raw,index) => {
                if(!raw || typeof raw !== 'object') return null
                const row=raw as Record<string,unknown>
                return <li key={index} className="rounded-lg bg-background/40 p-2 text-xs">{row.Scope==='global'?'全局规则':'指定会话规则'} #{String(row.RulesetID)} · v{String(row.Version)} · 优先级 {String(row.Priority)}<Badge className="ml-2" variant={row.Status==='matched'?'success':'secondary'}>{row.Status==='matched'?'命中':row.Status==='disabled'?'已关闭':'未命中'}</Badge><span className="ml-2 text-muted-foreground">{String(row.ReasonCode ?? '')}</span></li>
              })}</ol> : null}
              <dl className="mt-2 grid grid-cols-2 gap-x-3 gap-y-1 text-meta">
                {Object.entries(step.detail).filter(([key]) => key!=='evaluations').map(([key, value]) => (
                  <div key={key} className="contents">
                    <dt className="truncate text-muted-foreground">{key}</dt>
                    <dd className="truncate font-mono">{formatDetail(value)}</dd>
                  </div>
                ))}
              </dl>
            </li>
          ))}
        </ol>
      )}
      {Object.keys(detail.scores).length > 0 ? (
        <div className="rounded-md bg-muted/40 p-3 text-meta">
          <div className="mb-1 font-medium">语义分数</div>
          <div className="grid grid-cols-2 gap-x-3 gap-y-1">
            {Object.entries(detail.scores).map(([key, value]) => (
              <div key={key} className="contents">
                <span className="truncate text-muted-foreground">{key}</span>
                <span className="font-mono">{value.toFixed(3)}{typeof thresholdFor(key) === 'number' ? <span className="ml-2 text-xs text-muted-foreground">条件 {key === 'addressed_to_other' ? '<' : '≥'} {String(thresholdFor(key))} · {(key === 'addressed_to_other' ? value < Number(thresholdFor(key)) : value >= Number(thresholdFor(key))) ? '满足' : '未满足'}</span> : null}<span className="mt-1 block h-1.5 overflow-hidden rounded-full bg-primary/10"><span className="block h-full rounded-full bg-primary" style={{width:`${Math.max(0,Math.min(1,value))*100}%`}} /></span></span>
              </div>
            ))}
          </div>
        </div>
      ) : null}
    </div>
  )
}

function formatDetail(value: unknown): string {
  if (typeof value === 'string') return value || '—'
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  if (value == null) return '—'
  return JSON.stringify(value)
}

export function DecisionHistoryPage() {
  const { selectedBotId } = useAccountContext()
  return <div className="space-y-6"><PageHeader title="决策记录" actions={<Button asChild variant="outline"><Link to="/decisions">返回意图概览</Link></Button>} /><DecisionLog key={selectedBotId ?? 'none'} /></div>
}
