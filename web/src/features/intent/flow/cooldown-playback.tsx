import { useEffect, useState } from 'react'
import { Button } from '../../../components/ui/button'
import type { IntentRun } from '../../../lib/api/contracts'
import { cn } from '../../../lib/utils'
import { reasonText } from './reason-text'

const labels = {
  blocked: '直接拦截', deferred: '候选话题暂缓', bypassed: '合格续接，绕过冷却',
  restored: '恢复冷却并拦截', equal: '间隔等于阈值，放行', disabled: '冷却已关闭',
  input_missing: '间隔输入不可用', passed: '间隔超过阈值，放行',
  not_executed: '冷却未执行', not_revisited: '其他规则结束判定，冷却未恢复',
} as const

type CooldownEvent = {
  RuleID: 'cooldown'
  Version: string
  Sequence: number
  Attempt?: number
  Branch: keyof typeof labels
  Operator?: string
  ElapsedMS?: number
  ThresholdMS?: number
  PrecisionMS?: number
  InputSource?: string
  PolicySource?: string
  PolicyVersion?: number
  TemplateVersion?: number
  TopicSource?: string
  ReasonCode?: string
}

function readEvents(detail?: Record<string, unknown>): CooldownEvent[] {
  const events = detail?.RuleEvents
  if (!Array.isArray(events)) return []
  return events.filter((value): value is CooldownEvent => {
    if (!value || typeof value !== 'object') return false
    const event = value as Record<string, unknown>
    return event.RuleID === 'cooldown' && event.Version === 'cooldown-v1' &&
      Number.isSafeInteger(event.Sequence) && typeof event.Branch === 'string' &&
      Object.prototype.hasOwnProperty.call(labels, event.Branch)
  })
}

const inputs: Record<string, string> = {
  activity_stats: '发言活动统计', authorized_history: '已授权近期历史', unavailable: '无可用来源',
}
const sources: Record<string, string> = { independent: '会话独立策略', default: '账号默认策略' }
const topics: Record<string, string> = { quote: '消息引用', supplement: '本人补材料', semantic: '语义归属' }

function Evidence({ event }: { event: CooldownEvent }) {
  const rows: Array<[string, string | number]> = [['规则与版本', `${event.RuleID} · ${event.Version}`]]
  if (event.Attempt) rows.push(['任务尝试', event.Attempt])
  if (typeof event.ThresholdMS === 'number') rows.push(['生效阈值', `${event.ThresholdMS} ms`])
  if (typeof event.ElapsedMS === 'number' && typeof event.ThresholdMS === 'number' && event.Operator === '<') {
    rows.push(['比较条件', `${event.ElapsedMS} ms < ${event.ThresholdMS} ms`])
  }
  if (event.PrecisionMS) rows.push(['间隔精度', `整秒换算，步长 ${event.PrecisionMS} ms`])
  if (event.InputSource) rows.push(['间隔来源', inputs[event.InputSource] ?? '未识别'])
  if (event.PolicySource) rows.push(['配置来源', sources[event.PolicySource] ?? '未识别'])
  if (event.PolicyVersion) rows.push(['策略版本', event.PolicyVersion])
  if (event.TemplateVersion) rows.push(['模板版本', event.TemplateVersion])
  if (event.TopicSource) rows.push(['续接依据', topics[event.TopicSource] ?? '未识别'])
  if (event.ReasonCode) rows.push(['结论原因', reasonText(event.ReasonCode)])
  return <dl className="space-y-2 text-xs">
    {rows.map(([label, value]) => <div className="grid grid-cols-[5rem_minmax(0,1fr)] gap-2" key={label}>
      <dt className="text-muted-foreground">{label}</dt><dd className="break-words font-medium">{value}</dd>
    </div>)}
  </dl>
}

function MissingEvidence() {
  return <p className="mt-2 text-sm text-muted-foreground">历史记录缺少冷却执行证据，实际比较和分支未知。</p>
}

export function CooldownNodeEvidence({ detail }: { detail: Record<string, unknown> }) {
  const events = readEvents(detail)
  if (!events.length) return <MissingEvidence />
  return <ol className="space-y-3" aria-label="冷却节点证据">
    {events.map(event => <li key={event.Sequence} className="space-y-2 rounded-md border p-3">
      <p className="text-sm font-medium">{labels[event.Branch]}</p>
      <Evidence event={event} />
    </li>)}
  </ol>
}

function PlaybackSteps({ events }: { events: CooldownEvent[] }) {
  const last = events.length - 1
  const [selected, setSelected] = useState(last)
  const [playing, setPlaying] = useState(false)
  const [animation, setAnimation] = useState(0)
  const event = events[selected] ?? events[last]

  useEffect(() => {
    if (!playing) return
    const timer = window.setTimeout(() => {
      if (selected >= last) setPlaying(false)
      else {
        setSelected(selected + 1)
        setAnimation(value => value + 1)
      }
    }, 900)
    return () => window.clearTimeout(timer)
  }, [last, playing, selected, animation])

  function seek(index: number) {
    setPlaying(false)
    setSelected(Math.max(0, Math.min(index, last)))
    setAnimation(value => value + 1)
  }

  return <section aria-label="冷却判定回放" className="rounded-xl border border-border bg-card p-4 focus-visible:outline-ring"
    tabIndex={0} onKeyDown={e => {
      if (e.target !== e.currentTarget || (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight')) return
      e.preventDefault()
      seek(selected + (e.key === 'ArrowRight' ? 1 : -1))
    }}>
    <div className="flex flex-wrap items-center justify-between gap-3">
      <h3 className="text-sm font-semibold">冷却判定回放</h3>
      <div className="flex flex-wrap gap-2" aria-label="冷却回放控制">
        <Button variant="outline" size="sm" onClick={() => { seek(0); setPlaying(true) }} type="button">从头播放</Button>
        <Button variant="outline" size="sm" disabled={!playing} onClick={() => setPlaying(false)} type="button">暂停</Button>
        <Button variant="outline" size="sm" disabled={selected === 0} onClick={() => seek(selected - 1)} type="button">上一步</Button>
        <Button variant="outline" size="sm" disabled={selected >= last} onClick={() => seek(selected + 1)} type="button">下一步</Button>
        <Button variant="outline" size="sm" onClick={() => seek(last)} type="button">跳到决定步骤</Button>
      </div>
    </div>
    <div className="mt-4 grid gap-4 lg:grid-cols-[minmax(9rem,1fr)_minmax(13rem,1.3fr)_minmax(12rem,1fr)]">
      <ol className="space-y-1" aria-label="执行顺序">
        {events.map((item, index) => <li key={item.Sequence}>
          <button type="button" aria-current={index === selected ? 'step' : undefined} onClick={() => seek(index)}
            className={cn('w-full rounded-md px-2 py-2 text-left text-xs hover:bg-muted focus-visible:outline-ring',
              index === selected && 'bg-primary/10 font-semibold')}>
            {item.Sequence}. {labels[item.Branch]}
          </button>
        </li>)}
      </ol>
      <ol className="space-y-2 text-xs" aria-label="冷却执行分支">
        {events.map((item, index) => <li key={item.Sequence} data-active={index === selected} data-branch={item.Branch}
          className={cn('rounded-md border px-3 py-2', index === selected && 'border-primary/60 bg-primary/10')}>
          <span className="flex items-center justify-between gap-2">
            <span>{item.Branch === 'not_executed' ? '固定闸门' : index === 0 ? '回复冷却' : '话题判定完成'}</span><span aria-hidden>→</span><span>{labels[item.Branch]}</span>
          </span>
          {index === selected && <span aria-hidden className="relative mt-2 block h-px bg-primary/40">
            <span key={animation} className={cn('absolute -top-1 right-0 size-2 rounded-full bg-primary',
              playing && 'motion-safe:animate-[nagi-cooldown-edge_650ms_ease-out_both]')} />
          </span>}
        </li>)}
      </ol>
      <div aria-live="polite" className="rounded-md border p-3">
        <p className="mb-2 text-xs font-semibold">{labels[event.Branch]}</p>
        <Evidence event={event} />
      </div>
    </div>
    <style>{'@keyframes nagi-cooldown-edge { from { right: calc(100% - 0.5rem); } to { right: 0; } }'}</style>
  </section>
}

export function CooldownPlayback({ run }: { run: IntentRun }) {
  const events = readEvents(run.nodes.find(node => node.nodeId === 'rules')?.detail)
  if (!events.length) return <section aria-label="冷却判定回放" className="rounded-xl border bg-card p-4">
    <h3 className="text-sm font-semibold">冷却判定回放</h3>
    <MissingEvidence />
  </section>
  return <PlaybackSteps key={`${run.decisionId}:${run.runId}:${events.length}`} events={events} />
}
