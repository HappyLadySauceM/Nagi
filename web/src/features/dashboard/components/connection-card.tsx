import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '../../../components/ui/card'
import { Badge } from '../../../components/ui/badge'
import { FeedbackState } from '../../../components/feedback-state'
import { InfoRow } from '../../../components/info-row'
import { ShineBorder } from '../../../components/magicui/shine-border'
import { StatusIndicator, type StatusTone } from '../../../components/status-indicator'
import { Skeleton } from '../../../components/ui/skeleton'
import { ApiError } from '../../../lib/api/client'
import { formatDateTime, formatRelativeTime, formatUptime, UNAVAILABLE_TEXT } from '../../../lib/format'
import { useAccountSnapshot, useReadyz, useStatus } from '../../system/queries'

interface ReadinessView {
  tone: StatusTone
  label: string
  description: string
}

/**
 * Maps a failed probe to one concrete cause. The probe answers 503 only when
 * OneBot is down, so any transport failure must not be reported as offline.
 * 把探活失败映射到具体原因。只有 503 才表示 OneBot 未就绪，
 * 传输层失败不能被描述成机器人离线。
 */
function describeReadiness(readyz: {
  isSuccess: boolean
  isLoading: boolean
  error: unknown
}): ReadinessView {
  if (readyz.isSuccess) {
    return {
      tone: 'ok',
      label: 'OneBot 就绪',
      description: '进程在线，OneBot 连接已建立',
    }
  }
  if (readyz.isLoading) {
    return { tone: 'unknown', label: '检测就绪状态', description: '正在请求 /readyz' }
  }
  if (readyz.error instanceof ApiError) {
    switch (readyz.error.kind) {
      case 'unavailable':
        return {
          tone: 'warn',
          label: 'OneBot 未就绪',
          description: '进程仍在线，但 OneBot 连接尚未建立',
        }
      case 'unauthorized':
        return {
          tone: 'error',
          label: '就绪检查未授权',
          description: '请检查开发代理注入的管理员令牌',
        }
      case 'cancelled':
        return { tone: 'unknown', label: '检测就绪状态', description: '上一次就绪检查已取消' }
      case 'network':
      case 'timeout':
        return {
          tone: 'error',
          label: '后端不可达',
          description: '无法访问 /readyz，进程状态未知',
        }
      default:
        return { tone: 'error', label: '就绪检查失败', description: readyz.error.message }
    }
  }
  return { tone: 'error', label: '就绪检查失败', description: '未获得可用的就绪响应' }
}

export function ConnectionCard() {
  const status = useStatus()
  const accountSnapshot = useAccountSnapshot()
  const readyz = useReadyz()

  const isLoading = status.isLoading || accountSnapshot.isLoading

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
  const account = accountSnapshot.data
  const connected = account?.connected ?? snapshot?.onebot.connected ?? false
  const selfId = account?.botId ?? snapshot?.onebot.selfId
  const heartbeat = snapshot?.onebot.lastHeartbeat
  const readiness = describeReadiness(readyz)

  return (
    <Card className="h-full overflow-hidden">
      {connected ? <ShineBorder /> : null}
      <CardHeader className="flex-row items-start justify-between gap-3 space-y-0">
        <div className="min-w-0">
          <CardTitle>连接与运行</CardTitle>
          <CardDescription className="mt-1">
            {accountSnapshot.isFallback
              ? '数据来自 /api/v1/status 与 /readyz（当前后端未注册 /api/v1/accounts）'
              : '数据来自 /api/v1/status、/api/v1/accounts 与 /readyz'}
          </CardDescription>
        </div>
        <Badge variant={connected ? 'success' : 'warning'} className="shrink-0">
          {connected ? '已连接' : '未连接'}
        </Badge>
      </CardHeader>
      <CardContent className="pt-2">
        <div className="mb-3">
          <StatusIndicator
            tone={readiness.tone}
            label={readiness.label}
            description={readiness.description}
            live
          />
        </div>
        <InfoRow label="版本" value={snapshot?.version ?? UNAVAILABLE_TEXT} mono />
        <InfoRow label="运行时长" value={formatUptime(snapshot?.uptimeSeconds)} />
        <InfoRow
          label="机器人账号"
          value={selfId === undefined ? '尚未识别' : String(selfId)}
          mono
        />
        <InfoRow
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
          {account?.dbConfigured === undefined ? (
            <Badge variant="outline">数据库状态未知</Badge>
          ) : account.dbConfigured ? (
            <Badge variant="accent">数据库已配置</Badge>
          ) : (
            <Badge variant="warning">数据库未配置</Badge>
          )}
          {snapshot?.echoEnabled ? <Badge variant="outline">echo 已开启</Badge> : null}
        </div>
      </CardContent>
    </Card>
  )
}
