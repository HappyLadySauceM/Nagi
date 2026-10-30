import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../../../lib/api/client'
import { SettingsPage } from '../settings-page'

vi.mock('../../../lib/api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api/client')>()
  return { ...actual, apiRequest: vi.fn() }
})

const { apiRequest } = await import('../../../lib/api/client')
const mockedRequest = vi.mocked(apiRequest)

function settingsView(overrides: Record<string, unknown> = {}) {
  const settings = {
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
      BotName: 'Nagi',
      TimeoutSeconds: 3,
      ContinuationThreshold: 0.8,
      InterjectionThreshold: 0.95,
      AddressedOtherMax: 0.6,
      HistoryLimit: 30,
    },
    Agent: {
      MaxSteps: 6,
      MaxToolCalls: 5,
      MaxRunDurationSeconds: 30,
      Sending: { MaxMessagesPerRun: 3, MaxCharsPerMessage: 500, MaxTotalCharsPerRun: 1500 },
    },
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
    UpdatedAt: '2026-10-30T11:20:00Z',
    UpdatedBy: 'admin',
    Persisted: true,
    Credentials: { AI: true, Intent: true },
    Providers: ['builtin.reply', 'builtin.time'],
    ...overrides,
  }
}

const toolsCatalog = {
  Providers: ['builtin.reply', 'builtin.time'],
  Tools: [
    {
      ID: 'qq.send_current',
      Provider: 'builtin.reply',
      Description: '发送',
      Category: 'reply',
      ScopeKinds: ['group'],
      RiskLevel: 'write',
      ReadOnly: false,
      SideEffect: true,
      Permission: 'qq.reply.current',
      DefaultGranted: false,
      Applicable: true,
      UnavailableReason: '',
    },
  ],
  Permissions: [{ ID: 'qq.reply.current', Tools: ['qq.send_current'], DefaultGranted: false }],
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <SettingsPage />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  mockedRequest.mockReset()
})

describe('SettingsPage', () => {
  it('renders every section and counts unsaved changes', async () => {
    // 渲染三个分区，并统计未保存的改动。
    mockedRequest.mockImplementation((path: string) => {
      if (path.startsWith('/api/v1/tools')) {
        return Promise.resolve(toolsCatalog)
      }
      return Promise.resolve(settingsView())
    })
    renderPage()

    expect(await screen.findByText('生成模型')).toBeInTheDocument()
    // The label appears in both the section nav and the card title.
    // 该文案同时出现在分区导航与卡片标题中。
    expect(screen.getAllByText('发送与限额').length).toBeGreaterThan(0)
    expect(screen.getAllByText('工具与能力').length).toBeGreaterThan(0)
    expect(screen.getByText('已是最新配置')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('switch', { name: '启用生成模型' }))
    expect(await screen.findByText('有 1 项未保存')).toBeInTheDocument()
  })

  it('saves the draft and adopts the stored revision', async () => {
    // 保存后采用服务端修订号，未保存提示消失。
    let putBody: unknown
    mockedRequest.mockImplementation((path: string, options?: { method?: string; body?: unknown }) => {
      if (path.startsWith('/api/v1/tools')) {
        return Promise.resolve(toolsCatalog)
      }
      if (options?.method === 'PUT') {
        putBody = options.body
        return Promise.resolve(settingsView({ Version: 2 }))
      }
      return Promise.resolve(settingsView())
    })
    renderPage()

    fireEvent.click(await screen.findByRole('switch', { name: '启用生成模型' }))
    fireEvent.click(screen.getByRole('button', { name: '保存配置' }))

    await waitFor(() => expect(screen.getByText('配置已保存，立即生效')).toBeInTheDocument())
    expect(putBody).toMatchObject({ Version: 1 })
    expect(screen.getByText('已是最新配置')).toBeInTheDocument()
  })

  it('shows a conflict without discarding the draft', async () => {
    // 版本冲突提示刷新，草稿保持不变。
    mockedRequest.mockImplementation((path: string, options?: { method?: string }) => {
      if (path.startsWith('/api/v1/tools')) {
        return Promise.resolve(toolsCatalog)
      }
      if (options?.method === 'PUT') {
        return Promise.reject(
          new ApiError({ kind: 'conflict', endpoint: '/api/v1/settings', message: '配置已被其他页面修改' }),
        )
      }
      return Promise.resolve(settingsView())
    })
    renderPage()

    fireEvent.click(await screen.findByRole('switch', { name: '启用生成模型' }))
    fireEvent.click(screen.getByRole('button', { name: '保存配置' }))

    expect(
      await screen.findByText('配置已被其他页面修改，请刷新后重试'),
    ).toBeInTheDocument()
    expect(screen.getByText('有 1 项未保存')).toBeInTheDocument()
  })

  it('blocks saving while a field is invalid', async () => {
    // 非法输入立即标红，保存按钮保持禁用。
    mockedRequest.mockImplementation((path: string) => {
      if (path.startsWith('/api/v1/tools')) {
        return Promise.resolve(toolsCatalog)
      }
      return Promise.resolve(settingsView())
    })
    renderPage()

    const steps = await screen.findByLabelText('最大推理步数')
    fireEvent.change(steps, { target: { value: '' } })

    expect(await screen.findByText('取值范围 1-50')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存配置' })).toBeDisabled()
  })

  it('lists registered providers without an enable switch', async () => {
    // 提供方随进程注册，全部可用；页面只展示清单，没有启用开关。
    mockedRequest.mockImplementation((path: string) => {
      if (path.startsWith('/api/v1/tools')) {
        return Promise.resolve(toolsCatalog)
      }
      return Promise.resolve(settingsView())
    })
    renderPage()

    expect(await screen.findByText('builtin.reply')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'builtin.reply' })).toBeNull()
    expect(screen.getByText(/进程注册多少提供方就全部可用/)).toBeInTheDocument()
  })

  it('blames an old binary, not the database, when the route is missing', async () => {
    // 404 未注册表示旧二进制/未重启，页面不能再说「需要数据库」。
    mockedRequest.mockImplementation((path: string) => {
      if (path.startsWith('/api/v1/tools')) {
        return Promise.resolve(toolsCatalog)
      }
      return Promise.reject(
        new ApiError({
          kind: 'not-found',
          endpoint: '/api/v1/settings',
          status: 404,
          message: '该接口未注册',
          detail: 'unregistered',
        }),
      )
    })
    renderPage()

    expect(await screen.findByText('后端没有 /api/v1/settings 接口')).toBeInTheDocument()
    expect(screen.getByText(/旧版本二进制/)).toBeInTheDocument()
    expect(screen.queryByText(/系统配置需要数据库/)).toBeNull()
  })

  it('asks for a database only when the endpoint reports 503', async () => {
    // 503 才提示填写 database.dsn。
    mockedRequest.mockImplementation((path: string) => {
      if (path.startsWith('/api/v1/tools')) {
        return Promise.resolve(toolsCatalog)
      }
      return Promise.reject(
        new ApiError({
          kind: 'unavailable',
          endpoint: '/api/v1/settings',
          status: 503,
          message: 'database not configured',
        }),
      )
    })
    renderPage()

    expect(await screen.findByText('系统配置需要数据库')).toBeInTheDocument()
    expect(screen.getByText(/database\.dsn/)).toBeInTheDocument()
  })
})
