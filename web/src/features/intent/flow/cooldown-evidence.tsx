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
  if (typeof event.ElapsedMS === 'number') rows.push(['实际间隔', `${event.ElapsedMS} ms`])
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
