import { useNavigate, useSearch } from '@tanstack/react-router'
import { RefreshCw, UserRoundSearch } from 'lucide-react'
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
import { formatDateTime, formatRelativeTime } from '../../lib/format'
import { cn } from '../../lib/utils'
import { useScopeIndex } from '../scopes/queries'
import { DirectoryToolbar } from './components/directory-toolbar'
import { ScopeStatusBadge } from './components/scope-status-badge'
import { useContacts, useSyncDirectory } from './queries'

export function ContactsPage() {
  const { search, page, limit } = useSearch({ from: '/contacts' })
  const navigate = useNavigate({ from: '/contacts' })
  const params = { search, page, limit }

  const contacts = useContacts(params)
  const scopeIndex = useScopeIndex()
  const sync = useSyncDirectory()

  const scopes = indexScopes(scopeIndex.data)
  const rows = contacts.data?.items ?? []
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
        title="联系人"
        description="从 OneBot 同步的好友目录，按昵称与备注搜索。作用域默认关闭，只有显式保存后才会参与对话。"
        actions={
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => void contacts.refetch()}>
              <RefreshCw
                className={cn('size-3.5', contacts.isFetching && 'animate-spin')}
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
          searchLabel="搜索联系人"
          searchPlaceholder="按昵称或备注搜索"
          searchValue={search}
          onSearchChange={changeSearch}
          total={contacts.data?.total}
          totalLabel="位好友"
        />

        {contacts.isLoading ? (
          <div className="space-y-2 p-5">
            {Array.from({ length: 6 }).map((_, index) => (
              <Skeleton key={index} className="h-10 w-full" />
            ))}
          </div>
        ) : contacts.isError ? (
          <FeedbackState
            variant="error"
            error={contacts.error}
            onRetry={() => void contacts.refetch()}
          />
        ) : rows.length === 0 ? (
          hasFilter ? (
            <FeedbackState
              variant="empty"
              title="没有匹配的联系人"
              description={`没有昵称或备注包含「${search}」的好友。`}
              action={
                <Button variant="outline" size="sm" onClick={() => changeSearch('')}>
                  清除搜索
                </Button>
              }
            />
          ) : (
            <FeedbackState
              variant="empty"
              title="尚未同步联系人"
              description="目录为空。同步会从 OneBot 拉取好友与群列表，需要机器人已连接。"
              action={
                <Button size="sm" onClick={runSync} disabled={isSyncing}>
                  <UserRoundSearch aria-hidden />
                  立即同步
                </Button>
              }
            />
          )
        ) : (
          <Table label="联系人列表">
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>好友</TableHead>
                <TableHead className="text-right">QQ 号</TableHead>
                <TableHead className="text-right">最近同步</TableHead>
                <TableHead>策略状态</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((contact) => (
                <TableRow key={contact.userId}>
                  <TableCell>
                    <div className="min-w-0">
                      <p className="truncate text-xs font-medium text-foreground">
                        {contact.remark || contact.nickname || '未命名好友'}
                      </p>
                      {contact.remark && contact.nickname ? (
                        <p className="truncate text-[11px] text-muted-foreground">
                          昵称 {contact.nickname}
                        </p>
                      ) : null}
                    </div>
                  </TableCell>
                  <TableCell className="text-right font-mono text-muted-foreground tabular-nums">
                    {contact.userId}
                  </TableCell>
                  <TableCell
                    className="whitespace-nowrap text-right text-muted-foreground tabular-nums"
                    title={contact.lastSeenAt ? formatDateTime(contact.lastSeenAt) : undefined}
                  >
                    {contact.lastSeenAt ? formatRelativeTime(contact.lastSeenAt) : '未记录'}
                  </TableCell>
                  <TableCell>
                    <ScopeStatusBadge policy={scopes.get(`private:${contact.userId}`)} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}

        {rows.length > 0 ? (
          <div className="px-5 pb-4">
            <Pagination
              total={contacts.data?.total ?? 0}
              page={page}
              limit={limit}
              pending={contacts.isFetching}
              itemLabel="位好友"
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
