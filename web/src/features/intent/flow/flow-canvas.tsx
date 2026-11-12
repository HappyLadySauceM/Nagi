import { useMemo, useState } from 'react'
import { AlertTriangle, CheckCircle2, CircleHelp, MinusCircle, XCircle } from 'lucide-react'
import { Badge } from '../../../components/ui/badge'
import { cn } from '../../../lib/utils'
import type {
  IntentFlow,
  IntentFlowEdge,
  IntentFlowNode,
  IntentRun,
  IntentRunNode,
} from '../../../lib/api/contracts'
import { FlowNodeDetail } from './flow-node-detail'

const NODE_WIDTH = 184
const NODE_HEIGHT = 148
const COLUMN_GAP = 34
const PADDING = 20
const ROW_Y = 70

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

function nodeX(node: IntentFlowNode) {
  return PADDING + Math.max(0, node.stageIndex) * (NODE_WIDTH + COLUMN_GAP)
}

function edgePath(edge: IntentFlowEdge, nodes: Map<string, IntentFlowNode>) {
  const from = nodes.get(edge.fromNodeId)
  const to = nodes.get(edge.toNodeId)
  if (!from || !to) return ''
  const x1 = nodeX(from) + NODE_WIDTH
  const y1 = ROW_Y + NODE_HEIGHT / 2
  const x2 = nodeX(to)
  const y2 = ROW_Y + NODE_HEIGHT / 2
  if (to.stageIndex - from.stageIndex <= 1) {
    return `M ${x1} ${y1} L ${x2} ${y2}`
  }
  const archY = 22
  return `M ${x1} ${y1} C ${x1 + 55} ${archY}, ${x2 - 55} ${archY}, ${x2} ${y2}`
}

function edgeClass(status: IntentRun['edges'][number]['status'] | 'pending') {
  if (status === 'taken') return 'stroke-primary'
  if (status === 'not_taken') return 'stroke-muted-foreground/35'
  return 'stroke-muted-foreground/20'
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
  const maxStage = Math.max(0, ...flow.nodes.map((node) => node.stageIndex))
  const width = PADDING * 2 + maxStage * (NODE_WIDTH + COLUMN_GAP) + NODE_WIDTH
  const height = ROW_Y + NODE_HEIGHT + 42
  const selectedNode = selectedNodeId ? nodes.get(selectedNodeId) : undefined
  const selectedRunNode = selectedNodeId ? runNodes.get(selectedNodeId) : undefined

  return (
    <>
      <div className={cn('rounded-xl border border-border bg-card', compact ? 'p-3' : 'p-4')}>
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
          <div>
            <h3 className="text-sm font-semibold">意图判定流程</h3>
            <p className="mt-1 text-meta text-muted-foreground">
              只读固定流程 · 点击节点查看实际输入、分支条件和证据
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
        <div className="overflow-x-auto">
          <div className="relative" style={{ width, height }}>
            <svg className="pointer-events-none absolute inset-0 size-full overflow-visible" aria-hidden>
              {flow.edges.map((edge) => (
                <path
                  key={edge.edgeId}
                  d={edgePath(edge, nodes)}
                  fill="none"
                  strokeWidth={2}
                  strokeDasharray={runEdges.get(edge.edgeId)?.status === 'not_taken' || !run ? '5 5' : undefined}
                  className={edgeClass(runEdges.get(edge.edgeId)?.status ?? 'pending')}
                />
              ))}
            </svg>
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
                    'absolute flex flex-col rounded-xl border bg-background p-3 text-left shadow-sm transition-shadow hover:shadow-md focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring',
                    meta?.className ?? 'border-border bg-background',
                    selectedNodeId === node.nodeId && 'ring-2 ring-ring',
                  )}
                  style={{ left: nodeX(node), top: ROW_Y, width: NODE_WIDTH }}
                >
                  <span className="flex items-center justify-between gap-2">
                    <span className="flex items-center gap-2">
                      <span className="flex size-6 items-center justify-center rounded-md bg-muted font-mono text-[11px] font-semibold text-muted-foreground">
                        {KIND_ICONS[node.kind] ?? '•'}
                      </span>
                      <span className="text-sm font-medium">{node.title}</span>
                    </span>
                    <Badge variant={meta?.variant ?? 'outline'} className="px-1.5 py-0 text-[10px]">
                      {statusLabel}
                    </Badge>
                  </span>
                  <span className="mt-2 line-clamp-2 text-[11px] leading-relaxed text-muted-foreground">
                    {runNode?.summary || node.description}
                  </span>
                  <span className="mt-2 flex items-center gap-1 text-[10px] text-muted-foreground">
                    <Icon className="size-3" aria-hidden />
                    {runNode ? `v${runNode.version} · ${runNode.durationMs} ms` : `Node ${node.stageIndex + 1}`}
                  </span>
                </button>
              )
            })}
          </div>
        </div>
        <p className="mt-2 text-[11px] text-muted-foreground">
          未走分支以虚线显示；“未记录”表示历史记录没有快照，不表示执行失败。
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
        edges={flow.edges}
        showSnapshotStatus={showSnapshotStatus}
      />
    </>
  )
}
