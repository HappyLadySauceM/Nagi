import { describe, expect, it } from 'vitest'
import { ApiError } from '../api/client'
import { describeError } from '../errors'

describe('describeError', () => {
  it('explains an unregistered route without blaming the database alone', () => {
    // 未注册路由可能是旧二进制，不能武断地说成数据库未配置。
    const described = describeError(
      new ApiError({
        kind: 'not-found',
        endpoint: '/api/v1/settings',
        status: 404,
        message: '该接口未注册',
        detail: 'unregistered',
      }),
    )

    expect(described.title).toBe('后端未提供该接口')
    expect(described.description).toContain('旧版本二进制')
    expect(described.description).toContain('database.dsn')
    expect(described.description).not.toContain('只在已配置 database.dsn 时注册')
  })

  it('keeps a 503 focused on the missing dependency', () => {
    // 503 才代表依赖未就绪，文案应点名数据库。
    const described = describeError(
      new ApiError({
        kind: 'unavailable',
        endpoint: '/api/v1/settings',
        status: 503,
        message: 'database not configured',
      }),
    )

    expect(described.description).toContain('数据库')
  })

  it('still separates a missing resource from a missing route', () => {
    // 有信封的 404 是资源不存在，不应复用路由未注册的文案。
    const described = describeError(
      new ApiError({
        kind: 'not-found',
        endpoint: '/api/v1/memories/9',
        status: 404,
        message: '记忆不存在',
        detail: 'missing',
      }),
    )

    expect(described.title).toBe('资源不存在')
    expect(described.description).toBe('记忆不存在')
  })
})
