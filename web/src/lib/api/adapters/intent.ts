import type {
  DecisionDetailEntry,
  DecisionEntry,
  DecisionStepEntry,
  IntentDecisionKind,
  InboundMediaEvidence,
  ModelMediaEvidence,
  PictureEvidence,
} from '../contracts'
import { asArray, asRecord, contractError, readNumber, readOptionalString, readOptionalTime, readString } from './guard'
import { toIntentRun } from './flow'

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
  const ingress = record.Ingress == null ? undefined : asRecord(record.Ingress, DECISIONS_ENDPOINT, 'Ingress')
  return {
    ...toDecisionEntry(record),
    ingress: ingress && {
      messageId: readNumber(ingress, 'MessageID', DECISIONS_ENDPOINT),
      platformMessageId: readNumber(ingress, 'PlatformMessageID', DECISIONS_ENDPOINT),
      senderId: readNumber(ingress, 'SenderID', DECISIONS_ENDPOINT),
      text: readString(ingress, 'Text', DECISIONS_ENDPOINT, ''),
      segmentTypes: asArray(ingress.SegmentTypes ?? [], DECISIONS_ENDPOINT, 'SegmentTypes').filter((value): value is string => typeof value === 'string'),
      receivedAt: readOptionalTime(ingress, 'ReceivedAt', DECISIONS_ENDPOINT),
      traceId: readString(ingress, 'TraceID', DECISIONS_ENDPOINT, ''),
      jobStatus: readString(ingress, 'JobStatus', DECISIONS_ENDPOINT, ''),
      media: toInboundMedia(ingress.Media),
    },
    scores: toScores(record.Scores),
    steps: asArray(record.Steps ?? [], DECISIONS_ENDPOINT, 'Steps').map(toStep),
    run: record.Run == null ? undefined : toIntentRun(record.Run),
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

export const INBOUND_ENDPOINT = '/api/v1/intent/inbound'

export function toInboundMessage(raw: unknown) {
  const record = asRecord(raw, INBOUND_ENDPOINT, 'message')
  const decision = readString(record, 'Decision', INBOUND_ENDPOINT, '')
  if (decision && !DECISION_KINDS.includes(decision as IntentDecisionKind)) {
    throw contractError(INBOUND_ENDPOINT, `未知决策类型 ${decision}`)
  }
  return {
    messageId: readNumber(record, 'MessageID', INBOUND_ENDPOINT),
    jobId: readNumber(record, 'JobID', INBOUND_ENDPOINT),
    decisionId: readNumber(record, 'DecisionID', INBOUND_ENDPOINT),
    decisionIds: asArray(record.DecisionIDs ?? [], INBOUND_ENDPOINT, 'DecisionIDs')
      .filter((value): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value > 0),
    conversationId: readString(record, 'ConversationID', INBOUND_ENDPOINT),
    senderId: readNumber(record, 'SenderID', INBOUND_ENDPOINT),
    platformMessageId: readNumber(record, 'PlatformMessageID', INBOUND_ENDPOINT),
    jobStatus: readString(record, 'JobStatus', INBOUND_ENDPOINT, ''),
    jobError: readString(record, 'JobError', INBOUND_ENDPOINT, ''),
    decision: decision as IntentDecisionKind | '',
    reasonCode: readString(record, 'ReasonCode', INBOUND_ENDPOINT, ''),
    model: readString(record, 'Model', INBOUND_ENDPOINT, ''),
    text: readString(record, 'Text', INBOUND_ENDPOINT, ''),
    traceId: readString(record, 'TraceID', INBOUND_ENDPOINT, ''),
    segmentTypes: asArray(record.SegmentTypes ?? [], INBOUND_ENDPOINT, 'SegmentTypes')
      .filter((value): value is string => typeof value === 'string' && value !== 'text'),
    media: toInboundMedia(record.Media),
    attachments: asArray(record.Attachments ?? [], INBOUND_ENDPOINT, 'Attachments').map(value => {
      const attachment = asRecord(value, INBOUND_ENDPOINT, 'attachment')
      return {
        id: readString(attachment, 'ID', INBOUND_ENDPOINT),
        kind: readString(attachment, 'Kind', INBOUND_ENDPOINT),
        status: readString(attachment, 'Status', INBOUND_ENDPOINT),
        filename: readString(attachment, 'Filename', INBOUND_ENDPOINT, ''),
        failureCode: readString(attachment, 'FailureCode', INBOUND_ENDPOINT, ''),
        size: readNumber(attachment, 'SizeBytes', INBOUND_ENDPOINT, 0),
      }
    }),
    receivedAt: readOptionalTime(record, 'ReceivedAt', INBOUND_ENDPOINT),
  }
}

function toPictures(raw: unknown): PictureEvidence {
  const record = asRecord(raw, INBOUND_ENDPOINT, 'pictures')
  return {
    imageCount: readNumber(record, 'ImageCount', INBOUND_ENDPOINT),
    images: asArray(record.Images ?? [], INBOUND_ENDPOINT, 'Images').map(value => {
      const image = asRecord(value, INBOUND_ENDPOINT, 'picture')
      return {
        segmentIndex: readNumber(image, 'SegmentIndex', INBOUND_ENDPOINT),
        status: readString(image, 'Status', INBOUND_ENDPOINT),
      }
    }),
  }
}

function toModelMedia(raw: unknown): ModelMediaEvidence {
  const record = asRecord(raw, INBOUND_ENDPOINT, 'model media')
  const indexes = (raw: unknown) => asArray(raw ?? [], INBOUND_ENDPOINT, 'segment indexes')
    .filter((value): value is number => typeof value === 'number' && Number.isInteger(value) && value >= 0)
  return {
    state: readString(record, 'State', INBOUND_ENDPOINT),
    ...(record.StartFailed === true ? { startFailed: true } : {}),
    calls: asArray(record.Calls ?? [], INBOUND_ENDPOINT, 'Calls').map(value => {
      const call = asRecord(value, INBOUND_ENDPOINT, 'call')
      return {
        outcome: readString(call, 'Outcome', INBOUND_ENDPOINT),
        reason: readOptionalString(call, 'Reason', INBOUND_ENDPOINT),
        attemptCountUnknown: call.AttemptCountUnknown === true,
        httpStatus: call.HTTPStatus == null ? undefined : readNumber(call, 'HTTPStatus', INBOUND_ENDPOINT),
        responseBody: readOptionalString(call, 'ResponseBody', INBOUND_ENDPOINT),
        attempts: asArray(call.Attempts ?? [], INBOUND_ENDPOINT, 'Attempts').map(value => {
          const attempt = asRecord(value, INBOUND_ENDPOINT, 'attempt')
          return {
            shape: readString(attempt, 'Shape', INBOUND_ENDPOINT),
            state: readString(attempt, 'State', INBOUND_ENDPOINT),
            images: indexes(attempt.Images),
            hints: indexes(attempt.Hints),
            httpStatus: attempt.HTTPStatus == null ? undefined : readNumber(attempt, 'HTTPStatus', INBOUND_ENDPOINT),
            responseBody: readOptionalString(attempt, 'ResponseBody', INBOUND_ENDPOINT),
          }
        }),
      }
    }),
  }
}

function toInboundMedia(raw: unknown): InboundMediaEvidence | undefined {
  if (raw == null) return undefined
  const record = asRecord(raw, INBOUND_ENDPOINT, 'Media')
  return {
    atIngress: record.AtIngress == null ? undefined : toPictures(record.AtIngress),
    current: toPictures(record.Current),
    decisions: asArray(record.Decisions ?? [], INBOUND_ENDPOINT, 'Decisions').map(value => {
      const decision = asRecord(value, INBOUND_ENDPOINT, 'decision media')
      return {
        decisionId: readNumber(decision, 'DecisionID', INBOUND_ENDPOINT),
        atDecision: decision.AtDecision == null ? undefined : toPictures(decision.AtDecision),
        intent: toModelMedia(decision.Intent),
        generation: toModelMedia(decision.Generation),
      }
    }),
  }
}

export function toInboundList(raw: unknown) {
  const record = asRecord(raw, INBOUND_ENDPOINT, 'list')
  return {
    items: asArray(record.Items, INBOUND_ENDPOINT, 'Items').map(toInboundMessage),
    blocked: readNumber(record, 'BlockedSinceStart', INBOUND_ENDPOINT, 0),
  }
}
