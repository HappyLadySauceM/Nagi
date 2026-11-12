import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ReplayEvaluationsPanel } from '../replay-evaluations-panel'

const response = (data: unknown) =>
  new Response(JSON.stringify({ code: 200, message: 'success', data }), {
    headers: { 'Content-Type': 'application/json' },
  })

const run = {
  DecisionID: 7,
  JobID: 9,
  RunID: 'run-1',
  FlowVersion: 'intent-flow-v1',
  Provider: 'jev',
  Model: 'decision-model',
  RulesetVersion: 'scope:1@1',
  SnapshotStatus: 'complete',
  MissingFields: [],
  Summary: { Decision: 'reply', ReasonCode: 'private_normal', ReasonText: '私聊正常回复' },
  Nodes: [],
  Edges: [],
  Delivery: { Outcome: 'completed', Reason: '', Denied: 0, Items: [] },
  Scores: {},
  LatencyMS: 10,
  CreatedAt: '2026-11-12T08:00:00Z',
}

const summary = {
  ID: 1,
  DecisionID: 7,
  Mode: 'recalculate',
  Name: '本地重算 A',
  Changed: true,
  Summary: { Decision: 'silent', ReasonCode: '', ReasonText: '回复 → 沉默' },
  CreatedAt: '2026-11-12T08:02:00Z',
  Reproduction: { FullyReproduced: false, Reasons: ['prompt_current'] },
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

it('lists saved evaluations and compares two selected records', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn((input: string) => {
      const url = new URL(String(input), 'http://localhost')
      if (url.pathname === '/api/v1/intent/replay-evaluations') {
        return Promise.resolve(
          response({
            Items: [
              summary,
              {
                ...summary,
                ID: 2,
                Name: '本地重算 B',
                Summary: { Decision: 'reply', ReasonCode: '', ReasonText: '回复一致' },
                Changed: false,
                Reproduction: { FullyReproduced: true, Reasons: [] },
              },
            ],
            Limit: 20,
          }),
        )
      }
      if (url.pathname === '/api/v1/intent/replay-evaluations/1/comparison') {
        return Promise.resolve(
          response({
            EvaluationID: 1,
            Against: 'evaluation:2',
            Changed: true,
            Original: run,
            Replay: { ...run, Summary: { ...run.Summary, Decision: 'silent' } },
            Differences: [{ Field: 'Decision', Before: 'reply', After: 'silent' }],
            Reproduction: { FullyReproduced: false, Reasons: ['prompt_current'] },
          }),
        )
      }
      throw new Error(`Unexpected request ${url.pathname}`)
    }),
  )

  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <ReplayEvaluationsPanel decisionId={7} />
    </QueryClientProvider>,
  )

  expect(await screen.findByText('本地重算 A')).toBeVisible()
  expect(screen.getByText('本地重算 B')).toBeVisible()
  expect(screen.getAllByText('非完全复现')).toHaveLength(1)

  fireEvent.click(screen.getByRole('checkbox', { name: '选择评估 1' }))
  fireEvent.click(screen.getByRole('checkbox', { name: '选择评估 2' }))
  fireEvent.click(screen.getByRole('button', { name: '对比所选' }))

  expect(await screen.findByText('对比 #1 ↔ #2')).toBeVisible()
  expect(screen.getByText('存在差异')).toBeVisible()
  expect(screen.getByText('Decision')).toBeVisible()
  expect(screen.getByText('reply')).toBeVisible()
  expect(screen.getByText('silent')).toBeVisible()
  expect(screen.getByText(/非完全复现原因：prompt_current/)).toBeVisible()
  await waitFor(() => expect(screen.getByRole('button', { name: '对比所选' })).toBeEnabled())
  client.clear()
})
