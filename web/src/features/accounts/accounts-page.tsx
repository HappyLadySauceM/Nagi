import { Link } from '@tanstack/react-router'
import { RefreshCw, ShieldCheck, TriangleAlert, Users } from 'lucide-react'
import { toast } from 'sonner'
import { Avatar } from '../../components/avatar'
import { Badge } from '../../components/ui/badge'
import { Button } from '../../components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '../../components/ui/card'
import { FeedbackState } from '../../components/feedback-state'
import { InfoRow } from '../../components/info-row'
import { PageHeader } from '../../components/page-header'
import { Skeleton } from '../../components/ui/skeleton'
import { StatusIndicator } from '../../components/status-indicator'
import type { AccountSummary } from '../../lib/api/contracts'
import { describeError } from '../../lib/errors'
import {
  formatCount,
  formatDateTime,
  formatRelativeTime,
  formatUptime,
  UNAVAILABLE_TEXT,
} from '../../lib/format'
import { cn } from '../../lib/utils'
import { useSyncDirectory } from '../directory/queries'
import { useStatus } from '../system/queries'
import { accountLabel, useAccountContext } from './account-context'

function statusOf(account: AccountSummary): { label: string; variant: 'success' | 'warning' | 'destructive' } {
  if (account.rejected) {
    return { label: '已拒绝连接', variant: 'destructive' }
  }
  if (account.connected && account.selfIdKnown) {
    return { label: '在线', variant: 'success' }
  }
  if (account.connected) {
    return { label: '等待登录信息', variant: 'warning' }
  }
  return { label: '离线', variant: 'warning' }
}

function AccountCard({
  account,
  active,
  onSelect,
}: {
  account: AccountSummary
  active: boolean
  onSelect: () => void
}) {
  const status = statusOf(account)
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-pressed={active}
      className={cn(
        'flex w-full cursor-pointer flex-col gap-3 rounded-xl border p-4 text-left transition-colors duration-150',
        active
          ? 'border-primary/40 bg-primary/8'
          : 'border-border bg-card hover:border-primary/25 hover:bg-secondary/40',
      )}
    >
      <span className="flex items-center gap-3">
        <Avatar id={account.botId} name={accountLabel(account)} size="md" />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-body font-medium text-foreground">
            {accountLabel(account)}
          </span>
          <span className="block font-mono text-caption text-muted-foreground tabular-nums">
            {account.botId ?? '等待 get_login_info'}
          </span>
        </span>
        <Badge variant={status.variant}>{status.label}</Badge>
      </span>
      <span className="block truncate font-mono text-caption text-muted-foreground">
        {account.endpoint || '未配置端点'}
      </span>
      {account.rejected ? (
        <span className="flex items-start gap-1.5 rounded-lg border border-destructive/25 bg-destructive/10 px-2 py-1.5 text-caption leading-relaxed text-destructive">
          <TriangleAlert className="mt-0.5 size-3 shrink-0" aria-hidden />
          {account.rejected}
        </span>
      ) : null}
    </button>
  )
}

export function AccountsPage() {
  const {
    accounts,
    selectedBotId,
    selectedAccount,
    selectAccount,
    dbConfigured,
    connected,
    isLoading,
    isFetching,
    isError,
    error,
    refetch,
  } = useAccountContext()
  const status = useStatus()
  const sync = useSyncDirectory(selectedBotId)

  const runSync = () => {
    sync.mutate(undefined, {
      onSuccess: (result) =>
        toast.success('目录同步完成', {
          description: `好友 ${result.contacts} 位 · 群 ${result.groups} 个`,
        }),
      onError: (syncError) =>
        toast.error('同步失败', { description: describeError(syncError).description }),
    })
  }

  return (
    <div className="space-y-4">
      <PageHeader
        title="账号连接"
        description="每个 SnowLuma OneBot 端点对应一个 QQ 账号。连接后用 get_login_info 自动识别身份，预期号码不一致时会拒绝该端点。"
        actions={
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={refetch} disabled={isFetching}>
              <RefreshCw className={cn('size-3.5', isFetching && 'animate-spin')} aria-hidden />
              刷新
            </Button>
            <Button size="sm" onClick={runSync} disabled={selectedBotId === undefined || sync.isPending}>
              <RefreshCw className={cn('size-3.5', sync.isPending && 'animate-spin')} aria-hidden />
              {sync.isPending ? '同步中…' : '同步选中账号目录'}
            </Button>
          </div>
        }
      />

      {isError ? (
        <Card>
          <FeedbackState variant="error" error={error} onRetry={refetch} />
        </Card>
      ) : isLoading ? (
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 2 }).map((_, index) => (
            <Skeleton key={index} className="h-28 w-full rounded-xl" />
          ))}
        </div>
      ) : accounts.length === 0 ? (
        <Card>
          <FeedbackState
            variant="empty"
            title="没有配置 OneBot 端点"
            description="在 configs/config.yaml 的 onebot.endpoints 中添加 SnowLuma 端点后重启进程。"
          />
        </Card>
      ) : (
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          {accounts.map((account) => (
            <AccountCard
              key={account.endpoint + String(account.botId ?? '')}
              account={account}
              active={account.botId !== undefined && account.botId === selectedBotId}
              onSelect={() => {
                if (account.botId !== undefined) {
                  selectAccount(account.botId)
                }
              }}
            />
          ))}
        </div>
      )}

      <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_minmax(0,360px)]">
        <Card>
          <CardHeader className="flex-row items-start justify-between gap-3 space-y-0">
            <div className="min-w-0">
              <CardTitle>选中账号</CardTitle>
              <CardDescription className="mt-1">
                {selectedAccount
                  ? `端点 ${selectedAccount.endpoint}`
                  : '尚未选择账号；上方卡片可切换当前账号。'}
              </CardDescription>
            </div>
            {selectedAccount ? (
              <Badge variant={statusOf(selectedAccount).variant}>
                {statusOf(selectedAccount).label}
              </Badge>
            ) : null}
          </CardHeader>
          <CardContent className="pt-2">
            {selectedAccount ? (
              <>
                <InfoRow
                  label="QQ 账号"
                  value={
                    selectedAccount.botId === undefined
                      ? '尚未识别（等待 get_login_info）'
                      : String(selectedAccount.botId)
                  }
                  mono={selectedAccount.botId !== undefined}
                />
                <InfoRow
                  label="预期号码"
                  value={selectedAccount.expectedQq === undefined ? '未设置' : String(selectedAccount.expectedQq)}
                  mono
                />
                <InfoRow label="连接状态" value={selectedAccount.connected ? '已连接' : '未连接'} />
                <InfoRow
                  label="最近心跳"
                  value={
                    selectedAccount.lastHeartbeat
                      ? formatRelativeTime(selectedAccount.lastHeartbeat)
                      : '未收到心跳'
                  }
                />
                <InfoRow
                  label="目录同步"
                  value={selectedAccount.syncedAt ? formatRelativeTime(selectedAccount.syncedAt) : '尚未同步'}
                />
                {selectedAccount.rejected ? (
                  <p className="mt-3 rounded-lg border border-destructive/25 bg-destructive/10 px-3 py-2 text-meta leading-relaxed text-destructive">
                    {selectedAccount.rejected}。请检查该端点在 SnowLuma 中登录的 QQ 号，或调整 expect-qq。
                  </p>
                ) : null}
                <div className="mt-4 flex flex-wrap gap-2">
                  <Button asChild variant="outline" size="sm">
                    <Link to="/directory" search={{ tab: 'friends', search: '', page: 1, limit: 20 }}>
                      <Users aria-hidden />
                      查看联系人
                    </Link>
                  </Button>
                  <Button asChild variant="ghost" size="sm">
                    <Link to="/directory" search={{ tab: 'groups', search: '', page: 1, limit: 20 }}>
                      <ShieldCheck aria-hidden />
                      配置群聊策略
                    </Link>
                  </Button>
                </div>
              </>
            ) : (
              <FeedbackState
                variant="empty"
                title="尚未识别账号身份"
                description="端点已配置，但还没有收到登录信息。确认 SnowLuma 已登录后刷新。"
                onRetry={refetch}
              />
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>运行时</CardTitle>
            <CardDescription className="mt-1">来自 /api/v1/status 的进程快照</CardDescription>
          </CardHeader>
          <CardContent className="pt-2">
            {status.isLoading ? (
              <div className="space-y-2.5">
                {Array.from({ length: 5 }).map((_, index) => (
                  <Skeleton key={index} className="h-4 w-full" />
                ))}
              </div>
            ) : status.isError ? (
              <FeedbackState
                variant="error"
                error={status.error}
                onRetry={() => void status.refetch()}
                compact
              />
            ) : (
              <>
                <div className="mb-3">
                  <StatusIndicator
                    tone={connected ? 'ok' : 'warn'}
                    label={connected ? '至少一个账号在线' : '所有账号均离线'}
                    description={
                      connected
                        ? '正向 WebSocket 已建立，可接收事件'
                        : '请确认 SnowLuma 已登录并启用 WS 服务端'
                    }
                    live
                  />
                </div>
                <InfoRow label="服务版本" value={status.data?.version ?? UNAVAILABLE_TEXT} mono />
                <InfoRow label="运行时长" value={formatUptime(status.data?.uptimeSeconds)} />
                <InfoRow label="数据库" value={dbConfigured ? '已配置' : '未配置'} />
                <InfoRow label="已处理事件" value={formatCount(status.data?.handledEvents)} mono />
                {selectedAccount?.lastHeartbeat ? (
                  <p className="pt-2 text-caption text-muted-foreground">
                    心跳时间：{formatDateTime(selectedAccount.lastHeartbeat)}
                  </p>
                ) : null}
              </>
            )}
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader className="flex-row items-start justify-between gap-3 space-y-0">
          <div className="min-w-0">
            <CardTitle>已注册处理器</CardTitle>
            <CardDescription className="mt-1">
              每条连接都会装载同一套处理器，账号身份由分发器注入
            </CardDescription>
          </div>
          <span className="shrink-0 font-mono text-meta text-muted-foreground tabular-nums">
            {status.data ? `${status.data.handlers.length} 个` : '—'}
          </span>
        </CardHeader>
        <CardContent className="pt-2">
          {status.data && status.data.handlers.length > 0 ? (
            <div className="flex flex-wrap gap-2">
              {status.data.handlers.map((handler) => (
                <Badge key={handler} variant="secondary" className="font-mono text-meta">
                  {handler}
                </Badge>
              ))}
            </div>
          ) : (
            <p className="text-meta leading-relaxed text-muted-foreground">
              当前没有注册处理器。若 AI 与 echo 均已关闭，机器人只会保持连接而不会响应消息。
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
