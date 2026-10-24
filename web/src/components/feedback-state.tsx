import {
  AlertTriangle,
  Inbox,
  Loader2,
  Lock,
  PlugZap,
  RefreshCw,
  ServerCrash,
  ShieldAlert,
} from 'lucide-react'
import { ApiError } from '../lib/api/client'
import { Button } from './ui/button'
import { Skeleton } from './ui/skeleton'
import { cn } from '../lib/utils'

export type FeedbackVariant =
  | 'loading'
  | 'empty'
  | 'error'
  | 'unauthorized'
  | 'unconfigured'
  | 'offline'

export interface ErrorPresentation {
  variant: Extract<FeedbackVariant, 'error' | 'unauthorized' | 'unconfigured' | 'offline'>
  title: string
  description: string
  canRetry: boolean
}

/** Maps any thrown value to actionable copy; failures are never shown as empty data. */
export function describeError(error: unknown): ErrorPresentation {
  if (error instanceof ApiError) {
    switch (error.kind) {
      case 'network':
        return {
          variant: 'offline',
          title: '无法连接后端服务',
          description:
            '请确认 Go 服务已在 127.0.0.1:5080 运行，并检查开发代理配置（QQBOT_API_TARGET）。',
          canRetry: true,
        }
      case 'timeout':
        return {
          variant: 'offline',
          title: '请求超时',
          description: '后端未在预期时间内响应，可稍后重试；若目录同步耗时较长属正常现象。',
          canRetry: true,
        }
      case 'unauthorized':
        return {
          variant: 'unauthorized',
          title: '未通过管理端鉴权',
          description:
            'Go 服务启用了 server.admin-token。请在 web/.env.local 设置 QQBOT_ADMIN_TOKEN 后重启开发服务器；令牌只由代理注入，不会进入浏览器。',
          canRetry: true,
        }
      case 'not-found':
        if (error.detail === 'unregistered') {
          return {
            variant: 'unconfigured',
            title: '该接口未注册',
            description:
              '依赖数据库的管理路由只在已配置 database.dsn 时注册。请先在 configs/config.yaml 配置数据库并重启后端。',
            canRetry: true,
          }
        }
        return {
          variant: 'error',
          title: '资源不存在',
          description: error.message,
          canRetry: false,
        }
      case 'unavailable':
        return {
          variant: 'error',
          title: '依赖服务未就绪',
          description:
            error.endpoint === '/readyz'
              ? 'OneBot 连接尚未建立；进程仍在线，请确认 SnowLuma 已登录并启用 WS 服务端。'
              : `${error.message}；通常表示数据库未配置或暂不可用。`,
          canRetry: true,
        }
      case 'contract':
        return {
          variant: 'error',
          title: '响应格式与前端契约不一致',
          description: `前后端版本可能不一致（${error.detail ?? error.endpoint}），请更新前端或核对接口变更。`,
          canRetry: true,
        }
      case 'server':
        return {
          variant: 'error',
          title: '服务端处理失败',
          description: `${error.message}（${error.endpoint}）`,
          canRetry: true,
        }
    }
  }

  return {
    variant: 'error',
    title: '请求失败',
    description: error instanceof Error ? error.message : '未知错误，请查看运行日志。',
    canRetry: true,
  }
}

/** One-line cause used by compact blocks that repeat under a detailed card. */
export function shortErrorReason(error: unknown): string {
  if (error instanceof ApiError) {
    switch (error.kind) {
      case 'network':
        return '无法连接后端'
      case 'timeout':
        return '请求超时'
      case 'unauthorized':
        return '需要配置 QQBOT_ADMIN_TOKEN'
      case 'not-found':
        return error.detail === 'unregistered' ? '数据库未配置' : '资源不存在'
      case 'unavailable':
        return '依赖服务未就绪'
      case 'contract':
        return '响应格式不符合契约'
      case 'server':
        return '服务端处理失败'
    }
  }
  return '未知错误'
}

const VARIANT_ICONS = {
  error: AlertTriangle,
  unauthorized: Lock,
  unconfigured: ShieldAlert,
  offline: PlugZap,
  empty: Inbox,
  loading: Loader2,
} as const

interface FeedbackStateProps {
  variant: FeedbackVariant
  title?: string
  description?: string
  error?: unknown
  onRetry?: () => void
  compact?: boolean
  className?: string
}

export function FeedbackState({
  variant,
  title,
  description,
  error,
  onRetry,
  compact = false,
  className,
}: FeedbackStateProps) {
  if (variant === 'loading') {
    return (
      <div className={cn('space-y-2', compact ? 'py-2' : 'py-6', className)} aria-busy="true">
        <Skeleton className="h-4 w-1/3" />
        <Skeleton className="h-4 w-2/3" />
        <Skeleton className="h-4 w-1/2" />
      </div>
    )
  }

  const presentation = error !== undefined ? describeError(error) : undefined
  const Icon = VARIANT_ICONS[variant]
  const resolvedTitle =
    title ??
    presentation?.title ??
    (variant === 'empty' ? '暂无数据' : '加载失败')
  const resolvedDescription = description ?? presentation?.description
  const showRetry = onRetry !== undefined && variant !== 'empty'

  return (
    <div
      className={cn(
        'flex flex-col items-center justify-center gap-2 text-center',
        compact ? 'py-6' : 'py-12',
        className,
      )}
      role={variant === 'empty' ? 'status' : 'alert'}
    >
      <span
        className={cn(
          'flex size-9 items-center justify-center rounded-full border',
          variant === 'unauthorized' || variant === 'unconfigured'
            ? 'border-warning/30 bg-warning/10 text-warning'
            : variant === 'empty'
              ? 'border-border bg-secondary/60 text-muted-foreground'
              : 'border-destructive/30 bg-destructive/10 text-destructive',
        )}
      >
        {variant === 'empty' ? (
          <Icon className="size-4" aria-hidden />
        ) : variant === 'offline' ? (
          <ServerCrash className="size-4" aria-hidden />
        ) : (
          <Icon className="size-4" aria-hidden />
        )}
      </span>
      <p className="text-sm font-medium text-foreground">{resolvedTitle}</p>
      {resolvedDescription ? (
        <p className="max-w-md text-xs leading-relaxed text-muted-foreground">
          {resolvedDescription}
        </p>
      ) : null}
      {showRetry ? (
        <Button variant="outline" size="sm" onClick={onRetry} className="mt-1">
          <RefreshCw aria-hidden />
          重试
        </Button>
      ) : null}
    </div>
  )
}
