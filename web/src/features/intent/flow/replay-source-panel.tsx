import { Badge } from '../../../components/ui/badge'
import type { IntentReplayInfo } from '../../../lib/api/contracts'

const FIELDS: Array<{ key: keyof Pick<IntentReplayInfo, 'input' | 'semantic' | 'prompt' | 'policy' | 'rules'>; label: string }> = [
  { key: 'input', label: '输入' },
  { key: 'semantic', label: '语义' },
  { key: 'prompt', label: 'Prompt' },
  { key: 'policy', label: '策略' },
  { key: 'rules', label: '规则' },
]

const SOURCE_LABELS: Record<string, string> = {
  snapshot: '快照',
  current: '当前',
}

function formatVerifiedTokens(label: string, tokens?: number, covered?: number, requests?: number, countUnknown?: boolean): string {
  const verified = tokens !== undefined && covered !== undefined && covered > 0
  const totalUnknown = requests === undefined || Boolean(countUnknown) || (covered ?? 0) < requests
  if (requests === 0 && !countUnknown) return `${label} token 总量 0（无外部请求）`
  const evidence = verified ? `已核实 ${tokens}（${covered} 笔）` : '已核实 0 笔'
  return `${label} token ${evidence}${totalUnknown || !verified ? '，总量未知' : ''}`
}

export function ReplayUsagePanel({ replay }: { replay?: IntentReplayInfo }) {
  if (!replay || replay.externalRequests === undefined) return null
  const { budget, externalRequests, requestCountUnknown } = replay
  return (
    <div className="space-y-1 text-xs text-muted-foreground">
      <p>{requestCountUnknown
        ? `已确认至少 ${externalRequests} 笔，物理请求总数未知。`
        : `实际外部请求 ${externalRequests} 次。`}</p>
      <p>{formatVerifiedTokens('输入', replay.inputTokens, replay.inputUsageResponses ?? replay.usageResponses, externalRequests, requestCountUnknown)}。</p>
      <p>{formatVerifiedTokens('输出', replay.outputTokens, replay.outputUsageResponses ?? replay.usageResponses, externalRequests, requestCountUnknown)}。</p>
      {budget ? <p>请求次数预算剩余 {budget.remainingRequests} / {budget.requestLimit}。</p> : null}
      {budget?.remainingAmountMicros !== undefined && budget.amountLimitMicros !== undefined ? (
        <p>金额预算剩余 {budget.remainingAmountMicros} / {budget.amountLimitMicros} 微单位 {budget.currency}。</p>
      ) : null}
    </div>
  )
}

/** Shows which parts of a replay/recalculate result come from the snapshot and which from current config. */
export function ReplaySourcePanel({ replay }: { replay?: IntentReplayInfo }) {
  if (!replay) {
    return null
  }
  const items = FIELDS.map(({ key, label }) => ({
    key,
    label,
    source: replay[key],
    text: SOURCE_LABELS[replay[key]] ?? '未记录',
  }))
  const hasCurrent = items.some((item) => item.source === 'current')
  const hasUnknown = items.some((item) => item.source !== 'snapshot' && item.source !== 'current')
  const allSnapshot =
    !hasCurrent && !hasUnknown && items.every((item) => item.source === 'snapshot')
  return (
    <div
      data-testid="replay-source-panel"
      className="space-y-2 rounded-md border border-border/70 bg-muted/25 p-3"
    >
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-xs font-medium">来源标记</span>
        <Badge variant={hasCurrent ? 'warning' : allSnapshot ? 'secondary' : 'destructive'}>
          {hasCurrent ? '混合版本' : allSnapshot ? '全部来自快照' : '来源不完整'}
        </Badge>
        <span className="text-xs text-muted-foreground">仅预览 · 不写库 · 不发送</span>
      </div>
      <dl aria-label="来源标记明细" className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs sm:grid-cols-5">
        {items.map((item) => (
          <div key={item.key} className="flex items-baseline justify-between gap-2 sm:block">
            <dt className="text-muted-foreground">{item.label}</dt>
            <dd className={item.source === 'current' ? 'font-medium text-warning' : ''}>
              {item.text}
            </dd>
          </div>
        ))}
      </dl>
      <p className="text-xs text-muted-foreground">
        {hasCurrent
          ? '该结果由已保存快照与当前配置混合计算，不是原执行的完全复现。'
          : allSnapshot
            ? '该结果全部来自已保存快照。'
            : '来源不完整，存在未记录项，无法判断是否为原执行复现。'}
        {hasUnknown && hasCurrent ? ' 部分来源未记录。' : ''}
        {replay.persisted ? '' : ' Persisted=false，不写入决策记录，也不会发送消息。'}
      </p>
    </div>
  )
}
