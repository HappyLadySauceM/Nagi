import { ApiError } from '../client'
import {
  JOB_STATUSES_TRACE,
  MEMORY_STATUSES_TRACE,
  type IntentDecisionCount,
  type MetricsSnapshot,
  type StatusCount,
} from '../contracts'

const METRICS_ENDPOINT = '/api/v1/metrics'

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function contractError(): ApiError {
  return new ApiError({
    kind: 'contract',
    endpoint: METRICS_ENDPOINT,
    message: '指标响应结构与前端契约不一致',
  })
}

function readCounts(value: unknown): Record<string, number> {
  if (!isRecord(value)) {
    throw contractError()
  }
  const counts: Record<string, number> = {}
  for (const [key, raw] of Object.entries(value)) {
    if (typeof raw !== 'number' || !Number.isFinite(raw)) {
      throw contractError()
    }
    counts[key] = raw
  }
  return counts
}

/**
 * The backend groups by existing rows only, so a missing status means a real
 * zero. Known statuses keep a stable order and unknown ones are appended by
 * descending count so new backend states still surface.
 */
function orderedCounts(counts: Record<string, number>, known: readonly string[]): StatusCount[] {
  const rows: StatusCount[] = known.map((key) => ({ key, count: counts[key] ?? 0 }))
  const extras = Object.keys(counts)
    .filter((key) => !known.includes(key))
    .sort((left, right) => (counts[right] ?? 0) - (counts[left] ?? 0))
    .map((key) => ({ key, count: counts[key] ?? 0 }))
  return [...rows, ...extras]
}

function readDecisions(value: unknown): IntentDecisionCount[] {
  if (!Array.isArray(value)) {
    throw contractError()
  }
  return value
    .map((item) => {
      if (!isRecord(item)) {
        throw contractError()
      }
      const { decision, reason_code: reasonCode, count } = item
      if (
        typeof decision !== 'string' ||
        typeof reasonCode !== 'string' ||
        typeof count !== 'number' ||
        !Number.isFinite(count)
      ) {
        throw contractError()
      }
      return { decision, reasonCode, count }
    })
    .sort((left, right) => right.count - left.count)
}

/** Normalises `GET /api/v1/metrics`. */
export function toMetricsSnapshot(raw: unknown): MetricsSnapshot {
  if (!isRecord(raw)) {
    throw contractError()
  }
  const { messages_24h: messages24h, intent_decisions_24h: decisions, message_jobs: jobs, memories } = raw
  if (typeof messages24h !== 'number' || !Number.isFinite(messages24h)) {
    throw contractError()
  }
  return {
    messages24h,
    intentDecisions24h: readDecisions(decisions),
    messageJobs: orderedCounts(readCounts(jobs), JOB_STATUSES_TRACE),
    memories: orderedCounts(readCounts(memories), MEMORY_STATUSES_TRACE),
  }
}

/** Sums an ordered distribution, used by KPI cards. */
export function sumCounts(rows: StatusCount[] | undefined): number | undefined {
  if (!rows) {
    return undefined
  }
  return rows.reduce((total, row) => total + row.count, 0)
}

/**
 * Sums memories while excluding soft-deleted rows, because the card reports
 * what the bot can still recall. `excludedDeleted` lets the copy state the
 * boundary instead of hiding the difference.
 * 汇总记忆时排除已软删除的条目，因为卡片展示的是机器人仍可检索的知识。
 * `excludedDeleted` 让说明文字可以写明统计边界，而不是隐藏差异。
 */
export function summarizeMemories(
  rows: StatusCount[] | undefined,
): { total: number; excludedDeleted: number } | undefined {
  if (!rows) {
    return undefined
  }
  let total = 0
  let excludedDeleted = 0
  for (const row of rows) {
    if (row.key === 'deleted') {
      excludedDeleted += row.count
      continue
    }
    total += row.count
  }
  return { total, excludedDeleted }
}
