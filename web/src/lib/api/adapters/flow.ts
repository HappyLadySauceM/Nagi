import type {
  IntentCost,
  IntentDelivery,
  IntentDeliveryItem,
  IntentEdgeStatus,
  IntentEvidence,
  IntentFlow,
  IntentFlowEdge,
  IntentFlowNode,
  IntentNodeStatus,
  IntentRun,
  IntentRunEdge,
  IntentRunNode,
  IntentRunSummary,
  IntentSnapshotStatus,
  PromptDetail,
  PromptStatus,
  PromptSummary,
  PromptVersion,
} from '../contracts'
import {
  asArray,
  asRecord,
  contractError,
  readNumber,
  readOptionalString,
  readOptionalTime,
  readString,
} from './guard'

const FLOW_ENDPOINT = '/api/v1/intent/flow'
const DECISIONS_ENDPOINT = '/api/v1/intent/decisions'
const PROMPTS_ENDPOINT = '/api/v1/intent/prompts'

const NODE_STATUSES: IntentNodeStatus[] = [
  'executed',
  'skipped',
  'failed',
  'degraded',
  'unknown',
]
const EDGE_STATUSES: IntentEdgeStatus[] = ['taken', 'not_taken', 'unknown']
const SNAPSHOT_STATUSES: IntentSnapshotStatus[] = [
  'complete',
  'partial',
  'missing',
  'legacy',
]
const PROMPT_STATUSES: PromptStatus[] = ['draft', 'published', 'archived']

function readEnum<T extends string>(
  record: Record<string, unknown>,
  key: string,
  endpoint: string,
  values: readonly T[],
): T {
  const value = readString(record, key, endpoint)
  if (!values.includes(value as T)) {
    throw contractError(endpoint, `${key} 取值未知：${value}`)
  }
  return value as T
}

function readRecordMap(value: unknown, endpoint: string, label: string): Record<string, unknown> {
  if (value == null) {
    return {}
  }
  return asRecord(value, endpoint, label)
}

function readStringArray(value: unknown, endpoint: string, label: string): string[] {
  if (value == null) {
    return []
  }
  return asArray(value, endpoint, label).map((item, index) => {
    if (typeof item !== 'string') {
      throw contractError(endpoint, `${label}[${index}] 不是字符串`)
    }
    return item
  })
}

function readNumberMap(value: unknown, endpoint: string, label: string): Record<string, number> {
  if (value == null) {
    return {}
  }
  const record = asRecord(value, endpoint, label)
  const out: Record<string, number> = {}
  for (const [key, item] of Object.entries(record)) {
    if (typeof item !== 'number' || !Number.isFinite(item)) {
      throw contractError(endpoint, `${label}.${key} 不是有限数字`)
    }
    out[key] = item
  }
  return out
}

function toFlowNode(raw: unknown): IntentFlowNode {
  const record = asRecord(raw, FLOW_ENDPOINT, 'node')
  return {
    nodeId: readString(record, 'NodeID', FLOW_ENDPOINT),
    kind: readString(record, 'Kind', FLOW_ENDPOINT),
    title: readString(record, 'Title', FLOW_ENDPOINT),
    description: readString(record, 'Description', FLOW_ENDPOINT, ''),
    stageIndex: readNumber(record, 'StageIndex', FLOW_ENDPOINT, 0),
    version: readNumber(record, 'Version', FLOW_ENDPOINT, 0),
  }
}

function toFlowEdge(raw: unknown): IntentFlowEdge {
  const record = asRecord(raw, FLOW_ENDPOINT, 'edge')
  return {
    edgeId: readString(record, 'EdgeID', FLOW_ENDPOINT),
    fromNodeId: readString(record, 'FromNodeID', FLOW_ENDPOINT),
    toNodeId: readString(record, 'ToNodeID', FLOW_ENDPOINT),
    label: readString(record, 'Label', FLOW_ENDPOINT, ''),
    condition: readString(record, 'Condition', FLOW_ENDPOINT, ''),
  }
}

export function toIntentFlow(raw: unknown): IntentFlow {
  const record = asRecord(raw, FLOW_ENDPOINT, 'flow')
  return {
    flowVersion: readString(record, 'FlowVersion', FLOW_ENDPOINT),
    nodes: asArray(record.Nodes, FLOW_ENDPOINT, 'Nodes').map(toFlowNode),
    edges: asArray(record.Edges, FLOW_ENDPOINT, 'Edges').map(toFlowEdge),
  }
}

function toEvidence(raw: unknown): IntentEvidence {
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'evidence')
  return {
    kind: readString(record, 'Kind', DECISIONS_ENDPOINT, ''),
    text: readString(record, 'Text', DECISIONS_ENDPOINT, ''),
  }
}

function toRunNode(raw: unknown): IntentRunNode {
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'run node')
  return {
    nodeId: readString(record, 'NodeID', DECISIONS_ENDPOINT),
    version: readNumber(record, 'Version', DECISIONS_ENDPOINT, 0),
    status: readEnum(record, 'Status', DECISIONS_ENDPOINT, NODE_STATUSES),
    branch: readString(record, 'Branch', DECISIONS_ENDPOINT, ''),
    reasonCode: readString(record, 'ReasonCode', DECISIONS_ENDPOINT, ''),
    summary: readString(record, 'Summary', DECISIONS_ENDPOINT, ''),
    detail: readRecordMap(record.Detail, DECISIONS_ENDPOINT, 'Detail'),
    evidence:
      record.Evidence == null
        ? []
        : asArray(record.Evidence, DECISIONS_ENDPOINT, 'Evidence').map(toEvidence),
    durationMs: readNumber(record, 'DurationMS', DECISIONS_ENDPOINT, 0),
  }
}

function toRunEdge(raw: unknown): IntentRunEdge {
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'run edge')
  return {
    edgeId: readString(record, 'EdgeID', DECISIONS_ENDPOINT),
    status: readEnum(record, 'Status', DECISIONS_ENDPOINT, EDGE_STATUSES),
  }
}

function toDeliveryItem(raw: unknown): IntentDeliveryItem {
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'delivery item')
  return {
    id: readString(record, 'ID', DECISIONS_ENDPOINT, ''),
    sequence: readNumber(record, 'Sequence', DECISIONS_ENDPOINT, 0),
    status: readString(record, 'Status', DECISIONS_ENDPOINT, ''),
    text: readString(record, 'Text', DECISIONS_ENDPOINT, ''),
    platformMessageId: readNumber(record, 'PlatformMessageID', DECISIONS_ENDPOINT, 0),
    errorCode: readString(record, 'ErrorCode', DECISIONS_ENDPOINT, ''),
  }
}

function toDelivery(raw: unknown): IntentDelivery {
  if (raw == null) {
    return { outcome: '', reason: '', denied: 0, items: [] }
  }
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'Delivery')
  return {
    outcome: readString(record, 'Outcome', DECISIONS_ENDPOINT, ''),
    reason: readString(record, 'Reason', DECISIONS_ENDPOINT, ''),
    denied: readNumber(record, 'Denied', DECISIONS_ENDPOINT, 0),
    items:
      record.Items == null
        ? []
        : asArray(record.Items, DECISIONS_ENDPOINT, 'Delivery.Items').map(toDeliveryItem),
  }
}

function toRunSummary(raw: unknown): IntentRunSummary {
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'Summary')
  return {
    decision: readEnum(record, 'Decision', DECISIONS_ENDPOINT, ['reply', 'silent', 'defer'] as const),
    reasonCode: readString(record, 'ReasonCode', DECISIONS_ENDPOINT, ''),
    reasonText: readString(record, 'ReasonText', DECISIONS_ENDPOINT, ''),
  }
}

export function toIntentCost(raw: unknown): IntentCost | undefined {
  if (raw == null) {
    return undefined
  }
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'Cost')
  const cost: IntentCost = {
    mayIncur: readBooleanCompat(record, 'MayIncur', DECISIONS_ENDPOINT),
  }
  const estimate = readOptionalString(record, 'CostEstimate', DECISIONS_ENDPOINT)
  if (estimate) {
    cost.costEstimate = estimate
  }
  return cost
}

function readBooleanCompat(
  record: Record<string, unknown>,
  key: string,
  endpoint: string,
): boolean {
  const value = record[key]
  if (typeof value === 'boolean') {
    return value
  }
  throw contractError(endpoint, `${key} 不是布尔值`)
}

export function toIntentRun(raw: unknown): IntentRun {
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'run')
  return {
    decisionId: readNumber(record, 'DecisionID', DECISIONS_ENDPOINT, 0),
    jobId: readNumber(record, 'JobID', DECISIONS_ENDPOINT, 0),
    runId: readString(record, 'RunID', DECISIONS_ENDPOINT, ''),
    flowVersion: readString(record, 'FlowVersion', DECISIONS_ENDPOINT, ''),
    provider: readString(record, 'Provider', DECISIONS_ENDPOINT, ''),
    model: readString(record, 'Model', DECISIONS_ENDPOINT, ''),
    rulesetVersion: readString(record, 'RulesetVersion', DECISIONS_ENDPOINT, ''),
    snapshotStatus: readEnum(record, 'SnapshotStatus', DECISIONS_ENDPOINT, SNAPSHOT_STATUSES),
    missingFields: readStringArray(record.MissingFields, DECISIONS_ENDPOINT, 'MissingFields'),
    summary: toRunSummary(record.Summary),
    nodes:
      record.Nodes == null
        ? []
        : asArray(record.Nodes, DECISIONS_ENDPOINT, 'Nodes').map(toRunNode),
    edges:
      record.Edges == null
        ? []
        : asArray(record.Edges, DECISIONS_ENDPOINT, 'Edges').map(toRunEdge),
    delivery: toDelivery(record.Delivery),
    scores: readNumberMap(record.Scores, DECISIONS_ENDPOINT, 'Scores'),
    latencyMs: readNumber(record, 'LatencyMS', DECISIONS_ENDPOINT, 0),
    createdAt: readOptionalTime(record, 'CreatedAt', DECISIONS_ENDPOINT),
  }
}

function toPromptSummary(raw: unknown): PromptSummary {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'prompt')
  return {
    promptId: readString(record, 'PromptID', PROMPTS_ENDPOINT),
    name: readString(record, 'Name', PROMPTS_ENDPOINT, ''),
    activeVersion: readNumber(record, 'ActiveVersion', PROMPTS_ENDPOINT, 0),
    status: readEnum(record, 'Status', PROMPTS_ENDPOINT, PROMPT_STATUSES),
    source: readString(record, 'Source', PROMPTS_ENDPOINT, ''),
    updatedAt: readOptionalString(record, 'UpdatedAt', PROMPTS_ENDPOINT) ?? '',
  }
}

export function toPromptList(raw: unknown): PromptSummary[] {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'list')
  return asArray(record.Items, PROMPTS_ENDPOINT, 'Items').map(toPromptSummary)
}

function toPromptVersion(raw: unknown): PromptVersion {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'version')
  return {
    id: readNumber(record, 'ID', PROMPTS_ENDPOINT, 0),
    promptId: readString(record, 'PromptID', PROMPTS_ENDPOINT),
    name: readString(record, 'Name', PROMPTS_ENDPOINT, ''),
    provider: readString(record, 'Provider', PROMPTS_ENDPOINT, ''),
    version: readNumber(record, 'Version', PROMPTS_ENDPOINT, 0),
    status: readEnum(record, 'Status', PROMPTS_ENDPOINT, PROMPT_STATUSES),
    source: readString(record, 'Source', PROMPTS_ENDPOINT, ''),
    sourceRef: readString(record, 'SourceRef', PROMPTS_ENDPOINT, ''),
    hash: readString(record, 'Hash', PROMPTS_ENDPOINT, ''),
    note: readString(record, 'Note', PROMPTS_ENDPOINT, ''),
    content: record.Content,
    createdAt: readOptionalString(record, 'CreatedAt', PROMPTS_ENDPOINT) ?? '',
    publishedAt: readOptionalString(record, 'PublishedAt', PROMPTS_ENDPOINT) ?? '',
  }
}

export function toPromptDetail(raw: unknown): PromptDetail {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'prompt detail')
  return {
    promptId: readString(record, 'PromptID', PROMPTS_ENDPOINT),
    name: readString(record, 'Name', PROMPTS_ENDPOINT, ''),
    versions:
      record.Versions == null
        ? []
        : asArray(record.Versions, PROMPTS_ENDPOINT, 'Versions').map(toPromptVersion),
  }
}

export interface RecalculateResult {
  before: IntentRun
  after: IntentRun
  changed: boolean
}

export function toRecalculateResult(raw: unknown): RecalculateResult {
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'recalculate')
  return {
    before: toIntentRun(record.Before),
    after: toIntentRun(record.After),
    changed: readBooleanCompat(record, 'Changed', DECISIONS_ENDPOINT),
  }
}
