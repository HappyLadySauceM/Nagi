import { describe, expect, it } from 'vitest'
import { toIntentFlow, toIntentRun, toRecalculateResult } from '../../../../lib/api/adapters/flow'

const flow = {
  FlowVersion: 'intent-flow-v1',
  Nodes: [
    { NodeID: 'policy', Kind: 'policy', Title: '策略与权限', Description: '检查策略', StageIndex: 0, Version: 1 },
    { NodeID: 'decision', Kind: 'decision', Title: '最终决策', Description: '给出结论', StageIndex: 1, Version: 1 },
  ],
  Edges: [
    { EdgeID: 'e_policy_decision', FromNodeID: 'policy', ToNodeID: 'decision', Label: '结束', Condition: 'blocked' },
  ],
}

const run = {
  DecisionID: 7,
  JobID: 9,
  RunID: 'run-1',
  FlowVersion: 'intent-flow-v1',
  Provider: 'jev',
  Model: 'decision-model',
  RulesetVersion: 'scope:1@1',
  SnapshotStatus: 'partial',
  MissingFields: ['prompt_version'],
  Summary: { Decision: 'reply', ReasonCode: 'private_normal', ReasonText: '私聊正常回复' },
  Nodes: [
    {
      NodeID: 'policy',
      Version: 1,
      Status: 'executed',
      Branch: 'authorized',
      ReasonCode: '',
      Summary: '通过',
      Detail: { Source: 'independent', PolicyVersion: 3 },
      Evidence: [{ Kind: 'policy', Text: 'reply_mode=intent_based' }],
      DurationMS: 2,
    },
    {
      NodeID: 'semantic',
      Version: 1,
      Status: 'degraded',
      Branch: 'unavailable',
      ReasonCode: 'semantic_unavailable',
      Summary: '语义不可用',
      Detail: { Called: false, Available: false },
      Evidence: [],
      DurationMS: 0,
    },
  ],
  Edges: [{ EdgeID: 'e_policy_decision', Status: 'taken' }],
  Delivery: { Outcome: 'completed', Reason: '', Denied: 0, Items: [] },
  Scores: { directed_to_bot: 0.2 },
  LatencyMS: 10,
  CreatedAt: '2026-11-12T08:00:00Z',
}

describe('flow adapters', () => {
  it('parses the fixed flow definition', () => {
    expect(toIntentFlow(flow)).toMatchObject({
      flowVersion: 'intent-flow-v1',
      nodes: [{ nodeId: 'policy', stageIndex: 0 }, { nodeId: 'decision', stageIndex: 1 }],
      edges: [{ edgeId: 'e_policy_decision', fromNodeId: 'policy', toNodeId: 'decision' }],
    })
  })

  it('parses optional replay source markers', () => {
    const parsed = toIntentRun({
      ...run,
      Replay: {
        Input: 'snapshot',
        Semantic: 'current',
        Prompt: 'snapshot',
        Policy: 'current',
        Rules: 'snapshot',
        Persisted: false,
      },
    })
    expect(parsed.replay).toEqual({
      input: 'snapshot',
      semantic: 'current',
      prompt: 'snapshot',
      policy: 'current',
      rules: 'snapshot',
      persisted: false,
    })
    expect(toIntentRun(run).replay).toBeUndefined()
  })

  it('keeps replay markers on the recalculated run and none on the stored before run', () => {
    const parsed = toRecalculateResult({
      Before: run,
      After: {
        ...run,
        Replay: {
          Input: 'snapshot',
          Semantic: 'snapshot',
          Prompt: 'snapshot',
          Policy: 'current',
          Rules: 'current',
          Persisted: false,
        },
      },
      Changed: false,
    })
    expect(parsed.before.replay).toBeUndefined()
    expect(parsed.after.replay).toMatchObject({ input: 'snapshot', policy: 'current', persisted: false })
  })

  it('parses node statuses, evidence, delivery and snapshot completeness', () => {
    const parsed = toIntentRun(run)
    expect(parsed.summary).toEqual({ decision: 'reply', reasonCode: 'private_normal', reasonText: '私聊正常回复' })
    expect(parsed.nodes[1].status).toBe('degraded')
    expect(parsed.nodes[0].evidence[0]).toEqual({ kind: 'policy', text: 'reply_mode=intent_based' })
    expect(parsed.snapshotStatus).toBe('partial')
    expect(parsed.missingFields).toEqual(['prompt_version'])
    expect(parsed.delivery.outcome).toBe('completed')
  })
})
