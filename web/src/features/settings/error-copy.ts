import { ApiError } from '../../lib/api/client'
import { describeError } from '../../lib/errors'

export interface SettingsErrorCopy {
  title: string
  description: string
}

/**
 * Settings-specific failure copy. A 404 means the route is absent (usually an
 * old binary), while a 503 means the route exists but needs a database; the
 * generic mapping cannot tell them apart on its own.
 * 配置页专属的失败文案。404 表示路由不存在（通常是旧二进制），
 * 503 表示路由存在但需要数据库；通用映射无法单独区分两者。
 */
export function settingsErrorCopy(error: unknown): SettingsErrorCopy {
  if (error instanceof ApiError) {
    if (error.kind === 'unavailable') {
      return {
        title: '系统配置需要数据库',
        description:
          '配置保存在数据库中。请在 configs/config.yaml 填写 database.dsn 后重启后端，再回到本页。',
      }
    }
    if (error.kind === 'not-found' && error.detail === 'unregistered') {
      return {
        title: '后端没有 /api/v1/settings 接口',
        description:
          '进程很可能仍在运行旧版本二进制。请重新构建并重启后端（just build 或 just run），刷新后重试。',
      }
    }
  }
  const described = describeError(error)
  return { title: described.title, description: described.description }
}
