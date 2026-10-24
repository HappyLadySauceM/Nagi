import { ApiError } from '../client'

/**
 * Shared wire guards. Every adapter validates the payload shape and raises a
 * `contract` error instead of silently substituting defaults, so a backend
 * change surfaces as an explicit problem rather than as fake data.
 * 共用的线上数据守卫：适配器校验结构，不一致时抛出 contract 错误，
 * 而不是静默填默认值把问题伪装成正常数据。
 */

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

export function contractError(endpoint: string, detail: string): ApiError {
  return new ApiError({
    kind: 'contract',
    endpoint,
    message: '后端响应结构与前端契约不一致',
    detail,
  })
}

export function asRecord(
  value: unknown,
  endpoint: string,
  label: string,
): Record<string, unknown> {
  if (!isRecord(value)) {
    throw contractError(endpoint, `${label} 不是对象`)
  }
  return value
}

export function asArray(value: unknown, endpoint: string, label: string): unknown[] {
  if (!Array.isArray(value)) {
    throw contractError(endpoint, `${label} 不是数组`)
  }
  return value
}

export function readBoolean(
  record: Record<string, unknown>,
  key: string,
  endpoint: string,
  fallback?: boolean,
): boolean {
  const value = record[key]
  if (typeof value === 'boolean') {
    return value
  }
  if (fallback !== undefined && value === undefined) {
    return fallback
  }
  throw contractError(endpoint, `${key} 不是布尔值`)
}

export function readNumber(
  record: Record<string, unknown>,
  key: string,
  endpoint: string,
  fallback?: number,
): number {
  const value = record[key]
  if (typeof value === 'number' && Number.isFinite(value)) {
    return value
  }
  if (fallback !== undefined && value === undefined) {
    return fallback
  }
  throw contractError(endpoint, `${key} 不是有效数字`)
}

export function readOptionalNumber(
  record: Record<string, unknown>,
  key: string,
  endpoint: string,
): number | undefined {
  const value = record[key]
  if (value === undefined || value === null) {
    return undefined
  }
  if (typeof value === 'number' && Number.isFinite(value)) {
    return value
  }
  throw contractError(endpoint, `${key} 不是有效数字`)
}

export function readString(
  record: Record<string, unknown>,
  key: string,
  endpoint: string,
  fallback?: string,
): string {
  const value = record[key]
  if (typeof value === 'string') {
    return value
  }
  if (fallback !== undefined && value === undefined) {
    return fallback
  }
  throw contractError(endpoint, `${key} 不是字符串`)
}

export function readOptionalString(
  record: Record<string, unknown>,
  key: string,
  endpoint: string,
): string | undefined {
  const value = record[key]
  if (value === undefined || value === null || value === '') {
    return undefined
  }
  if (typeof value === 'string') {
    return value
  }
  throw contractError(endpoint, `${key} 不是字符串`)
}

export function readOptionalBoolean(
  record: Record<string, unknown>,
  key: string,
  endpoint: string,
): boolean | undefined {
  const value = record[key]
  if (value === undefined || value === null) {
    return undefined
  }
  if (typeof value === 'boolean') {
    return value
  }
  throw contractError(endpoint, `${key} 不是布尔值`)
}

/**
 * Go zero-value timestamps mean "never": they serialise either as year 1 or as
 * the Unix epoch, so anything before 2000 is treated as absent.
 * Go 的零值时间表示「从未发生」，序列化可能是公元 1 年或 Unix 纪元；
 * 统一把 2000 年之前的值视为缺失。
 */
export function readOptionalTime(
  record: Record<string, unknown>,
  key: string,
  endpoint: string,
): string | undefined {
  const value = readOptionalString(record, key, endpoint)
  if (value === undefined) {
    return undefined
  }
  const year = Number(value.slice(0, 4))
  if (Number.isNaN(year) || year < 2000) {
    return undefined
  }
  return value
}

/** Normalises a PascalCase page (`Items/Total/Page/Limit`) or lowercase equivalent. */
export function readPageEnvelope(
  raw: unknown,
  endpoint: string,
  itemsKey: 'Items' | 'items',
): { items: unknown[]; total: number; page: number; limit: number } {
  const record = asRecord(raw, endpoint, 'page')
  return {
    items: asArray(record[itemsKey], endpoint, itemsKey),
    total: readNumber(record, 'Total' in record ? 'Total' : 'total', endpoint),
    page: readNumber(record, 'Page' in record ? 'Page' : 'page', endpoint),
    limit: readNumber(record, 'Limit' in record ? 'Limit' : 'limit', endpoint),
  }
}
