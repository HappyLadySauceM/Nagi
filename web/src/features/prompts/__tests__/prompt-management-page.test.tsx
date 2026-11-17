import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
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

const mockedRequest = vi.mocked(apiRequest)

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
      Layers: [
        {
          BotID: 998001,
          ScopeType: 'account',
          ScopeID: 0,
          ActiveVersion: 3,
          ActiveVersionID: 20,
          ActiveSource: 'local',
          Status: 'published',
          UpdatedAt: '2026-11-15T08:02:00Z',
        },
      ],
    },
    {
      PromptID: 'intent.participation',
      Kind: 'intent_participation',
      Name: '意图判断',
      Provider: 'openai',
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
      ArchivedAt: null,
    }),
  ],
}

const jevQuestions = {
  directed_to_bot: { Type: 'noul', Instructions: '指向机器人？' },
  continues_bot_thread: { Type: 'noul', Instructions: '续聊？' },
  addressed_to_other: { Type: 'noul', Instructions: '对他人？' },
  appropriate_to_join: { Type: 'noul', Instructions: '插话？' },
  intent: { Type: 'choice', Instructions: '功能？', Criteria: { question: '提问', chat: '闲聊', command: '动作', closing: '告别', other: '其它' } },
}

function intentDetail(provider: string) {
  const content =
    provider === 'jev'
      ? { Provider: 'jev', SystemOne: { Questions: jevQuestions } }
      : { Provider: 'openai', OpenAI: { SystemPrompt: '判定器', UserTemplate: '{{state}}\n{{question}}', ResponseShape: 'single-letter' } }
  return {
    PromptID: 'intent.participation',
    Kind: 'intent_participation',
    Provider: provider,
    Builtin: { Version: 1, Status: 'builtin', Source: 'builtin', Hash: 'sha256:intent-builtin', Content: content },
    Active: {
      ...version({}),
      ID: 20,
      PromptID: 'intent.participation',
      Kind: 'intent_participation',
      Provider: provider,
      Version: 3,
      Content: content,
    },
    Versions: [],
  }
}

const effective = {
  PromptID: 'chat.reply',
  Kind: 'chat_reply',
  Provider: 'openai',
  BotID: 998001,
  Target: { Type: 'group', ID: 700 },
  Layers: [
    { Layer: 'account_default', VersionID: 12, Version: 2, Source: 'local', Hash: 'sha256:a', Content: { Provider: 'openai', OpenAI: { SystemPrompt: '账号默认' } } },
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
  mockedRequest.mockImplementation((path: string) => {
    if (path.startsWith('/api/v1/prompts?')) return Promise.resolve(catalog)
    if (path.includes('/versions/13/publish')) {
      return Promise.resolve({ Version: version({ Status: 'published' }), ArchivedVersionID: 12 })
    }
    if (path.includes('/rollback')) {
      return Promise.resolve({ Version: version({ ID: 10, Status: 'published' }), ArchivedVersionID: 12 })
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
    if (path.includes('/diff')) {
      return Promise.resolve({
        PromptID: 'chat.reply',
        Provider: 'openai',
        From: { Source: 'active', ID: 12, Version: 2, Status: 'published', Hash: 'a' },
        To: { ID: 13, Version: 3, Status: 'draft', Hash: 'b' },
        Changed: true,
        UnifiedDiff: '',
        Lines: [{ Op: '+', Text: '新内容' }],
      })
    }
    if (path.includes('/effective')) return Promise.resolve(effective)
    if (path.match(/\/api\/v1\/prompts\/intent\.participation\?/)) {
      const url = new URL(path, 'http://local')
      return Promise.resolve(intentDetail(url.searchParams.get('provider') ?? 'jev'))
    }
    if (path.match(/\/api\/v1\/prompts\/chat\.reply\?/)) {
      return Promise.resolve(path.includes('scope_type=group') ? { ...detail, Active: null, Versions: [] } : detail)
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
  it('shows builtin, active and draft states across the three layers', async () => {
    renderPage()
    expect(await screen.findByText('内置默认')).toBeTruthy()
    expect(screen.getAllByText('实际生效').length).toBeGreaterThan(0)
    expect(screen.getAllByText('编辑草稿').length).toBeGreaterThan(0)
    expect(screen.getByRole('button', { name: /聊天回复/ })).toBeVisible()
    expect(screen.getByRole('button', { name: /意图判断/ })).toBeVisible()
    expect(screen.getByRole('button', { name: /记忆抽取/ })).toBeVisible()
    expect(screen.getByText('账号默认')).toBeVisible()
    expect(screen.getByText('群聊类型补充')).toBeVisible()
    expect(screen.getByText('私聊类型补充')).toBeVisible()
  })

  it('publishes only a draft and never offers rollback on drafts', async () => {
    renderPage()
    fireEvent.click(await screen.findByText('v3'))
    expect(await screen.findByRole('button', { name: '发布' })).toBeVisible()
    expect(screen.queryByRole('button', { name: /回滚到此版本/ })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: '发布' }))
    fireEvent.click(await screen.findByRole('button', { name: '确认发布' }))
    await waitFor(() =>
      expect(mockedRequest).toHaveBeenCalledWith(
        '/api/v1/prompts/versions/13/publish',
        expect.objectContaining({ method: 'POST' }),
      ),
    )
  })

  it('rolls back only an archived version that was published before', async () => {
    renderPage()
    const archivedRow = (await screen.findByText('已归档')).closest('tr')
    expect(archivedRow).not.toBeNull()
    fireEvent.click(archivedRow!)
    fireEvent.click(await screen.findByRole('button', { name: /回滚到此版本/ }))
    fireEvent.click(await screen.findByRole('button', { name: '确认回滚' }))
    await waitFor(() =>
      expect(mockedRequest).toHaveBeenCalledWith(
        '/api/v1/prompts/chat.reply/rollback',
        expect.objectContaining({
          method: 'POST',
          body: expect.objectContaining({ version_id: 10, scope_id: 0, scope_type: 'account' }),
        }),
      ),
    )
  })

  it('restores builtin and keeps the archived history', async () => {
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: /恢复内置默认/ }))
    fireEvent.click(await screen.findByRole('button', { name: '确认恢复' }))
    await waitFor(() =>
      expect(mockedRequest).toHaveBeenCalledWith(
        '/api/v1/prompts/chat.reply/restore-builtin',
        expect.objectContaining({
          method: 'POST',
          body: expect.objectContaining({ bot_id: 998001, scope_type: 'account' }),
        }),
      ),
    )
  })

  it('selects the catalog entry for the active provider and switches protocol fields', async () => {
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: /意图判断/ }))
    await screen.findByText('指向机器人')
    fireEvent.change(screen.getByLabelText('意图 Prompt Provider'), { target: { value: 'openai' } })
    await screen.findByText('UserTemplate')
    await waitFor(() =>
      expect(mockedRequest).toHaveBeenCalledWith(
        expect.stringContaining('/api/v1/prompts/intent.participation?'),
        expect.anything(),
      ),
    )
    expect(mockedRequest.mock.calls.some(([path]) => String(path).includes('provider=openai'))).toBe(true)
  })

  it('shows an empty supplement instead of the account builtin for a supplement layer', async () => {
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: /群聊类型补充/ }))
    expect(await screen.findByText('空补充')).toBeVisible()
    expect(screen.getByText(/该层暂无本地发布版本/)).toBeVisible()
    expect(screen.queryByText(/内置默认 v1/)).toBeNull()
  })

  it('shows the layered effective result for a real target', async () => {
    renderPage()
    fireEvent.change(await screen.findByLabelText('生效目标 ID'), { target: { value: '700' } })
    fireEvent.click(screen.getByRole('button', { name: '查看分层结果' }))
    expect(await screen.findByText('最终内容')).toBeVisible()
    expect(screen.getByText(/server_constraints/)).toBeVisible()
    expect(mockedRequest.mock.calls.some(([path]) => String(path).includes('/effective') && String(path).includes('target_id=700'))).toBe(true)
  })

  it('can render only the intent prompt group for the intent module', async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })
    render(
      <QueryClientProvider client={client}>
        <PromptManagementPage kinds={['intent_participation']} initialKind="intent_participation" />
      </QueryClientProvider>,
    )
    expect(await screen.findByRole('button', { name: /意图判断/ })).toBeVisible()
    expect(screen.queryByRole('button', { name: /聊天回复/ })).toBeNull()
    expect(screen.queryByRole('button', { name: /记忆抽取/ })).toBeNull()
  })
})
