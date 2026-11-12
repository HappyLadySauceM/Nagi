import type { ReactNode } from 'react'
import { AlertTriangle, CheckCircle2, CircleHelp, MinusCircle, XCircle } from 'lucide-react'
import { Badge } from '../../../components/ui/badge'
import { Sheet, SheetContent, SheetDescription, SheetTitle } from '../../../components/ui/sheet'
import { cn } from '../../../lib/utils'
import type {
  IntentFlowEdge,
  IntentFlowNode,
  IntentRun,
  IntentRunNode,
  IntentSnapshotStatus,
} from '../../../lib/api/contracts'

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

function FieldGrid({ rows }: { rows: Array<[string, unknown]> }) {
  const visible = rows.filter(([, value]) => value !== undefined)
  if (visible.length === 0) return null
  return (
    <dl className="grid grid-cols-1 gap-x-4 gap-y-2 text-sm sm:grid-cols-2">
      {visible.map(([label, value]) => (
        <div key={label} className="contents">
          <dt className="truncate text-muted-foreground">{label}</dt>
          <dd className="break-words font-mono text-xs leading-relaxed">{valueText(value)}</dd>
        </div>
      ))}
    </dl>
  )
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="space-y-3 border-t border-border pt-4 first:border-t-0 first:pt-0">
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

function PolicyDetail({ detail }: { detail: Record<string, unknown> }) {
  return (
    <Section title="策略与权限">
      <FieldGrid
        rows={[
          ['生效策略来源', SOURCE_LABELS[String(detail.Source ?? '')] ?? detail.Source],
          ['策略版本', detail.PolicyVersion],
          ['模板版本', detail.TemplateVersion],
          ['作用域', detail.ScopeType && detail.ScopeID ? `${String(detail.ScopeType)} ${String(detail.ScopeID)}` : undefined],
          ['启用', detail.Enabled],
          ['回复模式', detail.ReplyMode],
          ['最小回复间隔', detail.MinReplyIntervalMS === undefined ? undefined : `${String(detail.MinReplyIntervalMS)} ms`],
          ['每小时上限', detail.MaxRepliesPerHour],
        ]}
      />
    </Section>
  )
}

function ContextDetail({ detail }: { detail: Record<string, unknown> }) {
  return (
    <Section title="输入授权与历史来源">
      <div className="flex flex-wrap gap-2 text-xs">
        {[
          ['历史读取', detail.AllowHistoryRead],
          ['记忆读取', detail.AllowMemoryRead],
          ['记忆写入', detail.AllowMemoryWrite],
        ].map(([label, value]) => (
          <span key={String(label)} className={cn('rounded-md border px-2 py-1', value === true ? 'border-success/30 bg-success/10 text-success' : 'border-border text-muted-foreground')}>
            {String(label)}：{value === undefined ? '未记录' : value ? '已授权' : '未授权'}
          </span>
        ))}
      </div>
      <FieldGrid
        rows={[
          ['历史来源', detail.HistorySource],
          ['历史条数', detail.HistoryCount],
          ['记忆条数', detail.MemoryCount],
          ['跳过', detail.Skipped],
          ['降级', detail.Degraded],
        ]}
      />
    </Section>
  )
}

function RuleDetail({ detail }: { detail: Record<string, unknown> }) {
  const evaluations = Array.isArray(detail.Evaluations) ? detail.Evaluations.filter(isRecord) : []
  const builtin = isRecord(detail.BuiltinRules) ? detail.BuiltinRules : {}
  return (
    <Section title="规则求值">
      <FieldGrid
        rows={[
          ['命中规则', detail.Matched],
          ['被拦截', detail.Blocked],
          ['明确指向', detail.Explicit],
          ['需要语义', detail.NeedSemantic],
          ['始终回复', detail.AlwaysReply],
        ]}
      />
      {Object.keys(builtin).length > 0 ? (
        <div className="flex flex-wrap gap-2 text-xs">
          {Object.entries(builtin).map(([key, value]) => (
            <span key={key} className={cn('rounded-md border px-2 py-1', value ? 'border-success/30 bg-success/10 text-success' : 'border-border text-muted-foreground')}>
              {key}：{value ? '开' : '关'}
            </span>
          ))}
        </div>
      ) : null}
      {evaluations.length > 0 ? (
        <div className="overflow-x-auto rounded-lg border border-border">
          <table className="w-full min-w-[520px] text-left text-xs">
            <thead className="bg-muted/50 text-muted-foreground">
              <tr>
                <th className="px-3 py-2 font-medium">范围</th>
                <th className="px-3 py-2 font-medium">规则版本</th>
                <th className="px-3 py-2 font-medium">优先级</th>
                <th className="px-3 py-2 font-medium">条件</th>
                <th className="px-3 py-2 font-medium">结果</th>
              </tr>
            </thead>
            <tbody>
              {evaluations.map((evaluation, index) => (
                <tr key={index} className="border-t border-border/70">
                  <td className="px-3 py-2">{evaluation.Scope === 'specific' ? '指定会话' : '全局'}</td>
                  <td className="px-3 py-2 font-mono">#{String(evaluation.RulesetID ?? '—')} · v{String(evaluation.Version ?? '—')}</td>
                  <td className="px-3 py-2">{String(evaluation.Priority ?? '—')}</td>
                  <td className="max-w-[240px] break-words font-mono text-[11px]">{valueText(evaluation.Match)}</td>
                  <td className="px-3 py-2">
                    <Badge variant={evaluation.Status === 'matched' ? 'success' : evaluation.Status === 'disabled' ? 'secondary' : 'outline'}>
                      {evaluation.Status === 'matched' ? '命中' : evaluation.Status === 'disabled' ? '已关闭' : '未命中'}
                    </Badge>
                    {evaluation.ReasonCode ? <span className="ml-2 font-mono text-[11px] text-muted-foreground">{String(evaluation.ReasonCode)}</span> : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className="text-xs text-muted-foreground">本次没有版本化规则条目或未记录逐条求值。</p>
      )}
    </Section>
  )
}

function ComparisonList({ comparisons }: { comparisons: unknown[] }) {
  if (comparisons.length === 0) return null
  return (
    <div className="space-y-1.5 rounded-lg border border-border p-3 text-xs">
      <div className="font-medium">阈值比较</div>
      {comparisons.filter(isRecord).map((row, index) => {
        const satisfied = row.Satisfied === true
        return (
          <div key={index} className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span className="font-mono">{String(row.Name ?? '—')}</span>
            <span>{String(row.Value ?? '—')}</span>
            <span className="text-muted-foreground">{String(row.Operator ?? '')}</span>
            <span>{String(row.Threshold ?? '—')}</span>
            <Badge variant={satisfied ? 'success' : 'secondary'}>{satisfied ? '满足' : '未满足'}</Badge>
          </div>
        )
      })}
    </div>
  )
}

function SemanticDetail({ detail }: { detail: Record<string, unknown> }) {
  const scores = isRecord(detail.Scores) ? detail.Scores : {}
  const comparisons = Array.isArray(detail.Comparisons) ? detail.Comparisons : []
  return (
    <Section title="模型与 Prompt">
      <FieldGrid
        rows={[
          ['是否调用', detail.Called],
          ['是否可用', detail.Available],
          ['失败码', detail.FailureCode],
          ['Provider', detail.Provider],
          ['模型', detail.Model],
          ['Prompt ID', detail.PromptID],
          ['Prompt 版本', detail.PromptVersion],
          ['Prompt 来源', SOURCE_LABELS[String(detail.PromptSource ?? '')] ?? detail.PromptSource],
          ['Prompt Hash', detail.PromptHash],
          ['降级原因', detail.PromptFallbackReason],
          ['语义耗时', detail.LatencyMS === undefined ? undefined : `${String(detail.LatencyMS)} ms`],
        ]}
      />
      {Object.keys(scores).length > 0 ? (
        <div className="grid grid-cols-2 gap-2 rounded-lg bg-muted/40 p-3 text-xs">
          {Object.entries(scores).map(([key, value]) => (
            <div key={key} className="contents">
              <span className="text-muted-foreground">{key}</span>
              <span className="font-mono">{valueText(value)}</span>
            </div>
          ))}
        </div>
      ) : null}
      <ComparisonList comparisons={comparisons} />
    </Section>
  )
}

function DecisionDetail({ detail }: { detail: Record<string, unknown> }) {
  return (
    <Section title="最终判定">
      <FieldGrid
        rows={[
          ['决策', detail.Decision],
          ['原因码', detail.ReasonCode],
          ['中文原因', detail.ReasonText],
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

export function FlowNodeDetail({
  open,
  onOpenChange,
  node,
  runNode,
  run,
  edges,
  showSnapshotStatus = true,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  node?: IntentFlowNode
  runNode?: IntentRunNode
  run?: IntentRun
  edges?: IntentFlowEdge[]
  showSnapshotStatus?: boolean
}) {
  const status = runNode?.status ?? 'unknown'
  const snapshotWarning = showSnapshotStatus && run ? SNAPSHOT_WARNING[run.snapshotStatus] : ''
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-full overflow-y-auto bg-background p-6 sm:max-w-2xl">
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
            <>
              <Section title="节点结论">
                <p className="text-sm leading-relaxed">{runNode.summary || '未记录节点摘要。'}</p>
                <FieldGrid
                  rows={[
                    ['状态', STATUS_META[runNode.status].label],
                    ['分支', runNode.branch],
                    ['原因码', runNode.reasonCode],
                    ['耗时', `${runNode.durationMs} ms`],
                  ]}
                />
              </Section>
              {runNode.nodeId === 'policy' || node?.kind === 'policy' ? <PolicyDetail detail={runNode.detail} /> : null}
              {runNode.nodeId === 'context' || node?.kind === 'context' ? <ContextDetail detail={runNode.detail} /> : null}
              {runNode.nodeId === 'rules' || node?.kind === 'rules' ? <RuleDetail detail={runNode.detail} /> : null}
              {runNode.nodeId === 'semantic' || node?.kind === 'semantic' ? <SemanticDetail detail={runNode.detail} /> : null}
              {runNode.nodeId === 'decision' || node?.kind === 'decision' ? <DecisionDetail detail={runNode.detail} /> : null}
              {!['policy', 'context', 'rules', 'semantic', 'decision'].includes(runNode.nodeId) ? <GenericDetail detail={runNode.detail} /> : null}
              {edges && edges.length > 0 ? (
                <Section title="实际分支条件">
                  <div className="space-y-2 text-xs">
                    {edges.filter((edge) => edge.fromNodeId === runNode.nodeId || edge.toNodeId === runNode.nodeId).map((edge) => {
                      const status = run?.edges.find((item) => item.edgeId === edge.edgeId)?.status ?? 'unknown'
                      const label = status === 'taken' ? '已走' : status === 'not_taken' ? '未走' : '未知'
                      return (
                        <div key={edge.edgeId} className="rounded-md border border-border px-3 py-2">
                          <span className="font-mono text-[11px]">{edge.fromNodeId} → {edge.toNodeId}</span>
                          <span className="ml-2 text-muted-foreground">{edge.label || '分支'}</span>
                          <Badge variant={status === 'taken' ? 'success' : status === 'not_taken' ? 'secondary' : 'outline'} className="ml-2">{label}</Badge>
                          {edge.condition ? <p className="mt-1 break-all font-mono text-[10px] text-muted-foreground">{edge.condition}</p> : null}
                        </div>
                      )
                    })}
                  </div>
                </Section>
              ) : null}
              <Section title="可验证短证据">
                {runNode.evidence.length > 0 ? (
                  <ul className="space-y-2">
                    {runNode.evidence.map((item, index) => (
                      <li key={index} className="flex gap-2 rounded-md bg-muted/40 px-3 py-2 text-xs">
                        <span className="shrink-0 rounded bg-background px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">{item.kind || 'evidence'}</span>
                        <span className="break-all font-mono">{item.text || '—'}</span>
                      </li>
                    ))}
                  </ul>
                ) : (
                  <p className="text-xs text-muted-foreground">本节点没有短证据记录。</p>
                )}
              </Section>
            </>
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
