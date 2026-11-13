import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { FlowWorkbench } from '../flow-workbench'

vi.mock('../../../accounts/account-context', () => ({
  useAccountContext: () => ({ selectedBotId: 1, dbConfigured: true }),
}))

const response = (data: unknown) =>
  new Response(JSON.stringify({ code: 200, message: 'success', data }), {
    headers: { 'Content-Type': 'application/json' },
  })

const entry = {
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
}

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
  ],
  Edges: [{ EdgeID: 'e1', Status: 'taken' }],
  Delivery: { Outcome: 'completed', Reason: '', Denied: 0, Items: [] },
  Scores: {},
  LatencyMS: 10,
  CreatedAt: '2026-11-12T08:00:00Z',
}

const detail = { ...entry, Scores: {}, Steps: [], Run: run }

const flow = {
  FlowVersion: 'intent-flow-v1',
  Nodes: [{ NodeID: 'policy', Kind: 'policy', Title: '策略与权限', Description: '检查策略', StageIndex: 0, Version: 1 }],
  Edges: [],
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

it('removes inline details and opens the drawer from a row via click, Enter and Space', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn((input: string) => {
      const url = new URL(String(input), 'http://localhost')
      if (url.pathname === '/api/v1/intent/decisions' && url.searchParams.get('limit') === '7') {
        return Promise.resolve(response({ Items: [entry], Limit: 7 }))
      }
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
      <FlowWorkbench botId={1} />
    </QueryClientProvider>,
  )

  expect(await screen.findByText('conv-42')).toBeVisible()
  expect(screen.queryByText('决策详情')).toBeNull()
  expect(screen.queryByText('最终判定')).toBeNull()

  const row = screen.getByText('conv-42').closest('tr')
  expect(row).not.toBeNull()
  fireEvent.click(row!)
  expect(await screen.findByText('决策详情')).toBeVisible()

  fireEvent.click(screen.getByRole('button', { name: '关闭详情' }))
  await waitFor(() => expect(screen.queryByText('决策详情')).toBeNull())

  fireEvent.keyDown(row!, { key: 'Enter' })
  expect(await screen.findByText('决策详情')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: '关闭详情' }))
  await waitFor(() => expect(screen.queryByText('决策详情')).toBeNull())

  fireEvent.keyDown(row!, { key: ' ' })
  expect(await screen.findByText('决策详情')).toBeVisible()
  client.clear()
})
