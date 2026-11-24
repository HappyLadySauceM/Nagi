import { useModels, modelReady } from '../settings/models'
import { allowWorkspaceChange } from './edit-guard'
import { IntentConfig } from './config'
import { useState } from 'react'
import { Link, useNavigate, useParams } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { apiRequest } from '../../lib/api/client'
import { asRecord, readNumber } from '../../lib/api/adapters/guard'
import { Select } from '../../components/ui/select'
import { RulesPage } from './rules-page'
import { LiveDecisionLog } from './live-decision-log'
import { useLiveDecisions } from './use-live-decisions'
import { DecisionDetailSheet } from './flow/decision-detail-sheet'
import { Button } from '../../components/ui/button'
import { FeedbackState } from '../../components/feedback-state'
import { PageHeader } from '../../components/page-header'
import { Skeleton } from '../../components/ui/skeleton'
import { formatCount } from '../../lib/format'
import { cn } from '../../lib/utils'
import { useAccountContext } from '../accounts/account-context'

const TABS = [
  ['overview', '运行概览'], ['rules', '参与判断'], ['config', '识别配置'],
] as const
type IntentTab = typeof TABS[number][0]

export function IntentPage() {
  const { selectedBotId } = useAccountContext()
  return <IntentWorkspace key={selectedBotId ?? 'none'} />
}

function IntentWorkspace() {
  const [tab, setTab] = useState<IntentTab>('overview')
  async function changeTab(next: IntentTab) {
    if (next !== tab && !(await allowWorkspaceChange())) return false
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
            onKeyDown={async event => {
              const next = event.key === 'ArrowRight' ? (index + 1) % TABS.length : event.key === 'ArrowLeft' ? (index + TABS.length - 1) % TABS.length : event.key === 'Home' ? 0 : event.key === 'End' ? TABS.length - 1 : null
              if (next === null) return
              event.preventDefault()
              if (await changeTab(TABS[next][0])) document.getElementById(`intent-tab-${TABS[next][0]}`)?.focus()
            }}>
            {label}
          </button>
        ))}
      </div>
      <div role="tabpanel" id={`intent-panel-${tab}`} aria-labelledby={`intent-tab-${tab}`}>
        {tab === 'overview' ? <IntentOverview /> : null}
        {tab === 'rules' ? <RulesPage embedded /> : null}
 {tab === 'config' ? <IntentConfig/> : null}
      </div>
    </div>
  )
}

function IntentOverview() {
  const { selectedBotId, dbConfigured } = useAccountContext()
  if (selectedBotId === undefined) return <FeedbackState variant="empty" title="先选择机器人账号" description="选择账号后查看它的意图识别数据。" action={<Button asChild variant="outline"><Link to="/accounts">查看账号连接</Link></Button>} />
  if (!dbConfigured) return <FeedbackState variant="unconfigured" title="未配置数据库" description="配置数据库后即可查看意图识别统计与决策记录。" />
  return <div className="space-y-7">
    <LiveDecisionLog botId={selectedBotId} recent renderSummary={live => <DecisionSummary botId={selectedBotId} live={live} />} />
  </div>
}

function DecisionSummary({ botId, live }: { botId: number; live: ReturnType<typeof useLiveDecisions> }) {
  const models = useModels()
  const [hours, setHours] = useState('24')
  const authorized = live.view !== null
  const query = useQuery({
    queryKey: ['intent', 'overview', botId, hours],
    enabled: authorized,
    queryFn: async ({ signal }) => {
      const raw = await apiRequest(`/api/v1/intent/overview?bot_id=${botId}&hours=${hours}`, { signal })
      const row = asRecord(raw, '/api/v1/intent/overview', 'overview')
      return Object.fromEntries(['Total', 'SemanticCalls', 'SemanticSuccess', 'Degraded', 'Replies', 'Silent', 'AverageLatencyMS'].map(key => [key, readNumber(row, key, '/api/v1/intent/overview', 0)]))
    },
  })
  const value = authorized ? query.data : undefined
  return (
    <section className="space-y-4" aria-label="决策摘要">
      {models.data && !modelReady(models.data.Catalog, 'intent') && <div role="status" className="rounded-lg border border-amber-500/30 bg-amber-500/10 p-3 text-sm text-amber-600 dark:text-amber-400">尚未选择可用的默认意图模型，语义判断将降级为规则判断。<Link className="ml-2 underline" to="/settings">前往模型配置</Link></div>}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-sm font-medium">决策摘要</h2>
        <div className="flex items-center gap-2">
          <Select aria-label="统计时间范围" containerClassName="w-36" value={hours} onChange={event => setHours(event.target.value)}>
            <option value="24">最近 24 小时</option><option value="168">最近 7 天</option><option value="720">最近 30 天</option>
          </Select>
          <Button variant="outline" size="icon" aria-label="刷新统计" disabled={!authorized || query.isFetching} onClick={() => void query.refetch()}><RefreshCw className={cn('size-4', query.isFetching && 'animate-spin')} /></Button>
        </div>
      </div>
      {!authorized ? live.error ? <FeedbackState variant="error" title="暂无法读取决策摘要" error={live.error} onRetry={live.reset} /> : <p role="status" className="text-sm text-muted-foreground">正在核验决策摘要授权…</p> : query.isError ? <FeedbackState variant="error" error={query.error} onRetry={() => void query.refetch()} /> : query.isLoading ? (
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
    </section>
  )
}

export function DecisionHistoryPage() {
  const { selectedBotId, dbConfigured } = useAccountContext()
  return <div className="space-y-6">
    <PageHeader title="决策记录" actions={<Button asChild variant="outline"><Link to="/decisions">返回意图概览</Link></Button>} />
    {selectedBotId === undefined ? <FeedbackState variant="empty" title="先选择机器人账号" />
      : !dbConfigured ? <FeedbackState variant="unconfigured" title="未配置数据库" />
        : <LiveDecisionLog key={selectedBotId} botId={selectedBotId} />}
  </div>
}

export function InboundDetailPage() {
  const params = useParams({ from: '/decisions/inbound/$botId/$messageId' })
  const navigate = useNavigate()
  const botId = Number(params.botId)
  const messageId = Number(params.messageId)
  if (!Number.isSafeInteger(botId) || botId <= 0 || !Number.isSafeInteger(messageId) || messageId <= 0) {
    return <FeedbackState variant="error" title="入站地址无效" />
  }
  return <AuthorizedInboundDetail key={`${botId}:${messageId}`} botId={botId} messageId={messageId} onClose={() => void navigate({ to: '/decisions/history' })} />
}

function AuthorizedInboundDetail({ botId, messageId, onClose }: { botId: number; messageId: number; onClose: () => void }) {
  const live = useLiveDecisions(botId)
  if (!live.view) {
    if (live.error) return <FeedbackState variant="error" title="暂无法读取入站详情" error={live.error} onRetry={live.reset} />
    return <p role="status" className="text-sm text-muted-foreground">正在核验入站详情授权…</p>
  }
  return <DecisionDetailSheet inbound={{ botId, messageId }} onClose={onClose} />
}
