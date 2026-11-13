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

it('explains reply mode, cited context, matched rules and semantic scores in Chinese', async () => {
  const explainedRun = {
    ...run,
    Nodes: [
      {
        NodeID: 'policy',
        Version: 1,
        Status: 'executed',
        Branch: 'authorized',
        ReasonCode: '',
        Summary: '策略通过',
        Detail: { ReplyMode: 'intent_based', ScopeType: 'private', ScopeID: 8, MaxRepliesPerHour: 0 },
        Evidence: [],
        DurationMS: 0,
      },
      {
        NodeID: 'context',
        Version: 1,
        Status: 'executed',
        Branch: 'history_read',
        ReasonCode: '',
        Summary: '读取上下文',
        Detail: {
          HistorySource: 'database',
          Messages: [{ MessageID: 8, SenderID: 3, IsBot: false, Text: '你好呀，有什么想聊的？', Current: true }],
        },
        Evidence: [],
        DurationMS: 0,
      },
      {
        NodeID: 'rules',
        Version: 1,
        Status: 'executed',
        Branch: 'semantic',
        ReasonCode: '',
        Summary: '交给语义',
        Detail: {
          NeedSemantic: true,
          BuiltinRules: { closing: false },
          Evaluations: [
            {
              RulesetID: 4,
              Version: 2,
              Scope: 'specific',
              Priority: 1,
              Match: { reply_mode: 'intent_based', text_contains: '你好' },
              Action: { type: 'require_semantic', reason_code: 'ask_model' },
              Status: 'matched',
              ReasonCode: 'ask_model',
            },
          ],
        },
        Evidence: [],
        DurationMS: 0,
      },
      {
        NodeID: 'semantic',
        Version: 1,
        Status: 'executed',
        Branch: 'available',
        ReasonCode: '',
        Summary: '语义可用',
        Detail: {
          Called: true,
          Available: true,
          intent_category: 'question',
          Comparisons: [
            { Name: 'directed_to_bot', Value: 0.9, Threshold: 0.8, Satisfied: true },
            { Name: 'addressed_to_other', Value: 0.2, Threshold: 0.6, Satisfied: false },
          ],
        },
        Evidence: [],
        DurationMS: 4,
      },
    ],
  }
  const explainedFlow = {
    FlowVersion: 'intent-flow-v1',
    Nodes: [
      { NodeID: 'policy', Kind: 'policy', Title: '策略与权限', Description: '检查策略', StageIndex: 0, Version: 1 },
      { NodeID: 'context', Kind: 'context', Title: '上下文', Description: '读取上下文', StageIndex: 1, Version: 1 },
      { NodeID: 'rules', Kind: 'rules', Title: '规则', Description: '执行规则', StageIndex: 2, Version: 1 },
      { NodeID: 'semantic', Kind: 'semantic', Title: '语义判定', Description: '模型判断', StageIndex: 3, Version: 1 },
    ],
    Edges: [],
  }
  vi.stubGlobal(
    'fetch',
    vi.fn((input: string) => {
      const url = new URL(String(input), 'http://localhost')
      if (url.pathname === '/api/v1/intent/decisions/42') return Promise.resolve(response({ ...detail, Run: explainedRun }))
      if (url.pathname === '/api/v1/intent/flow') return Promise.resolve(response(explainedFlow))
      if (url.pathname === '/api/v1/intent/replay-evaluations') return Promise.resolve(response({ Items: [], Limit: 20 }))
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

  fireEvent.click(await screen.findByRole('button', { name: '策略与权限 实际执行' }))
  expect(await screen.findByText('由意图判定是否插话')).toBeVisible()
  expect(screen.getByText('没有被点名时，由语义判定决定要不要插话。')).toBeVisible()
  expect(screen.getByText('不限')).toBeVisible()

  fireEvent.keyDown(document, { key: 'Escape' })
  await waitFor(() => expect(screen.queryByText('由意图判定是否插话')).toBeNull())
  fireEvent.click(await screen.findByRole('button', { name: '上下文 实际执行' }))
  fireEvent.click(await screen.findByRole('button', { name: /查看原文/ }))
  expect(screen.getByText('你好呀，有什么想聊的？')).toBeVisible()
  expect(screen.getByText('收起')).toBeVisible()

  fireEvent.keyDown(document, { key: 'Escape' })
  await waitFor(() => expect(screen.queryByText('查看原文')).toBeNull())
  fireEvent.click(await screen.findByRole('button', { name: '规则 实际执行' }))
  expect(await screen.findByText('没有明确指向，交给语义判定决定要不要插话')).toBeVisible()
  expect(screen.getByText('告别或结束时保持沉默')).toBeVisible()
  expect(screen.getByText('关闭')).toBeVisible()
  expect(screen.getByText('回复模式是「由意图判定是否插话」，正文包含「你好」')).toBeVisible()
  expect(screen.getByText('交给语义判定')).toBeVisible()
  expect(screen.getByText('命中')).toBeVisible()

  fireEvent.keyDown(document, { key: 'Escape' })
  await waitFor(() => expect(screen.queryByText('交给语义判定')).toBeNull())
  fireEvent.click(await screen.findByRole('button', { name: '语义判定 实际执行' }))
  expect(await screen.findByText('求助或提问')).toBeVisible()
  expect(screen.getByText('是否在跟机器人说话')).toBeVisible()
  expect(screen.getByText('成立')).toBeVisible()
  expect(screen.getByText('是否主要在跟另一位真人说话')).toBeVisible()
  expect(screen.getByText('不抑制')).toBeVisible()
  expect(screen.getByText(/这是私聊，聊天功能是「求助或提问」，不是告别，所以仍然回复/)).toBeVisible()
  client.clear()
})

it('shows the blocking hard gate and does not call a closing private chat a non-goodbye', async () => {
  const blockedRun = {
    ...run,
    Summary: { Decision: 'reply', ReasonCode: 'private_normal', ReasonText: '私聊正常回复' },
    Nodes: [
      {
        NodeID: 'rules',
        Version: 1,
        Status: 'executed',
        Branch: 'blocked',
        ReasonCode: 'cooldown',
        Summary: '规则拦截',
        Detail: { Blocked: true, BuiltinRules: {} },
        Evidence: [],
        DurationMS: 0,
      },
      {
        NodeID: 'semantic',
        Version: 1,
        Status: 'executed',
        Branch: 'available',
        ReasonCode: '',
        Summary: '语义可用',
        Detail: { Called: true, Available: true, intent_category: 'closing', Comparisons: [] },
        Evidence: [],
        DurationMS: 1,
      },
    ],
  }
  const blockedFlow = {
    FlowVersion: 'intent-flow-v1',
    Nodes: [
      { NodeID: 'rules', Kind: 'rules', Title: '规则', Description: '执行规则', StageIndex: 0, Version: 1 },
      { NodeID: 'semantic', Kind: 'semantic', Title: '语义判定', Description: '模型判断', StageIndex: 1, Version: 1 },
    ],
    Edges: [],
  }
  vi.stubGlobal(
    'fetch',
    vi.fn((input: string) => {
      const url = new URL(String(input), 'http://localhost')
      if (url.pathname === '/api/v1/intent/decisions/42') return Promise.resolve(response({ ...detail, Run: blockedRun }))
      if (url.pathname === '/api/v1/intent/flow') return Promise.resolve(response(blockedFlow))
      if (url.pathname === '/api/v1/intent/replay-evaluations') return Promise.resolve(response({ Items: [], Limit: 20 }))
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
  fireEvent.click(await screen.findByRole('button', { name: '规则 实际执行' }))
  expect(await screen.findByText('未达到最小回复间隔')).toBeVisible()
  expect(screen.getByText('还在冷却，直接沉默')).toBeVisible()
  expect(screen.getByText('拦截')).toBeVisible()
  expect(screen.getAllByText('未执行').length).toBeGreaterThan(0)

  fireEvent.keyDown(document, { key: 'Escape' })
  await waitFor(() => expect(screen.queryByText('还在冷却，直接沉默')).toBeNull())
  fireEvent.click(await screen.findByRole('button', { name: '语义判定 实际执行' }))
  expect(await screen.findByText(/聊天功能是「告别或结束」，告别时保持沉默已关闭，所以仍然回复/)).toBeVisible()
  expect(screen.queryByText(/不是告别/)).toBeNull()
  client.clear()
})
