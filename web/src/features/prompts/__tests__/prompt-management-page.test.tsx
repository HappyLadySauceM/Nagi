import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiRequest } from '../../../lib/api/client'
import { PromptManagementPage } from '../prompt-management-page'

vi.mock('../../../lib/api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api/client')>()
  return { ...actual, apiRequest: vi.fn() }
})

vi.mock('../../accounts/account-context', () => ({
  useAccountContext: () => ({
    selectedBotId: 998001,
    selectedAccount: { nickname: '测试账号' },
  }),
}))

vi.mock('../../settings/queries', () => ({
  useSystemSettings: () => ({
    data: {
      settings: {
        role: {
          botName: 'Nagi',
          variables: [{ name: 'user_name', value: '小明' }],
        },
      },
      version: 1,
    },
    isLoading: false,
    isError: false,
    error: null,
  }),
}))

const mockedRequest = vi.mocked(apiRequest)

function settingsView(role: { BotName: string; Variables: { Name: string; Value: string }[] } = {
  BotName: 'Nagi',
  Variables: [{ Name: 'user_name', Value: '小明' }],
}) {
  const settings = {
    Role: role,
    Context: { HistoryLimit: 30 },
    AI: { Enabled: true, BaseURL: '', Model: '', MaxOutputTokens: 1024, GenerationTimeoutSeconds: 30 },
    Intent: {
      Enabled: true,
      Provider: 'jev',
      Endpoint: '',
      Model: '',
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
    Tools: { TimeoutSeconds: 5, MaxResultBytes: 8192, PermissionDefaults: {} },
  }
  return {
    Settings: settings,
    Defaults: settings,
    Version: 1,
    UpdatedAt: '2026-11-16T00:00:00Z',
    UpdatedBy: 'admin',
    Persisted: true,
    Credentials: { AI: true, Intent: true },
    Providers: [],
  }
}

const catalog = {
  Items: [
    {
      PromptID: 'chat.reply',
      Kind: 'chat_reply',
      Name: '聊天回复',
      Provider: 'openai',
      BuiltinVersion: 1,
      Layers: [
        {
          BotID: 998001,
          ScopeType: 'account',
          ScopeID: 0,
          ActiveVersion: 2,
          ActiveVersionID: 12,
          ActiveSource: 'local',
          Status: 'published',
          UpdatedAt: '2026-11-15T08:01:00Z',
        },
      ],
    },
    {
      PromptID: 'intent.participation',
      Kind: 'intent_participation',
      Name: '意图判断',
      Provider: 'jev',
      BuiltinVersion: 1,
      Layers: [],
    },
  ],
}

function version(overrides: Record<string, unknown>) {
  return {
    ID: 12,
    PromptID: 'chat.reply',
    Kind: 'chat_reply',
    Name: '聊天回复',
    Provider: 'openai',
    BotID: 998001,
    ScopeType: 'account',
    ScopeID: 0,
    Version: 2,
    Status: 'published',
    Source: 'local',
    SourceRef: '',
    Hash: 'sha256:active',
    Note: '',
    Content: { Provider: 'openai', OpenAI: { SystemPrompt: '你好 {{bot_name}}' } },
    CreatedAt: '2026-11-15T08:00:00Z',
    PublishedAt: '2026-11-15T08:01:00Z',
    ArchivedAt: null,
    ...overrides,
  }
}

const detail = {
  PromptID: 'chat.reply',
  Kind: 'chat_reply',
  Provider: 'openai',
  Builtin: {
    Version: 1,
    Status: 'builtin',
    Source: 'builtin',
    Hash: 'sha256:builtin',
    Content: { Provider: 'openai', OpenAI: { SystemPrompt: '内置默认' } },
  },
  Active: version({}),
  Versions: [
    version({}),
    version({ ID: 13, Version: 3, Status: 'draft', Hash: 'sha256:draft', PublishedAt: null }),
    version({
      ID: 10,
      Version: 1,
      Status: 'archived',
      Hash: 'sha256:archived',
      PublishedAt: '2026-11-01T08:00:00Z',
      ArchivedAt: '2026-11-02T08:00:00Z',
    }),
  ],
}

const intentDetail = {
  PromptID: 'intent.participation',
  Kind: 'intent_participation',
  Provider: 'jev',
  Builtin: {
    Version: 1,
    Status: 'builtin',
    Source: 'builtin',
    Hash: 'sha256:intent',
    Content: { Provider: 'jev', SystemOne: { Questions: {} } },
  },
  Active: null,
  Versions: [],
}

const effective = {
  PromptID: 'chat.reply',
  Kind: 'chat_reply',
  Provider: 'openai',
  BotID: 998001,
  Target: { Type: 'group', ID: 700 },
  Layers: [
    { Layer: 'account_default', VersionID: 12, Version: 2, Source: 'local', Hash: 'sha256:a' },
    { Layer: 'server_constraints', Editable: false, Hash: 'sha256:server', Summary: '工具权限、发送权限、JSON 结构由服务端固定' },
  ],
  Final: { Content: { Provider: 'openai', OpenAI: { SystemPrompt: '最终 System' } }, Variables: { bot_name: 'Nagi', scope_type: 'group' } },
  Hash: 'sha256:effective',
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <PromptManagementPage />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  mockedRequest.mockReset()
  mockedRequest.mockImplementation((path: string, options?: { method?: string }) => {
    if (path === '/api/v1/settings' && options?.method === 'PATCH') return Promise.resolve(settingsView())
    if (path.startsWith('/api/v1/prompts?')) return Promise.resolve(catalog)
    if (path.endsWith('/versions') && options?.method === 'POST') {
      return Promise.resolve(version({ ID: 14, Version: 4, Status: 'draft', PublishedAt: null }))
    }
    if (/\/versions\/\d+$/.test(path) && options?.method === 'PATCH') {
      return Promise.resolve(version({ ID: 13, Version: 3, Status: 'draft' }))
    }
    if (path.includes('/versions/13/publish')) {
      return Promise.resolve({ Version: version({ Status: 'published' }), ArchivedVersionID: 12 })
    }
    if (path.includes('/rollback')) {
      return Promise.resolve({ Version: version({ ID: 10, Status: 'published' }), ArchivedVersionID: 12 })
    }
    if (path === '/api/v1/prompts/versions/13?bot_id=998001') {
      return Promise.resolve({})
    }
    if (path.includes('/restore-builtin')) {
      return Promise.resolve({
        PromptID: 'chat.reply',
        ScopeType: 'account',
        ScopeID: 0,
        Provider: 'openai',
        Active: null,
        Resolved: { Source: 'builtin', Version: 1, Hash: 'sha256:builtin' },
        ArchivedVersionID: 12,
      })
    }
    if (path.includes('/effective')) return Promise.resolve(effective)
    if (path.match(/\/api\/v1\/prompts\/chat\.reply\?/)) {
      return Promise.resolve(path.includes('scope_type=group') ? { ...detail, Active: null, Versions: [] } : detail)
    }
    if (path.match(/\/api\/v1\/prompts\/intent\.participation\?/)) {
      return Promise.resolve(intentDetail)
    }
    throw new Error(`unexpected request: ${path}`)
  })
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

describe('PromptManagementPage', () => {
  it('shows the three scopes, kind segments and variable chips without role settings or a Provider row', async () => {
    renderPage()
    expect(await screen.findByRole('button', { name: /账号默认/ })).toBeVisible()
    expect(screen.getByRole('button', { name: /群聊类型补充/ })).toBeVisible()
    expect(screen.getByRole('button', { name: /私聊类型补充/ })).toBeVisible()
    expect(screen.getByRole('button', { name: /聊天回复/ })).toBeVisible()
    expect(screen.getByRole('button', { name: /记忆抽取/ })).toBeVisible()
    expect(screen.queryByText('角色设置')).toBeNull()
    expect(screen.queryByText(/Provider/)).toBeNull()
    expect(screen.queryByText(/三层 Prompt/)).toBeNull()
    expect(await screen.findByText('{{bot_name}}')).toBeVisible()
    expect(screen.getByText('{{scope_type}}')).toBeVisible()
  })

  it('edits an existing draft inline and saves it through the update endpoint', async () => {
    renderPage()
    expect(await screen.findByRole('button', { name: '保存草稿' })).toBeVisible()
    expect(screen.getByRole('button', { name: '发布' })).toBeVisible()
    expect(screen.getByRole('button', { name: '删除草稿' })).toBeVisible()

    const textarea = screen.getByLabelText('System Prompt')
    fireEvent.change(textarea, { target: { value: '新内容 {{bot_name}}' } })
    fireEvent.click(screen.getByRole('button', { name: '保存草稿' }))

    await waitFor(() =>
      expect(mockedRequest).toHaveBeenCalledWith(
        '/api/v1/prompts/versions/13',
        expect.objectContaining({
          method: 'PATCH',
          body: expect.objectContaining({
            content: expect.objectContaining({
              OpenAI: expect.objectContaining({ SystemPrompt: '新内容 {{bot_name}}' }),
            }),
          }),
        }),
      ),
    )
  })

  it('publishes the inline draft after confirmation', async () => {
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: '发布' }))
    fireEvent.click(await screen.findByRole('button', { name: '确认发布' }))
    await waitFor(() =>
      expect(mockedRequest).toHaveBeenCalledWith(
        '/api/v1/prompts/versions/13/publish',
        expect.objectContaining({ method: 'POST' }),
      ),
    )
  })

  it('opens the version history drawer and deletes a draft from it', async () => {
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: /历史版本/ }))
    const drawer = await screen.findByRole('dialog')
    expect(within(drawer).getByText('历史版本')).toBeVisible()
    fireEvent.click(within(drawer).getByRole('button', { name: '删除 Prompt v3' }))
    fireEvent.click(await screen.findByRole('button', { name: '确认删除' }))
    await waitFor(() =>
      expect(mockedRequest).toHaveBeenCalledWith(
        '/api/v1/prompts/versions/13?bot_id=998001',
        expect.objectContaining({ method: 'DELETE' }),
      ),
    )
  })

  it('opens the layered result drawer and queries the effective prompt', async () => {
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: /分层结果/ }))
    expect(await screen.findByRole('dialog')).toBeVisible()
    fireEvent.change(screen.getByLabelText('生效目标 ID'), { target: { value: '700' } })
    fireEvent.click(screen.getByRole('button', { name: '查看分层结果' }))
    expect(await screen.findByText('最终内容')).toBeVisible()
    expect(screen.getByText(/server_constraints/)).toBeVisible()
    expect(
      mockedRequest.mock.calls.some(
        ([path]) => String(path).includes('/effective') && String(path).includes('target_id=700'),
      ),
    ).toBe(true)
  })

  it('creates an empty supplement draft from a supplement layer', async () => {
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: /群聊类型补充/ }))
    expect(await screen.findByText('空补充')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: '新建草稿' }))
    await waitFor(() =>
      expect(mockedRequest).toHaveBeenCalledWith(
        '/api/v1/prompts/chat.reply/versions',
        expect.objectContaining({
          method: 'POST',
          body: expect.objectContaining({ base: 'empty', scope_type: 'group' }),
        }),
      ),
    )
  })

  it('renders the intent group without a kind switch or knowledge preview', async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <PromptManagementPage kinds={['intent_participation']} initialKind="intent_participation" />
      </QueryClientProvider>,
    )
    expect(await screen.findByRole('button', { name: /历史版本/ })).toBeVisible()
    expect(screen.queryByRole('button', { name: /聊天回复/ })).toBeNull()
    expect(screen.queryByRole('button', { name: /记忆抽取/ })).toBeNull()
    expect(screen.queryByRole('button', { name: /知识上下文预览/ })).toBeNull()
    expect(screen.queryByText(/Provider/)).toBeNull()
  })

  it('renders runtime variable chips and drops the legacy bot-name input', async () => {
    renderPage()
    expect(await screen.findByText('{{bot_name}}')).toBeVisible()
    for (const token of ['{{scope_type}}', '{{target_id}}', '{{sender_id}}', '{{bot_id}}']) {
      expect(screen.getByText(token)).toBeVisible()
    }
    expect(screen.queryByLabelText('机器人名称')).toBeNull()
    expect(screen.queryByText(/由运行时决定，只读/)).toBeNull()
  })

  it('opens a runtime chip to insert its token without allowing value edits', async () => {
    renderPage()
    const textarea = (await screen.findByLabelText('System Prompt')) as HTMLTextAreaElement
    fireEvent.click(await screen.findByRole('button', { name: '变量 {{target_id}}' }))
    expect(await screen.findByText('运行时：会话号（群号 / QQ）')).toBeVisible()
    expect(screen.queryByLabelText(/的值$/)).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: '插入 {{target_id}}' }))
    await waitFor(() => expect(textarea.value).toContain('{{target_id}}'))
  })

  it('edits the built-in bot_name chip and persists it through the settings PATCH', async () => {
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: '变量 {{bot_name}}' }))
    const input = await screen.findByLabelText('{{bot_name}} 的值')
    fireEvent.change(input, { target: { value: '新名字' } })
    fireEvent.click(screen.getByRole('button', { name: '保存变量' }))
    await waitFor(() =>
      expect(mockedRequest).toHaveBeenCalledWith(
        '/api/v1/settings',
        expect.objectContaining({
          method: 'PATCH',
          body: expect.objectContaining({
            Version: 1,
            Sections: expect.objectContaining({
              Role: expect.objectContaining({ BotName: '新名字' }),
            }),
          }),
        }),
      ),
    )
  })

  it('edits and deletes a custom variable through its chip', async () => {
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: '变量 {{user_name}}' }))
    const nameInput = await screen.findByLabelText('变量名称 1')
    fireEvent.change(nameInput, { target: { value: 'nickname' } })
    expect(screen.getByText('{{nickname}}')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: '删除变量 nickname' }))
    await waitFor(() => expect(screen.queryByText('{{nickname}}')).toBeNull())
  })
})
