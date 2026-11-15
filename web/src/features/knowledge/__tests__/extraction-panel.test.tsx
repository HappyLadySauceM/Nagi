import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiRequest } from '../../../lib/api/client'
import { ExtractionPanel } from '../components/extraction-panel'

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

const settings = {
  Scope: 'global',
  Enabled: false,
  Concurrency: 1,
  BatchMaxMessages: 10,
  BatchMaxRunes: 4000,
  MaxWaitMS: 5000,
  PollIntervalMS: 1000,
  LeaseDurationMS: 30000,
  MaxAttempts: 3,
  RetryDelaysMS: [30000, 120000],
  Source: 'config',
  Editable: false,
}

function job(overrides: Record<string, unknown> = {}) {
  return {
    ID: 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee',
    BotID: 998001,
    ConversationID: 'conv-1',
    ScopeType: 'group',
    ScopeID: 700,
    MessageID: 123,
    Status: 'failed',
    Attempts: 3,
    RoundAttempts: 1,
    NextRunAt: '2026-11-16T08:00:00Z',
    LastErrorCode: 'model_timeout',
    ManualRetries: 0,
    SourceAvailable: true,
    CreatedAt: '2026-11-15T07:00:00Z',
    UpdatedAt: '2026-11-15T07:30:00Z',
    ...overrides,
  }
}

function renderPanel() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <ExtractionPanel />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  mockedRequest.mockReset()
  mockedRequest.mockImplementation((path: string, options?: { method?: string }) => {
    if (path === '/api/v1/memory-extraction/settings') {
      return Promise.resolve(settings)
    }
    if (path.startsWith('/api/v1/memory-extraction/jobs?') && (options?.method ?? 'GET') === 'GET') {
      return Promise.resolve({
        items: [
          job(),
          job({
            ID: 'bbbbbbbb-bbbb-cccc-dddd-eeeeeeeeeeee',
            SourceAvailable: false,
            LastErrorCode: 'source_removed',
          }),
          job({
            ID: 'cccccccc-bbbb-cccc-dddd-eeeeeeeeeeee',
            Status: 'done',
            LastErrorCode: '',
          }),
        ],
        total: 3,
        page: 1,
        limit: 20,
      })
    }
    if (
      path === '/api/v1/memory-extraction/jobs/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/retry' &&
      options?.method === 'POST'
    ) {
      return Promise.resolve({ retried: true })
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

describe('ExtractionPanel', () => {
  it('shows the global read-only settings and the default-off explanation', async () => {
    renderPanel()

    expect(await screen.findByText('未开启（默认关闭）')).toBeVisible()
    expect(screen.getByText('全局只读')).toBeVisible()
    expect(screen.getByText(/作用域为/)).toBeVisible()
    expect(screen.getByText(/仅新增消息/)).toBeVisible()
    expect(screen.getByText('人工审批：抽取结果先进入候选，审批后才进入生效记忆。')).toBeVisible()
    expect(screen.getByText('model_timeout')).toBeVisible()
  })

  it('allows only one failed job with an available source to retry', async () => {
    renderPanel()

    expect(await screen.findByText('model_timeout')).toBeVisible()
    const retryButtons = screen.getAllByRole('button', { name: '重试' })
    expect(retryButtons).toHaveLength(3)
    expect(retryButtons[0]).toBeEnabled()
    expect(retryButtons[1]).toBeDisabled()
    expect(retryButtons[2]).toBeDisabled()

    fireEvent.click(retryButtons[0])
    expect(await screen.findByText('重新排队该抽取任务？')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: '确认重试' }))

    await waitFor(() =>
      expect(mockedRequest).toHaveBeenCalledWith(
        '/api/v1/memory-extraction/jobs/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/retry',
        expect.objectContaining({ method: 'POST' }),
      ),
    )
  })
})
