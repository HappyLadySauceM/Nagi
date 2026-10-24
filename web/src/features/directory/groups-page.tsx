import { Link, useNavigate, useSearch } from '@tanstack/react-router'
import { RefreshCw, UsersRound } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '../../components/ui/button'
import { Card } from '../../components/ui/card'
import { FeedbackState } from '../../components/feedback-state'
import { PageHeader } from '../../components/page-header'
import { Pagination } from '../../components/pagination'
import { Skeleton } from '../../components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '../../components/ui/table'
import { indexScopes } from '../../lib/api/adapters/scope'
import { describeError } from '../../lib/errors'
import { formatCount, formatDateTime, formatRelativeTime } from '../../lib/format'
import { cn } from '../../lib/utils'
import { useScopeIndex } from '../scopes/queries'
import { DirectoryToolbar } from './components/directory-toolbar'
import { ScopeStatusBadge } from './components/scope-status-badge'
import { useGroups, useSyncDirectory } from './queries'

export function GroupsPage() {
  const { search, page, limit } = useSearch({ from: '/groups' })
  const navigate = useNavigate({ from: '/groups' })
  const params = { search, page, limit }

  const groups = useGroups(params)
  const scopeIndex = useScopeIndex()
  const sync = useSyncDirectory()

  const scopes = indexScopes(scopeIndex.data)
  const rows = groups.data?.items ?? []
  const hasFilter = search.trim().length > 0
  const isSyncing = sync.isPending

  const changeSearch = (value: string) => {
    void navigate({ search: (prev) => ({ ...prev, search: value, page: 1 }), replace: true })
  }

  const runSync = () => {
    sync.mutate(undefined, {
      onSuccess: (result) =>
        toast.success('目录同步完成', {
          description: `好友 ${result.contacts} 位 · 群 ${result.groups} 个`,
        }),
      onError: (error) =>
        toast.error('同步失败', { description: describeError(error).description }),
    })
  }

  return (
    <div className="space-y-4">
      <PageHeader
        title="群聊"
        description="已同步的群列表。成员需要进入群详情单独刷新，策略默认关闭，未保存的群不会响应任何消息。"
        actions={
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => void groups.refetch()}>
              <RefreshCw
                className={cn('size-3.5', groups.isFetching && 'animate-spin')}
                aria-hidden
              />
              刷新
            </Button>
            <Button size="sm" onClick={runSync} disabled={isSyncing}>
              <RefreshCw className={cn('size-3.5', isSyncing && 'animate-spin')} aria-hidden />
              {isSyncing ? '同步中…' : '同步目录'}
            </Button>
          </div>
        }
      />

      <Card className="overflow-hidden">
        <DirectoryToolbar
          searchLabel="搜索群聊"
          searchPlaceholder="按群名搜索"
          searchValue={search}
          onSearchChange={changeSearch}
          total={groups.data?.total}
          totalLabel="个群"
        />

        {groups.isLoading ? (
          <div className="space-y-2 p-5">
            {Array.from({ length: 6 }).map((_, index) => (
              <Skeleton key={index} className="h-10 w-full" />
            ))}
          </div>
        ) : groups.isError ? (
          <FeedbackState
            variant="error"
            error={groups.error}
            onRetry={() => void groups.refetch()}
          />
        ) : rows.length === 0 ? (
          hasFilter ? (
            <FeedbackState
              variant="empty"
              title="没有匹配的群"
              description={`没有群名包含「${search}」的群聊。`}
              action={
                <Button variant="outline" size="sm" onClick={() => changeSearch('')}>
                  清除搜索
                </Button>
              }
            />
          ) : (
            <FeedbackState
              variant="empty"
              title="尚未同步群列表"
              description="目录为空。同步会从 OneBot 拉取好友与群列表，需要机器人已连接。"
              action={
                <Button size="sm" onClick={runSync} disabled={isSyncing}>
                  <UsersRound aria-hidden />
                  立即同步
                </Button>
              }
            />
          )
        ) : (
          <Table label="群聊列表">
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>群聊</TableHead>
                <TableHead className="text-right">群号</TableHead>
                <TableHead className="text-right">成员数</TableHead>
                <TableHead>策略状态</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((group) => (
                <TableRow key={group.groupId}>
                  <TableCell>
                    <div className="min-w-0">
                      <p className="truncate text-xs font-medium text-foreground">
                        {group.name || '未命名群聊'}
                      </p>
                      <p
                        className="truncate text-[11px] text-muted-foreground"
                        title={group.lastSeenAt ? formatDateTime(group.lastSeenAt) : undefined}
                      >
                        {group.lastSeenAt
                          ? `最近同步 ${formatRelativeTime(group.lastSeenAt)}`
                          : '未记录同步时间'}
                      </p>
                    </div>
                  </TableCell>
                  <TableCell className="text-right font-mono text-muted-foreground tabular-nums">
                    {group.groupId}
                  </TableCell>
                  <TableCell className="text-right text-muted-foreground tabular-nums">
                    {formatCount(group.memberCount)}
                  </TableCell>
                  <TableCell>
                    <ScopeStatusBadge policy={scopes.get(`group:${group.groupId}`)} />
                  </TableCell>
                  <TableCell>
                    <div className="flex items-center justify-end gap-3">
                      <Link
                        to="/groups/$groupId"
                        params={{ groupId: String(group.groupId) }}
                        search={{ search: '', page: 1, limit: 20, name: group.name }}
                        className="text-[11px] font-medium text-muted-foreground transition-colors hover:text-foreground hover:underline"
                      >
                        成员
                      </Link>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}

        {rows.length > 0 ? (
          <div className="px-5 pb-4">
            <Pagination
              total={groups.data?.total ?? 0}
              page={page}
              limit={limit}
              pending={groups.isFetching}
              itemLabel="个群"
              onPageChange={(next) =>
                void navigate({ search: (prev) => ({ ...prev, page: next }) })
              }
              onLimitChange={(next) =>
                void navigate({ search: (prev) => ({ ...prev, limit: next, page: 1 }) })
              }
            />
          </div>
        ) : null}
      </Card>
    </div>
  )
}
