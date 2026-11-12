import { Badge } from '../../../components/ui/badge'
import { FeedbackState } from '../../../components/feedback-state'
import { DECISION_PHASE_LABELS, type DecisionDetailEntry } from '../../../lib/api/contracts'
import { formatDateTime } from '../../../lib/format'

/**
 * Legacy linear timeline. Rows recorded before the fixed-flow snapshot existed
 * keep their original lower-case step details, so this view is still the only
 * faithful representation for those decisions.
 * 旧记录只保存了线性 step 与小写 detail；历史缺快照时继续展示原时间线，
 * 不拿当前流程定义补造节点结果。
 */
export function LegacyStepTimeline({ detail }: { detail: DecisionDetailEntry }) {
  const rawThresholds = detail.steps.find((step) => step.phase === 'final')?.detail.thresholds
  const thresholds =
    rawThresholds && typeof rawThresholds === 'object'
      ? (rawThresholds as Record<string, unknown>)
      : undefined
  const thresholdFor = (key: string) =>
    thresholds?.[
      key === 'appropriate_to_join'
        ? 'interjection'
        : key === 'addressed_to_other'
          ? 'addressed_other_max'
          : 'continuation'
    ]
  return (
    <div className="mt-4 space-y-3">
      {detail.steps.some(
        (step) =>
          step.phase === 'semantic' && step.detail.called === true && step.detail.available === false,
      ) ? (
        <p role="alert" className="rounded-md border border-warning p-3 text-sm">
          语义服务不可用，已按既有规则降级。以下零值不代表真实语义分数。
        </p>
      ) : null}
      <div className="text-meta text-muted-foreground">
        {detail.createdAt ? formatDateTime(detail.createdAt) : '时间未知'} · job {detail.jobId}
        {detail.rulesetVersion ? ` · 规则版本 ${detail.rulesetVersion}` : ''}
      </div>
      {!thresholds ? (
        <p className="text-xs text-muted-foreground">
          此历史记录未保存阈值快照，无法还原当时的阈值比较。
        </p>
      ) : null}
      {detail.steps.length === 0 ? (
        <FeedbackState
          variant="empty"
          title="该记录没有步骤"
          description="步骤落库上线前的历史决策只保存了最终结论。"
        />
      ) : (
        <ol className="space-y-4 border-l-2 border-primary/30 pl-4">
          {detail.steps.map((step) => (
            <li
              key={step.index}
              className="relative rounded-xl border border-primary/25 bg-gradient-to-br from-primary/10 to-transparent p-4 before:absolute before:-left-[23px] before:top-5 before:size-3 before:rounded-full before:bg-primary before:shadow-[0_0_12px_hsl(var(--primary)/0.5)]"
            >
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
              {Array.isArray(step.detail.evaluations) ? (
                <ol className="mt-3 space-y-2">
                  {step.detail.evaluations.map((raw, index) => {
                    if (!raw || typeof raw !== 'object') return null
                    const row = raw as Record<string, unknown>
                    return (
                      <li key={index} className="rounded-lg bg-background/40 p-2 text-xs">
                        {row.Scope === 'global' ? '全局规则' : '指定会话规则'} #{String(row.RulesetID)} · v
                        {String(row.Version)} · 优先级 {String(row.Priority)}
                        <Badge
                          className="ml-2"
                          variant={row.Status === 'matched' ? 'success' : 'secondary'}
                        >
                          {row.Status === 'matched'
                            ? '命中'
                            : row.Status === 'disabled'
                              ? '已关闭'
                              : '未命中'}
                        </Badge>
                        <span className="ml-2 text-muted-foreground">
                          {String(row.ReasonCode ?? '')}
                        </span>
                      </li>
                    )
                  })}
                </ol>
              ) : null}
              <dl className="mt-2 grid grid-cols-2 gap-x-3 gap-y-1 text-meta">
                {Object.entries(step.detail)
                  .filter(([key]) => key !== 'evaluations')
                  .map(([key, value]) => (
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
                <span className="font-mono">
                  {value.toFixed(3)}
                  {typeof thresholdFor(key) === 'number' ? (
                    <span className="ml-2 text-xs text-muted-foreground">
                      条件 {key === 'addressed_to_other' ? '<' : '≥'} {String(thresholdFor(key))} ·{' '}
                      {(key === 'addressed_to_other'
                        ? value < Number(thresholdFor(key))
                        : value >= Number(thresholdFor(key)))
                        ? '满足'
                        : '未满足'}
                    </span>
                  ) : null}
                  <span className="mt-1 block h-1.5 overflow-hidden rounded-full bg-primary/10">
                    <span
                      className="block h-full rounded-full bg-primary"
                      style={{ width: `${Math.max(0, Math.min(1, value)) * 100}%` }}
                    />
                  </span>
                </span>
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
