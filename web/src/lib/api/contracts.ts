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
