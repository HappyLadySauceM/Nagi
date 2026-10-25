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

/** Server-side defaults mirror `scope/domain.Default` for unknown scopes. */
export const DEFAULT_SCOPE_FORM: ScopeForm = {
  enabled: false,
  replyMode: 'mention_only',
  storeMessages: false,
  allowHistoryRead: false,
  allowMemoryWrite: false,
  minReplyIntervalMs: 10_000,
  maxRepliesPerHour: 20,
  retentionDays: 30,
  toolPermissions: {},
}

const DEFAULT_MIN_REPLY_INTERVAL_MS = 10_000
const DEFAULT_MAX_REPLIES_PER_HOUR = 20
const DEFAULT_RETENTION_DAYS = 30

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
  if (scopeId <= 0) {
    throw contractError(SCOPE_ENDPOINT, 'ScopeID 缺失')
  }

  const replyModeRaw = readString(record, 'ReplyMode', SCOPE_ENDPOINT, '')
  const replyMode = isReplyMode(replyModeRaw) ? replyModeRaw : DEFAULT_SCOPE_FORM.replyMode

  return {
    scopeType,
    scopeId,
    enabled: readBoolean(record, 'Enabled', SCOPE_ENDPOINT),
    replyMode,
    storeMessages: readBoolean(record, 'StoreMessages', SCOPE_ENDPOINT),
    allowMemoryWrite: readBoolean(record, 'AllowMemoryWrite', SCOPE_ENDPOINT),
    allowHistoryRead: readBoolean(record, 'AllowHistoryRead', SCOPE_ENDPOINT),
    minReplyIntervalMs: positiveOr(
      readNumber(record, 'MinReplyIntervalMS', SCOPE_ENDPOINT, 0),
      DEFAULT_MIN_REPLY_INTERVAL_MS,
    ),
    maxRepliesPerHour: positiveOr(
      readNumber(record, 'MaxRepliesPerHour', SCOPE_ENDPOINT, 0),
      DEFAULT_MAX_REPLIES_PER_HOUR,
    ),
    retentionDays: positiveOr(
      readNumber(record, 'RetentionDays', SCOPE_ENDPOINT, 0),
      DEFAULT_RETENTION_DAYS,
    ),
    toolPermissions: readToolPermissions(record, SCOPE_ENDPOINT),
  }
}

/** `GET /api/v1/scopes` returns a bare array of stored policies (with identity). */
export function toScopePolicyList(raw: unknown): ScopePolicy[] {
  return asArray(raw, SCOPE_ENDPOINT, 'policies').map((row) => toScopePolicy(row))
}

export function toScopeForm(policy: ScopePolicy): ScopeForm {
  return {
    enabled: policy.enabled,
    replyMode: policy.replyMode,
    storeMessages: policy.storeMessages,
    allowHistoryRead: policy.allowHistoryRead,
    allowMemoryWrite: policy.allowMemoryWrite,
    minReplyIntervalMs: policy.minReplyIntervalMs,
    maxRepliesPerHour: policy.maxRepliesPerHour,
    retentionDays: policy.retentionDays,
    toolPermissions: { ...policy.toolPermissions },
  }
}

/**
 * PUT replaces the whole policy, so every snake_case field is always sent.
 * 服务端 PUT 是整份覆盖，必须始终提交全部 snake_case 字段。
 */
export function toScopePutBody(form: ScopeForm): Record<string, unknown> {
  return {
    enabled: form.enabled,
    reply_mode: form.replyMode,
    store_messages: form.storeMessages,
    allow_history_read: form.allowHistoryRead,
    allow_memory_write: form.allowMemoryWrite,
    min_reply_interval_ms: form.minReplyIntervalMs,
    max_replies_per_hour: form.maxRepliesPerHour,
    retention_days: form.retentionDays,
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
    left.retentionDays === right.retentionDays &&
    isPermissionMapEqual(left.toolPermissions, right.toolPermissions)
  )
}

/** Risky capabilities that need an explicit confirmation before saving. */
export function scopeDangerFlags(form: ScopeForm, baseline?: ScopeForm): string[] {
  const flags: string[] = []
  const turnedOn = (key: 'storeMessages' | 'allowHistoryRead' | 'allowMemoryWrite') =>
    form[key] && (!baseline || !baseline[key])

  if (turnedOn('storeMessages')) {
    flags.push('保存群聊/私聊消息内容（按留存天数清理）')
  }
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
