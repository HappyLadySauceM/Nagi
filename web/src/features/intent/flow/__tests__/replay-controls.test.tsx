import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ReplayControls } from '../replay-controls'

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

const response = (data: unknown) =>
  new Response(JSON.stringify({ code: 200, message: 'success', data }), {
    headers: { 'Content-Type': 'application/json' },
  })

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

it('quotes model cost and requires confirmation before replaying', async () => {
  const replayBodies: unknown[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn((input: string, init?: RequestInit) => {
      const url = new URL(String(input), 'http://localhost')
      const method = init?.method ?? 'GET'
      if (url.pathname === '/api/v1/intent/replay-evaluations' && method === 'GET') {
        return Promise.resolve(response({ Items: [], Limit: 20 }))
      }
      if (url.pathname === '/api/v1/intent/decisions/7/replay-model/estimate' && method === 'POST') {
        return Promise.resolve(
          response({
            DecisionID: 7,
            Provider: 'jev',
            Model: 'decision-model',
            SampleCount: 1,
            EstimatedExternalRequests: 1,
            TokenEstimate: { Input: 100, Output: 40, Total: 140, Method: 'estimate' },
            Cost: { MayIncur: true, Known: false, Text: '费用未知' },
            ConfirmationToken: 'token-1',
            ExpiresAt: '2026-11-12T08:05:00Z',
            ParametersFingerprint: 'fingerprint-1',
          }),
        )
      }
      if (url.pathname === '/api/v1/intent/decisions/7/replay-model' && method === 'POST') {
        replayBodies.push(JSON.parse(String(init?.body)))
        return Promise.resolve(
          response({
            Run: { ...run, Replay: { Input: 'snapshot', Semantic: 'snapshot', Prompt: 'snapshot', Policy: 'snapshot', Rules: 'snapshot', Persisted: false } },
            Cost: { MayIncur: true, Known: false, Text: '费用未知' },
            EvaluationID: 42,
          }),
        )
      }
      throw new Error(`Unexpected request ${method} ${url.pathname}`)
    }),
  )

  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <ReplayControls decisionId={7} />
    </QueryClientProvider>,
  )

  expect(await screen.findByText('还没有保存的回放评估。')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: /重新调用模型/ }))

  await screen.findByText('重新调用模型？')
  const dialogText = await screen.findByRole('alertdialog').then((dialog) => dialog.textContent ?? '')
  expect(dialogText).toContain('模型：jev / decision-model')
  expect(dialogText).toContain('样本数：1')
  expect(dialogText).toContain('预计外部请求：1 次')
  expect(dialogText).toContain('费用：费用未知')
  expect(screen.queryByText(/免费/)).toBeNull()

  fireEvent.click(screen.getByRole('button', { name: '取消' }))
  await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
  expect(replayBodies).toHaveLength(0)

  fireEvent.click(screen.getByRole('button', { name: /重新调用模型/ }))
  await screen.findByText('重新调用模型？')
  fireEvent.click(screen.getByRole('button', { name: '确认调用' }))

  await waitFor(() => expect(replayBodies).toHaveLength(1))
  expect(replayBodies[0]).toEqual({ ConfirmationToken: 'token-1' })
  expect(await screen.findByText('已保存评估 #42')).toBeVisible()
  client.clear()
})
