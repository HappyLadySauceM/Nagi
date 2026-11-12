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
  IntentPromptInfo,
  IntentReplayInfo,
  IntentRun,
  IntentRunEdge,
  IntentRunNode,
  IntentRunSummary,
  IntentSnapshotStatus,
  PromptDetail,
  PromptStatus,
  PromptSummary,
  PromptVersion,
  PromptSyncStatus,
  PromptSyncResult,
  ReplayComparison,
  ReplayEstimate,
  ReplayEvaluation,
  ReplayEvaluationSummary,
  ReplayParameters,
  ReplayReproduction,
  ReplayDifference,
} from '../contracts'
import {
  asArray,
  asRecord,
  contractError,
  readBoolean,
  readNumber,
  readOptionalNumber,
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

function toReplayInfo(raw: unknown): IntentReplayInfo | undefined {
  if (raw == null) {
    return undefined
  }
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'Replay')
  return {
    input: readString(record, 'Input', DECISIONS_ENDPOINT, ''),
    semantic: readString(record, 'Semantic', DECISIONS_ENDPOINT, ''),
    prompt: readString(record, 'Prompt', DECISIONS_ENDPOINT, ''),
    policy: readString(record, 'Policy', DECISIONS_ENDPOINT, ''),
    rules: readString(record, 'Rules', DECISIONS_ENDPOINT, ''),
    persisted: readBooleanCompat(record, 'Persisted', DECISIONS_ENDPOINT),
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
  const known = readOptionalBoolean(record, 'Known', DECISIONS_ENDPOINT)
  if (known !== undefined) {
    cost.known = known
  }
  const amount = readOptionalNumber(record, 'Amount', DECISIONS_ENDPOINT)
  if (amount !== undefined) {
    cost.amount = amount
  }
  const currency = readOptionalString(record, 'Currency', DECISIONS_ENDPOINT)
  if (currency) {
    cost.currency = currency
  }
  const text = readOptionalString(record, 'Text', DECISIONS_ENDPOINT)
  if (text) {
    cost.text = text
  }
  return cost
}

function readOptionalBoolean(
  record: Record<string, unknown>,
  key: string,
  endpoint: string,
): boolean | undefined {
  const value = record[key]
  if (value === undefined || value === null) {
    return undefined
  }
  if (typeof value === 'boolean') {
    return value
  }
  throw contractError(endpoint, `${key} 不是布尔值`)
}

function toIntentPrompt(raw: unknown): IntentPromptInfo | undefined {
  if (raw == null) {
    return undefined
  }
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'Prompt')
  return {
    id: readString(record, 'ID', DECISIONS_ENDPOINT, ''),
    name: readString(record, 'Name', DECISIONS_ENDPOINT, ''),
    version: readNumber(record, 'Version', DECISIONS_ENDPOINT, 0),
    source: readString(record, 'Source', DECISIONS_ENDPOINT, ''),
    sourceRef: readString(record, 'SourceRef', DECISIONS_ENDPOINT, ''),
    hash: readString(record, 'Hash', DECISIONS_ENDPOINT, ''),
  }
}

function toReproduction(raw: unknown): ReplayReproduction {
  if (raw == null) {
    return { fullyReproduced: false, reasons: [] }
  }
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'Reproduction')
  return {
    fullyReproduced: readBooleanCompat(record, 'FullyReproduced', DECISIONS_ENDPOINT),
    reasons: readStringArray(record.Reasons, DECISIONS_ENDPOINT, 'Reasons'),
  }
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
    replay: toReplayInfo(record.Replay),
    prompt: toIntentPrompt(record.Prompt),
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


function toPromptSyncCandidate(raw: unknown): PromptSyncStatus['candidates'][number] {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'sync candidate')
  return {
    id: readNumber(record, 'ID', PROMPTS_ENDPOINT, 0),
    version: readNumber(record, 'Version', PROMPTS_ENDPOINT, 0),
    name: readString(record, 'Name', PROMPTS_ENDPOINT, ''),
    hash: readString(record, 'Hash', PROMPTS_ENDPOINT, ''),
    sourceRef: readString(record, 'SourceRef', PROMPTS_ENDPOINT, ''),
    createdAt: readOptionalString(record, 'CreatedAt', PROMPTS_ENDPOINT) ?? '',
  }
}

export function toPromptSyncStatus(raw: unknown): PromptSyncStatus {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'sync status')
  const activeRaw = record.Active
  const active = activeRaw == null ? undefined : (() => {
    const item = asRecord(activeRaw, PROMPTS_ENDPOINT, 'sync active')
    return {
      version: readNumber(item, 'Version', PROMPTS_ENDPOINT, 0),
      source: readString(item, 'Source', PROMPTS_ENDPOINT, ''),
      sourceRef: readString(item, 'SourceRef', PROMPTS_ENDPOINT, ''),
      status: readEnum(item, 'Status', PROMPTS_ENDPOINT, PROMPT_STATUSES),
    }
  })()
  return {
    promptId: readString(record, 'PromptID', PROMPTS_ENDPOINT),
    provider: readString(record, 'Provider', PROMPTS_ENDPOINT, ''),
    configured: readBoolean(record, 'Configured', PROMPTS_ENDPOINT, false),
    status: readEnum(record, 'Status', PROMPTS_ENDPOINT, ['not_configured', 'idle', 'ok', 'failed'] as const),
    reasonCode: readString(record, 'ReasonCode', PROMPTS_ENDPOINT, ''),
    message: readString(record, 'Message', PROMPTS_ENDPOINT, ''),
    lastAttemptAt: readOptionalTime(record, 'LastAttemptAt', PROMPTS_ENDPOINT),
    lastSuccessAt: readOptionalTime(record, 'LastSuccessAt', PROMPTS_ENDPOINT),
    active,
    candidates:
      record.Candidates == null
        ? []
        : asArray(record.Candidates, PROMPTS_ENDPOINT, 'Candidates').map(toPromptSyncCandidate),
  }
}

export function toPromptSyncResult(raw: unknown): PromptSyncResult {
  const record = asRecord(raw, PROMPTS_ENDPOINT, 'sync result')
  return {
    promptId: readString(record, 'PromptID', PROMPTS_ENDPOINT),
    provider: readString(record, 'Provider', PROMPTS_ENDPOINT, ''),
    remoteName: readString(record, 'RemoteName', PROMPTS_ENDPOINT, ''),
    remoteVersion: readNumber(record, 'RemoteVersion', PROMPTS_ENDPOINT, 0),
    candidate: toPromptVersion(record.Candidate),
    status: readEnum(record, 'Status', PROMPTS_ENDPOINT, ['already_synced', 'candidate_created'] as const),
  }
}

function toReplayTokenEstimate(raw: unknown): ReplayEstimate['tokenEstimate'] {
  if (raw == null) {
    return undefined
  }
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'TokenEstimate')
  const tokenNumber = (primary: string, legacy: string) => {
    const value = record[primary] ?? record[legacy]
    return typeof value === 'number' && Number.isFinite(value) ? value : 0
  }
  return {
    inputTokens: tokenNumber('InputTokens', 'Input'),
    outputTokens: tokenNumber('OutputTokens', 'Output'),
    totalTokens: tokenNumber('TotalTokens', 'Total'),
    method: readString(record, 'Method', DECISIONS_ENDPOINT, ''),
  }
}

export function toReplayEstimate(raw: unknown): ReplayEstimate {
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'replay estimate')
  return {
    decisionId: readNumber(record, 'DecisionID', DECISIONS_ENDPOINT, 0),
    provider: readString(record, 'Provider', DECISIONS_ENDPOINT, ''),
    model: readString(record, 'Model', DECISIONS_ENDPOINT, ''),
    sampleCount: readNumber(record, 'SampleCount', DECISIONS_ENDPOINT, 1),
    estimatedExternalRequests: readNumber(record, 'EstimatedExternalRequests', DECISIONS_ENDPOINT, 0),
    tokenEstimate: toReplayTokenEstimate(record.TokenEstimate),
    cost: toIntentCost(record.Cost),
    confirmationToken: readString(record, 'ConfirmationToken', DECISIONS_ENDPOINT, ''),
    expiresAt: readOptionalTime(record, 'ExpiresAt', DECISIONS_ENDPOINT),
    parametersFingerprint: readString(record, 'ParametersFingerprint', DECISIONS_ENDPOINT, ''),
  }
}

function toReplayDifference(raw: unknown): ReplayDifference {
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'replay difference')
  return {
    field: readString(record, 'Field', DECISIONS_ENDPOINT, ''),
    before: String(record.Before ?? ''),
    after: String(record.After ?? ''),
  }
}

export function toReplayEvaluationSummary(raw: unknown): ReplayEvaluationSummary {
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'replay evaluation')
  return {
    id: readNumber(record, 'ID', DECISIONS_ENDPOINT, 0),
    decisionId: readNumber(record, 'DecisionID', DECISIONS_ENDPOINT, 0),
    mode: readEnum(record, 'Mode', DECISIONS_ENDPOINT, ['recalculate', 'model'] as const),
    name: readString(record, 'Name', DECISIONS_ENDPOINT, ''),
    note: readOptionalString(record, 'Note', DECISIONS_ENDPOINT),
    changed: readBooleanCompat(record, 'Changed', DECISIONS_ENDPOINT),
    summary: toRunSummary(record.Summary),
    createdAt: readOptionalTime(record, 'CreatedAt', DECISIONS_ENDPOINT),
    reproduction: toReproduction(record.Reproduction),
  }
}

export function toReplayEvaluationList(raw: unknown): ReplayEvaluationSummary[] {
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'replay evaluation list')
  return asArray(record.Items, DECISIONS_ENDPOINT, 'Items').map(toReplayEvaluationSummary)
}

function toReplayParameters(raw: unknown): ReplayParameters | undefined {
  if (raw == null) {
    return undefined
  }
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'ReplayParameters')
  return {
    provider: readString(record, 'Provider', DECISIONS_ENDPOINT, ''),
    model: readString(record, 'Model', DECISIONS_ENDPOINT, ''),
    promptId: readString(record, 'PromptID', DECISIONS_ENDPOINT, ''),
    promptVersion: readNumber(record, 'PromptVersion', DECISIONS_ENDPOINT, 0),
    promptHash: readString(record, 'PromptHash', DECISIONS_ENDPOINT, ''),
    rulesetVersion: readString(record, 'RulesetVersion', DECISIONS_ENDPOINT, ''),
    sampleCount: readNumber(record, 'SampleCount', DECISIONS_ENDPOINT, 1),
    estimatedExternalRequests: readNumber(record, 'EstimatedExternalRequests', DECISIONS_ENDPOINT, 0),
    tokenEstimate: toReplayTokenEstimate(record.TokenEstimate),
  }
}

export function toReplayEvaluation(raw: unknown): ReplayEvaluation {
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'replay evaluation')
  return {
    id: readNumber(record, 'ID', DECISIONS_ENDPOINT, 0),
    decisionId: readNumber(record, 'DecisionID', DECISIONS_ENDPOINT, 0),
    botId: readNumber(record, 'BotID', DECISIONS_ENDPOINT, 0),
    mode: readEnum(record, 'Mode', DECISIONS_ENDPOINT, ['recalculate', 'model'] as const),
    name: readString(record, 'Name', DECISIONS_ENDPOINT, ''),
    note: readString(record, 'Note', DECISIONS_ENDPOINT, ''),
    changed: readBooleanCompat(record, 'Changed', DECISIONS_ENDPOINT),
    before: toIntentRun(record.Before),
    after: toIntentRun(record.After),
    parameters: toReplayParameters(record.Parameters),
    cost: toIntentCost(record.Cost),
    reproduction: toReproduction(record.Reproduction),
    createdAt: readOptionalTime(record, 'CreatedAt', DECISIONS_ENDPOINT),
  }
}

export function toReplayComparison(raw: unknown): ReplayComparison {
  const record = asRecord(raw, DECISIONS_ENDPOINT, 'replay comparison')
  return {
    evaluationId: readNumber(record, 'EvaluationID', DECISIONS_ENDPOINT, 0),
    against: readString(record, 'Against', DECISIONS_ENDPOINT, ''),
    changed: readBooleanCompat(record, 'Changed', DECISIONS_ENDPOINT),
    original: toIntentRun(record.Original),
    replay: toIntentRun(record.Replay),
    differences:
      record.Differences == null
        ? []
        : asArray(record.Differences, DECISIONS_ENDPOINT, 'Differences').map(toReplayDifference),
    reproduction: toReproduction(record.Reproduction),
  }
}
