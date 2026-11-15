import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { MemoriesPage } from '../memories-page'

const mocks = vi.hoisted(() => ({ navigate: vi.fn(), selectedBotId: 998001 as number | undefined }))

vi.mock('@tanstack/react-router', () => ({
  useSearch: () => ({
    search: '',
    ownerKey: '',
    status: '',
    visibility: '',
    page: 1,
    limit: 20,
  }),
  useNavigate: () => mocks.navigate,
}))

vi.mock('../../accounts/account-context', () => ({
  useAccountContext: () => ({
    selectedBotId: mocks.selectedBotId,
    selectedAccount: { nickname: '测试账号' },
  }),
}))

function response(data: unknown): Response {
  return new Response(JSON.stringify({ code: 200, message: 'success', data }), {
    headers: { 'Content-Type': 'application/json' },
  })
}

function failure(status: number, message: string): Response {
  return new Response(JSON.stringify({ code: status, message }), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

const knowledgeSummary = {
  Memories: {
    Total: 1,
    Active: 1,
    Candidate: 0,
    Superseded: 0,
    ByVisibility: { bot_shared: 0, group: 1, private_user: 0, conversation: 0 },
  },
  Extraction: {
    Enabled: false,
    PendingJobs: 0,
    ProcessingJobs: 0,
    FailedJobs: 0,
    LastErrorCode: '',
  },
  Prompts: {
    'chat.reply': { ActiveVersion: 1, Source: 'builtin' },
    'intent.participation': { ActiveVersion: 1, Source: 'builtin' },
    'memory.extraction': { ActiveVersion: 1, Source: 'builtin' },
  },
}

function memory() {
  return {
    ID: 'memory-1',
    BotID: 998001,
    Visibility: 'group',
    OwnerKey: '700001',
    Content: '需要永久删除的记忆',
    Source: 'manual',
    Confidence: 0.8,
    Status: 'active',
    CreatedAt: '2026-11-11T10:00:00Z',
    UpdatedAt: '2026-11-11T10:00:00Z',
  }
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <MemoriesPage />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  mocks.navigate.mockReset()
  mocks.selectedBotId = 998001
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

it('confirms permanent deletion and removes the memory after success', async () => {
  let deleted = false
  const writes: { method: string; path: string }[] = []

  vi.stubGlobal(
    'fetch',
    vi.fn((input: string, init?: RequestInit) => {
      const url = new URL(String(input), 'http://localhost')
      const method = init?.method ?? 'GET'
      if (url.pathname === '/api/v1/knowledge/summary' && method === 'GET') {
        return Promise.resolve(response(knowledgeSummary))
      }
      if (url.pathname === '/api/v1/memories' && method === 'GET') {
        return Promise.resolve(
          response({
            items: deleted ? [] : [memory()],
            total: deleted ? 0 : 1,
            page: 1,
            limit: 20,
          }),
        )
      }
      if (url.pathname === '/api/v1/memories/memory-1' && method === 'DELETE') {
        writes.push({ method, path: url.pathname })
        deleted = true
        return Promise.resolve(response({}))
      }
      throw new Error(`Unexpected request ${method} ${url.pathname}`)
    }),
  )

  renderPage()

  expect(await screen.findByText('需要永久删除的记忆')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: '永久删除记忆' }))

  expect(await screen.findByText('永久删除该记忆？')).toBeVisible()
  expect(screen.getByText('该记忆将从数据库中永久删除，无法恢复。')).toBeVisible()
  expect(screen.queryByText(/软删除|历史数据仍保留/)).toBeNull()

  fireEvent.click(screen.getByRole('button', { name: '永久删除' }))

  await waitFor(() => expect(writes).toHaveLength(1))
  expect(writes[0]).toEqual({ method: 'DELETE', path: '/api/v1/memories/memory-1' })
  expect(await screen.findByText('还没有长期记忆')).toBeVisible()
})

it('explicitly supersedes one same-subject active memory and shows the relation', async () => {
  const candidate = {
    ID: 'candidate-1',
    BotID: 998001,
    Visibility: 'group',
    OwnerKey: '700001',
    Content: '旧偏好后续事实',
    Source: 'manual',
    SourceMessageID: 0,
    SourceSenderID: 1001,
    Origin: 'manual',
    SubjectType: 'user',
    SubjectID: '42',
    Confidence: 0.8,
    Status: 'candidate',
    CreatedAt: '2026-11-11T10:00:00Z',
    UpdatedAt: '2026-11-11T10:00:00Z',
  }
  const oldMemory = {
    ...candidate,
    ID: 'old-1',
    Content: '旧偏好事实',
    Status: 'active',
  }
  const otherSubject = {
    ...candidate,
    ID: 'old-2',
    Content: '另一主体的事实',
    SubjectID: '99',
    Status: 'active',
  }
  const writes: { method: string; path: string; body: unknown }[] = []
  let superseded = false

  vi.stubGlobal(
    'fetch',
    vi.fn((input: string, init?: RequestInit) => {
      const url = new URL(String(input), 'http://localhost')
      const method = init?.method ?? 'GET'
      if (url.pathname === '/api/v1/knowledge/summary' && method === 'GET') {
        return Promise.resolve(response(knowledgeSummary))
      }
      if (url.pathname === '/api/v1/memories' && method === 'GET') {
        if (url.searchParams.get('status') === 'active') {
          return Promise.resolve(response({ items: superseded ? [otherSubject] : [oldMemory, otherSubject], total: superseded ? 1 : 2, page: 1, limit: 200 }))
        }
        return Promise.resolve(response({ items: [candidate], total: 1, page: 1, limit: 20 }))
      }
      if (url.pathname === '/api/v1/memories/candidate-1/management' && method === 'GET') {
        return Promise.resolve(response({ Memory: candidate, Replacements: [] }))
      }
      if (url.pathname === '/api/v1/memories/old-1/management' && method === 'GET') {
        return Promise.resolve(response({
          Memory: { ...oldMemory, Status: 'superseded' },
          Replacements: [{
            ReplacementID: 'replacement-1',
            Direction: 'as_old',
            NewMemoryID: 'candidate-1',
            OldMemoryID: 'old-1',
            Actor: 'admin',
            Reason: '补充事实',
            NewStatus: 'active',
            OldStatus: 'superseded',
            CreatedAt: '2026-11-16T00:00:00Z',
            Counterpart: { MemoryID: 'candidate-1', Status: 'active', Missing: false },
          }],
        }))
      }
      if (url.pathname === '/api/v1/memories/candidate-1/supersede' && method === 'POST') {
        writes.push({ method, path: url.pathname, body: JSON.parse(String(init?.body ?? '{}')) })
        superseded = true
        return Promise.resolve(response({
          NewMemoryID: 'candidate-1',
          OldMemoryID: 'old-1',
          NewStatus: 'active',
          OldStatus: 'superseded',
          ReplacedAt: '2026-11-16T00:00:00Z',
          ReplacedBy: 'admin',
          Idempotent: false,
        }))
      }
      throw new Error(`Unexpected request ${method} ${url.pathname}`)
    }),
  )

  mocks.selectedBotId = undefined
  renderPage()
  expect(await screen.findByText('旧偏好后续事实')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: '用新事实替代旧事实' }))

  expect(await screen.findByText('选择要替代的旧 active 事实')).toBeVisible()
  expect(await screen.findAllByRole('radio')).toHaveLength(1)
  expect(screen.queryByText('另一主体的事实')).toBeNull()
  fireEvent.click(screen.getByRole('radio'))
  fireEvent.change(screen.getByPlaceholderText('例如：用户补充了新的偏好事实'), {
    target: { value: '补充事实' },
  })
  fireEvent.click(screen.getByRole('button', { name: '确认替代' }))

  await waitFor(() => expect(writes).toHaveLength(1))
  expect(writes[0]).toMatchObject({
    method: 'POST',
    path: '/api/v1/memories/candidate-1/supersede',
    body: { old_memory_id: 'old-1', reason: '补充事实' },
  })
  expect(String(writes[0].body && (writes[0].body as { idempotency_key?: string }).idempotency_key)).not.toBe('')

  expect(await screen.findByText('本记忆被新事实替代')).toBeVisible()
  expect(screen.getByText(/操作者：admin/)).toBeVisible()
  expect(screen.getByText(/旧偏好事实/)).toBeVisible()
})

it('shows an explicit error instead of success when supersede conflicts', async () => {
  const candidate = {
    ID: 'candidate-conflict',
    BotID: 998001,
    Visibility: 'group',
    OwnerKey: '700001',
    Content: '冲突候选事实',
    Source: 'manual',
    SourceSenderID: 1001,
    Origin: 'manual',
    SubjectType: 'user',
    SubjectID: '42',
    Confidence: 0.8,
    Status: 'candidate',
    CreatedAt: '2026-11-11T10:00:00Z',
    UpdatedAt: '2026-11-11T10:00:00Z',
  }
  const oldMemory = { ...candidate, ID: 'old-conflict', Content: '冲突旧事实', Status: 'active' }

  vi.stubGlobal(
    'fetch',
    vi.fn((input: string, init?: RequestInit) => {
      const url = new URL(String(input), 'http://localhost')
      const method = init?.method ?? 'GET'
      if (url.pathname === '/api/v1/knowledge/summary' && method === 'GET') return Promise.resolve(response(knowledgeSummary))
      if (url.pathname === '/api/v1/memories' && method === 'GET') {
        if (url.searchParams.get('status') === 'active') return Promise.resolve(response({ items: [oldMemory], total: 1, page: 1, limit: 200 }))
        return Promise.resolve(response({ items: [candidate], total: 1, page: 1, limit: 20 }))
      }
      if (url.pathname === '/api/v1/memories/candidate-conflict/management' && method === 'GET') {
        return Promise.resolve(response({ Memory: candidate, Replacements: [] }))
      }
      if (url.pathname === '/api/v1/memories/candidate-conflict/supersede' && method === 'POST') {
        return Promise.resolve(failure(409, 'subject_mismatch'))
      }
      throw new Error(`Unexpected request ${method} ${url.pathname}`)
    }),
  )

  renderPage()
  expect(await screen.findByText('冲突候选事实')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: '用新事实替代旧事实' }))
  expect(await screen.findByText('选择要替代的旧 active 事实')).toBeVisible()
  expect(await screen.findAllByRole('radio')).toHaveLength(1)
  fireEvent.click(screen.getByRole('radio'))
  fireEvent.click(screen.getByRole('button', { name: '确认替代' }))

  expect(await screen.findByText('旧事实的主体、可见性或归属与候选不一致，未执行替代。')).toBeVisible()
  expect(screen.queryByText('本记忆被新事实替代')).toBeNull()
})

it('creates a candidate with an explicit user subject and stable id', async () => {
  const writes: { method: string; path: string; body: unknown }[] = []

  vi.stubGlobal(
    'fetch',
    vi.fn((input: string, init?: RequestInit) => {
      const url = new URL(String(input), 'http://localhost')
      const method = init?.method ?? 'GET'
      if (url.pathname === '/api/v1/knowledge/summary' && method === 'GET') {
        return Promise.resolve(response(knowledgeSummary))
      }
      if (url.pathname === '/api/v1/memories' && method === 'GET') {
        return Promise.resolve(response({ items: [], total: 0, page: 1, limit: 20 }))
      }
      if (url.pathname === '/api/v1/memories' && method === 'POST') {
        writes.push({ method, path: url.pathname, body: JSON.parse(String(init?.body ?? '{}')) })
        return Promise.resolve(response({ id: 'new-memory', duplicate: false }))
      }
      throw new Error(`Unexpected request ${method} ${url.pathname}`)
    }),
  )

  renderPage()
  expect(await screen.findByText('还没有长期记忆')).toBeVisible()
  fireEvent.click(screen.getAllByRole('button', { name: '新建记忆' })[0])

  fireEvent.change(screen.getByLabelText('主体类型'), { target: { value: 'user' } })
  fireEvent.change(screen.getByLabelText('主体 ID'), { target: { value: '42' } })
  fireEvent.change(screen.getByLabelText('归属'), { target: { value: '700001' } })
  fireEvent.change(screen.getByLabelText('内容'), {
    target: { value: '用户补充的偏好事实' },
  })
  fireEvent.click(screen.getByRole('button', { name: '创建记忆' }))

  await waitFor(() => expect(writes).toHaveLength(1))
  expect(writes[0]).toMatchObject({
    method: 'POST',
    path: '/api/v1/memories',
    body: {
      bot_id: 998001,
      visibility: 'group',
      owner_key: '700001',
      subject_type: 'user',
      subject_id: '42',
      content: '用户补充的偏好事实',
    },
  })
})

it('uses the row bot id for management when the current account is not selected', async () => {
  mocks.selectedBotId = undefined
  const candidate = {
    ...memory(),
    ID: 'candidate-row-bot',
    Status: 'candidate',
    SubjectType: 'user',
    SubjectID: '42',
  }
  let managementBotId: string | null = null

  vi.stubGlobal(
    'fetch',
    vi.fn((input: string, init?: RequestInit) => {
      const url = new URL(String(input), 'http://localhost')
      const method = init?.method ?? 'GET'
      if (url.pathname === '/api/v1/knowledge/summary' && method === 'GET') {
        return Promise.resolve(response(knowledgeSummary))
      }
      if (url.pathname === '/api/v1/memories' && method === 'GET') {
        return Promise.resolve(response({ items: [candidate], total: 1, page: 1, limit: 20 }))
      }
      if (url.pathname === '/api/v1/memories/candidate-row-bot/management' && method === 'GET') {
        managementBotId = url.searchParams.get('bot_id')
        return Promise.resolve(response({ Memory: candidate, Replacements: [] }))
      }
      throw new Error(`Unexpected request ${method} ${url.pathname}`)
    }),
  )

  renderPage()
  expect(await screen.findByText('需要永久删除的记忆')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: '查看替代关系' }))

  await waitFor(() => expect(managementBotId).toBe('998001'))
  expect(await screen.findByText('暂无替代关系。')).toBeVisible()
})
