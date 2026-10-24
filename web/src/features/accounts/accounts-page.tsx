import { Copy, RefreshCw } from 'lucide-react'
import { toast } from 'sonner'
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
import { PageHeader } from '../../components/page-header'
import { Skeleton } from '../../components/ui/skeleton'
import { StatusIndicator } from '../../components/status-indicator'
import {
  formatCount,
  formatDateTime,
  formatRelativeTime,
  formatUptime,
  UNAVAILABLE_TEXT,
} from '../../lib/format'
import { cn } from '../../lib/utils'
import { useAccounts, useStatus } from '../system/queries'

function InfoRow({
  label,
  value,
  mono = false,
}: {
  label: string
  value: string
  mono?: boolean
}) {
  return (
    <div className="flex items-baseline justify-between gap-3 border-b border-border/60 py-2 last:border-b-0">
      <span className="shrink-0 text-xs text-muted-foreground">{label}</span>
      <span
        className={cn(
          'truncate text-xs text-foreground',
          mono && 'font-mono tabular-nums',
        )}
        title={value}
      >
        {value}
      </span>
    </div>
  )
}

export function AccountsPage() {
  const accounts = useAccounts()
  const status = useStatus()

  const botId = accounts.data?.botId ?? status.data?.onebot.selfId
  const isRefreshing = accounts.isFetching || status.isFetching

  const refreshAll = () => {
    void accounts.refetch()
    void status.refetch()
  }

  const copyBotId = () => {
    if (botId === undefined) {
      return
    }
    navigator.clipboard
      .writeText(String(botId))
      .then(() => toast.success('已复制机器人账号', { description: String(botId) }))
      .catch((error: unknown) => {
        console.error(error)
        toast.error('复制失败，请手动选择账号文本')
      })
  }

  return (
    <div className="space-y-4">
      <PageHeader
        title="QQ 账号"
        description="当前接入 SnowLuma 的机器人账号快照。控制台只描述这一个账号，不提供多实例切换。"
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={copyBotId}
              disabled={botId === undefined}
            >
              <Copy aria-hidden />
              复制账号 ID
            </Button>
            <Button variant="outline" size="sm" onClick={refreshAll} disabled={isRefreshing}>
              <RefreshCw className={cn('size-3.5', isRefreshing && 'animate-spin')} aria-hidden />
              刷新
            </Button>
          </div>
        }
      />

      <div className="grid gap-3 lg:grid-cols-2">
        <Card>
          <CardHeader className="flex-row items-start justify-between gap-3 space-y-0">
            <div className="min-w-0">
              <CardTitle>账号与连接</CardTitle>
              <CardDescription className="mt-1">
                来自 /api/v1/accounts 的连接快照
              </CardDescription>
            </div>
            {accounts.data ? (
              <Badge variant={accounts.data.connected ? 'success' : 'warning'}>
                {accounts.data.connected ? 'OneBot 已连接' : 'OneBot 未连接'}
              </Badge>
            ) : null}
          </CardHeader>
          <CardContent className="pt-2">
            {accounts.isLoading ? (
              <div className="space-y-2.5">
                {Array.from({ length: 3 }).map((_, index) => (
                  <Skeleton key={index} className="h-4 w-full" />
                ))}
              </div>
            ) : accounts.isError ? (
              <FeedbackState
                variant="error"
                error={accounts.error}
                onRetry={() => void accounts.refetch()}
                compact
              />
            ) : (
              <>
                <InfoRow
                  label="机器人账号"
                  value={botId === undefined ? '尚未识别（OneBot 未发送登录信息）' : String(botId)}
                  mono={botId !== undefined}
                />
                <InfoRow label="连接状态" value={accounts.data?.connected ? '已连接' : '未连接'} />
                <InfoRow
                  label="数据库"
                  value={accounts.data?.dbConfigured ? '已配置' : '未配置'}
                />
                {!accounts.data?.dbConfigured ? (
                  <p className="mt-3 rounded-lg border border-warning/25 bg-warning/10 px-3 py-2 text-[11px] leading-relaxed text-warning">
                    数据库未配置时，目录、作用域与记忆路由不会注册；控制台只能展示连接状态。
                  </p>
                ) : null}
              </>
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
                    tone={status.data?.onebot.connected ? 'ok' : 'warn'}
                    label={status.data?.onebot.connected ? 'OneBot 连接正常' : 'OneBot 未连接'}
                    description={
                      status.data?.onebot.connected
                        ? '正向 WebSocket 已建立，可接收事件'
                        : '请确认 SnowLuma 已登录并启用 WS 服务端'
                    }
                    live
                  />
                </div>
                <InfoRow label="服务版本" value={status.data?.version ?? UNAVAILABLE_TEXT} mono />
                <InfoRow label="运行时长" value={formatUptime(status.data?.uptimeSeconds)} />
                <InfoRow
                  label="心跳间隔"
                  value={
                    status.data?.onebot.heartbeatIntervalMs === undefined
                      ? '未上报'
                      : `${formatCount(status.data.onebot.heartbeatIntervalMs)} ms`
                  }
                  mono
                />
                <InfoRow
                  label="最近心跳"
                  value={
                    status.data?.onebot.lastHeartbeat
                      ? formatRelativeTime(status.data.onebot.lastHeartbeat)
                      : '未收到心跳'
                  }
                />
                <InfoRow
                  label="已处理事件"
                  value={formatCount(status.data?.handledEvents)}
                  mono
                />
                {status.data?.onebot.lastHeartbeat ? (
                  <p className="pt-2 text-[11px] text-muted-foreground">
                    心跳时间：{formatDateTime(status.data.onebot.lastHeartbeat)}
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
              事件分发器当前装载的处理器名称，决定哪些上下文会响应消息
            </CardDescription>
          </div>
          <span className="shrink-0 font-mono text-[11px] text-muted-foreground tabular-nums">
            {status.data ? `${status.data.handlers.length} 个` : '—'}
          </span>
        </CardHeader>
        <CardContent className="pt-2">
          {status.isLoading ? (
            <div className="flex flex-wrap gap-2">
              {Array.from({ length: 3 }).map((_, index) => (
                <Skeleton key={index} className="h-6 w-20" />
              ))}
            </div>
          ) : status.isError ? (
            <FeedbackState
              variant="error"
              error={status.error}
              onRetry={() => void status.refetch()}
              compact
            />
          ) : status.data && status.data.handlers.length > 0 ? (
            <div className="flex flex-wrap gap-2">
              {status.data.handlers.map((handler) => (
                <Badge key={handler} variant="secondary" className="font-mono text-[11px]">
                  {handler}
                </Badge>
              ))}
            </div>
          ) : (
            <p className="text-xs leading-relaxed text-muted-foreground">
              当前没有注册处理器。若 AI 与 echo 均已关闭，机器人只会保持连接而不会响应消息。
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
