import type { ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { IntentPage } from '../intent-page'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ to, children }: { to: string; children: ReactNode }) => <a href={to}>{children}</a>,
  useNavigate: () => vi.fn(),
}))

vi.mock('../../accounts/account-context', () => ({
  useAccountContext: () => ({ selectedBotId: 1, dbConfigured: true }),
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

afterEach(() => { cleanup() })

it('merges rules and semantic prompts into one 参与判断 tab', async () => {
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
