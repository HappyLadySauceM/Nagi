import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { MemoriesPage } from '../memories-page'

const mocks = vi.hoisted(() => ({ navigate: vi.fn() }))

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
    selectedBotId: 998001,
    selectedAccount: { nickname: '测试账号' },
  }),
}))

function response(data: unknown): Response {
  return new Response(JSON.stringify({ code: 200, message: 'success', data }), {
    headers: { 'Content-Type': 'application/json' },
  })
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