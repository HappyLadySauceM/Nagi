import { Cloud, RefreshCw, Upload } from 'lucide-react'
import { Badge } from '../../../components/ui/badge'
import { Button } from '../../../components/ui/button'
import { Card } from '../../../components/ui/card'
import { Skeleton } from '../../../components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '../../../components/ui/table'
import type { PromptSyncCandidate, PromptSyncStatus } from '../../../lib/api/contracts'
import { describeError } from '../../../lib/errors'
import { formatRelativeTime } from '../../../lib/format'

const STATUS_LABELS: Record<PromptSyncStatus['status'], string> = {
  not_configured: '未配置（仅本地）',
  idle: '未同步',
  ok: '已同步',
  failed: '同步失败',
}

const STATUS_VARIANTS: Record<PromptSyncStatus['status'], 'secondary' | 'success' | 'warning' | 'destructive'> = {
  not_configured: 'secondary',
  idle: 'warning',
  ok: 'success',
  failed: 'destructive',
}

const SOURCE_LABELS: Record<string, string> = {
  builtin: '内置',
  local: '本地',
  langfuse: 'Langfuse',
}

interface PromptSyncPanelProps {
  status?: PromptSyncStatus
  loading: boolean
  error?: unknown
  onRetry: () => void
  onSync: () => void
  syncing: boolean
  onActivate: (candidate: PromptSyncCandidate) => void
  activating: boolean
}

/** Optional Langfuse prompt sync status and candidate activation.
 * 可选的 Langfuse Prompt 同步状态与候选激活面板。
 */
export function PromptSyncPanel({
  status,
  loading,
  error,
  onRetry,
  onSync,
  syncing,
  onActivate,
  activating,
}: PromptSyncPanelProps) {
  if (loading) {
    return <Skeleton className="h-28 w-full" />
  }
  if (error) {
    return (
      <Card className="space-y-3 p-4">
        <div className="flex items-center justify-between gap-3">
          <h3 className="text-sm font-semibold">Langfuse 同步（可选）</h3>
          <Button size="sm" variant="outline" onClick={onRetry}>
            <RefreshCw className="size-3.5" aria-hidden />
            重试
          </Button>
        </div>
        <p role="alert" className="text-sm text-destructive">
          {describeError(error).description}
        </p>
      </Card>
    )
  }
  if (!status) {
    return null
  }
  return (
    <Card className="space-y-4 p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 className="flex items-center gap-2 text-sm font-semibold">
            <Cloud className="size-4" aria-hidden />
            Langfuse 同步（可选）
          </h3>
          <p className="mt-1 text-meta text-muted-foreground">
            仅显式同步时拉取远端 Prompt；同步成功后先写本地草稿，再由你确认激活。未配置时只使用本地版本。
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant={STATUS_VARIANTS[status.status]}>{STATUS_LABELS[status.status]}</Badge>
          {status.status === 'failed' ? <Badge variant="warning">已降级：继续使用本地版本</Badge> : null}
          <Button
            size="sm"
            variant="outline"
            onClick={onSync}
            disabled={!status.configured || syncing}
            title={status.configured ? undefined : '未配置 Langfuse，本地版本不受影响'}
          >
            <Upload className="size-3.5" aria-hidden />
            {syncing ? '同步中…' : '同步最新版本'}
          </Button>
        </div>
      </div>

      {!status.configured ? (
        <p className="text-sm text-muted-foreground">
          未配置 Langfuse：本地版本仍可使用，不影响启动、判定和生成。
        </p>
      ) : null}
      {status.message ? <p className="text-sm text-muted-foreground">{status.message}</p> : null}
      {status.reasonCode ? (
        <p className="font-mono text-xs text-muted-foreground">原因码：{status.reasonCode}</p>
      ) : null}
      <div className="flex flex-wrap gap-4 text-xs text-muted-foreground">
        <span>最近尝试：{status.lastAttemptAt ? formatRelativeTime(status.lastAttemptAt) : '—'}</span>
        <span>最近成功：{status.lastSuccessAt ? formatRelativeTime(status.lastSuccessAt) : '—'}</span>
        {status.active ? (
          <span>
            当前生效：v{status.active.version} · {(SOURCE_LABELS[status.active.source] ?? status.active.source) || '—'}
            {status.active.sourceRef ? ` · ${status.active.sourceRef}` : ''}
          </span>
        ) : null}
      </div>

      {status.candidates.length === 0 ? (
        <p className="text-sm text-muted-foreground">暂无同步候选。</p>
      ) : (
        <div className="overflow-hidden rounded-lg border border-border">
          <Table label="Langfuse 同步候选">
            <TableHeader>
              <TableRow>
                <TableHead>候选</TableHead>
                <TableHead>版本</TableHead>
                <TableHead>Hash</TableHead>
                <TableHead>来源</TableHead>
                <TableHead>同步时间</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {status.candidates.map((candidate) => (
                <TableRow key={candidate.id}>
                  <TableCell className="max-w-56 truncate">{candidate.name || candidate.sourceRef || '—'}</TableCell>
                  <TableCell className="font-mono">v{candidate.version}</TableCell>
                  <TableCell className="max-w-48 truncate font-mono text-meta text-muted-foreground">{candidate.hash || '—'}</TableCell>
                  <TableCell className="max-w-56 truncate text-meta text-muted-foreground">{candidate.sourceRef || '—'}</TableCell>
                  <TableCell className="text-meta text-muted-foreground">
                    {candidate.createdAt ? formatRelativeTime(candidate.createdAt) : '—'}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={activating}
                      onClick={() => onActivate(candidate)}
                    >
                      激活
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </Card>
  )
}
