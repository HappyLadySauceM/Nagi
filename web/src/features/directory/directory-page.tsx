import { useState } from 'react'
import { useNavigate, useSearch } from '@tanstack/react-router'
import { RefreshCw, Settings2, UserRoundSearch, UsersRound } from 'lucide-react'
import { toast } from 'sonner'
import { Avatar } from '../../components/avatar'
import { FeedbackState } from '../../components/feedback-state'
import { PageHeader } from '../../components/page-header'
import { Pagination } from '../../components/pagination'
import { Button } from '../../components/ui/button'
import { Card } from '../../components/ui/card'
import { Skeleton } from '../../components/ui/skeleton'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuTrigger } from '../../components/ui/dropdown-menu'
import { indexScopes } from '../../lib/api/adapters/scope'
import type { Contact, Group, ScopePolicy } from '../../lib/api/contracts'
import { describeError } from '../../lib/errors'
import { formatCount, formatRelativeTime } from '../../lib/format'
import { cn } from '../../lib/utils'
import { useAccountContext } from '../accounts/account-context'
import { useScope, useScopeIndex } from '../scopes/queries'
import { ContactDetailPanel, type DirectorySelection } from './components/contact-detail-panel'
import { ScopeEditDialog } from './components/scope-edit-dialog'
import { DirectoryTabs } from './components/directory-tabs'
import { DirectoryToolbar } from './components/directory-toolbar'
import { useContacts, useGroups, useSyncDirectory } from './queries'
import type { DirectoryTab } from '../../lib/search-params'

/**
 * Right-edge status of a row: a colored dot plus its label, so the state is
 * readable without hovering.
 * 行尾状态：色点 + 文字标签，无需悬停即可辨认状态。
 */
function PolicyStatusIndicator({ policy }: { policy?: ScopePolicy }) {
  const label = `${policy?.enabled ? '已启用' : '已关闭'} · ${policy?.source === 'default' ? '跟随默认' : policy ? '独立策略' : '默认策略'}`
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
        'flex w-full cursor-pointer items-start gap-3 rounded-md px-3 py-3 text-left transition-colors duration-150',
        active ? 'bg-secondary ring-1 ring-border' : 'hover:bg-secondary/60',
      )}
    >
      <Avatar id={contact.userId} name={name} size="md" />
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5">
          <span className="line-clamp-2 break-words text-body font-medium text-foreground">{name}</span>
        </span>
        <span className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-caption text-muted-foreground">
          <span className="whitespace-nowrap font-mono tabular-nums">{contact.userId}</span>
          {contact.remark && contact.nickname ? (
            <span className="truncate">昵称 {contact.nickname}</span>
          ) : null}
        </span>
        <span className="mt-2 block"><PolicyStatusIndicator policy={policy} /></span>
      </span>
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
        'flex w-full cursor-pointer items-start gap-3 rounded-md px-3 py-3 text-left transition-colors duration-150',
        active ? 'bg-secondary ring-1 ring-border' : 'hover:bg-secondary/60',
      )}
    >
      <Avatar id={group.groupId} kind="group" name={name} size="md" />
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5">
          <span className="line-clamp-2 break-words text-body font-medium text-foreground">{name}</span>
        </span>
        <span className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-caption text-muted-foreground">
          <span className="whitespace-nowrap font-mono tabular-nums">{group.groupId}</span>
          <span className="whitespace-nowrap">{formatCount(group.memberCount)} 人</span>
          {group.lastSeenAt ? <span className="whitespace-nowrap">{formatRelativeTime(group.lastSeenAt)}</span> : null}
        </span>
        <span className="mt-2 block"><PolicyStatusIndicator policy={policy} /></span>
      </span>
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
  const friendDefault = useScope(selectedBotId, 'private', 0)
  const groupDefault = useScope(selectedBotId, 'group', 0)
  const [editingDefault, setEditingDefault] = useState<'private' | 'group' | null>(null)
  const sync = useSyncDirectory(selectedBotId)

  const [selectedKey, setSelectedKey] = useState<number | null>(null)
  const [mobileDetail,setMobileDetail] = useState(false)

  const scopes = indexScopes(scopeIndex.data)
  const friendRows = pinnedFirst(contacts.data?.items ?? [], (row) =>
    scopes.has(`private:${row.userId}`),
  )
  const groupRows = pinnedFirst(groups.data?.items ?? [], (row) =>
    scopes.has(`group:${row.groupId}`),
  )
  const effectiveScopes = new Map(scopes)
  for (const row of friendRows) if(row.policy) effectiveScopes.set(`private:${row.userId}`,row.policy)
  for (const row of groupRows) if(row.policy) effectiveScopes.set(`group:${row.groupId}`,row.policy)
  for (const row of friendRows) if (!effectiveScopes.has(`private:${row.userId}`) && friendDefault.data) effectiveScopes.set(`private:${row.userId}`, { ...friendDefault.data, scopeId: row.userId, source: 'default' })
  for (const row of groupRows) if (!effectiveScopes.has(`group:${row.groupId}`) && groupDefault.data) effectiveScopes.set(`group:${row.groupId}`, { ...groupDefault.data, scopeId: row.groupId, source: 'default' })
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
            policy: effectiveScopes.get(`private:${selectedContact.userId}`),
          }
        : undefined
      : selectedGroup
        ? {
            kind: 'group',
            id: selectedGroup.groupId,
            name: selectedGroup.name,
            memberCount: selectedGroup.memberCount,
            lastSeenAt: selectedGroup.lastSeenAt,
            policy: effectiveScopes.get(`group:${selectedGroup.groupId}`),
          }
        : undefined

  const changeTab = (next: DirectoryTab) => {
    setSelectedKey(null)
    setMobileDetail(false)
    void navigate({ search: (prev) => ({ ...prev, tab: next, page: 1 }), replace: true })
  }

  const changeSearch = (value: string) => {
    setSelectedKey(null)
    setMobileDetail(false)
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
            ? `${selectedAccount.nickname || selectedAccount.botId} · 管理好友、群聊与机器人的参与策略。`
            : '管理好友、群聊与机器人的参与策略。'
        }
        actions={
          <div className="flex flex-wrap items-center gap-2">
            {selectedBotId !== undefined ? (
              <DropdownMenu>
                <DropdownMenuTrigger asChild><Button variant="outline" size="sm"><Settings2 aria-hidden />默认策略</Button></DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-64">
                  <DropdownMenuLabel>未独立配置的会话跟随默认策略</DropdownMenuLabel>
                  {([{ kind: 'private' as const, title: '好友默认策略', query: friendDefault }, { kind: 'group' as const, title: '群聊默认策略', query: groupDefault }]).map(({ kind, title, query }) => (
                    <DropdownMenuItem key={kind} disabled={query.isLoading} onSelect={() => query.isError ? void query.refetch() : setEditingDefault(kind)} className="flex items-center justify-between gap-3 py-3">
                      <span>{title}</span><span className="text-meta text-muted-foreground">{query.isLoading ? '加载中' : query.isError ? '读取失败 · 重试' : query.data?.enabled ? '已启用' : '已关闭'}</span>
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
            ) : null}
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

      {selectedBotId !== undefined && editingDefault ? <ScopeEditDialog key={`${selectedBotId}:${editingDefault}`} open onOpenChange={(open) => { if (!open) setEditingDefault(null) }} botId={selectedBotId} scopeType={editingDefault} scopeId={0} targetLabel={editingDefault === 'private' ? '默认好友策略' : '默认群聊策略'} /> : null}

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
        <div className="grid overflow-hidden rounded-lg border border-border bg-card xl:h-[calc(100dvh-13rem)] xl:min-h-[560px] xl:grid-cols-[clamp(400px,34%,560px)_minmax(0,1fr)]">
          <div className={cn('min-h-[520px] flex-col overflow-hidden xl:min-h-0', mobileDetail ? 'hidden xl:flex' : 'flex')}>
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
                        policy={effectiveScopes.get(`private:${contact.userId}`)}
                        onSelect={() => {setSelectedKey(contact.userId);setMobileDetail(true)}}
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
                      policy={effectiveScopes.get(`group:${group.groupId}`)}
                      onSelect={() => {setSelectedKey(group.groupId);setMobileDetail(true)}}
                    />
                  ))}
                </div>
              )}
            </div>

            <div className="border-t border-border/60 px-4 py-3">
              <Pagination
                compact
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
          </div>

          <div className={cn('min-h-0 min-w-0 overflow-y-auto xl:border-l xl:border-border',mobileDetail ? 'block' : 'hidden xl:block')}>
            <Button className="m-4 xl:hidden" variant="outline" onClick={()=>setMobileDetail(false)}>返回联系人列表</Button>
            <ContactDetailPanel key={`${selectedBotId}:${tab}:${selection?.id}`} selection={selection} />

          </div>
        </div>
      )}
    </div>
  )
}
