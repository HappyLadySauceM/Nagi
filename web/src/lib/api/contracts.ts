/**
 * Front-end DTOs use camelCase only; the Go management API keeps its own wire
 * shape and every difference is normalised in `lib/api/adapters`.
 *
 * Wire facts verified against the Go handlers (2026-10-24):
 * - Catalogue pages (`/contacts`, `/groups`, `/groups/{id}/members`) serialise
 *   domain structs without JSON tags, so the payload keys are PascalCase:
 *   `Items/Total/Page/Limit`, `UserID/GroupID/Nickname/...`.
 * - The memory listing envelope is the exception: its keys are lowercase
 *   `items/total/page/limit`, while each memory entry is PascalCase.
 * - `/api/v1/metrics` keys are snake_case and hold aggregate counters, not a
 *   time series.
 * - Write endpoints only accept snake_case bodies.
 */

export interface StatusSnapshot {
  version: string
  uptimeSeconds: number
  echoEnabled: boolean
  handlers: string[]
  handledEvents: number
}

/** One configured SnowLuma endpoint and the QQ account discovered on it. */
export interface AccountSummary {
  /** Undefined until `get_login_info` or the first event identifies the account. */
  botId?: number
  nickname: string
  endpoint: string
  expectedQq?: number
  connected: boolean
  selfIdKnown: boolean
  /** Non-empty when the endpoint was refused, e.g. an unexpected QQ number. */
  rejected?: string
  heartbeatSeen: boolean
  lastHeartbeat?: string
  syncedAt?: string
}

export interface AccountsSnapshot {
  connected: boolean
  dbConfigured: boolean
  accounts: AccountSummary[]
}

export interface PingState {
  status: string
}

export interface IntentDecisionCount {
  decision: string
  reasonCode: string
  count: number
}

/** One ordered distribution row; known statuses are zero-filled by the adapter. */
export interface StatusCount {
  key: string
  count: number
}

export interface MetricsSnapshot {
  messages24h: number
  intentDecisions24h: IntentDecisionCount[]
  messageJobs: StatusCount[]
  memories: StatusCount[]
}

/** Ordered counters used by the dashboard distribution charts. */
export const JOB_STATUSES_TRACE = ['pending', 'processing', 'done', 'failed', 'cancelled'] as const

export const JOB_STATUS_LABELS: Record<string, string> = {
  pending: '等待中',
  processing: '处理中',
  done: '已完成',
  failed: '失败',
  cancelled: '已取消',
}

export const MEMORY_STATUSES_TRACE = ['candidate', 'active', 'superseded', 'deleted'] as const

export const MEMORY_STATUS_LABELS: Record<string, string> = {
  candidate: '候选',
  active: '已启用',
  superseded: '已取代',
  deleted: '已删除',
}

export const DECISION_LABELS: Record<string, string> = {
  reply: '回复',
  silent: '不回复',
  defer: '延后判断',
}

/** One server-paged list; `page` and `limit` are the values actually displayed. */
export interface PageResult<T> {
  items: T[]
  total: number
  page: number
  limit: number
}

export interface Contact {
  userId: number
  nickname: string
  remark: string
  lastSeenAt?: string
}

export interface Group {
  groupId: number
  name: string
  memberCount: number
  lastSeenAt?: string
}

export interface GroupMember {
  groupId: number
  userId: number
  nickname: string
  card: string
  role: string
  lastSeenAt?: string
}

export interface DirectorySyncResult {
  botId: number
  contacts: number
  groups: number
  members: number
}

export type ScopeType = 'group' | 'private'

export type ReplyMode = 'mention_only' | 'intent_based' | 'always_reply' | 'observe_only'

/**
 * Explicit per-scope overrides of tool permissions. A missing key means "use the
 * backend default", so the UI never freezes a default into the database.
 * 作用域级的工具权限显式覆盖。缺少键表示「使用后端默认」，
 * 界面因此不会把默认值冻结进数据库。
 */
export type ToolPermissionOverrides = Record<string, boolean>

/** One registered tool exposed to the model. */
export interface ToolCatalogEntry {
  id: string
  provider: string
  description: string
  category: string
  /** Conversation kinds the tool applies to; empty means every kind. */
  scopeKinds: ScopeType[]
  /**
   * Graded impact of the tool; empty when the backend omits it or reports a
   * level this build does not know yet. The field is informational, so an
   * unfamiliar value must not fail the catalogue.
   * 工具的影响级别；后端省略该字段或下发本版本未知的级别时为空串。
   * 该字段仅用于展示，未知取值不应让清单加载失败。
   */
  riskLevel: 'read' | 'write' | 'admin' | ''
  readOnly: boolean
  sideEffect: boolean
  permission: string
  defaultGranted: boolean
  /**
   * False when the catalogue was requested for a scope kind this tool does not
   * apply to. Legacy backends omit the field, which reads as true.
   * 工具不适用于请求的会话类型时为 false；旧后端不返回该字段时按 true 处理。
   */
  applicable: boolean
  /**
   * Stable machine-readable reason when `applicable` is false; empty otherwise.
   * `applicable` 为 false 时的稳定机器可读原因，其余情况为空串。
   */
  unavailableReason: string
}

/** One permission shared by one or more tools. */
export interface ToolPermission {
  id: string
  tools: string[]
  defaultGranted: boolean
}

/** `GET /api/v1/tools`: what is actually registered right now. */
export interface ToolsCatalog {
  providers: string[]
  tools: ToolCatalogEntry[]
  permissions: ToolPermission[]
  allowSideEffects: boolean
}

/** Form-shaped policy; `PUT` always writes every field. */
export interface ScopeForm {
  enabled: boolean
  replyMode: ReplyMode
  storeMessages: boolean
  allowHistoryRead: boolean
  allowMemoryWrite: boolean
  minReplyIntervalMs: number
  maxRepliesPerHour: number
  retentionDays: number
  toolPermissions: ToolPermissionOverrides
}

/** Effective policy of one scope, including the default-deny values. */
export interface ScopePolicy extends ScopeForm {
  scopeType: ScopeType
  scopeId: number
}

export const SCOPE_TYPE_LABELS: Record<ScopeType, string> = {
  group: '群聊',
  private: '私聊',
}

export const REPLY_MODE_LABELS: Record<ReplyMode, string> = {
  mention_only: '仅响应 @ 与引用',
  intent_based: '由意图判定是否插话',
  always_reply: '每条消息都回复',
  observe_only: '只记录不回复',
}

export const REPLY_MODE_HINTS: Record<ReplyMode, string> = {
  mention_only: '最可控：只有被 @ 或引用时才参与对话。',
  intent_based: 'Jev 判断是否值得插话，未命中则不回复。',
  always_reply: '对每条收到的消息都生成回复，注意限频与打扰。',
  observe_only: '只写入消息与记忆，不主动发言。',
}

export type MemoryVisibility = 'bot_shared' | 'group' | 'private_user' | 'conversation'

export type MemoryStatus = 'candidate' | 'active' | 'superseded' | 'deleted'

export interface MemoryEntry {
  id: string
  botId: number
  visibility: MemoryVisibility
  ownerKey: string
  content: string
  sourceMessageId?: number
  source?: string
  confidence: number
  status: MemoryStatus
  approvedBy?: string
  expiresAt?: string
  createdAt?: string
  updatedAt?: string
}

export interface MemoryCreateResult {
  id: string
  duplicate: boolean
}

export const MEMORY_VISIBILITY_LABELS: Record<MemoryVisibility, string> = {
  bot_shared: '全局共享',
  group: '群级',
  private_user: '个人',
  conversation: '会话级',
}

export const MEMORY_VISIBILITY_HINTS: Record<MemoryVisibility, string> = {
  bot_shared: '对该机器人下的所有会话可见，审批前请确认内容适合全局共享。',
  group: '仅同一群内的会话可见，需要归属群号。',
  private_user: '仅该好友的私聊可见，需要归属 QQ 号。',
  conversation: '仅单个会话可见，需要会话归属标识。',
}

export type IntentDecisionKind = 'reply' | 'silent' | 'defer'

export interface DecisionStepEntry {
  index: number
  phase: string
  reasonCode: string
  detail: Record<string, unknown>
  createdAt?: string
}

export interface DecisionEntry {
  id: number
  jobId: number
  botId: number
  conversationId: string
  decision: IntentDecisionKind
  reasonCode: string
  provider: string
  model: string
  rulesetVersion: string
  latencyMs: number
  createdAt?: string
}

export interface DecisionDetailEntry extends DecisionEntry {
  scores: Record<string, number>
  steps: DecisionStepEntry[]
}

export const DECISION_KIND_LABELS: Record<IntentDecisionKind, string> = {
  reply: '回复',
  silent: '沉默',
  defer: '延后',
}

export const DECISION_PHASE_LABELS: Record<string, string> = {
  policy: '策略',
  rules: '规则',
  semantic: '语义',
  final: '结论',
}

export type RuleActionType = 'reply' | 'silent' | 'require_semantic'

export interface RuleMatch {
  replyMode?: string
  kind?: string
  explicit?: boolean
  textContains?: string
  minSecondsSinceReply?: number
}

export interface RuleEntry {
  priority: number
  match: RuleMatch
  action: { type: RuleActionType; reasonCode: string }
}

export interface Ruleset {
  id: number
  botId: number
  scopeType: ScopeType
  scopeId: number
  version: number
  status: 'draft' | 'published' | 'archived'
  note: string
  entries: RuleEntry[]
}

export interface RuleSimulation {
  blocked: boolean
  explicit: boolean
  needSemantic: boolean
  alwaysReply: boolean
  forceReply: boolean
  reasonCode: string
  rulesetVersion: string
}

export const RULE_ACTION_LABELS: Record<RuleActionType, string> = {
  reply: '强制回复',
  silent: '强制沉默',
  require_semantic: '要求语义判定',
}

export const RULESET_STATUS_LABELS: Record<Ruleset['status'], string> = {
  draft: '草稿',
  published: '已发布',
  archived: '已归档',
}
