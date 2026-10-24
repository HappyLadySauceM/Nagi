import { ApiError } from './api/client'

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

/**
 * Maps any thrown value to actionable copy. A failed request is never
 * presented as empty data, and cancellation is not presented as a timeout.
 * 把任意抛出值映射为可执行文案。请求失败绝不表现为空数据，
 * 取消也不会被描述成超时。
 */
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
      case 'cancelled':
        return {
          variant: 'error',
          title: '请求已取消',
          description: '请求在完成前被取消，未影响服务端数据。',
          canRetry: false,
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

/**
 * One-line cause used by compact blocks that repeat under a detailed card.
 * 供详细卡片下方的紧凑区块使用的一行原因说明。
 */
export function shortErrorReason(error: unknown): string {
  if (error instanceof ApiError) {
    switch (error.kind) {
      case 'network':
        return '无法连接后端'
      case 'timeout':
        return '请求超时'
      case 'cancelled':
        return '请求已取消'
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
