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

export interface OnebotStatus {
  connected: boolean
  selfId?: number
  heartbeatSeen?: boolean
  lastHeartbeat?: string
  heartbeatIntervalMs?: number
}

export interface StatusSnapshot {
  version: string
  uptimeSeconds: number
  echoEnabled: boolean
  onebot: OnebotStatus
  handlers: string[]
  handledEvents: number
}

export interface AccountsSnapshot {
  connected: boolean
  dbConfigured: boolean
  botId?: number
}

/**
 * Connection facts merged from `/api/v1/accounts`, or from `/api/v1/status`
 * when an older backend does not register the accounts route.
 */
export interface AccountSnapshot {
  connected: boolean
  botId?: number
  /** Undefined when the source cannot tell, which is the `/status` fallback. */
  dbConfigured?: boolean
  source: 'accounts' | 'status'
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

/** Form-shaped policy; `PUT` always writes every field. */
export interface ScopeForm {
  enabled: boolean
  replyMode: ReplyMode
  storeMessages: boolean
  allowHistoryRead: boolean
  allowMemoryWrite: boolean
  toolPolicy: string[]
  minReplyIntervalMs: number
  maxRepliesPerHour: number
  retentionDays: number
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
