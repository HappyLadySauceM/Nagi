import { useMemo, useState } from 'react'
import { AlertTriangle, CheckCircle2, CircleHelp, MinusCircle, XCircle } from 'lucide-react'
import { Badge } from '../../../components/ui/badge'
import { cn } from '../../../lib/utils'
import type {
  IntentFlow,
  IntentRun,
  IntentRunNode,
} from '../../../lib/api/contracts'
import { FlowNodeDetail } from './flow-node-detail'

const STATUS_META: Record<
  IntentRunNode['status'],
  { label: string; variant: 'success' | 'secondary' | 'destructive' | 'warning' | 'outline'; icon: typeof CheckCircle2; className: string }
> = {
  executed: { label: '实际执行', variant: 'success', icon: CheckCircle2, className: 'border-success/40 bg-success/5' },
  skipped: { label: '跳过', variant: 'secondary', icon: MinusCircle, className: 'border-dashed border-border bg-muted/30' },
  failed: { label: '失败', variant: 'destructive', icon: XCircle, className: 'border-destructive/40 bg-destructive/5' },
  degraded: { label: '降级', variant: 'warning', icon: AlertTriangle, className: 'border-warning/40 bg-warning/5' },
  unknown: { label: '未记录', variant: 'outline', icon: CircleHelp, className: 'border-dashed border-border/80 bg-background' },
}

const KIND_ICONS: Record<string, string> = {
  policy: 'P',
  context: 'C',
  rules: 'R',
  semantic: 'S',
  decision: 'D',
}

function edgeStatusLabel(status: string) {
  if (status === 'taken') return '已走'
  if (status === 'not_taken') return '未走'
  if (status === 'unknown') return '未知'
  return '待运行'
}

/** Read-only fixed-flow canvas. It never supports dragging or edge editing. */
export function FlowCanvas({
  flow,
  run,
  compact = false,
  showSnapshotStatus = true,
}: {
  flow: IntentFlow
  run?: IntentRun
  compact?: boolean
  showSnapshotStatus?: boolean
}) {
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null)
  const nodes = useMemo(() => new Map(flow.nodes.map((node) => [node.nodeId, node])), [flow.nodes])
  const runNodes = useMemo(
    () => new Map((run?.nodes ?? []).map((node) => [node.nodeId, node])),
    [run?.nodes],
  )
  const runEdges = useMemo(
    () => new Map((run?.edges ?? []).map((edge) => [edge.edgeId, edge])),
    [run?.edges],
  )
  const selectedNode = selectedNodeId ? nodes.get(selectedNodeId) : undefined
  const selectedRunNode = selectedNodeId ? runNodes.get(selectedNodeId) : undefined

  return (
    <>
      <div className={cn('rounded-xl border border-border bg-card', compact ? 'p-3' : 'p-4')}>
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
          <div>
            <h3 className="text-sm font-semibold">意图判定流程</h3>
            <p className="mt-1 text-meta text-muted-foreground">
              只读固定流程 · 点击节点查看判定说明
            </p>
          </div>
          <div className="flex flex-wrap gap-2 text-[11px] text-muted-foreground">
            {[
              ['实际执行', 'bg-success'],
              ['跳过', 'bg-muted-foreground/40'],
              ['降级', 'bg-warning'],
              ['失败', 'bg-destructive'],
              ['未记录', 'border border-dashed bg-transparent'],
            ].map(([label, dot]) => (
              <span key={label} className="inline-flex items-center gap-1">
                <span className={cn('inline-block size-2 rounded-full', dot)} aria-hidden />
                {label}
              </span>
            ))}
          </div>
        </div>
        <div className="grid gap-3" style={{ gridTemplateColumns: `repeat(${Math.max(flow.nodes.length, 1)}, minmax(0, 1fr))` }}>
          {flow.nodes.map((node) => {
            const runNode = runNodes.get(node.nodeId)
            const meta = runNode ? STATUS_META[runNode.status] : run ? STATUS_META.unknown : undefined
            const statusLabel = runNode ? meta?.label : run ? '未记录' : '待运行'
            const Icon = meta?.icon ?? CircleHelp
            return (
              <button
                key={node.nodeId}
                type="button"
                onClick={() => setSelectedNodeId(node.nodeId)}
                aria-label={`${node.title} ${statusLabel}`}
                className={cn(
                  'flex min-h-36 flex-col rounded-xl border bg-background p-3 text-left shadow-sm transition-shadow hover:shadow-md focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring',
                  meta?.className ?? 'border-border bg-background',
                  selectedNodeId === node.nodeId && 'ring-2 ring-ring',
                )}
              >
                <span className="flex items-start justify-between gap-2">
                  <span className="flex min-w-0 items-center gap-2">
                    <span className="flex size-6 shrink-0 items-center justify-center rounded-md bg-muted font-mono text-[11px] font-semibold text-muted-foreground">
                      {KIND_ICONS[node.kind] ?? '•'}
                    </span>
                    <span className="text-sm font-medium leading-snug">{node.title}</span>
                  </span>
                  <Badge variant={meta?.variant ?? 'outline'} className="shrink-0 px-1.5 py-0 text-[10px]">
                    {statusLabel}
                  </Badge>
                </span>
                <span className="mt-2 text-[12px] leading-relaxed text-muted-foreground">
                  {runNode?.summary || node.description}
                </span>
                <span className="mt-auto flex items-center gap-1 pt-3 text-[10px] text-muted-foreground">
                  <Icon className="size-3" aria-hidden />
                  {runNode ? `v${runNode.version} · ${runNode.durationMs} ms` : `第 ${node.stageIndex + 1} 步`}
                </span>
              </button>
            )
          })}
        </div>
        <div className="mt-3 flex flex-wrap gap-2">
          {flow.edges.map((edge) => {
            const status = runEdges.get(edge.edgeId)?.status ?? (run ? 'unknown' : 'pending')
            const from = nodes.get(edge.fromNodeId)?.title ?? edge.fromNodeId
            const to = nodes.get(edge.toNodeId)?.title ?? edge.toNodeId
            return (
              <span
                key={edge.edgeId}
                className={cn(
                  'rounded-md border px-2 py-1 text-[11px]',
                  status === 'taken' && 'border-primary/40 bg-primary/10 text-foreground',
                  status === 'not_taken' && 'border-dashed text-muted-foreground',
                  status !== 'taken' && status !== 'not_taken' && 'border-border text-muted-foreground',
                )}
              >
                {from} → {to}：{edgeStatusLabel(status)}
                {edge.label ? ` · ${edge.label}` : ''}
              </span>
            )
          })}
        </div>
        <p className="mt-2 text-[11px] text-muted-foreground">
          五个节点都会出现；未走的分支标为「未走」。「未记录」表示历史记录没有快照，不表示执行失败。
        </p>
      </div>
      <FlowNodeDetail
        open={selectedNodeId !== null}
        onOpenChange={(open) => {
          if (!open) setSelectedNodeId(null)
        }}
        node={selectedNode}
        runNode={selectedRunNode}
        run={run}
        showSnapshotStatus={showSnapshotStatus}
      />
    </>
  )
}
