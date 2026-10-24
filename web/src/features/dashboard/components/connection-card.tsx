import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '../../../components/ui/card'
import { Badge } from '../../../components/ui/badge'
import { FeedbackState } from '../../../components/feedback-state'
import { ShineBorder } from '../../../components/magicui/shine-border'
import { StatusIndicator, type StatusTone } from '../../../components/status-indicator'
import { Skeleton } from '../../../components/ui/skeleton'
import { formatDateTime, formatRelativeTime, formatUptime, UNAVAILABLE_TEXT } from '../../../lib/format'
import { useAccounts, useReadyz, useStatus } from '../../system/queries'

function Row({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-baseline justify-between gap-3 border-b border-border/60 py-2 last:border-b-0">
      <span className="shrink-0 text-xs text-muted-foreground">{label}</span>
      <span
        className={
          mono
            ? 'truncate font-mono text-xs text-foreground tabular-nums'
            : 'truncate text-xs text-foreground'
        }
        title={value}
      >
        {value}
      </span>
    </div>
  )
}

export function ConnectionCard() {
  const status = useStatus()
  const accounts = useAccounts()
  const readyz = useReadyz()

  const isLoading = status.isLoading || accounts.isLoading

  if (isLoading) {
    return (
      <Card className="h-full">
        <CardHeader>
          <CardTitle>连接与运行</CardTitle>
          <CardDescription>正在读取运行时快照</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2.5">
          {Array.from({ length: 5 }).map((_, index) => (
            <Skeleton key={index} className="h-4 w-full" />
          ))}
        </CardContent>
      </Card>
    )
  }

  if (status.isError) {
    return (
      <Card className="h-full">
        <CardHeader>
          <CardTitle>连接与运行</CardTitle>
          <CardDescription>运行时快照不可获取</CardDescription>
        </CardHeader>
        <CardContent>
          <FeedbackState
            variant="error"
            error={status.error}
            onRetry={() => void status.refetch()}
            compact
          />
        </CardContent>
      </Card>
    )
  }

  const snapshot = status.data
  const account = accounts.data
  const connected = account?.connected ?? snapshot?.onebot.connected ?? false
  const selfId = account?.botId ?? snapshot?.onebot.selfId
  const heartbeat = snapshot?.onebot.lastHeartbeat

  const readyzTone: StatusTone = readyz.isSuccess ? 'ok' : readyz.isLoading ? 'unknown' : 'warn'
  const readyzLabel = readyz.isSuccess
    ? 'OneBot 就绪'
    : readyz.isLoading
      ? '检测就绪状态'
      : 'OneBot 未就绪'

  return (
    <Card className="h-full overflow-hidden">
      {connected ? <ShineBorder /> : null}
      <CardHeader className="flex-row items-start justify-between gap-3 space-y-0">
        <div className="min-w-0">
          <CardTitle>连接与运行</CardTitle>
          <CardDescription className="mt-1">
            数据来自 /api/v1/status、/api/v1/accounts 与 /readyz
          </CardDescription>
        </div>
        <Badge variant={connected ? 'success' : 'warning'} className="shrink-0">
          {connected ? '已连接' : '未连接'}
        </Badge>
      </CardHeader>
      <CardContent className="pt-2">
        <div className="mb-3">
          <StatusIndicator tone={readyzTone} label={readyzLabel} live />
        </div>
        <Row label="版本" value={snapshot?.version ?? UNAVAILABLE_TEXT} mono />
        <Row label="运行时长" value={formatUptime(snapshot?.uptimeSeconds)} />
        <Row
          label="机器人账号"
          value={selfId === undefined ? '尚未识别' : String(selfId)}
          mono
        />
        <Row
          label="最近心跳"
          value={heartbeat ? formatRelativeTime(heartbeat) : '未收到心跳'}
          mono={Boolean(heartbeat)}
        />
        {heartbeat ? (
          <p className="pt-2 text-[11px] text-muted-foreground">
            心跳时间：{formatDateTime(heartbeat)}
          </p>
        ) : null}
        <div className="mt-3 flex flex-wrap items-center gap-2">
          <Badge variant={account?.dbConfigured ? 'accent' : 'warning'}>
            {account?.dbConfigured ? '数据库已配置' : '数据库未配置'}
          </Badge>
          {snapshot?.echoEnabled ? <Badge variant="outline">echo 已开启</Badge> : null}
        </div>
      </CardContent>
    </Card>
  )
}
