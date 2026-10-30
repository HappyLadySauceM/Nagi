import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { apiRequest } from '../../lib/api/client'
import { asRecord, readNumber } from '../../lib/api/adapters/guard'
import { Select } from '../../components/ui/select'
import { RulesPage, SimulationPanel } from './rules-page'
import { Input } from '../../components/ui/input'
import { OutboundTimeline } from './outbound-timeline'
import { RefreshCw } from 'lucide-react'
import { Badge } from '../../components/ui/badge'
import { Button } from '../../components/ui/button'
import { Card } from '../../components/ui/card'
import { FeedbackState } from '../../components/feedback-state'
import { PageHeader } from '../../components/page-header'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '../../components/ui/sheet'
import { Skeleton } from '../../components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '../../components/ui/table'
import {
  DECISION_KIND_LABELS,
  DECISION_PHASE_LABELS,
  type DecisionDetailEntry,
  type DecisionEntry,
} from '../../lib/api/contracts'
import { formatDateTime, formatRelativeTime } from '../../lib/format'
import { useAccountContext } from '../accounts/account-context'
import { useDecision, useDecisions } from './queries'

const KIND_VARIANTS: Record<DecisionEntry['decision'], 'success' | 'secondary' | 'warning'> = {
  reply: 'success',
  silent: 'secondary',
  defer: 'warning',
}

export function IntentPage() {
 const {selectedBotId}=useAccountContext()
 return <IntentWorkspace key={selectedBotId ?? 'none'} />
}
function IntentWorkspace() {
 const [tab,setTab]=useState('overview')
 const tabs=[['overview','运行概览'],['rules','规则管理'],['decisions','决策记录'],['simulate','模拟验证']]
 return <div className="space-y-5">
  <PageHeader title="意图识别" description="从授权、规则、语义分数到最终投递，查看每一步实际发生了什么。" />
  <div role="tablist" aria-label="意图识别模块" className="flex flex-wrap gap-2 rounded-xl border border-primary/25 bg-primary/5 p-2">
   {tabs.map(([key,label])=><Button role="tab" aria-selected={tab===key} key={key} variant={tab===key?'default':'ghost'} onClick={()=>{
    if(key!==tab && !window.dispatchEvent(new Event('nagi:account-changing',{cancelable:true}))) return
    setTab(key)
   }}>{label}</Button>)}
  </div>
  {tab==='overview'?<IntentOverview/>:null}
  {tab==='rules'?<RulesPage/>:null}
  {tab==='decisions'?<DecisionLog/>:null}
  {tab==='simulate'?<SimulationPanel/>:null}
 </div>
}
function IntentOverview() {
 const {selectedBotId}=useAccountContext()
 const [hours,setHours]=useState('24')
 const query=useQuery({
  queryKey:['intent','overview',selectedBotId,hours],
  enabled:selectedBotId!==undefined,
  queryFn:async({signal})=>{
   const raw=await apiRequest(`/api/v1/intent/overview?bot_id=${selectedBotId}&hours=${hours}`,{signal})
   const row=asRecord(raw,'/api/v1/intent/overview','overview')
   return Object.fromEntries(['Total','SemanticCalls','SemanticSuccess','Degraded','Replies','Silent','AverageLatencyMS'].map(key=>[key,readNumber(row,key,'/api/v1/intent/overview',0)]))
  },
 })
 const value=query.data
 return <div className="space-y-4">
  <div className="flex items-center gap-3"><Select aria-label="统计时间范围" value={hours} onChange={e=>setHours(e.target.value)}><option value="24">最近 24 小时</option><option value="168">最近 7 天</option><option value="720">最近 30 天</option></Select><Button variant="outline" disabled={query.isFetching} onClick={()=>void query.refetch()}>刷新</Button></div>
  {query.isError?<FeedbackState variant="error" error={query.error} onRetry={()=>void query.refetch()}/>:query.isLoading?<Skeleton className="h-40 w-full"/>:value?<div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
   {[['决策总数',value.Total],['语义调用',value.SemanticCalls],['调用成功率',value.SemanticCalls ? `${(value.SemanticSuccess/value.SemanticCalls*100).toFixed(1)}%`:'—'],['降级率',value.SemanticCalls ? `${(value.Degraded/value.SemanticCalls*100).toFixed(1)}%`:'—'],['平均语义耗时',`${value.AverageLatencyMS.toFixed(0)} ms`],['回复',value.Replies],['沉默',value.Silent]].map(([label,count])=><Card key={label} className="border-primary/20 bg-gradient-to-br from-primary/10 to-transparent p-5"><p className="text-sm text-muted-foreground">{label}</p><p className="mt-3 text-3xl font-semibold tabular-nums">{count}</p></Card>)}
  </div>:null}
  <div className="grid gap-3 sm:grid-cols-5">{['策略约束','范围与规则','语义识别','阈值判断','回复与投递'].map((label,i)=><div key={label} className="rounded-xl border border-primary/30 bg-primary/5 p-4"><span className="mb-3 flex size-8 items-center justify-center rounded-full bg-primary/20 text-primary motion-safe:animate-pulse">{i+1}</span><span className="text-sm">{label}</span></div>)}</div>
  <p className="text-sm text-muted-foreground">调用成功率表示语义服务返回可用结果，不代表判断准确率。严格阈值保持服务当前配置；具体分数、阈值与降级原因可在决策记录中查看。</p>
 </div>
}
function DecisionLog() {
  const { selectedBotId, dbConfigured } = useAccountContext()
  const [selected, setSelected] = useState<number | null>(null)
  const [conversation, setConversation] = useState('')
 const [reason, setReason] = useState('')
 const [cursor,setCursor] = useState(0)
 const decisions = useDecisions(selectedBotId, conversation, 20, cursor, reason)
  const detail = useDecision(selected)

  return (
    <div className="space-y-4">
      <PageHeader
        title="决策记录"
        description="每条消息为什么回复、为什么沉默，都能在这里逐条还原；数据来自意图流水线落库的决策与步骤。"
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={() => void decisions.refetch()}
            disabled={decisions.isFetching || !dbConfigured}
          >
            <RefreshCw className={decisions.isFetching ? 'animate-spin' : undefined} />
            刷新
          </Button>
        }
      />

      <div className="flex flex-wrap gap-2"><Input aria-label="会话 ID" placeholder="按会话 ID 筛选" value={conversation} onChange={e=>{setConversation(e.target.value);setCursor(0);setSelected(null)}} /><Input aria-label="原因码筛选" placeholder="按原因码筛选" value={reason} onChange={e=>{setReason(e.target.value);setCursor(0);setSelected(null)}} /><Button variant="outline" onClick={()=>setCursor(0)}>最新记录</Button><Button variant="outline" disabled={!decisions.data?.items.length || decisions.isFetching} onClick={()=>{setSelected(null);setCursor(decisions.data!.items.at(-1)!.id)}}>更早记录</Button></div>
      {!dbConfigured ? (
        <FeedbackState
          variant="unconfigured"
          title="未配置数据库"
          description="决策记录依赖数据库；配置 database.dsn 后自动开启。"
        />
      ) : decisions.isError ? (
        <FeedbackState
          variant="error"
          title="读取决策记录失败"
          error={decisions.error}
          onRetry={() => void decisions.refetch()}
        />
      ) : decisions.isLoading ? (
        <Skeleton className="h-64 w-full" />
      ) : decisions.data && decisions.data.items.length === 0 ? (
        <FeedbackState
          variant="empty"
          title="还没有决策记录"
          description="机器人收到消息并完成一次意图判定后，记录会出现在这里。"
        />
      ) : (
        <Card className="overflow-hidden">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>时间</TableHead>
                <TableHead>决策</TableHead>
                <TableHead>原因码</TableHead>
                <TableHead>模型</TableHead>
                <TableHead className="text-right">耗时</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {decisions.data?.items.map((entry) => (
                <TableRow
                  key={entry.id}
                  className="cursor-pointer"
                  onClick={() => setSelected(entry.id)}
                >
                  <TableCell className="whitespace-nowrap text-meta text-muted-foreground">
                    {entry.createdAt ? formatRelativeTime(entry.createdAt) : '—'}
                  </TableCell>
                  <TableCell>
                    <Badge variant={KIND_VARIANTS[entry.decision]}>
                      {DECISION_KIND_LABELS[entry.decision]}
                    </Badge>
                  </TableCell>
                  <TableCell className="font-mono text-meta">{entry.reasonCode || '—'}</TableCell>
                  <TableCell className="text-meta text-muted-foreground">
                    {entry.model || '—'}
                  </TableCell>
                  <TableCell className="text-right text-meta text-muted-foreground">
                    {entry.latencyMs} ms
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}

      <Sheet open={selected !== null} onOpenChange={(open) => !open && setSelected(null)}>
        <SheetContent side="right" className="w-full overflow-y-auto p-5 sm:max-w-xl">
          <SheetTitle>决策详情</SheetTitle>
          <SheetDescription>
            按判定顺序列出策略、规则、语义与结论；语义步骤展示实际 Provider 与模型；未调用时跳过。
          </SheetDescription>
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
            <><StepTimeline detail={detail.data} /><OutboundTimeline botId={detail.data.botId} jobId={detail.data.jobId} /></>
          ) : null}
        </SheetContent>
      </Sheet>
    </div>
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
