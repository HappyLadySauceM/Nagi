import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiRequest } from '../../../lib/api/client'
import { BuiltinRulesPanel } from '../components/builtin-rules-panel'

vi.mock('../../../lib/api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api/client')>()
  return { ...actual, apiRequest: vi.fn() }
})

const mockedRequest = vi.mocked(apiRequest)

const catalog = {
  Version: 'builtin-v1',
  Source: 'builtin',
  FixedConstraints: [
    { ConstraintID: 'tool_permissions', Order: 50, Title: '工具权限', Condition: 'tool permission not granted', Action: 'deny_tool', ReasonCode: 'authorization_revoked', Editable: false, Source: 'server' },
  ],
  Rules: [
    { RuleID: 'closing', Order: 10, Title: '告别结束', Condition: 'intent=closing', Action: 'silent', Editable: true, Source: 'builtin' },
    { RuleID: 'addressed_to_other', Order: 20, Title: '对他人说话优先抑制', Condition: 'addressed_to_other >= AddressedOtherMax', Action: 'silent', Editable: true, Source: 'builtin' },
    { RuleID: 'continuation', Order: 30, Title: '延续机器人话题', Condition: 'continues_bot_thread >= Continuation', Action: 'reply', Editable: true, Source: 'builtin' },
    { RuleID: 'interjection', Order: 40, Title: '自然插话', Condition: 'reply_mode=intent_based && appropriate_to_join >= Interjection', Action: 'reply', Editable: true, Source: 'builtin' },
    { RuleID: 'directed_to_bot', Order: 50, Title: '指向机器人', Condition: 'directed_to_bot >= Continuation', Action: 'reply', Editable: true, Source: 'builtin' },
  ],
}

const effective = {
  BuiltinRules: [
    { RuleID: 'closing', Order: 10, Title: '告别结束', Condition: 'intent=closing', Action: 'silent', Editable: true, Source: 'builtin', Enabled: false, Applicable: true, Status: 'not_applicable', Reason: 'disabled' },
    { RuleID: 'addressed_to_other', Order: 20, Title: '对他人说话优先抑制', Condition: 'addressed_to_other >= AddressedOtherMax', Action: 'silent', Editable: true, Source: 'builtin', Enabled: true, Applicable: true, Status: 'active', Reason: '' },
    { RuleID: 'continuation', Order: 30, Title: '延续机器人话题', Condition: 'continues_bot_thread >= Continuation', Action: 'reply', Editable: true, Source: 'builtin', Enabled: true, Applicable: true, Status: 'active', Reason: '' },
    { RuleID: 'interjection', Order: 40, Title: '自然插话', Condition: 'reply_mode=intent_based && appropriate_to_join >= Interjection', Action: 'reply', Editable: true, Source: 'builtin', Enabled: true, Applicable: true, Status: 'active', Reason: '' },
    { RuleID: 'directed_to_bot', Order: 50, Title: '指向机器人', Condition: 'directed_to_bot >= Continuation', Action: 'reply', Editable: true, Source: 'builtin', Enabled: true, Applicable: true, Status: 'active', Reason: '' },
  ],
  FixedConstraints: [
    { ConstraintID: 'tool_permissions', Order: 50, Title: '工具权限', Condition: 'tool permission not granted', Action: 'deny_tool', ReasonCode: 'authorization_revoked', Editable: false, Source: 'server', Applicable: true, Status: 'active', Reason: '' },
  ],
  ActiveRuleset: null,
  Version: 'builtin-v1',
  BuiltinRulesVersion: 'builtin-v1',
  Source: 'builtin',
}

function renderPanel() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <BuiltinRulesPanel botId={998001} scopeType="group" scopeId={700} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  mockedRequest.mockReset()
  mockedRequest.mockImplementation((path: string) => {
    if (path === '/api/v1/intent/builtin-rules') return Promise.resolve(catalog)
    if (path.startsWith('/api/v1/intent/rules/effective')) return Promise.resolve(effective)
    throw new Error(`unexpected request: ${path}`)
  })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('BuiltinRulesPanel', () => {
  it('renders the backend catalog and marks a disabled rule as 不适用', async () => {
    renderPanel()

    expect(await screen.findByText('告别结束')).toBeVisible()
    expect(screen.getByText('对他人说话优先抑制')).toBeVisible()
    expect(screen.getByText('延续机器人话题')).toBeVisible()
    expect(screen.getByText('自然插话')).toBeVisible()
    expect(screen.getByText('指向机器人')).toBeVisible()
    expect(screen.getByText('已关闭')).toBeVisible()
    expect(screen.getAllByText('不适用').length).toBeGreaterThanOrEqual(2)
    expect(screen.getByText('无（使用内置规则）')).toBeVisible()
    expect(screen.getByText('工具权限')).toBeVisible()
    expect(screen.getByText(/图与执行共用后端同一份规则定义/)).toBeVisible()
  })
})
