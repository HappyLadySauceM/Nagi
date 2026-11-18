import type { ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { IntentConfig } from '../config'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ to, children }: { to: string; children: ReactNode }) => <a href={to}>{children}</a>,
}))

vi.mock('../edit-guard', () => ({
  useEditGuard: () => undefined,
}))

vi.mock('../../accounts/account-context', () => ({
  useAccountContext: () => ({ selectedBotId: 998001 }),
}))

vi.mock('../../../lib/api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api/client')>()
  return { ...actual, apiRequest: vi.fn() }
})

const { apiRequest } = await import('../../../lib/api/client')
const mockedRequest = vi.mocked(apiRequest)

function settingsView(overrides: Record<string, unknown> = {}) {
  const settings = {
    Role: { BotName: 'Nagi' },
    Context: { HistoryLimit: 30 },
    AI: {
      Enabled: true,
      BaseURL: 'https://llm.example/v1',
      Model: 'test-model',
      MaxOutputTokens: 400,
      GenerationTimeoutSeconds: 25,
    },
    Intent: {
      Enabled: true,
      Provider: 'jev',
      Endpoint: 'https://api.example/v1/systemone',
      Model: 'jev-latest',
      TimeoutSeconds: 3,
      ContinuationThreshold: 0.8,
      InterjectionThreshold: 0.95,
      AddressedOtherMax: 0.6,
    },
    Agent: {
      MaxSteps: 6,
      MaxToolCalls: 5,
      MaxRunDurationSeconds: 30,
      Sending: { MaxMessagesPerRun: 0, MaxCharsPerMessage: 4500, MaxTotalCharsPerRun: 0 },
    },
    Messages: { RetentionDays: 30 },
    Tools: {
      TimeoutSeconds: 5,
      MaxResultBytes: 8192,
      PermissionDefaults: {},
    },
  }
  return {
    Settings: settings,
    Defaults: settings,
    Version: 1,
    UpdatedAt: '2026-11-16T00:00:00Z',
    UpdatedBy: 'admin',
    Persisted: true,
    Credentials: { AI: true, Intent: true },
    Providers: ['builtin.reply'],
    ...overrides,
  }
}

const modelsView = {
  Catalog: {
    Profiles: [
      {
        ID: 'intent-model',
        Name: 'Intent Model',
        Usage: 'intent',
        Protocol: 'jev',
        BaseURL: 'https://api.example/v1/systemone',
        Model: 'jev-latest',
        Images: false,
        Tools: false,
        Voice: '',
        MaxOutputTokens: 0,
        TimeoutSeconds: 3,
        Version: 1,
        VerifiedVersion: 1,
        VerifiedAt: '2026-11-16T00:00:00Z',
        HasCredential: true,
      },
    ],
    Defaults: { intent: 'intent-model' },
    Selections: {},
    HasBilibiliCredential: false,
  },
  Version: 1,
}

const effectiveRules = {
  BuiltinRules: [
    { RuleID: 'closing', Order: 10, Title: '告别结束', Condition: 'intent=closing', Action: 'silent', Editable: true, Source: 'builtin', Enabled: true, Applicable: true, Status: 'active', Reason: '' },
    { RuleID: 'addressed_to_other', Order: 20, Title: '对他人说话优先抑制', Condition: 'addressed_to_other >= AddressedOtherMax', Action: 'silent', Editable: true, Source: 'builtin', Enabled: false, Applicable: false, Status: 'not_applicable', Reason: 'disabled' },
    { RuleID: 'continuation', Order: 30, Title: '延续机器人话题', Condition: 'continues_bot_thread >= Continuation', Action: 'reply', Editable: true, Source: 'builtin', Enabled: true, Applicable: true, Status: 'active', Reason: '' },
    { RuleID: 'interjection', Order: 40, Title: '自然插话', Condition: 'reply_mode=intent_based && appropriate_to_join >= Interjection', Action: 'reply', Editable: true, Source: 'builtin', Enabled: false, Applicable: false, Status: 'not_applicable', Reason: 'disabled' },
    { RuleID: 'directed_to_bot', Order: 50, Title: '指向机器人', Condition: 'directed_to_bot >= Continuation', Action: 'reply', Editable: true, Source: 'builtin', Enabled: true, Applicable: true, Status: 'active', Reason: '' },
  ],
  FixedConstraints: [],
  ActiveRuleset: null,
  Version: 'builtin-v1',
  BuiltinRulesVersion: 'builtin-v1',
  Source: 'builtin',
}

function renderConfig() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <IntentConfig />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  mockedRequest.mockReset()
  vi.stubGlobal('ResizeObserver', class {
    observe = vi.fn()
    unobserve = vi.fn()
    disconnect = vi.fn()
  })
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe('IntentConfig', () => {
  it('shows the pipeline and shared context sections', async () => {
    mockedRequest.mockImplementation((path: string) => {
      if (path === '/api/v1/settings') return Promise.resolve(settingsView())
      if (path.startsWith('/api/v1/intent/rules/effective')) return Promise.resolve(effectiveRules)
      throw new Error(`unexpected request: ${path}`)
    })
    renderConfig()

    expect(await screen.findByRole('switch', { name: '回复决策流水线' })).toBeVisible()
    expect(screen.getByText('会话上下文')).toBeVisible()
    expect(screen.queryByText('群聊高级设置')).toBeNull()
    expect(screen.queryByText('角色设置')).toBeNull()
    expect(screen.queryByText('意图模型')).toBeNull()
    expect(screen.queryByLabelText('机器人自称')).toBeNull()
    expect(screen.getByLabelText('历史条数上限')).toHaveValue(30)
    expect(screen.getByText(/默认 30，范围 1–200/)).toBeVisible()
    expect(screen.queryByLabelText('接口地址')).toBeNull()
    expect(screen.queryByLabelText('模型名称')).toBeNull()
    expect(screen.queryByLabelText('请求超时')).toBeNull()
  })

  it('saves context and keeps the stored thresholds without a Role section', async () => {
    let patchBody: unknown
    mockedRequest.mockImplementation((path: string, options?: { method?: string; body?: unknown }) => {
      if (path === '/api/v1/settings' && options?.method === 'PATCH') {
        patchBody = options.body
        return Promise.resolve(settingsView({ Version: 2 }))
      }
      if (path === '/api/v1/settings') return Promise.resolve(settingsView())
      if (path.startsWith('/api/v1/intent/rules/effective')) return Promise.resolve(effectiveRules)
      throw new Error(`unexpected request: ${path}`)
    })
    renderConfig()

    fireEvent.change(await screen.findByLabelText('历史条数上限'), { target: { value: '40' } })
    fireEvent.click(screen.getByRole('button', { name: '保存配置' }))

    await waitFor(() => expect(patchBody).toBeDefined())
    const body = patchBody as { Version: number; Sections: Record<string, Record<string, unknown>> }
    expect(body.Version).toBe(1)
    expect(body.Sections).not.toHaveProperty('Role')
    expect(body.Sections.Context).toEqual({ HistoryLimit: 40 })
    expect(body.Sections.Intent).toMatchObject({
      Enabled: true,
      ContinuationThreshold: 0.8,
      InterjectionThreshold: 0.95,
      AddressedOtherMax: 0.6,
    })
    expect(body.Sections.Intent).not.toHaveProperty('Provider')
    expect(body.Sections.Intent).not.toHaveProperty('Endpoint')
    expect(body.Sections.Intent).not.toHaveProperty('Model')
    expect(body.Sections.Intent).not.toHaveProperty('TimeoutSeconds')
    expect(await screen.findByText('配置已保存')).toBeVisible()
  })

  it('blocks saving when the history limit is cleared', async () => {
    mockedRequest.mockImplementation((path: string) => {
      if (path === '/api/v1/settings') return Promise.resolve(settingsView())
      if (path === '/api/v1/models') return Promise.resolve(modelsView)
      if (path.startsWith('/api/v1/intent/rules/effective')) return Promise.resolve(effectiveRules)
      throw new Error(`unexpected request: ${path}`)
    })
    renderConfig()

    fireEvent.change(await screen.findByLabelText('历史条数上限'), { target: { value: '' } })
    expect(await screen.findByText('请填写 1–200 的整数')).toBeVisible()
    expect(screen.getByRole('button', { name: '保存配置' })).toBeDisabled()
    expect(mockedRequest.mock.calls.some(([, options]) => options?.method === 'PATCH')).toBe(false)
  })
})
