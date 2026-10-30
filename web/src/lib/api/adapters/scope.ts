import type {
  ReplyMode,
  ScopeForm,
  ScopePolicy,
  ScopeType,
  ToolPermissionOverrides,
} from '../contracts'
import { asArray, asRecord, contractError, readBoolean, readNumber, readString } from './guard'

const SCOPE_ENDPOINT = '/api/v1/scopes'

const REPLY_MODES: readonly ReplyMode[] = [
  'mention_only',
  'intent_based',
  'always_reply',
  'observe_only',
]

const SCOPE_TYPES: readonly ScopeType[] = ['group', 'private']

/**
 * Server-side defaults mirror `scope/domain.Default`: storage and the reply
 * tool are on, per-run and hourly limits are unlimited, and one bubble is
 * capped by the QQ text limit. Zero means unlimited.
 * 服务端默认值对应 `scope/domain.Default`：存储与连续回复默认开启，
 * 每轮与每小时不限，单条取 QQ 文本上限；0 表示不限。
 */
export const DEFAULT_SCOPE_FORM: ScopeForm = {
  enabled: false,
  maxMessagesPerRun: 0, maxCharsPerMessage: 4500, maxTotalCharsPerRun: 0, messageIntervalMs: 700,
  replyMode: 'mention_only',
  storeMessages: true,
  allowHistoryRead: false,
  allowMemoryWrite: false,
  minReplyIntervalMs: 10_000,
  maxRepliesPerHour: 0,
  toolPermissions: {},
}

const DEFAULT_MIN_REPLY_INTERVAL_MS = 10_000
const DEFAULT_MAX_REPLIES_PER_HOUR = 0
const DEFAULT_MAX_MESSAGES_PER_RUN = 0
const DEFAULT_MAX_CHARS_PER_MESSAGE = 4500
const DEFAULT_MAX_TOTAL_CHARS_PER_RUN = 0

export interface ScopeIdentity {
  scopeType: ScopeType
  scopeId: number
}

export function isReplyMode(value: unknown): value is ReplyMode {
  return typeof value === 'string' && REPLY_MODES.includes(value as ReplyMode)
}

export function isScopeType(value: unknown): value is ScopeType {
  return typeof value === 'string' && SCOPE_TYPES.includes(value as ScopeType)
}

function positiveOr(value: number, fallback: number): number {
  return value > 0 ? value : fallback
}

/**
 * Parses the explicit permission overrides. `null` (Go nil map) and a missing
 * key both mean "no override", never "everything revoked".
 * 解析显式权限覆盖。`null`（Go nil map）与缺失键都表示「无覆盖」，
 * 绝不表示「全部撤销」。
 */
function readToolPermissions(
  record: Record<string, unknown>,
  endpoint: string,
): ToolPermissionOverrides {
  const raw = record.ToolPermissions
  if (raw === undefined || raw === null) {
    return {}
  }
  const source = asRecord(raw, endpoint, 'ToolPermissions')
  const overrides: ToolPermissionOverrides = {}
  for (const [permission, granted] of Object.entries(source)) {
    if (typeof granted !== 'boolean') {
      throw contractError(endpoint, `ToolPermissions.${permission} 不是布尔值`)
    }
    overrides[permission] = granted
  }
  return overrides
}

/** Compares override maps by content; key order is irrelevant. */
function isPermissionMapEqual(left: ToolPermissionOverrides, right: ToolPermissionOverrides): boolean {
  const leftKeys = Object.keys(left)
  if (leftKeys.length !== Object.keys(right).length) {
    return false
  }
  return leftKeys.every((key) => right[key] === left[key])
}

/**
 * Stored rows may be partial: older policies can carry an empty reply mode and
 * zero numbers. `scope/application` resolves those through `WithDefaults` when
 * reading the effective policy, so the adapter mirrors that instead of failing
 * the whole list.
 *
 * Identity is part of the contract: the Go repository now fills scope type and
 * id for stored rows, so a missing identity is a contract error rather than
 * something the caller patches up.
 * 历史策略行可能不完整（空 reply_mode、数值 0）；后端读取生效策略时用
 * `WithDefaults` 补齐，适配器保持同样语义。身份属于契约的一部分：
 * Go 仓储已为已存行补齐类型与 ID，缺失即视为契约错误，不再由调用方兜底。
 */
export function toScopePolicy(raw: unknown): ScopePolicy {
  const record = asRecord(raw, SCOPE_ENDPOINT, 'policy')

  const scopeTypeRaw = readString(record, 'ScopeType', SCOPE_ENDPOINT, '')
  if (!isScopeType(scopeTypeRaw)) {
    throw contractError(SCOPE_ENDPOINT, `ScopeType 取值未知：${scopeTypeRaw}`)
  }
  const scopeType = scopeTypeRaw

  const scopeId = readNumber(record, 'ScopeID', SCOPE_ENDPOINT, 0)
  if (!('ScopeID' in record) || scopeId < 0) {
    throw contractError(SCOPE_ENDPOINT, 'ScopeID 缺失')
  }

  const replyModeRaw = readString(record, 'ReplyMode', SCOPE_ENDPOINT, '')
  const replyMode = isReplyMode(replyModeRaw) ? replyModeRaw : DEFAULT_SCOPE_FORM.replyMode
  const policyNumber = (key: string, fallback: number) => {
    const value=readNumber(record,key,SCOPE_ENDPOINT,fallback)
    return isReplyMode(replyModeRaw) ? value : positiveOr(value,fallback)
  }

  return {
    scopeType,
    scopeId,
    maxMessagesPerRun: readNumber(record, 'MaxMessagesPerRun', SCOPE_ENDPOINT, DEFAULT_MAX_MESSAGES_PER_RUN),
    maxCharsPerMessage: readNumber(record, 'MaxCharsPerMessage', SCOPE_ENDPOINT, DEFAULT_MAX_CHARS_PER_MESSAGE),
    maxTotalCharsPerRun: readNumber(record, 'MaxTotalCharsPerRun', SCOPE_ENDPOINT, DEFAULT_MAX_TOTAL_CHARS_PER_RUN),
    messageIntervalMs: readNumber(record, 'MessageIntervalMS', SCOPE_ENDPOINT, 700),
    source: readString(record, 'Source', SCOPE_ENDPOINT, scopeId === 0 ? 'default' : 'independent'),
    version: readNumber(record, 'Version', SCOPE_ENDPOINT, 0),
    templateVersion: readNumber(record, 'TemplateVersion', SCOPE_ENDPOINT, 0),
    enabled: readBoolean(record, 'Enabled', SCOPE_ENDPOINT),
    replyMode,
    // Storage is a product baseline now; a stale false row must not silently
    // drop the conversation's messages.
    // 消息存储已是产品基线；陈旧的 false 行不得让会话消息被静默丢弃。
    storeMessages: true,
    allowMemoryWrite: readBoolean(record, 'AllowMemoryWrite', SCOPE_ENDPOINT),
    allowHistoryRead: readBoolean(record, 'AllowHistoryRead', SCOPE_ENDPOINT),
    minReplyIntervalMs: policyNumber('MinReplyIntervalMS',DEFAULT_MIN_REPLY_INTERVAL_MS),
    maxRepliesPerHour: policyNumber('MaxRepliesPerHour',DEFAULT_MAX_REPLIES_PER_HOUR),
    toolPermissions: readToolPermissions(record, SCOPE_ENDPOINT),
  }
}

/** `GET /api/v1/scopes` returns a bare array of stored policies (with identity). */
export function toScopePolicyList(raw: unknown): ScopePolicy[] {
  return asArray(raw, SCOPE_ENDPOINT, 'policies').map((row) => toScopePolicy(row))
}

export function toScopeForm(policy: ScopePolicy): ScopeForm {
  return {
    maxMessagesPerRun: policy.maxMessagesPerRun ?? DEFAULT_MAX_MESSAGES_PER_RUN, maxCharsPerMessage: policy.maxCharsPerMessage ?? DEFAULT_MAX_CHARS_PER_MESSAGE, maxTotalCharsPerRun: policy.maxTotalCharsPerRun ?? DEFAULT_MAX_TOTAL_CHARS_PER_RUN, messageIntervalMs: policy.messageIntervalMs ?? 700,
    enabled: policy.enabled,
    replyMode: policy.replyMode,
    storeMessages: true,
    allowHistoryRead: policy.allowHistoryRead,
    allowMemoryWrite: policy.allowMemoryWrite,
    minReplyIntervalMs: policy.minReplyIntervalMs,
    maxRepliesPerHour: policy.maxRepliesPerHour,
    toolPermissions: { ...policy.toolPermissions },
  }
}

/**
 * PUT replaces the whole policy, so every snake_case field is always sent.
 * 服务端 PUT 是整份覆盖，必须始终提交全部 snake_case 字段。
 */
export function toScopePutBody(form: ScopeForm): Record<string, unknown> {
  return {
    max_messages_per_run: form.maxMessagesPerRun ?? DEFAULT_MAX_MESSAGES_PER_RUN, max_chars_per_message: form.maxCharsPerMessage ?? DEFAULT_MAX_CHARS_PER_MESSAGE, max_total_chars_per_run: form.maxTotalCharsPerRun ?? DEFAULT_MAX_TOTAL_CHARS_PER_RUN, message_interval_ms: form.messageIntervalMs ?? 700,
    enabled: form.enabled,
    reply_mode: form.replyMode,
    // Storage is always submitted as on; the switch was removed from the UI.
    // 存储恒为开启提交；该开关已从界面移除。
    store_messages: true,
    allow_history_read: form.allowHistoryRead,
    allow_memory_write: form.allowMemoryWrite,
    min_reply_interval_ms: form.minReplyIntervalMs,
    max_replies_per_hour: form.maxRepliesPerHour,
    tool_permissions: form.toolPermissions,
  }
}

/** Index of stored policies keyed by `type:id`, for per-row status badges. */
export function indexScopes(policies: ScopePolicy[] | undefined): Map<string, ScopePolicy> {
  const byScope = new Map<string, ScopePolicy>()
  for (const policy of policies ?? []) {
    byScope.set(`${policy.scopeType}:${policy.scopeId}`, policy)
  }
  return byScope
}

export function isScopeEqual(left: ScopeForm, right: ScopeForm): boolean {
  return (
    left.enabled === right.enabled &&
    left.replyMode === right.replyMode &&
    left.storeMessages === right.storeMessages &&
    left.allowHistoryRead === right.allowHistoryRead &&
    left.allowMemoryWrite === right.allowMemoryWrite &&
    left.minReplyIntervalMs === right.minReplyIntervalMs &&
    left.maxRepliesPerHour === right.maxRepliesPerHour &&
    isPermissionMapEqual(left.toolPermissions, right.toolPermissions)
  )
}

/** Risky capabilities that need an explicit confirmation before saving. */
export function scopeDangerFlags(form: ScopeForm, baseline?: ScopeForm): string[] {
  const flags: string[] = []
  const turnedOn = (key: 'allowHistoryRead' | 'allowMemoryWrite') =>
    form[key] && (!baseline || !baseline[key])

  if (turnedOn('allowHistoryRead')) {
    flags.push('允许机器人读取该会话的聊天历史')
  }
  if (turnedOn('allowMemoryWrite')) {
    flags.push('允许机器人向长期记忆写入该会话的内容')
  }
  // An explicit true overrides a default-denied permission (the UI drops
  // overrides that match the default), so it always widens the tool surface.
  // 显式 true 覆盖的是默认拒绝的权限（与默认一致的覆盖会被界面移除），
  // 因此一定扩大了工具能力面。
  const baselineOverrides = baseline?.toolPermissions ?? {}
  for (const [permission, granted] of Object.entries(form.toolPermissions)) {
    if (granted && baselineOverrides[permission] !== true) {
      flags.push(`授予工具权限 ${permission}（模型可调用对应工具）`)
    }
  }
  return flags
}
