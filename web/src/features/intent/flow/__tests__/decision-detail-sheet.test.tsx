import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { useState } from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { DecisionDetailSheet } from '../decision-detail-sheet'

const response = (data: unknown) =>
  new Response(JSON.stringify({ code: 200, message: 'success', data }), {
    headers: { 'Content-Type': 'application/json' },
  })

const run = {
  DecisionID: 42,
  JobID: 9,
  RunID: 'run-42',
  FlowVersion: 'intent-flow-v1',
  Provider: 'jev',
  Model: 'decision-model',
  RulesetVersion: 'v1',
  SnapshotStatus: 'complete',
  MissingFields: [],
  Summary: { Decision: 'reply', ReasonCode: 'private_normal', ReasonText: '私聊正常回复' },
  Nodes: [
    { NodeID: 'policy', Version: 1, Status: 'executed', Branch: 'authorized', ReasonCode: '', Summary: '通过', Detail: {}, Evidence: [], DurationMS: 0 },
    { NodeID: 'decision', Version: 1, Status: 'executed', Branch: '', ReasonCode: 'private_normal', Summary: '回复', Detail: {}, Evidence: [], DurationMS: 0 },
  ],
  Edges: [{ EdgeID: 'e1', Status: 'taken' }],
  Delivery: { Outcome: 'completed', Reason: '', Denied: 0, Items: [] },
  Scores: {},
  LatencyMS: 10,
  CreatedAt: '2026-11-12T08:00:00Z',
}

const flow = {
  FlowVersion: 'intent-flow-v1',
  Nodes: [
    { NodeID: 'policy', Kind: 'policy', Title: '策略与权限', Description: '检查策略', StageIndex: 0, Version: 1 },
    { NodeID: 'decision', Kind: 'decision', Title: '最终决策', Description: '给出结论', StageIndex: 1, Version: 1 },
  ],
  Edges: [{ EdgeID: 'e1', FromNodeID: 'policy', ToNodeID: 'decision', Label: '继续', Condition: '策略通过' }],
}

const detail = {
  ID: 42,
  JobID: 9,
  BotID: 1,
  ConversationID: 'conv-42',
  Decision: 'reply',
  ReasonCode: 'private_normal',
  Provider: 'jev',
  Model: 'decision-model',
  RulesetVersion: 'v1',
  LatencyMS: 10,
  CreatedAt: '2026-11-12T08:00:00Z',
  Scores: {},
  Steps: [],
  Run: run,
}

function Harness() {
  const [id, setId] = useState<number | null>(42)
  return <DecisionDetailSheet decisionId={id} onClose={() => setId(null)} />
}

beforeEach(() => {
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe = vi.fn()
      unobserve = vi.fn()
      disconnect = vi.fn()
    },
  )
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

it('opens a second-level node drawer and Escape closes only the topmost layer', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn((input: string) => {
      const url = new URL(String(input), 'http://localhost')
      if (url.pathname === '/api/v1/intent/decisions/42') return Promise.resolve(response(detail))
      if (url.pathname === '/api/v1/intent/flow') return Promise.resolve(response(flow))
      if (url.pathname === '/api/v1/intent/replay-evaluations') {
        return Promise.resolve(response({ Items: [], Limit: 20 }))
      }
      if (url.pathname === '/api/v1/audit/runs') return Promise.resolve(response({ Items: [] }))
      throw new Error(`Unexpected request ${url.pathname}`)
    }),
  )

  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <Harness />
    </QueryClientProvider>,
  )

  expect(await screen.findByText('决策详情')).toBeVisible()
  fireEvent.click(await screen.findByRole('button', { name: '策略与权限 实际执行' }))
  expect(await screen.findByText('节点结论')).toBeVisible()
  const drawers = screen.getAllByRole('dialog', { hidden: true })
  expect(drawers).toHaveLength(2)
  expect(within(drawers[1]).getByText('版本')).toBeVisible()
  expect(within(drawers[1]).getByText('v1', { exact: true })).toBeVisible()

  fireEvent.keyDown(document, { key: 'Escape' })
  await waitFor(() => expect(screen.queryByText('节点结论')).toBeNull())
  expect(screen.getByText('决策详情')).toBeVisible()

  fireEvent.keyDown(document, { key: 'Escape' })
  await waitFor(() => expect(screen.queryByText('决策详情')).toBeNull())
  client.clear()
})
