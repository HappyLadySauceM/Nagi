import { expect, it } from 'vitest'
import { mergeRunEvents, mergeRunRows, readRunBatch, recordedFlow } from '../live-decision-data'

function event(sequence: number, kind: string, Data = {}) {
  return {
    RunID: 'run-1', ConversationID: 'conversation-1', StartedAt: '2026-11-22T00:00:00Z',
    At: '2026-11-22T00:00:00Z', Sequence: sequence, Kind: kind, Data,
  }
}

it('keeps the same row when completion and replayed stages arrive', () => {
  const start = event(1, 'start', { State: 'running' })
  const end = event(8, 'end', { State: 'completed', DecisionID: 42, Decision: 'silent' })
  const { events } = readRunBatch(JSON.stringify({ Events: [start], LeaseMS: 10000 }))
  const active = mergeRunEvents([], events)
  expect(active[0]).toMatchObject({ state: 'running', decisionId: 0, decision: '' })
  const resumed = readRunBatch(JSON.stringify({ Events: [end, start, event(3, 'rules')], LeaseMS: 10000 }))
  const rows = mergeRunEvents(active, resumed.events)
  expect(rows).toHaveLength(1)
  expect(rows[0]).toMatchObject({ runId: 'run-1', state: 'completed', decisionId: 42, decision: 'silent' })
  expect(rows[0].events.map(item => item.sequence)).toEqual([1, 3, 8])
})

it('rejects a batch that extends authorization beyond the approved lease', () => {
  expect(() => readRunBatch(JSON.stringify({ Events: [], LeaseMS: 10001 }))).toThrow()
})


it('merges a late partial page without replacing terminal metadata or committed stages', () => {
  const batch = (Events: ReturnType<typeof event>[]) => readRunBatch(JSON.stringify({ Events, LeaseMS: 10000 })).events
  const terminal = mergeRunEvents([], batch([event(1, 'start'), event(8, 'end', { State: 'completed', DecisionID: 42, Decision: 'silent', ReasonCode: 'observe_only' })]))[0]
  const partial = mergeRunEvents([], batch([event(3, 'rules')]))[0]
  const incomplete = { ...partial, state: 'completed' }
  const result = mergeRunRows([terminal], [partial, incomplete])
  expect(result).toHaveLength(1)
  expect(result[0]).toMatchObject({ state: 'completed', decisionId: 42, decision: 'silent', reason: 'observe_only' })
  expect(result[0].events.map(stage => stage.sequence)).toEqual([1, 3, 8])
})


it('projects committed cooldown comparisons and model timing for a failed run', () => {
  const facts = readRunBatch(JSON.stringify({ LeaseMS: 10000, Events: [
    event(3, 'rules', { RuleID: 'cooldown', Branch: 'deferred', ThresholdMS: 10000, ElapsedMS: 5000 }),
    event(4, 'semantic_started'), event(5, 'semantic', { Branch: 'used', LatencyMS: 37 }),
    event(6, 'cooldown_resolved', { RuleID: 'cooldown', Branch: 'restored', ThresholdMS: 10000, ElapsedMS: 5000 }),
    event(8, 'end', { State: 'failed', ReasonCode: 'decision_failed' }),
  ] })).events
  const [row] = mergeRunEvents([], facts)
  const canvas = recordedFlow(row)
  expect(canvas.nodes.find(node => node.nodeId === 'semantic')).toMatchObject({ status: 'executed', durationMs: 37 })
  expect(canvas.nodes.find(node => node.nodeId === 'rules')?.detail.RuleEvents).toMatchObject([
    { Sequence: 3, Branch: 'deferred', ThresholdMS: 10000, ElapsedMS: 5000 },
    { Sequence: 6, Branch: 'restored', ThresholdMS: 10000, ElapsedMS: 5000 },
  ])
  expect(recordedFlow(row, 3).nodes.find(node => node.nodeId === 'rules')?.detail.RuleEvents).toHaveLength(1)
})

it('marks a semantic start without a committed result as interrupted even at its selected step', () => {
  const facts = readRunBatch(JSON.stringify({ LeaseMS: 10000, Events: [event(4, 'semantic_started'),
    event(8, 'end', { State: 'interrupted', ReasonCode: 'claim_lost' })] })).events
  const [row] = mergeRunEvents([], facts)
  expect(recordedFlow(row, 4).nodes).toMatchObject([{ nodeId: 'semantic', status: 'unknown', branch: 'live_interrupted' }])
  expect(recordedFlow({ ...row, events: facts.slice(0, 1) }).nodes).toMatchObject([{ branch: 'live_started' }])
})
