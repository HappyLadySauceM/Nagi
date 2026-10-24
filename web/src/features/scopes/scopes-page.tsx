import { Link } from '@tanstack/react-router'
import { RefreshCw, ShieldOff } from 'lucide-react'
import { Badge } from '../../components/ui/badge'
import { Button } from '../../components/ui/button'
import { Card } from '../../components/ui/card'
import { FeedbackState } from '../../components/feedback-state'
import { PageHeader } from '../../components/page-header'
import { Skeleton } from '../../components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '../../components/ui/table'
import { REPLY_MODE_LABELS, SCOPE_TYPE_LABELS } from '../../lib/api/contracts'
import { formatCount } from '../../lib/format'
import { cn } from '../../lib/utils'
import { ScopeStatusBadge } from '../directory/components/scope-status-badge'
import { useScopeIndex } from './queries'

export function ScopesPage() {
  const scopes = useScopeIndex()
  const rows = scopes.data ?? []

  return (
    <div className="space-y-4">
      <PageHeader
        title="作用域策略"
        description="这里只列出已保存的策略。从未保存过的好友或群会解析为默认拒绝：不回复、不保存消息、不写记忆。"
        actions={
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => void scopes.refetch()}>
              <RefreshCw
                className={cn('size-3.5', scopes.isFetching && 'animate-spin')}
                aria-hidden
              />
              刷新
            </Button>
            <Button asChild size="sm">
              <Link to="/groups" search={{ search: '', page: 1, limit: 20 }}>
                去群聊配置
              </Link>
            </Button>
          </div>
        }
      />

      <Card className="overflow-hidden">
        <div className="flex flex-col gap-1 border-b border-border/60 px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
          <p className="text-xs text-muted-foreground">
            未出现在列表中的目标即为默认关闭，不会响应任何消息。
          </p>
          <span className="text-[11px] text-muted-foreground tabular-nums">
            {scopes.data ? `已保存 ${formatCount(rows.length)} 条策略` : '策略数量不可获取'}
          </span>
        </div>

        {scopes.isLoading ? (
          <div className="space-y-2 p-5">
            {Array.from({ length: 4 }).map((_, index) => (
              <Skeleton key={index} className="h-10 w-full" />
            ))}
          </div>
        ) : scopes.isError ? (
          <FeedbackState
            variant="error"
            error={scopes.error}
            onRetry={() => void scopes.refetch()}
          />
        ) : rows.length === 0 ? (
          <FeedbackState
            variant="empty"
            title="还没有保存过任何策略"
            description="从联系人页或群聊页选择目标并配置策略，保存后会出现在这里。"
            action={
              <Button asChild size="sm">
                <Link to="/groups" search={{ search: '', page: 1, limit: 20 }}>
                  <ShieldOff aria-hidden />
                  选择一个群聊
                </Link>
              </Button>
            }
          />
        ) : (
          <Table label="已保存策略列表">
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>目标</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>回复模式</TableHead>
                <TableHead>数据权限</TableHead>
                <TableHead className="text-right">限流 / 留存</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((policy) => (
                <TableRow key={`${policy.scopeType}:${policy.scopeId}`}>
                  <TableCell>
                    <div className="min-w-0">
                      <p className="truncate text-xs font-medium text-foreground">
                        {SCOPE_TYPE_LABELS[policy.scopeType]}
                      </p>
                      <p className="font-mono text-[11px] text-muted-foreground tabular-nums">
                        {policy.scopeId}
                      </p>
                    </div>
                  </TableCell>
                  <TableCell>
                    <ScopeStatusBadge policy={policy} />
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {REPLY_MODE_LABELS[policy.replyMode]}
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-wrap gap-1">
                      {policy.storeMessages ? <Badge variant="secondary">消息</Badge> : null}
                      {policy.allowHistoryRead ? <Badge variant="secondary">历史</Badge> : null}
                      {policy.allowMemoryWrite ? <Badge variant="secondary">记忆</Badge> : null}
                      {policy.toolPolicy.length > 0 ? (
                        <Badge variant="accent">工具 {policy.toolPolicy.length}</Badge>
                      ) : null}
                      {!policy.storeMessages &&
                      !policy.allowHistoryRead &&
                      !policy.allowMemoryWrite &&
                      policy.toolPolicy.length === 0 ? (
                        <span className="text-[11px] text-muted-foreground">仅对话</span>
                      ) : null}
                    </div>
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-right font-mono text-[11px] text-muted-foreground tabular-nums">
                    <span className="block">
                      间隔 {policy.minReplyIntervalMs}ms · 上限 {policy.maxRepliesPerHour}/时
                    </span>
                    <span className="block">留存 {policy.retentionDays} 天</span>
                  </TableCell>
                  <TableCell className="text-right">
                    <Link
                      to="/scopes/$type/$id"
                      params={{ type: policy.scopeType, id: String(policy.scopeId) }}
                      className="text-[11px] font-medium text-primary transition-colors hover:text-primary/80 hover:underline"
                    >
                      编辑
                    </Link>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Card>
    </div>
  )
}
