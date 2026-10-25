import type { AccountSummary, AccountsSnapshot, PingState, StatusSnapshot } from '../contracts'
import {
  asRecord,
  contractError,
  readBoolean,
  readNumber,
  readOptionalNumber,
  readOptionalString,
  readString,
} from './guard'

const STATUS_ENDPOINT = '/api/v1/status'
const ACCOUNTS_ENDPOINT = '/api/v1/accounts'

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
    handlers: handlersValue.filter((item): item is string => typeof item === 'string'),
    handledEvents: readNumber(record, 'handled_events', STATUS_ENDPOINT, 0),
  }
}

function toAccountSummary(raw: unknown): AccountSummary {
  const record = asRecord(raw, ACCOUNTS_ENDPOINT, 'account')
  const account: AccountSummary = {
    nickname: readString(record, 'nickname', ACCOUNTS_ENDPOINT, ''),
    endpoint: readString(record, 'endpoint', ACCOUNTS_ENDPOINT, ''),
    connected: readBoolean(record, 'connected', ACCOUNTS_ENDPOINT),
    selfIdKnown: readBoolean(record, 'self_id_known', ACCOUNTS_ENDPOINT, false),
    heartbeatSeen: readBoolean(record, 'heartbeat_seen', ACCOUNTS_ENDPOINT, false),
  }
  const botId = readOptionalNumber(record, 'bot_id', ACCOUNTS_ENDPOINT)
  if (botId !== undefined) {
    account.botId = botId
  }
  const expectedQq = readOptionalNumber(record, 'expected_qq', ACCOUNTS_ENDPOINT)
  if (expectedQq !== undefined) {
    account.expectedQq = expectedQq
  }
  const rejected = readOptionalString(record, 'rejected', ACCOUNTS_ENDPOINT)
  if (rejected !== undefined) {
    account.rejected = rejected
  }
  const lastHeartbeat = readOptionalString(record, 'last_heartbeat', ACCOUNTS_ENDPOINT)
  if (lastHeartbeat !== undefined) {
    account.lastHeartbeat = lastHeartbeat
  }
  const syncedAt = readOptionalString(record, 'synced_at', ACCOUNTS_ENDPOINT)
  if (syncedAt !== undefined) {
    account.syncedAt = syncedAt
  }
  return account
}

/** Normalises `GET /api/v1/accounts`; a missing `accounts` array is a contract error. */
export function toAccountsSnapshot(raw: unknown): AccountsSnapshot {
  const record = asRecord(raw, ACCOUNTS_ENDPOINT, 'accounts')
  if (!Array.isArray(record.accounts)) {
    throw contractError(ACCOUNTS_ENDPOINT, 'accounts 不是数组')
  }
  return {
    connected: readBoolean(record, 'connected', ACCOUNTS_ENDPOINT),
    dbConfigured: readBoolean(record, 'db_configured', ACCOUNTS_ENDPOINT),
    accounts: record.accounts.map(toAccountSummary),
  }
}

/** Normalises the `/livez` and `/readyz` probe payloads. */
export function toPingState(raw: unknown, endpoint: string): PingState {
  const record = asRecord(raw, endpoint, 'probe')
  return { status: readString(record, 'status', endpoint, 'unknown') }
}
