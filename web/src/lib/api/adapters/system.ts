import { ApiError } from '../client'
import type {
  AccountsSnapshot,
  OnebotStatus,
  PingState,
  StatusSnapshot,
} from '../contracts'

const STATUS_ENDPOINT = '/api/v1/status'
const ACCOUNTS_ENDPOINT = '/api/v1/accounts'

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function contractError(endpoint: string, detail: string): ApiError {
  return new ApiError({
    kind: 'contract',
    endpoint,
    message: '后端响应结构与前端契约不一致',
    detail,
  })
}

function asRecord(value: unknown, endpoint: string, label: string): Record<string, unknown> {
  if (!isRecord(value)) {
    throw contractError(endpoint, `${label} 不是对象`)
  }
  return value
}

function readBoolean(
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

function readNumber(
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

function readOptionalNumber(
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

function readString(
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

function readOptionalString(
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

function readOptionalBoolean(
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

function toOnebotStatus(value: unknown): OnebotStatus {
  const record = asRecord(value, STATUS_ENDPOINT, 'onebot')
  const status: OnebotStatus = {
    connected: readBoolean(record, 'connected', STATUS_ENDPOINT),
  }
  const selfId = readOptionalNumber(record, 'self_id', STATUS_ENDPOINT)
  if (selfId !== undefined) {
    status.selfId = selfId
  }
  const heartbeatSeen = readOptionalBoolean(record, 'heartbeat_seen', STATUS_ENDPOINT)
  if (heartbeatSeen !== undefined) {
    status.heartbeatSeen = heartbeatSeen
  }
  const lastHeartbeat = readOptionalString(record, 'last_heartbeat', STATUS_ENDPOINT)
  if (lastHeartbeat !== undefined) {
    status.lastHeartbeat = lastHeartbeat
  }
  const heartbeatIntervalMs = readOptionalNumber(
    record,
    'heartbeat_interval_ms',
    STATUS_ENDPOINT,
  )
  if (heartbeatIntervalMs !== undefined) {
    status.heartbeatIntervalMs = heartbeatIntervalMs
  }
  return status
}

/** Normalises `GET /api/v1/status`. */
export function toStatusSnapshot(raw: unknown): StatusSnapshot {
  const record = asRecord(raw, STATUS_ENDPOINT, 'status')
  const handlersValue = record.handlers
  if (!Array.isArray(handlersValue)) {
    throw contractError(STATUS_ENDPOINT, 'handlers 不是数组')
  }
  return {
    version: readString(record, 'version', STATUS_ENDPOINT, '未知'),
    uptimeSeconds: readNumber(record, 'uptime_seconds', STATUS_ENDPOINT, 0),
    echoEnabled: readBoolean(record, 'echo_enabled', STATUS_ENDPOINT, false),
    onebot: toOnebotStatus(record.onebot),
    handlers: handlersValue.filter((item): item is string => typeof item === 'string'),
    handledEvents: readNumber(record, 'handled_events', STATUS_ENDPOINT, 0),
  }
}

/** Normalises `GET /api/v1/accounts`, a single-account snapshot. */
export function toAccountsSnapshot(raw: unknown): AccountsSnapshot {
  const record = asRecord(raw, ACCOUNTS_ENDPOINT, 'accounts')
  const snapshot: AccountsSnapshot = {
    connected: readBoolean(record, 'connected', ACCOUNTS_ENDPOINT),
    dbConfigured: readBoolean(record, 'db_configured', ACCOUNTS_ENDPOINT),
  }
  const botId = readOptionalNumber(record, 'bot_id', ACCOUNTS_ENDPOINT)
  if (botId !== undefined) {
    snapshot.botId = botId
  }
  return snapshot
}

/** Normalises the `/livez` and `/readyz` probe payloads. */
export function toPingState(raw: unknown, endpoint: string): PingState {
  const record = asRecord(raw, endpoint, 'probe')
  return { status: readString(record, 'status', endpoint, 'unknown') }
}
