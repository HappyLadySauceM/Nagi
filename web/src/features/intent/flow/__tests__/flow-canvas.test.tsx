import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { FlowCanvas } from '../flow-canvas'
import type { IntentFlow, IntentRun } from '../../../../lib/api/contracts'

const flow: IntentFlow = {
  flowVersion: 'intent-flow-v1',
  nodes: [
    { nodeId: 'policy', kind: 'policy', title: '策略与权限', description: '检查策略', stageIndex: 0, version: 1 },
    { nodeId: 'semantic', kind: 'semantic', title: '语义判定', description: '模型判断', stageIndex: 1, version: 1 },
    { nodeId: 'decision', kind: 'decision', title: '最终决策', description: '给出结论', stageIndex: 2, version: 1 },
  ],
  edges: [
    { edgeId: 'e_policy_semantic', fromNodeId: 'policy', toNodeId: 'semantic', label: '继续', condition: '策略可用' },
    { edgeId: 'e_semantic_decision', fromNodeId: 'semantic', toNodeId: 'decision', label: '完成', condition: '语义完成' },
  ],
}

const run: IntentRun = {
  decisionId: 42,
  jobId: 1,
  runId: 'run-1',
  flowVersion: 'intent-flow-v1',
  provider: 'jev',
  model: 'model',
  rulesetVersion: 'v1',
  snapshotStatus: 'complete',
  missingFields: [],
  summary: { decision: 'reply', reasonCode: 'private_normal', reasonText: '私聊正常回复' },
  nodes: [
    { nodeId: 'policy', version: 1, status: 'executed', branch: 'authorized', reasonCode: '', summary: '通过', detail: {}, evidence: [], durationMs: 0 },
    { nodeId: 'semantic', version: 1, status: 'degraded', branch: 'unavailable', reasonCode: 'semantic_unavailable', summary: '语义降级', detail: {}, evidence: [], durationMs: 3 },
    { nodeId: 'decision', version: 1, status: 'skipped', branch: 'blocked', reasonCode: 'not_addressed', summary: '未进入', detail: {}, evidence: [], durationMs: 0 },
  ],
  edges: [
    { edgeId: 'e_policy_semantic', status: 'taken' },
    { edgeId: 'e_semantic_decision', status: 'not_taken' },
  ],
  delivery: { outcome: '', reason: '', denied: 0, items: [] },
  scores: {},
  latencyMs: 3,
  createdAt: '2026-11-12T08:00:00Z',
}

describe('FlowCanvas', () => {
  it('renders fixed nodes with distinct actual, degraded and skipped statuses', () => {
    render(<FlowCanvas flow={flow} run={run} />)
    expect(screen.getByRole('button', { name: '策略与权限 实际执行' })).toBeVisible()
    expect(screen.getByRole('button', { name: '语义判定 降级' })).toBeVisible()
    expect(screen.getByRole('button', { name: '最终决策 跳过' })).toBeVisible()
  })

  it('renders nodes as pending without a run and does not offer drag editing', () => {
    render(<FlowCanvas flow={flow} />)
    expect(screen.getByRole('button', { name: '策略与权限 待运行' })).toBeVisible()
    expect(screen.queryByRole('button', { name: /拖动|连线/ })).toBeNull()
  })
})
