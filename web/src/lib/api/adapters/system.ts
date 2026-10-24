import type {
  AccountsSnapshot,
  OnebotStatus,
  PingState,
  StatusSnapshot,
} from '../contracts'
import {
  asRecord,
  contractError,
  readBoolean,
  readNumber,
  readOptionalBoolean,
  readOptionalNumber,
  readOptionalString,
  readString,
} from './guard'

const STATUS_ENDPOINT = '/api/v1/status'
const ACCOUNTS_ENDPOINT = '/api/v1/accounts'

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
