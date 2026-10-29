import type { ScopeType, SendingPolicy } from '../contracts'
import { asRecord, readBoolean, readNumber, readString } from './guard'

const ENDPOINT = '/api/v1/agent/sending-policy'

/**
 * Parses the effective sending policy. A missing row is reported as
 * `exists: false` with the process defaults filled in by the server.
 * 解析生效中的出站策略。没有记录时 `exists: false`，服务端已填入进程默认值。
 */
export function toSendingPolicy(raw: unknown): SendingPolicy {
  const record = asRecord(raw, ENDPOINT, 'policy')
  return {
    botId: readNumber(record, 'BotID', ENDPOINT),
    scopeType: readString(record, 'ScopeType', ENDPOINT) as ScopeType,
    scopeId: readNumber(record, 'ScopeID', ENDPOINT),
    exists: readBoolean(record, 'Exists', ENDPOINT),
    allowSendCurrent: readBoolean(record, 'AllowSendCurrent', ENDPOINT),
    maxMessagesPerRun: readNumber(record, 'MaxMessagesPerRun', ENDPOINT),
    maxCharsPerMessage: readNumber(record, 'MaxCharsPerMessage', ENDPOINT),
    maxTotalCharsPerRun: readNumber(record, 'MaxTotalCharsPerRun', ENDPOINT),
  }
}
