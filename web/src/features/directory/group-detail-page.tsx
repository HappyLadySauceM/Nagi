import { useState } from 'react'
import { Link, useNavigate, useParams, useSearch } from '@tanstack/react-router'
import { ArrowLeft, RefreshCw } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '../../components/ui/button'
import { Card } from '../../components/ui/card'
import { FeedbackState } from '../../components/feedback-state'
import { InfoRow } from '../../components/info-row'
import { PageHeader } from '../../components/page-header'
import { Pagination } from '../../components/pagination'
import { Select } from '../../components/ui/select'
import { Skeleton } from '../../components/ui/skeleton'
import { ApiError } from '../../lib/api/client'
import { describeError } from '../../lib/errors'
import { formatCount } from '../../lib/format'
import { cn } from '../../lib/utils'
import { DirectoryToolbar } from './components/directory-toolbar'
import { MembersTable } from './components/members-table'
import { useGroupMembers, useRefreshGroupMembers } from './queries'

const ROLE_OPTIONS = [
  { value: '', label: '全部角色' },
  { value: 'owner', label: '群主' },
  { value: 'admin', label: '管理员' },
  { value: 'member', label: '成员' },
]

function refreshFailureHint(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.kind === 'conflict') {
      return '后端还没有可关联的机器人账号。请先在群聊页执行「同步目录」，成功后再刷新成员。'
    }
    if (error.kind === 'server') {
      return '刷新成员时后端调用 OneBot 失败。请确认机器人已连接、群号有效后重试。'
    }
  }
  return describeError(error).description
}

export function GroupDetailPage() {
  const { groupId } = useParams({ from: '/groups/$groupId' })
  const { search, page, limit, name } = useSearch({ from: '/groups/$groupId' })
  const navigate = useNavigate({ from: '/groups/$groupId' })

  const numericId = Number(groupId)
  const validGroup = Number.isInteger(numericId) && numericId > 0
  const params = { search, page, limit }

  const members = useGroupMembers(numericId, params, validGroup)
  const refresh = useRefreshGroupMembers(numericId, params)
  const [roleFilter, setRoleFilter] = useState('')

  const rows = members.data?.items ?? []
  const visibleRows = roleFilter ? rows.filter((row) => row.role === roleFilter) : rows
  const hasFilter = search.trim().length > 0

  const changeSearch = (value: string) => {
    void navigate({
      search: (prev) => ({ ...prev, search: value, page: 1 }),
      replace: true,
    })
  }

  const runRefresh = () => {
    refresh.mutate(undefined, {
      onSuccess: (result) =>
        toast.success('成员已刷新', {
          description: `后端返回 ${result.items.length} 人，该群共 ${result.total} 人`,
        }),
      onError: (error) =>
        toast.error('刷新成员失败', { description: refreshFailureHint(error) }),
    })
  }

  if (!validGroup) {
    return (
      <div className="space-y-4">
        <PageHeader title="群详情" description="群号无效，无法查询成员。" />
        <Card>
          <FeedbackState
            variant="error"
            title="群号无效"
            description="地址中的群号必须是正整数，请从群聊列表重新进入。"
            action={
              <Button asChild variant="outline" size="sm">
                <Link to="/groups" search={{ search: '', page: 1, limit: 20 }}>
                  返回群聊
                </Link>
              </Button>
            }
          />
        </Card>
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <PageHeader
        title={name || `群 ${groupId}`}
        description={`群号 ${groupId} · 成员列表按需从 OneBot 刷新，未刷新时展示上次同步结果。`}
        actions={
          <div className="flex items-center gap-2">
            <Button asChild variant="ghost" size="sm">
              <Link to="/groups" search={{ search: '', page: 1, limit: 20 }}>
                <ArrowLeft aria-hidden />
                群列表
              </Link>
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={runRefresh}
              disabled={refresh.isPending}
            >
              <RefreshCw
                className={cn('size-3.5', refresh.isPending && 'animate-spin')}
                aria-hidden
              />
              {refresh.isPending ? '刷新中…' : '刷新成员'}
            </Button>
            <Button asChild size="sm">
              <Link to="/scopes/$type/$id" params={{ type: 'group', id: groupId }}>
                配置策略
              </Link>
            </Button>
          </div>
        }
      />

      <Card className="px-5 py-2">
        <InfoRow label="群号" value={groupId} mono />
        <InfoRow label="群名" value={name || '未从列表进入，群名未加载'} />
        <InfoRow
          label="成员总数"
          value={members.data ? formatCount(members.data.total) : '不可获取'}
          mono
        />
        <InfoRow
          label="当前页"
          value={
            members.data
              ? `第 ${members.data.page} 页 · 每页 ${members.data.limit} 条`
              : '不可获取'
          }
        />
      </Card>

      <Card className="overflow-hidden">
        <DirectoryToolbar
          searchLabel="搜索群成员"
          searchPlaceholder="按昵称或群名片搜索"
          searchValue={search}
          onSearchChange={changeSearch}
          total={members.data?.total}
          totalLabel="名成员"
        >
          <div className="flex items-center gap-2">
            <Select
              aria-label="按角色筛选当前页"
              containerClassName="w-[132px]"
              value={roleFilter}
              onChange={(event) => setRoleFilter(event.target.value)}
            >
              {ROLE_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </Select>
            {roleFilter ? (
              <span className="text-[11px] text-muted-foreground">仅过滤当前页</span>
            ) : null}
          </div>
        </DirectoryToolbar>

        {members.isLoading ? (
          <div className="space-y-2 p-5">
            {Array.from({ length: 6 }).map((_, index) => (
              <Skeleton key={index} className="h-10 w-full" />
            ))}
          </div>
        ) : members.isError ? (
          <FeedbackState
            variant="error"
            error={members.error}
            onRetry={() => void members.refetch()}
          />
        ) : visibleRows.length === 0 ? (
          hasFilter || roleFilter ? (
            <FeedbackState
              variant="empty"
              title="当前页没有匹配的成员"
              description={
                hasFilter
                  ? `服务端没有返回昵称或群名片包含「${search}」的成员。`
                  : '当前页没有该角色的成员，可翻页后再试。'
              }
              action={
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    setRoleFilter('')
                    if (hasFilter) {
                      changeSearch('')
                    }
                  }}
                >
                  清除筛选
                </Button>
              }
            />
          ) : (
            <FeedbackState
              variant="empty"
              title="尚未刷新群成员"
              description="该群还没有成员记录，或成员已被清空。刷新会从 OneBot 拉取最新成员列表。"
              action={
                <Button size="sm" onClick={runRefresh} disabled={refresh.isPending}>
                  <RefreshCw aria-hidden />
                  刷新成员
                </Button>
              }
            />
          )
        ) : (
          <MembersTable members={visibleRows} />
        )}

        {rows.length > 0 ? (
          <div className="px-5 pb-4">
            <Pagination
              total={members.data?.total ?? 0}
              page={page}
              limit={limit}
              pending={members.isFetching}
              itemLabel="名成员"
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
