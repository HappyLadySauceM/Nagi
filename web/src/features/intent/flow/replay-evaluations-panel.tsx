import { useState } from 'react'
import { GitCompareArrows, History, RefreshCw } from 'lucide-react'
import { Badge } from '../../../components/ui/badge'
import { Button } from '../../../components/ui/button'
import { Card } from '../../../components/ui/card'
import { Skeleton } from '../../../components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../../components/ui/table'
import type { ReplayEvaluation, ReplayEvaluationSummary, ReplayReproduction } from '../../../lib/api/contracts'
import { formatRelativeTime } from '../../../lib/format'
import { useReplayComparison, useReplayEvaluation, useReplayEvaluations } from './queries'

const MODE_LABELS: Record<ReplayEvaluationSummary['mode'], string> = {
  recalculate: '本地重算',
  model: '模型重跑',
}

function againstLabel(against: string): string {
  if (against.startsWith('evaluation:')) {
    return `#${against.slice('evaluation:'.length)}`
  }
  return against === 'original' ? '原结果' : against || '—'
}

function ReproductionBadge({ reproduction }: { reproduction: ReplayReproduction }) {
  return (
    <Badge variant={reproduction.fullyReproduced ? 'success' : 'warning'}>
      {reproduction.fullyReproduced ? '完全复现' : '非完全复现'}
    </Badge>
  )
}

function EvaluationSummary({ evaluation }: { evaluation: ReplayEvaluation }) {
  return (
    <div className="space-y-3 rounded-lg border border-border p-3 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium">评估 #{evaluation.id}</span>
        <Badge variant="outline">{MODE_LABELS[evaluation.mode]}</Badge>
        <Badge variant={evaluation.changed ? 'warning' : 'success'}>
          {evaluation.changed ? '结论有变化' : '结论一致'}
        </Badge>
        <ReproductionBadge reproduction={evaluation.reproduction} />
      </div>
      {evaluation.name ? <p className="font-medium">{evaluation.name}</p> : null}
      {evaluation.parameters ? (
        <div className="grid gap-1 text-xs text-muted-foreground sm:grid-cols-2">
          <span>模型：{evaluation.parameters.provider} / {evaluation.parameters.model}</span>
          <span>
            Prompt：{evaluation.parameters.promptId || '—'} v{evaluation.parameters.promptVersion}
            {evaluation.parameters.promptHash ? ` · ${evaluation.parameters.promptHash.slice(0, 12)}` : ''}
          </span>
          <span>规则版本：{evaluation.parameters.rulesetVersion || '—'}</span>
          <span>
            样本：{evaluation.parameters.sampleCount} · 预计外部请求：{evaluation.parameters.estimatedExternalRequests}
          </span>
        </div>
      ) : null}
      <div className="grid gap-2 sm:grid-cols-2">
        <div>
          <div className="text-xs text-muted-foreground">原结论</div>
          <div className="mt-1">
            {evaluation.before.summary.decision} · {evaluation.before.summary.reasonText || evaluation.before.summary.reasonCode}
          </div>
        </div>
        <div>
          <div className="text-xs text-muted-foreground">评估结果</div>
          <div className="mt-1">
            {evaluation.after.summary.decision} · {evaluation.after.summary.reasonText || evaluation.after.summary.reasonCode}
          </div>
        </div>
      </div>
      {evaluation.reproduction.reasons.length > 0 ? (
        <p className="text-xs text-muted-foreground">
          非完全复现原因：{evaluation.reproduction.reasons.join('；')}
        </p>
      ) : null}
      {evaluation.cost?.text || evaluation.cost?.costEstimate ? (
        <p className="text-xs text-muted-foreground">
          费用：{evaluation.cost.text ?? evaluation.cost.costEstimate}
        </p>
      ) : null}
      {evaluation.note ? <p className="text-xs text-muted-foreground">备注：{evaluation.note}</p> : null}
    </div>
  )
}

/** Saved replay evaluations for one decision, with detail and two-way comparison.
 * 单条决策的已保存回放评估，支持详情与双记录对比。
 */
export function ReplayEvaluationsPanel({ decisionId }: { decisionId: number }) {
  const list = useReplayEvaluations(decisionId)
  const [selectedIds, setSelectedIds] = useState<number[]>([])
  const [detailId, setDetailId] = useState<number | null>(null)
  const [compareIds, setCompareIds] = useState<[number, number] | null>(null)
  const detail = useReplayEvaluation(detailId)
  const comparison = useReplayComparison(compareIds?.[0] ?? null, compareIds?.[1] ?? null)

  const toggleSelected = (id: number) => {
    setSelectedIds((previous) => {
      if (previous.includes(id)) {
        return previous.filter((item) => item !== id)
      }
      return previous.length >= 2 ? previous : [...previous, id]
    })
  }

  return (
    <Card className="space-y-4 p-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 className="flex items-center gap-2 text-sm font-semibold">
            <History className="size-4" aria-hidden />
            已保存的回放评估
          </h3>
          <p className="mt-1 text-meta text-muted-foreground">
            保存的是独立评估记录，不会修改原决策；可勾选两条记录对比结论、原因和版本。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" onClick={() => void list.refetch()} disabled={list.isFetching}>
            <RefreshCw className={list.isFetching ? 'size-3.5 animate-spin' : 'size-3.5'} aria-hidden />
            刷新
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={selectedIds.length !== 2 || comparison.isFetching}
            onClick={() => {
              if (selectedIds.length === 2) {
                setCompareIds([selectedIds[0], selectedIds[1]])
              }
            }}
          >
            <GitCompareArrows className="size-3.5" aria-hidden />
            对比所选
          </Button>
        </div>
      </div>

      {list.isLoading ? <Skeleton className="h-24 w-full" /> : null}
      {list.isError ? (
        <p role="alert" className="text-sm text-destructive">
          读取已保存评估失败；{list.error.message}
        </p>
      ) : null}
      {!list.isLoading && !list.isError && (list.data?.length ?? 0) === 0 ? (
        <p className="text-sm text-muted-foreground">还没有保存的回放评估。</p>
      ) : null}
      {(list.data?.length ?? 0) > 0 ? (
        <div className="overflow-hidden rounded-lg border border-border">
          <Table label="已保存的回放评估列表">
            <TableHeader>
              <TableRow>
                <TableHead className="w-10">选择</TableHead>
                <TableHead>名称</TableHead>
                <TableHead>模式</TableHead>
                <TableHead>结论</TableHead>
                <TableHead>复现</TableHead>
                <TableHead className="text-right">时间</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.data!.map((evaluation) => (
                <TableRow key={evaluation.id}>
                  <TableCell>
                    <input
                      type="checkbox"
                      aria-label={`选择评估 ${evaluation.id}`}
                      checked={selectedIds.includes(evaluation.id)}
                      onChange={() => toggleSelected(evaluation.id)}
                    />
                  </TableCell>
                  <TableCell>{evaluation.name || `评估 #${evaluation.id}`}</TableCell>
                  <TableCell><Badge variant="outline">{MODE_LABELS[evaluation.mode]}</Badge></TableCell>
                  <TableCell>
                    <Badge variant={evaluation.changed ? 'warning' : 'success'}>
                      {evaluation.changed ? '有变化' : '一致'}
                    </Badge>
                    <span className="ml-2 text-meta text-muted-foreground">
                      {evaluation.summary.reasonText || evaluation.summary.reasonCode || '—'}
                    </span>
                  </TableCell>
                  <TableCell><ReproductionBadge reproduction={evaluation.reproduction} /></TableCell>
                  <TableCell className="text-right text-meta text-muted-foreground">
                    {evaluation.createdAt ? formatRelativeTime(evaluation.createdAt) : '—'}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button size="sm" variant="ghost" onClick={() => setDetailId(evaluation.id)}>
                      查看
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      ) : null}

      {detailId !== null ? (
        detail.isLoading ? <Skeleton className="h-28 w-full" /> : detail.isError ? (
          <p role="alert" className="text-sm text-destructive">读取评估详情失败；{detail.error.message}</p>
        ) : detail.data ? (
          <EvaluationSummary evaluation={detail.data} />
        ) : null
      ) : null}

      {compareIds ? (
        comparison.isLoading ? <Skeleton className="h-32 w-full" /> : comparison.isError ? (
          <p role="alert" className="text-sm text-destructive">读取对比结果失败；{comparison.error.message}</p>
        ) : comparison.data ? (
          <div className="space-y-3 rounded-lg border border-border p-3 text-sm">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">
                对比 #{comparison.data.evaluationId} ↔ {againstLabel(comparison.data.against)}
              </span>
              <Badge variant={comparison.data.changed ? 'warning' : 'success'}>
                {comparison.data.changed ? '存在差异' : '无差异'}
              </Badge>
              <ReproductionBadge reproduction={comparison.data.reproduction} />
            </div>
            {comparison.data.differences.length === 0 ? (
              <p className="text-sm text-muted-foreground">两条记录的决策、原因和版本字段一致。</p>
            ) : (
              <Table label="回放对比差异">
                <TableHeader>
                  <TableRow>
                    <TableHead>字段</TableHead>
                    <TableHead>原结果</TableHead>
                    <TableHead>评估结果</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {comparison.data.differences.map((difference) => (
                    <TableRow key={difference.field}>
                      <TableCell className="font-mono text-meta">{difference.field}</TableCell>
                      <TableCell className="max-w-72 break-words">{difference.before || '—'}</TableCell>
                      <TableCell className="max-w-72 break-words">{difference.after || '—'}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
            {comparison.data.reproduction.reasons.length > 0 ? (
              <p className="text-xs text-muted-foreground">
                非完全复现原因：{comparison.data.reproduction.reasons.join('；')}
              </p>
            ) : null}
          </div>
        ) : null
      ) : null}
    </Card>
  )
}
