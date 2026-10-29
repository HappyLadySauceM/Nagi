import type {
  DecisionDetailEntry,
  DecisionEntry,
  DecisionStepEntry,
  IntentDecisionKind,
} from '../contracts'
import { asArray, asRecord, contractError, readNumber, readOptionalString, readOptionalTime, readString } from './guard'

const DECISIONS_ENDPOINT = '/api/v1/intent/decisions'

const DECISION_KINDS: IntentDecisionKind[] = ['reply', 'silent', 'defer']

/**
 * Parses one page of decisions. The list endpoint returns `{Items, Limit}`
 * without a total, so the caller pages by the last id instead of an offset.
 * 解析一页决策。列表接口只返回 `{Items, Limit}`，不含总数，
 * 因此调用方按最后一条 id 向前翻页，而不是用偏移量。
 */
export function toDecisionList(raw: unknown): { items: DecisionEntry[]; limit: number } {
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'list')
  return {
    items: asArray(record.Items, DECISIONS_ENDPOINT, 'Items').map(toDecisionEntry),
    limit: readNumber(record, 'Limit', DECISIONS_ENDPOINT, 20),
  }
}

function toDecisionEntry(raw: unknown): DecisionEntry {
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'decision')
  const decision = readString(record, 'Decision', DECISIONS_ENDPOINT)
  if (!DECISION_KINDS.includes(decision as IntentDecisionKind)) {
    throw contractError(DECISIONS_ENDPOINT, `未知决策类型 ${decision}`)
  }
  return {
    id: readNumber(record, 'ID', DECISIONS_ENDPOINT),
    jobId: readNumber(record, 'JobID', DECISIONS_ENDPOINT),
    botId: readNumber(record, 'BotID', DECISIONS_ENDPOINT),
    conversationId: readString(record, 'ConversationID', DECISIONS_ENDPOINT, ''),
    decision: decision as IntentDecisionKind,
    reasonCode: readString(record, 'ReasonCode', DECISIONS_ENDPOINT, ''),
    provider: readString(record, 'Provider', DECISIONS_ENDPOINT, ''),
    model: readString(record, 'Model', DECISIONS_ENDPOINT, ''),
    rulesetVersion: readString(record, 'RulesetVersion', DECISIONS_ENDPOINT, ''),
    latencyMs: readNumber(record, 'LatencyMS', DECISIONS_ENDPOINT, 0),
    createdAt: readOptionalTime(record, 'CreatedAt', DECISIONS_ENDPOINT),
  }
}

/**
 * Parses one decision with its explainable steps; an empty step list is valid
 * for decisions recorded before the steps table existed.
 * 解析单条决策及其可解释步骤；步骤表上线前的旧决策没有步骤，空列表是合法的。
 */
export function toDecisionDetail(raw: unknown): DecisionDetailEntry {
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'detail')
  return {
    ...toDecisionEntry(record),
    scores: toScores(record.Scores),
    steps: asArray(record.Steps ?? [], DECISIONS_ENDPOINT, 'Steps').map(toStep),
  }
}

function toScores(raw: unknown): Record<string, number> {
  if (raw == null) return {}
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'Scores')
  const out: Record<string, number> = {}
  for (const [key, value] of Object.entries(record)) {
    if (typeof value !== 'number' || !Number.isFinite(value)) {
      throw contractError(DECISIONS_ENDPOINT, `Scores.${key} 不是有限数字`)
    }
    out[key] = value
  }
  return out
}

function toStep(raw: unknown): DecisionStepEntry {
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'step')
  return {
    index: readNumber(record, 'Index', DECISIONS_ENDPOINT),
    phase: readString(record, 'Phase', DECISIONS_ENDPOINT),
    reasonCode: readOptionalString(record, 'ReasonCode', DECISIONS_ENDPOINT) ?? '',
    detail:
      record.Detail == null
        ? {}
        : asRecord(record.Detail, DECISIONS_ENDPOINT, 'Detail'),
    createdAt: readOptionalTime(record, 'CreatedAt', DECISIONS_ENDPOINT),
  }
}
