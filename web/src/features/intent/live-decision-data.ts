import type { IntentRun, IntentRunNode, IntentRunEdge } from '../../lib/api/contracts'
import { apiRequest } from '../../lib/api/client'
import { asArray, asRecord, contractError, readNumber, readOptionalNumber, readString } from '../../lib/api/adapters/guard'

export const RUN_SNAPSHOT = '/api/v1/intent/decision-runs/snapshot'
export const RUN_STREAM = '/api/v1/intent/decision-runs/stream'

export type RunEvent = ReturnType<typeof readEvent>
export type RunRow = ReturnType<typeof readRow>

function readEvent(raw: unknown) {
  const event = asRecord(raw, RUN_STREAM, 'event')
  const data = asRecord(event.Data, RUN_STREAM, 'event metadata')
  const sequence = readNumber(event, 'Sequence', RUN_STREAM)
  if (!Number.isSafeInteger(sequence) || sequence < 1) throw contractError(RUN_STREAM, '无效的步骤序号')
  return {
    runId: readString(event, 'RunID', RUN_STREAM),
    conversationId: readString(event, 'ConversationID', RUN_STREAM),
    startedAt: readString(event, 'StartedAt', RUN_STREAM),
    at: readString(event, 'At', RUN_STREAM),
    sequence,
    kind: readString(event, 'Kind', RUN_STREAM),
    state: readString(data, 'State', RUN_STREAM, ''),
    decisionId: readNumber(data, 'DecisionID', RUN_STREAM, 0),
    decision: readString(data, 'Decision', RUN_STREAM, ''),
    reason: readString(data, 'ReasonCode', RUN_STREAM, ''),
    branch: readString(data, 'Branch', RUN_STREAM, ''),
    ruleId: readString(data, 'RuleID', RUN_STREAM, ''),
    elapsed: readOptionalNumber(data, 'ElapsedMS', RUN_STREAM),
    threshold: readOptionalNumber(data, 'ThresholdMS', RUN_STREAM),
    latency: readOptionalNumber(data, 'LatencyMS', RUN_STREAM),
  }
}

function readRow(raw: unknown) {
  const row = asRecord(raw, RUN_SNAPSHOT, 'run')
  const events = asArray(row.Events ?? [], RUN_SNAPSHOT, 'Events').map(readEvent)
  const end = events.find(event => event.kind === 'end')
  return {
    runId: readString(row, 'RunID', RUN_SNAPSHOT),
    conversationId: readString(row, 'ConversationID', RUN_SNAPSHOT),
    decisionId: readNumber(row, 'DecisionID', RUN_SNAPSHOT),
    startedAt: readString(row, 'StartedAt', RUN_SNAPSHOT),
    state: readString(row, 'State', RUN_SNAPSHOT),
    decision: readString(row, 'Decision', RUN_SNAPSHOT),
    reason: end?.reason || readString(row, 'ReasonCode', RUN_SNAPSHOT),
    events,
  }
}

function readLease(row: Record<string, unknown>, endpoint: string) {
  const lease = readNumber(row, 'LeaseMS', endpoint)
  if (lease <= 0 || lease > 10_000) throw contractError(endpoint, '无效的授权租期')
  return lease
}

export async function loadRunSnapshot(botId: number, signal: AbortSignal, before?: string, selected?: string) {
  const params = new URLSearchParams({ bot_id: String(botId) })
  if (before) params.set('before', before)
  else if (selected) params.set('selected', selected)
  const page = asRecord(await apiRequest(`${RUN_SNAPSHOT}?${params}`, { signal }), RUN_SNAPSHOT, 'snapshot')
  return {
    rows: asArray(page.Rows, RUN_SNAPSHOT, 'Rows').map(readRow),
    cursor: readString(page, 'Cursor', RUN_SNAPSHOT),
    next: readString(page, 'Next', RUN_SNAPSHOT),
    lease: readLease(page, RUN_SNAPSHOT),
  }
}

export function readRunBatch(data: string) {
  const batch = asRecord(JSON.parse(data), RUN_STREAM, 'batch')
  return { events: asArray(batch.Events, RUN_STREAM, 'Events').map(readEvent), lease: readLease(batch, RUN_STREAM) }
}

export function rowKey(row: RunRow) {
  return row.runId ? `r:${row.runId}` : `d:${row.decisionId}`
}

function sortRuns(rows: RunRow[]) {
  return rows.sort((a, b) => Date.parse(b.startedAt) - Date.parse(a.startedAt) || rowKey(b).localeCompare(rowKey(a)))
}

export function mergeRunRows(rows: RunRow[], incoming: RunRow[]) {
  const byKey = new Map(rows.map(row => [rowKey(row), row]))
  for (const row of incoming) {
    const key = rowKey(row)
    const previous = byKey.get(key)
    if (!previous) {
      byKey.set(key, row)
      continue
    }
    const stages = new Map(row.events.map(event => [event.sequence, event]))
    for (const event of previous.events) stages.set(event.sequence, event)
    const events = [...stages.values()].sort((a, b) => a.sequence - b.sequence)
    const end = events.find(event => event.kind === 'end')
    const authoritative = previous.state !== 'running' ? previous : row
    byKey.set(key, {
      ...authoritative, events,
      state: end?.state || authoritative.state,
      decisionId: end?.decisionId || authoritative.decisionId || row.decisionId,
      decision: end?.decision || authoritative.decision || row.decision,
      reason: end?.reason || authoritative.reason || row.reason,
    })
  }
  return sortRuns([...byKey.values()])
}

export function mergeRunEvents(rows: RunRow[], events: RunEvent[]) {
  if (!events.length) return rows
  const byKey = new Map(rows.map(row => [rowKey(row), row]))
  for (const event of events) {
    const key = `r:${event.runId}`
    const old = byKey.get(key)
    if (old?.events.some(item => item.sequence === event.sequence)) continue
    const ordered = [...(old?.events ?? []), event].sort((a, b) => a.sequence - b.sequence)
    const end = ordered.find(item => item.kind === 'end')
    byKey.set(key, {
      runId: event.runId, conversationId: event.conversationId, startedAt: event.startedAt,
      events: ordered, decisionId: end?.decisionId ?? old?.decisionId ?? 0,
      state: end?.state || old?.state || 'running',
      decision: end?.decision ?? old?.decision ?? '', reason: end?.reason ?? old?.reason ?? '',
    })
  }
  return sortRuns([...byKey.values()])
}

const stages: Record<string, string> = {
  start: '开始意图判断', context: '上下文已准备', rules: '规则已判断',
  semantic_started: '语义判断已开始', semantic: '语义阶段已结束',
  cooldown_resolved: '冷却规则已复核', end: '运行已结束',
}

export function stageLabel(event: RunEvent) {
  return stages[event.kind] ?? '阶段未知'
}

const nodeByStage: Record<string, string> = {
  start: 'policy', context: 'context', rules: 'rules', semantic_started: 'semantic',
  semantic: 'semantic', cooldown_resolved: 'rules', end: 'decision',
}


export function recordedFlow(row: RunRow, through?: number): Pick<IntentRun, 'nodes' | 'edges'> {
  const nodes = new Map<string, IntentRunNode>()
  const edges = new Map<string, IntentRunEdge>()
  const take = (edgeId: string) => edges.set(edgeId, { edgeId, status: 'taken' })
  const cooldownEvents: Record<string, unknown>[] = []
  const ended = row.events.find(event => event.kind === 'end')
  for (const event of row.events) {
    if (through !== undefined && event.sequence > through) break
    const nodeId = nodeByStage[event.kind]
    if (!nodeId) continue
    const status = event.kind === 'end' ? (event.state === 'completed' ? 'executed' : 'failed')
      : event.kind === 'semantic' && event.branch === 'skipped' ? 'skipped'
        : event.kind === 'semantic' && event.branch === 'degraded' ? 'degraded' : 'executed'
    const branch = event.kind === 'semantic_started' ? (ended?.state === 'interrupted' ? 'live_interrupted' : ended ? 'live_unknown' : 'live_started')
      : event.kind === 'end' && event.state === 'interrupted' ? 'live_interrupted' : event.branch
    const nodeStatus = event.kind === 'semantic_started' ? 'unknown' : status
    if (event.ruleId === 'cooldown' && (event.kind === 'rules' || event.kind === 'cooldown_resolved')) {
      cooldownEvents.push({ RuleID: event.ruleId, Version: 'cooldown-v1', Sequence: event.sequence,
        Branch: event.branch, ElapsedMS: event.elapsed, ThresholdMS: event.threshold, Operator: '<' })
    }
    const detail: Record<string, unknown> = { Sequence: event.sequence }
    if (nodeId === 'rules' && cooldownEvents.length) detail.RuleEvents = [...cooldownEvents]
    if (event.kind === 'semantic' && event.latency !== undefined) detail.LatencyMS = event.latency
    nodes.set(nodeId, {
      nodeId, version: 1, status: nodeStatus, branch, reasonCode: event.reason,
      summary: stageLabel(event), detail, evidence: [], durationMs: event.latency ?? 0,
    })
    if (event.kind === 'context') take('e_policy_context')
    if (event.kind === 'rules') take('e_context_rules')
    if (event.kind === 'semantic_started') take('e_rules_semantic')
    if (event.kind === 'semantic' && event.branch !== 'skipped') take('e_rules_semantic')
    if (event.kind === 'end' && event.state === 'completed') {
      take(edges.has('e_rules_semantic') ? 'e_semantic_decision' : 'e_rules_decision')
    }
  }
  return { nodes: [...nodes.values()], edges: [...edges.values()] }
}
