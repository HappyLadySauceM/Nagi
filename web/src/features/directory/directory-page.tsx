import { useState } from 'react'
import { useNavigate, useSearch } from '@tanstack/react-router'
import { RefreshCw, UserRoundSearch, UsersRound } from 'lucide-react'
import { toast } from 'sonner'
import { Avatar } from '../../components/avatar'
import { FeedbackState } from '../../components/feedback-state'
import { PageHeader } from '../../components/page-header'
import { Pagination } from '../../components/pagination'
import { Badge } from '../../components/ui/badge'
import { Button } from '../../components/ui/button'
import { Card } from '../../components/ui/card'
import { Skeleton } from '../../components/ui/skeleton'
import { indexScopes } from '../../lib/api/adapters/scope'
import type { Contact, Group, ScopePolicy } from '../../lib/api/contracts'
import { describeError } from '../../lib/errors'
import { formatCount, formatRelativeTime } from '../../lib/format'
import { cn } from '../../lib/utils'
import { useAccountContext } from '../accounts/account-context'
import { useScopeIndex } from '../scopes/queries'
import { ContactDetailPanel, type DirectorySelection } from './components/contact-detail-panel'
import { DirectoryTabs } from './components/directory-tabs'
import { DirectoryToolbar } from './components/directory-toolbar'
import { ScopeStatusBadge } from './components/scope-status-badge'
import { useContacts, useGroups, useSyncDirectory } from './queries'
import type { DirectoryTab } from '../../lib/search-params'

/**
 * Right-edge status of a row: a colored dot plus its label, so the state is
 * readable without hovering.
 * 行尾状态：色点 + 文字标签，无需悬停即可辨认状态。
 */
function PolicyStatusIndicator({ policy }: { policy?: ScopePolicy }) {
  const label = policy ? (policy.enabled ? '已启用' : '已保存未启用') : '默认关闭'
  return (
    <span
      className={cn(
        'flex shrink-0 items-center gap-1.5 whitespace-nowrap text-caption',
        policy
          ? policy.enabled
            ? 'text-success'
            : 'text-warning'
          : 'text-muted-foreground/60',
      )}
    >
      <span
        aria-hidden
        className={cn(
          'size-2 rounded-full',
          policy ? (policy.enabled ? 'bg-success' : 'bg-warning') : 'bg-muted-foreground/30',
        )}
      />
      {label}
    </span>
  )
}

/** Rows with a stored policy float to the top; relative order is preserved. */
// 已保存策略的条目置顶；其余保持接口返回的相对顺序。
function pinnedFirst<T>(rows: T[], saved: (row: T) => boolean): T[] {
  return [...rows.filter(saved), ...rows.filter((row) => !saved(row))]
}

function FriendRow({
  contact,
  active,
  policy,
  onSelect,
}: {
  contact: Contact
  active: boolean
  policy?: ScopePolicy
  onSelect: () => void
}) {
  const name = contact.remark || contact.nickname || '未命名好友'
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-current={active ? 'true' : undefined}
      className={cn(
        'flex w-full cursor-pointer items-center gap-3 rounded-xl px-3 py-2.5 text-left transition-colors duration-150',
        active ? 'bg-primary/10 ring-1 ring-primary/25' : 'hover:bg-secondary/60',
      )}
    >
      <Avatar id={contact.userId} name={name} size="sm" />
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5">
          <span className="truncate text-body font-medium text-foreground">{name}</span>
        </span>
        <span className="mt-0.5 flex items-center gap-2 text-caption text-muted-foreground">
          <span className="font-mono tabular-nums">{contact.userId}</span>
          {contact.remark && contact.nickname ? (
            <span className="truncate">昵称 {contact.nickname}</span>
          ) : null}
        </span>
      </span>
      <PolicyStatusIndicator policy={policy} />
    </button>
  )
}

function GroupRow({
  group,
  active,
  policy,
  onSelect,
}: {
  group: Group
  active: boolean
  policy?: ScopePolicy
  onSelect: () => void
}) {
  const name = group.name || '未命名群聊'
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-current={active ? 'true' : undefined}
      className={cn(
        'flex w-full cursor-pointer items-center gap-3 rounded-xl px-3 py-2.5 text-left transition-colors duration-150',
        active ? 'bg-primary/10 ring-1 ring-primary/25' : 'hover:bg-secondary/60',
      )}
    >
      <Avatar id={group.groupId} kind="group" name={name} size="sm" />
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5">
          <span className="truncate text-body font-medium text-foreground">{name}</span>
        </span>
        <span className="mt-0.5 flex items-center gap-2 text-caption text-muted-foreground">
          <span className="font-mono tabular-nums">{group.groupId}</span>
          <span>{formatCount(group.memberCount)} 人</span>
          {group.lastSeenAt ? <span>· {formatRelativeTime(group.lastSeenAt)}</span> : null}
        </span>
      </span>
      <PolicyStatusIndicator policy={policy} />
    </button>
  )
}

export function DirectoryPage() {
  const { tab, search, page, limit } = useSearch({ from: '/directory' })
  const navigate = useNavigate({ from: '/directory' })
  const {
    selectedBotId,
    selectedAccount,
    isLoading: accountsLoading,
    isError: accountsError,
    error: accountError,
    refetch: refetchAccounts,
  } = useAccountContext()

  const params = { search, page, limit }
  const contacts = useContacts(selectedBotId, params, tab === 'friends')
  const groups = useGroups(selectedBotId, params, tab === 'groups')
  const scopeIndex = useScopeIndex(selectedBotId)
  const sync = useSyncDirectory(selectedBotId)

  const [selectedKey, setSelectedKey] = useState<number | null>(null)

  const scopes = indexScopes(scopeIndex.data)
  const friendRows = pinnedFirst(contacts.data?.items ?? [], (row) =>
    scopes.has(`private:${row.userId}`),
  )
  const groupRows = pinnedFirst(groups.data?.items ?? [], (row) =>
    scopes.has(`group:${row.groupId}`),
  )
  const activeQuery = tab === 'friends' ? contacts : groups
  const hasFilter = search.trim().length > 0
  const isSyncing = sync.isPending

  const selectedContact = friendRows.find((row) => row.userId === selectedKey) ?? friendRows[0]
  const selectedGroup = groupRows.find((row) => row.groupId === selectedKey) ?? groupRows[0]

  const selection: DirectorySelection | undefined =
    tab === 'friends'
      ? selectedContact
        ? {
            kind: 'friend',
            id: selectedContact.userId,
            name: selectedContact.remark || selectedContact.nickname,
            remark: selectedContact.nickname,
            lastSeenAt: selectedContact.lastSeenAt,
            policy: scopes.get(`private:${selectedContact.userId}`),
          }
        : undefined
      : selectedGroup
        ? {
            kind: 'group',
            id: selectedGroup.groupId,
            name: selectedGroup.name,
            memberCount: selectedGroup.memberCount,
            lastSeenAt: selectedGroup.lastSeenAt,
            policy: scopes.get(`group:${selectedGroup.groupId}`),
          }
        : undefined

  const changeTab = (next: DirectoryTab) => {
    setSelectedKey(null)
    void navigate({ search: (prev) => ({ ...prev, tab: next, page: 1 }), replace: true })
  }

  const changeSearch = (value: string) => {
    setSelectedKey(null)
    void navigate({ search: (prev) => ({ ...prev, search: value, page: 1 }), replace: true })
  }

  const runSync = () => {
    sync.mutate(undefined, {
      onSuccess: (result) =>
        toast.success('目录同步完成', {
          description: `好友 ${result.contacts} 位 · 群 ${result.groups} 个`,
        }),
      onError: (error) => toast.error('同步失败', { description: describeError(error).description }),
    })
  }

  const total = activeQuery.data?.total

  return (
    <div className="space-y-4">
      <PageHeader
        title="联系人"
        description={
          selectedAccount
            ? `当前账号 ${selectedAccount.nickname || selectedAccount.botId}，好友与群聊统一在这里查看。作用域默认关闭，保存后才会参与对话。`
            : '好友与群聊统一在这里查看。作用域默认关闭，保存后才会参与对话。'
        }
        actions={
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => void activeQuery.refetch()}>
              <RefreshCw
                className={cn('size-3.5', activeQuery.isFetching && 'animate-spin')}
                aria-hidden
              />
              刷新
            </Button>
            <Button size="sm" onClick={runSync} disabled={isSyncing || selectedBotId === undefined}>
              <RefreshCw className={cn('size-3.5', isSyncing && 'animate-spin')} aria-hidden />
              {isSyncing ? '同步中…' : '同步目录'}
            </Button>
          </div>
        }
      />

      {accountsError ? (
        <Card>
          <FeedbackState variant="error" error={accountError} onRetry={refetchAccounts} />
        </Card>
      ) : selectedBotId === undefined ? (
        <Card>
          <FeedbackState
            variant={accountsLoading ? 'loading' : 'offline'}
            title={accountsLoading ? '正在识别账号' : '尚未识别 QQ 账号'}
            description={
              accountsLoading
                ? '正在等待 OneBot 返回登录信息。'
                : '目录必须绑定具体账号。请确认 SnowLuma 已登录，或在设置中检查端点配置。'
            }
          />
        </Card>
      ) : (
        <div className="grid gap-3 xl:grid-cols-[minmax(0,400px)_minmax(0,1fr)]">
          <Card className="flex min-h-[520px] flex-col overflow-hidden">
            <div className="space-y-3 border-b border-border/60 px-4 py-4">
              <DirectoryTabs
                tab={tab}
                onTabChange={changeTab}
                friendCount={tab === 'friends' ? total : undefined}
                groupCount={tab === 'groups' ? total : undefined}
              />
              <DirectoryToolbar
                searchLabel={tab === 'friends' ? '搜索好友' : '搜索群聊'}
                searchPlaceholder={tab === 'friends' ? '按昵称、备注或 QQ 号搜索' : '按群名或群号搜索'}
                searchValue={search}
                onSearchChange={changeSearch}
                total={total}
                totalLabel={tab === 'friends' ? '位好友' : '个群'}
                className="flex-col items-stretch border-b-0 px-0 py-0 lg:flex-col"
              />
            </div>

            <div className="min-h-0 flex-1 overflow-y-auto px-2 py-2">
              {activeQuery.isLoading ? (
                <div className="space-y-2 p-2">
                  {Array.from({ length: 6 }).map((_, index) => (
                    <Skeleton key={index} className="h-14 w-full rounded-xl" />
                  ))}
                </div>
              ) : activeQuery.isError ? (
                <FeedbackState
                  variant="error"
                  error={activeQuery.error}
                  onRetry={() => void activeQuery.refetch()}
                  compact
                />
              ) : tab === 'friends' ? (
                friendRows.length === 0 ? (
                  <FeedbackState
                    variant="empty"
                    title={hasFilter ? '没有匹配的好友' : '尚未同步好友'}
                    description={
                      hasFilter
                        ? `没有昵称或备注包含「${search}」的好友。`
                        : '同步会从 OneBot 拉取好友与群列表，需要账号已连接。'
                    }
                    action={
                      hasFilter ? (
                        <Button variant="outline" size="sm" onClick={() => changeSearch('')}>
                          清除搜索
                        </Button>
                      ) : (
                        <Button size="sm" onClick={runSync} disabled={isSyncing}>
                          <UserRoundSearch aria-hidden />
                          立即同步
                        </Button>
                      )
                    }
                    compact
                  />
                ) : (
                  <div className="space-y-1">
                    {friendRows.map((contact) => (
                      <FriendRow
                        key={contact.userId}
                        contact={contact}
                        active={contact.userId === selectedContact?.userId}
                        policy={scopes.get(`private:${contact.userId}`)}
                        onSelect={() => setSelectedKey(contact.userId)}
                      />
                    ))}
                  </div>
                )
              ) : groupRows.length === 0 ? (
                <FeedbackState
                  variant="empty"
                  title={hasFilter ? '没有匹配的群聊' : '尚未同步群聊'}
                  description={
                    hasFilter
                      ? `没有群名包含「${search}」的群聊。`
                      : '同步会从 OneBot 拉取好友与群列表，需要账号已连接。'
                  }
                  action={
                    hasFilter ? (
                      <Button variant="outline" size="sm" onClick={() => changeSearch('')}>
                        清除搜索
                      </Button>
                    ) : (
                      <Button size="sm" onClick={runSync} disabled={isSyncing}>
                        <UsersRound aria-hidden />
                        立即同步
                      </Button>
                    )
                  }
                  compact
                />
              ) : (
                <div className="space-y-1">
                  {groupRows.map((group) => (
                    <GroupRow
                      key={group.groupId}
                      group={group}
                      active={group.groupId === selectedGroup?.groupId}
                      policy={scopes.get(`group:${group.groupId}`)}
                      onSelect={() => setSelectedKey(group.groupId)}
                    />
                  ))}
                </div>
              )}
            </div>

            <div className="border-t border-border/60 px-4 py-3">
              <Pagination
                total={total ?? 0}
                page={page}
                limit={limit}
                pending={activeQuery.isFetching}
                itemLabel={tab === 'friends' ? '位好友' : '个群'}
                onPageChange={(next) =>
                  void navigate({ search: (prev) => ({ ...prev, page: next }) })
                }
                onLimitChange={(next) =>
                  void navigate({ search: (prev) => ({ ...prev, limit: next, page: 1 }) })
                }
              />
            </div>
          </Card>

          <div className="min-w-0">
            <ContactDetailPanel selection={selection} />
            {selection ? (
              <div className="mt-3 flex flex-wrap items-center gap-2 text-caption text-muted-foreground">
                <Badge variant="secondary">
                  {tab === 'friends' ? '私聊作用域' : '群聊作用域'}
                </Badge>
                <span>
                  {selection.policy
                    ? '已保存策略，可随时调整回复模式与数据权限。'
                    : '尚未保存策略，当前按默认拒绝处理。'}
                </span>
                <ScopeStatusBadge policy={selection.policy} />
              </div>
            ) : null}
          </div>
        </div>
      )}
    </div>
  )
}
