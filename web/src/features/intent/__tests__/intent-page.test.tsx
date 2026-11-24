import type { ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { DecisionHistoryPage, IntentPage } from '../intent-page'

const fixture = vi.hoisted(() => ({ live: vi.fn(), account: vi.fn() }))

vi.mock('../use-live-decisions', () => ({ useLiveDecisions: fixture.live }))

vi.mock('@tanstack/react-router', () => ({
  Link: ({ to, children }: { to: string; children: ReactNode }) => <a href={to}>{children}</a>,
  useNavigate: () => vi.fn(),
}))

vi.mock('../../accounts/account-context', () => ({
  useAccountContext: fixture.account,
}))

vi.mock('../../settings/models', () => ({
  useModels: () => ({ data: undefined }),
  modelReady: () => false,
}))

vi.mock('../../../lib/api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api/client')>()
  return { ...actual, apiRequest: vi.fn(async () => ({ Total: 0, SemanticCalls: 0, SemanticSuccess: 0, Degraded: 0, Replies: 0, Silent: 0, AverageLatencyMS: 0 })) }
})

vi.mock('../flow/flow-workbench', () => ({ FlowWorkbench: () => <div>流程工作台</div> }))

vi.mock('../rules-page', () => ({
  RulesPage: ({ embedded }: { embedded?: boolean }) => <div>参与判断工作台{embedded ? '（内嵌）' : ''}</div>,
}))

afterEach(() => {
  cleanup()
  fixture.live.mockReset()
  fixture.account.mockReset()
})

function arrange(view: { rows: ReturnType<typeof row>[]; history: null; next: string } | null, error?: Error) {
  const reset = vi.fn()
  fixture.account.mockReturnValue({ selectedBotId: 1, dbConfigured: true })
  fixture.live.mockReturnValue({ view, error, paging: false, reset, older: vi.fn() })
  return reset
}

function row(id: number, day: number, decision = 'silent') {
  return {
    runId: `run-${id}`, decisionId: id, conversationId: `fixture-group-${id}`,
    startedAt: `2026-11-${day}T08:00:00Z`, state: id ? 'completed' : 'running', decision,
    reason: decision === 'silent' ? 'observe_only' : 'explicit_mention', events: [],
  }
}


it('merges rules and semantic prompts into one 参与判断 tab', async () => {
  arrange(null)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><IntentPage /></QueryClientProvider>)
  const tabs = screen.getAllByRole('tab')
  expect(tabs.map((tab) => tab.textContent)).toEqual(['运行概览', '参与判断', '识别配置'])
  expect(screen.queryByRole('tab', { name: '规则管理' })).toBeNull()
  expect(screen.queryByRole('tab', { name: '模拟验证' })).toBeNull()
  expect(screen.queryByRole('tab', { name: '意图提示词' })).toBeNull()
  fireEvent.click(screen.getByRole('tab', { name: '参与判断' }))
  expect(await screen.findByText('参与判断工作台（内嵌）')).toBeVisible()
  client.clear()
})

it('shows recent formal decisions newest first and keeps distinct loading, empty and retry states', async () => {
  arrange(null)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const page = () => <QueryClientProvider client={client}><IntentPage /></QueryClientProvider>
  const { rerender } = render(page())
  expect(screen.getByText('正在读取最近决策…')).toBeVisible()
  const reset = arrange(null, new Error('fixture temporarily unavailable'))
  rerender(page())
  const recent = screen.getByRole('region', { name: '最近决策' })
  expect(within(recent).getByRole('alert')).toHaveTextContent('暂无法读取最近决策')
  fireEvent.click(within(recent).getByRole('button', { name: '重试' }))
  expect(reset).toHaveBeenCalledOnce()
  arrange({ rows: [], history: null, next: '' })
  rerender(page())
  expect(within(recent).getByText('还没有决策记录')).toBeVisible()
  arrange({ rows: [row(1, 22), row(0, 24, ''), row(2, 23, 'reply')], history: null, next: '' })
  rerender(page())
  const rows = within(recent).getAllByRole('row')
  expect(rows).toHaveLength(3)
  expect(rows[1]).toHaveTextContent('fixture-group-2')
  expect(rows[1]).toHaveTextContent('明确 @ 或引用机器人')
  expect(rows[1]).toHaveTextContent('回复')
  expect(rows[1].querySelector('time')).toHaveAttribute('datetime', '2026-11-23T08:00:00Z')
  expect(rows[2]).toHaveTextContent('fixture-group-1')
  expect(within(recent).getByRole('link', { name: '查看全部' })).toHaveAttribute('href', '/decisions/history')
})

it.each([IntentPage, DecisionHistoryPage])('opens the selected drawer from a row and clears it when authorization is lost', async (Page) => {
  const first = row(1, 22)
  arrange({ rows: [first], history: null, next: '' })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const page = () => <QueryClientProvider client={client}><Page /></QueryClientProvider>
  const { rerender } = render(page())
  const button = screen.getByRole('button', { name: /查看决策 1/ })
  fireEvent.click(within(button.closest('tr')!).getByText('沉默'))
  expect(screen.getByRole('dialog')).toHaveTextContent('决策 #1 · fixture-group-1')
  arrange({ rows: [row(2, 24), first], history: null, next: '' })
  rerender(page())
  expect(screen.getByRole('dialog')).toHaveTextContent('决策 #1 · fixture-group-1')
  arrange({ rows: [], history: null, next: '' })
  rerender(page())
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(screen.queryByText(/fixture-group-1/)).toBeNull()
  client.clear()
})
