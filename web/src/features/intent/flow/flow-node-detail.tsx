import { useState, type ReactNode } from 'react'
import { AlertTriangle, CheckCircle2, CircleHelp, MinusCircle, XCircle } from 'lucide-react'
import { Badge } from '../../../components/ui/badge'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '../../../components/ui/sheet'
import { cn } from '../../../lib/utils'
import type {
  IntentFlowNode,
  IntentRun,
  IntentRunNode,
  IntentSnapshotStatus,
  ReplyMode,
} from '../../../lib/api/contracts'
import { REPLY_MODE_LABELS } from '../../../lib/api/contracts'
import {
  BRANCH_TEXT,
  BUILTIN_RULE_TEXT,
  INTENT_CATEGORY_TEXT,
  REPLY_MODE_TEXT,
  RULE_ACTION_TEXT,
  SCOPE_KIND_TEXT,
  SCORE_TEXT,
  reasonText,
} from './reason-text'

const STATUS_META: Record<
  IntentRunNode['status'],
  { label: string; variant: 'success' | 'secondary' | 'destructive' | 'warning' | 'outline'; icon: typeof CheckCircle2; className: string }
> = {
  executed: { label: '实际执行', variant: 'success', icon: CheckCircle2, className: 'border-success/30 bg-success/10 text-success' },
  skipped: { label: '跳过', variant: 'secondary', icon: MinusCircle, className: 'border-border bg-muted/40 text-muted-foreground' },
  failed: { label: '失败', variant: 'destructive', icon: XCircle, className: 'border-destructive/30 bg-destructive/10 text-destructive' },
  degraded: { label: '降级', variant: 'warning', icon: AlertTriangle, className: 'border-warning/30 bg-warning/10 text-warning' },
  unknown: { label: '未记录', variant: 'outline', icon: CircleHelp, className: 'border-dashed text-muted-foreground' },
}

const SNAPSHOT_WARNING: Record<IntentSnapshotStatus, string> = {
  complete: '',
  partial: '该历史记录的流程快照不完整，未记录字段不会被推测补齐。',
  missing: '该历史记录没有流程快照，节点与分支均不可还原。',
  legacy: '该历史记录产生于流程快照上线前，只保留最终结论。',
}

const SOURCE_LABELS: Record<string, string> = {
  default: '默认模板',
  template: '跟随模板',
  independent: '独立策略',
  builtin: '内置',
  local: '本地',
  langfuse: 'Langfuse（可选）',
}

const STATUS_LABELS: Record<string, string> = {
  prepared: '待发送',
  sending: '发送中',
  sent: '已送达',
  failed: '发送失败',
  unknown: '结果不明，请人工核对，勿重复发送',
  cancelled: '已取消',
  denied: '被拒绝',
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function valueText(value: unknown): string {
  if (value === null || value === undefined || value === '') return '—'
  if (typeof value === 'boolean') return value ? '是' : '否'
  if (typeof value === 'number') return String(value)
  if (typeof value === 'string') return value
  try {
    return JSON.stringify(value)
  } catch {
    return String(value)
  }
}

function FieldGrid({ rows, columns = 2, loose = false }: { rows: Array<[string, unknown]>; columns?: 1 | 2; loose?: boolean }) {
  const visible = rows.filter(([, value]) => value !== undefined)
  if (visible.length === 0) return null
  return (
    <dl className={cn('grid grid-cols-1 text-sm', loose ? 'gap-x-8 gap-y-4' : 'gap-x-4 gap-y-2', columns === 2 && 'sm:grid-cols-2')}>
      {visible.map(([label, value]) => (
        <div key={label} className="contents">
          <dt className={cn('truncate text-muted-foreground', loose && 'py-1')}>{label}</dt>
          <dd className={cn('break-words font-mono text-xs leading-relaxed', loose && 'py-1')}>{valueText(value)}</dd>
        </div>
      ))}
    </dl>
  )
}

function Section({ title, children, loose = false }: { title: string; children: ReactNode; loose?: boolean }) {
  return (
    <section className={cn('border-t border-border first:border-t-0 first:pt-0', loose ? 'space-y-4 pt-8' : 'space-y-3 pt-4')}>
      <h3 className="text-sm font-semibold">{title}</h3>
      {children}
    </section>
  )
}

function StatusBadge({ status }: { status: IntentRunNode['status'] }) {
  const meta = STATUS_META[status]
  const Icon = meta.icon
  return (
    <Badge variant={meta.variant} className="gap-1">
      <Icon className="size-3.5" aria-hidden />
      {meta.label}
    </Badge>
  )
}

function replyModePresentation(value: unknown) {
  const code = typeof value === 'string' ? value : ''
  const known = REPLY_MODE_TEXT[code]
  if (known) return known
  if (code in REPLY_MODE_LABELS) {
    return { label: REPLY_MODE_LABELS[code as ReplyMode], hint: '' }
  }
  return { label: valueText(value), hint: '' }
}

function PolicyDetail({ detail }: { detail: Record<string, unknown> }) {
  const mode = replyModePresentation(detail.ReplyMode)
  const scopeKind = typeof detail.ScopeType === 'string' ? SCOPE_KIND_TEXT[detail.ScopeType] ?? detail.ScopeType : ''
  return (
    <Section title="策略与权限">
      <FieldGrid
        rows={[
          ['生效策略来源', SOURCE_LABELS[String(detail.Source ?? '')] ?? detail.Source],
          ['策略版本', detail.PolicyVersion],
          ['模板版本', detail.TemplateVersion],
          ['作用域', scopeKind && detail.ScopeID !== undefined ? `${scopeKind} ${String(detail.ScopeID)}` : undefined],
          ['启用', detail.Enabled],
          ['回复模式', mode.label],
          ['最小回复间隔', detail.MinReplyIntervalMS === undefined ? undefined : `${String(detail.MinReplyIntervalMS)} ms`],
          ['每小时上限', detail.MaxRepliesPerHour === 0 ? '不限' : detail.MaxRepliesPerHour],
        ]}
      />
      {mode.hint ? <p className="text-xs leading-relaxed text-muted-foreground">{mode.hint}</p> : null}
    </Section>
  )
}

function contextMessages(value: unknown) {
  if (!Array.isArray(value)) return []
  return value.filter(isRecord).map((item) => ({
    id: typeof item.MessageID === 'number' ? item.MessageID : 0,
    senderId: typeof item.SenderID === 'number' ? item.SenderID : 0,
    isBot: item.IsBot === true,
    text: typeof item.Text === 'string' && item.Text !== '' ? item.Text : '（无文本）',
    current: item.Current === true,
  }))
}

function authState(value: unknown) {
  if (value === undefined) return '未记录'
  return value === true ? '已授权' : '未授权'
}

function contextAuthRows(detail: Record<string, unknown>): Array<[string, string]> {
  const historySource = detail.HistorySource === 'database' ? '会话消息' : detail.HistorySource === 'none' ? '未读取' : valueText(detail.HistorySource)
  return [
    ['历史读取', authState(detail.AllowHistoryRead)],
    ['记忆读取', authState(detail.AllowMemoryRead)],
    ['记忆写入', authState(detail.AllowMemoryWrite)],
    ['历史来源', historySource],
    ['历史条数', detail.HistoryCount === undefined ? '—' : String(detail.HistoryCount)],
    ['记忆条数', detail.MemoryCount === undefined ? '—' : String(detail.MemoryCount)],
    ['跳过', valueText(detail.Skipped)],
    ['降级', valueText(detail.Degraded)],
  ]
}

function ContextMessages({ detail }: { detail: Record<string, unknown> }) {
  const [openKey, setOpenKey] = useState<string | null>(null)
  const messages = contextMessages(detail.Messages)
  return (
    <section className="min-w-0 space-y-2">
      <h3 className="text-sm font-semibold">引用消息</h3>
      {messages.length > 0 ? (
        <ul className="divide-y divide-border overflow-hidden rounded-lg border border-border">
          {messages.map((message, index) => {
            const key = `${message.id}-${index}`
            const open = openKey === key
            const speaker = message.isBot ? '机器人' : `用户 ${message.senderId || '—'}`
            return (
              <li key={key} className={message.current ? 'bg-accent/30' : undefined}>
                <button
                  type="button"
                  aria-expanded={open}
                  onClick={() => setOpenKey(open ? null : key)}
                  className="w-full px-3 py-2.5 text-left hover:bg-muted/40"
                >
                  <span className="flex items-center gap-2 text-xs">
                    <span className="font-medium">{speaker}</span>
                    {message.current ? <Badge variant="accent">正在判定</Badge> : null}
                    <span className="ml-auto text-muted-foreground">{open ? '收起' : '查看原文'}</span>
                  </span>
                  <span className={cn('mt-1 block text-sm leading-relaxed', open ? 'whitespace-pre-wrap break-words' : 'line-clamp-3')}>
                    {message.text}
                  </span>
                </button>
              </li>
            )
          })}
        </ul>
      ) : (
        <p className="text-xs leading-relaxed text-muted-foreground">
          {typeof detail.HistoryCount === 'number' && detail.HistoryCount > 0
            ? `当时引用了 ${detail.HistoryCount} 条上下文，这次详情没有带上正文。`
            : '这条决策没有可展示的上下文正文。'}
        </p>
      )}
    </section>
  )
}

const HARD_GATES: Array<{ code: string; name: string; passed: string; failed: string }> = [
  { code: 'policy_disabled', name: '作用域已启用', passed: '作用域开着，可以继续', failed: '作用域关闭，直接沉默' },
  { code: 'observe_only', name: '不是仅观察', passed: '允许发言', failed: '仅观察，只记录不回复' },
  { code: 'cooldown', name: '冷却已结束', passed: '距上次回复已超过最小间隔', failed: '还在冷却，直接沉默' },
  { code: 'rate_limited', name: '未达每小时上限', passed: '本小时发言次数未超限', failed: '达到每小时上限，直接沉默' },
  { code: 'not_addressed', name: '符合指向要求', passed: '回复模式允许这条消息继续', failed: '仅响应 @ 与引用，这条没有指向机器人' },
]

// gateState infers each hard gate from the first blocking reason. Later gates
// did not run; an unknown block reason does not invent a failed gate.
// gateState 根据第一个拦截原因推断每条硬规则。后面的规则没有执行；
// 未知拦截原因不会被写成某一条硬规则失败。
function gateState(blocked: boolean, reason: string, code: string) {
  if (!blocked) return 'passed' as const
  const hit = HARD_GATES.findIndex((item) => item.code === reason)
  const index = HARD_GATES.findIndex((item) => item.code === code)
  if (hit < 0) return 'passed' as const
  if (index < hit) return 'passed' as const
  if (index === hit) return 'failed' as const
  return 'skipped' as const
}

function ruleReason(detail: Record<string, unknown>, reasonCode: string) {
  return typeof detail.ReasonCode === 'string' && detail.ReasonCode ? detail.ReasonCode : reasonCode
}

function ruleOutcome(detail: Record<string, unknown>, reasonCode: string) {
  if (detail.Blocked === true) {
    const code = ruleReason(detail, reasonCode)
    return code ? reasonText(code) : '硬规则已拦截'
  }
  if (detail.Explicit === true) return '消息明确 @ 或引用了机器人，不再做语义判定'
  if (detail.AlwaysReply === true) return '回复模式要求每条都回复，不再做语义判定'
  if (detail.NeedSemantic === true) return '没有明确指向，交给语义判定决定要不要插话'
  if (detail.Matched === true) return '版本化规则已经给出结论'
  return '内置规则没有拦截，也没有要求语义判定'
}

function storedField(record: Record<string, unknown>, ...keys: string[]) {
  for (const key of keys) {
    if (record[key] !== undefined) return record[key]
  }
  return undefined
}

function matchText(match: unknown) {
  if (!isRecord(match)) return '未记录条件'
  const parts: string[] = []
  const replyMode = storedField(match, 'reply_mode', 'ReplyMode')
  const kind = storedField(match, 'kind', 'Kind')
  const explicit = storedField(match, 'explicit', 'Explicit')
  const text = storedField(match, 'text_contains', 'TextContains')
  const seconds = storedField(match, 'min_seconds_since_reply', 'MinSecondsSinceReply')
  if (typeof replyMode === 'string' && replyMode) {
    parts.push(`回复模式是「${replyModePresentation(replyMode).label}」`)
  }
  if (kind === 'group' || kind === 'private') parts.push(SCOPE_KIND_TEXT[kind])
  if (explicit === true) parts.push('明确指向机器人')
  if (explicit === false) parts.push('没有明确指向')
  if (typeof text === 'string' && text) parts.push(`正文包含「${text}」`)
  if (typeof seconds === 'number' && seconds > 0) parts.push(`距上次回复至少 ${seconds} 秒`)
  return parts.length > 0 ? parts.join('，') : '任意消息'
}

function actionText(action: unknown) {
  if (!isRecord(action)) return ''
  const type = storedField(action, 'type', 'Type')
  if (typeof type !== 'string' || type === '') return ''
  return RULE_ACTION_TEXT[type] ?? type
}

function RuleDetail({ detail, reasonCode }: { detail: Record<string, unknown>; reasonCode: string }) {
  const rawEvaluations = Array.isArray(detail.Evaluations) ? detail.Evaluations : detail.evaluations
  const evaluations = Array.isArray(rawEvaluations) ? rawEvaluations.filter(isRecord) : []
  const builtin = isRecord(detail.BuiltinRules) ? detail.BuiltinRules : {}
  const blocked = detail.Blocked === true
  const reason = ruleReason(detail, reasonCode)
  return (
    <Section title="规则求值">
      <p className="text-sm leading-relaxed">{ruleOutcome(detail, reasonCode)}</p>
      <div className="space-y-2">
        <h4 className="text-xs font-medium text-muted-foreground">内置硬规则</h4>
        <ul className="space-y-1.5">
          {HARD_GATES.map((gate) => {
            const state = gateState(blocked, reason, gate.code)
            return (
              <li key={gate.code} className="flex items-start justify-between gap-3 rounded-md border border-border px-3 py-2 text-xs">
                <span>
                  <span className="font-medium">{gate.name}</span>
                  <span className="mt-0.5 block text-muted-foreground">{state === 'failed' ? gate.failed : state === 'skipped' ? '前面的规则已拦截，这条没有执行' : gate.passed}</span>
                </span>
                <Badge variant={state === 'failed' ? 'destructive' : state === 'skipped' ? 'secondary' : 'success'}>
                  {state === 'failed' ? '拦截' : state === 'skipped' ? '未执行' : '通过'}
                </Badge>
              </li>
            )
          })}
        </ul>
      </div>
      <div className="space-y-2">
        <h4 className="text-xs font-medium text-muted-foreground">语义阶段会用到的内置判断</h4>
        <ul className="space-y-1.5">
          {Object.entries(BUILTIN_RULE_TEXT).map(([key, label]) => {
            const configured = Object.prototype.hasOwnProperty.call(builtin, key)
            const enabled = configured ? builtin[key] !== false : true
            return (
              <li key={key} className="flex items-start justify-between gap-3 rounded-md border border-border px-3 py-2 text-xs">
                <span>
                  <span className="font-medium">{label}</span>
                  <span className="mt-0.5 block text-muted-foreground">{configured ? '本会话规则集显式设置' : '没有单独关闭，按默认开启'}</span>
                </span>
                <Badge variant={enabled ? 'success' : 'secondary'}>{enabled ? '开启' : '关闭'}</Badge>
              </li>
            )
          })}
        </ul>
      </div>
      <div className="space-y-2">
        <h4 className="text-xs font-medium text-muted-foreground">版本化规则</h4>
        {evaluations.length > 0 ? (
          <div className="overflow-x-auto rounded-lg border border-border">
            <table className="w-full min-w-[640px] text-left text-xs">
              <thead className="bg-muted/50 text-muted-foreground">
                <tr>
                  <th className="px-3 py-2 font-medium">范围</th>
                  <th className="px-3 py-2 font-medium">优先级</th>
                  <th className="px-3 py-2 font-medium">什么时候命中</th>
                  <th className="px-3 py-2 font-medium">命中后</th>
                  <th className="px-3 py-2 font-medium">本次结果</th>
                </tr>
              </thead>
              <tbody>
                {evaluations.map((evaluation, index) => (
                  <tr key={index} className="border-t border-border/70 align-top">
                    <td className="px-3 py-2">
                      {evaluation.Scope === 'specific' ? '当前会话' : evaluation.Scope === 'global' ? '全局' : '未记录范围'}
                      {evaluation.RulesetID ? (
                        <span className="mt-0.5 block text-muted-foreground">#{String(evaluation.RulesetID)} · v{String(evaluation.Version ?? '—')}</span>
                      ) : null}
                    </td>
                    <td className="px-3 py-2">{String(evaluation.Priority ?? '—')}</td>
                    <td className="px-3 py-2 leading-relaxed">{matchText(evaluation.Match)}</td>
                    <td className="px-3 py-2">{actionText(evaluation.Action) || reasonText(String(evaluation.ReasonCode ?? ''))}</td>
                    <td className="px-3 py-2">
                      <Badge variant={evaluation.Status === 'matched' ? 'success' : evaluation.Status === 'disabled' ? 'secondary' : 'outline'}>
                        {evaluation.Status === 'matched' ? '命中' : evaluation.Status === 'disabled' ? '已关闭' : '未命中'}
                      </Badge>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="text-xs leading-relaxed text-muted-foreground">这次没有版本化规则参与，只走了上面的内置规则。</p>
        )}
      </div>
    </Section>
  )
}

function scoreNumber(value: unknown) {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function comparisonEffect(name: string, satisfied: boolean) {
  if (name === 'addressed_to_other') {
    return satisfied
      ? { label: '会抑制', hint: '达到后，群聊优先保持沉默', variant: 'destructive' as const, bar: 'bg-destructive' }
      : { label: '不抑制', hint: '未达到，不因此沉默', variant: 'outline' as const, bar: 'bg-muted-foreground/50' }
  }
  return satisfied
    ? { label: '成立', hint: '达到后，群聊可以因此回复', variant: 'success' as const, bar: 'bg-success' }
    : { label: '不成立', hint: '未达到群聊开口条件', variant: 'secondary' as const, bar: 'bg-muted-foreground/50' }
}

function semanticVerdict(reasonCode: string, category: string) {
  if (reasonCode !== 'private_normal') return ''
  const kind = INTENT_CATEGORY_TEXT[category] ?? (category || '普通聊天')
  const because = category === 'closing' ? '告别时保持沉默已关闭，所以仍然回复。' : '不是告别，所以仍然回复。'
  return `这些分数只按群聊阈值标记，没有用来决定这次是否开口。这是私聊，聊天功能是「${kind}」，${because}`
}

function SemanticDetail({ detail, reasonCode }: { detail: Record<string, unknown>; reasonCode: string }) {
  const scores = isRecord(detail.Scores) ? detail.Scores : {}
  const thresholds = isRecord(detail.Thresholds) ? detail.Thresholds : {}
  const comparisons = Array.isArray(detail.Comparisons) ? detail.Comparisons.filter(isRecord) : []
  const category = typeof detail.intent_category === 'string' ? detail.intent_category : ''
  const called = detail.Called === true
  const available = detail.Available === true
  const skip = typeof detail.SkipReason === 'string' ? detail.SkipReason : ''
  const thresholdFor = (name: string) => {
    if (name === 'appropriate_to_join') return scoreNumber(thresholds.Interjection)
    if (name === 'addressed_to_other') return scoreNumber(thresholds.AddressedOtherMax)
    return scoreNumber(thresholds.Continuation)
  }
  const verdict = semanticVerdict(reasonCode, category)
  return (
    <Section title="语义判定">
      {verdict ? <p className="rounded-lg border border-primary/30 bg-primary/10 px-3 py-2 text-sm leading-relaxed">{verdict}</p> : null}
      <p className="text-sm leading-relaxed">
        {!called
          ? `没有调用语义模型。${skip ? reasonText(skip) : '规则已经足够决定是否开口'}`
          : available
            ? '语义模型返回了可用结果。分数从 0 到 1。群聊先看是不是在跟别人说话，再看是否延续话题、插话是否自然、是否在跟机器人说话。私聊除告别外不靠这些阈值决定开口。'
            : `语义模型不可用。${typeof detail.FailureCode === 'string' && detail.FailureCode ? reasonText(detail.FailureCode) : skip ? reasonText(skip) : '群聊因此不会自动插话，私聊仍按一对一策略回复'}`}
      </p>
      {category ? (
        <p className="text-sm">
          聊天功能：<span className="font-medium">{INTENT_CATEGORY_TEXT[category] ?? category}</span>
          {category === 'closing' ? '。告别或结束会优先保持沉默。' : ''}
        </p>
      ) : null}
      {comparisons.length > 0 ? (
        <ul className="space-y-2">
          {comparisons.map((row, index) => {
            const name = String(row.Name ?? '')
            const value = scoreNumber(row.Value)
            const threshold = scoreNumber(row.Threshold) ?? thresholdFor(name)
            const effect = comparisonEffect(name, row.Satisfied === true)
            const width = value === undefined ? 0 : Math.max(0, Math.min(100, Math.round(value * 100)))
            return (
              <li key={index} className="rounded-lg border border-border px-3 py-2">
                <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
                  <span className="font-medium">{SCORE_TEXT[name] ?? name}</span>
                  <span className="flex items-center gap-2">
                    <span className="font-mono">{value === undefined ? '—' : value.toFixed(2)}</span>
                    <span className="text-muted-foreground">阈值 {threshold === undefined ? '—' : threshold.toFixed(2)}</span>
                    <Badge variant={effect.variant}>{effect.label}</Badge>
                  </span>
                </div>
                <p className="mt-1 text-[11px] text-muted-foreground">{effect.hint}</p>
                <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-muted">
                  <div className={cn('h-full rounded-full', effect.bar)} style={{ width: `${width}%` }} />
                </div>
              </li>
            )
          })}
        </ul>
      ) : Object.keys(scores).length > 0 ? (
        <ul className="space-y-2">
          {Object.entries(scores).map(([key, value]) => (
            <li key={key} className="flex justify-between gap-3 text-xs">
              <span>{SCORE_TEXT[key] ?? key}</span>
              <span className="font-mono">{valueText(value)}</span>
            </li>
          ))}
        </ul>
      ) : null}
      <FieldGrid
        rows={[
          ['是否调用', called],
          ['是否可用', available],
          ['模型', detail.Model],
          ['Prompt 版本', detail.PromptVersion === undefined ? undefined : `v${String(detail.PromptVersion)}`],
          ['Prompt 来源', SOURCE_LABELS[String(detail.PromptSource ?? '')] ?? detail.PromptSource],
          ['语义耗时', detail.LatencyMS === undefined ? undefined : `${String(detail.LatencyMS)} ms`],
        ]}
      />
    </Section>
  )
}

function DecisionDetail({ detail }: { detail: Record<string, unknown> }) {
  return (
    <Section title="最终判定">
      <FieldGrid
        rows={[
          ['决策', typeof detail.Decision === 'string' ? BRANCH_TEXT[detail.Decision] ?? detail.Decision : detail.Decision],
          ['原因', typeof detail.ReasonText === 'string' && detail.ReasonText ? detail.ReasonText : typeof detail.ReasonCode === 'string' && detail.ReasonCode ? reasonText(detail.ReasonCode) : undefined],
          ['原因码', detail.ReasonCode],
        ]}
      />
    </Section>
  )
}

function GenericDetail({ detail }: { detail: Record<string, unknown> }) {
  return (
    <Section title="节点明细">
      <FieldGrid rows={Object.entries(detail).map(([key, value]) => [key, value])} />
    </Section>
  )
}

function NodeRunBody({
  node,
  runNode,
  run,
  contextSheet,
}: {
  node?: IntentFlowNode
  runNode: IntentRunNode
  run?: IntentRun
  contextSheet: boolean
}) {
  const conclusionRows: Array<[string, unknown]> = [
    ['状态', STATUS_META[runNode.status].label],
    ['版本', `v${runNode.version}`],
    ['分支', BRANCH_TEXT[runNode.branch] ?? runNode.branch],
    ['原因码', runNode.reasonCode],
    ['耗时', `${runNode.durationMs} ms`],
  ]
  const conclusion = (
    <Section title="节点结论" loose={contextSheet}>
      <p className="text-sm leading-relaxed">{runNode.summary || '未记录节点摘要。'}</p>
      <FieldGrid rows={conclusionRows} loose={contextSheet} />
    </Section>
  )
  const detail = runNode.nodeId === 'policy' || node?.kind === 'policy'
    ? <PolicyDetail detail={runNode.detail} />
    : runNode.nodeId === 'rules' || node?.kind === 'rules'
      ? <RuleDetail detail={runNode.detail} reasonCode={runNode.reasonCode} />
      : runNode.nodeId === 'semantic' || node?.kind === 'semantic'
        ? <SemanticDetail detail={runNode.detail} reasonCode={run?.summary.reasonCode ?? ''} />
        : runNode.nodeId === 'decision' || node?.kind === 'decision'
          ? <DecisionDetail detail={runNode.detail} />
          : runNode.nodeId === 'context' || node?.kind === 'context'
            ? null
            : <GenericDetail detail={runNode.detail} />
  if (contextSheet) {
    return (
      <div className="grid items-start gap-8 md:grid-cols-[minmax(0,1fr)_34rem]">
        <ContextMessages detail={runNode.detail} />
        <div className="min-w-0">
          {conclusion}
          <Section title="输入授权与引用上下文" loose>
            <FieldGrid rows={contextAuthRows(runNode.detail)} loose />
          </Section>
        </div>
      </div>
    )
  }
  return (
    <>
      {conclusion}
      {detail}
    </>
  )
}

export function FlowNodeDetail({
  open,
  onOpenChange,
  node,
  runNode,
  run,
  showSnapshotStatus = true,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  node?: IntentFlowNode
  runNode?: IntentRunNode
  run?: IntentRun
  showSnapshotStatus?: boolean
}) {
  const status = runNode?.status ?? 'unknown'
  const snapshotWarning = showSnapshotStatus && run ? SNAPSHOT_WARNING[run.snapshotStatus] : ''
  const contextSheet = node?.kind === 'context' || runNode?.nodeId === 'context'
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className={cn('max-w-none overflow-y-auto bg-background p-6 sm:max-w-none', contextSheet ? 'w-[min(1240px,calc(100vw-1rem))]' : 'w-[min(960px,calc(100vw-1rem))]')}>
        <div className="border-b border-border pb-5 pr-10">
          <div className="flex flex-wrap items-center gap-2">
            <SheetTitle className="text-lg font-semibold">{node?.title ?? '节点详情'}</SheetTitle>
            {runNode ? <StatusBadge status={status} /> : <Badge variant="outline">未记录</Badge>}
          </div>
          <SheetDescription className="mt-2 leading-relaxed">
            {node?.description || '该节点未在当前流程定义中登记。'}
          </SheetDescription>
          <p className="mt-2 font-mono text-xs text-muted-foreground">
            {node?.nodeId ?? runNode?.nodeId ?? '—'} · {run?.flowVersion ?? '—'}
          </p>
        </div>
        <div className="mt-5 space-y-5">
          {snapshotWarning ? (
            <p role="status" className="flex items-start gap-2 rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-xs leading-relaxed text-warning">
              <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden />
              <span>{snapshotWarning}</span>
            </p>
          ) : null}
          {runNode ? (
            <NodeRunBody
              node={node}
              runNode={runNode}
              run={run}
              contextSheet={contextSheet}
            />
          ) : (
            <Section title="历史记录">
              <p className="text-sm leading-relaxed text-muted-foreground">{snapshotWarning || '该节点没有可展示的运行结果。'}</p>
            </Section>
          )}
        </div>
      </SheetContent>
    </Sheet>
  )
}

export { STATUS_LABELS }
