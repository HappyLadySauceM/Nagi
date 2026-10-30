import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { RulesPage } from '../rules-page'

vi.mock('../../accounts/account-context', () => ({ useAccountContext: () => ({ selectedBotId: 1 }) }))
const response = (data: unknown) => new Response(JSON.stringify({ code: 200, message: 'success', data }), { headers: { 'Content-Type': 'application/json' } })
const version = (id: number, note: string, scopeId: number) => ({ ID: id, BotID: 1, ScopeType: 'private', ScopeID: scopeId, Version: 1, Status: 'published', Note: note, Entries: [], BuiltinRules: {}, Targets: [{ kind: 'private', id: scopeId }] })
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); window.history.replaceState({}, '', '/') })

it('keeps the selected rule family fixed while its cloned draft is being created', async () => {
  vi.stubGlobal('ResizeObserver', class { observe = vi.fn(); unobserve = vi.fn(); disconnect = vi.fn() })
  const first = version(1, '规则A', 123), second = version(2, '规则B', 456)
  const created = { ...first, ID: 3, Version: 2, Status: 'draft' }
  const reads: string[] = [], writes: Record<string, unknown>[] = []
  let finish: ((value: Response) => void) | undefined
  vi.stubGlobal('fetch', vi.fn((input: string, init?: RequestInit) => {
    const url = new URL(String(input), 'http://localhost')
    if (init?.method === 'POST') {
      writes.push(JSON.parse(String(init.body)))
      return new Promise<Response>(resolve => { finish = resolve })
    }
    reads.push(url.pathname)
    if (url.pathname.endsWith('/rulesets/1')) return Promise.resolve(response(first))
    if (url.pathname.endsWith('/rulesets/2')) return Promise.resolve(response(second))
    if (url.pathname.endsWith('/rulesets/3')) return Promise.resolve(response(created))
    if (url.pathname.endsWith('/rulesets')) return Promise.resolve(response({ Items: [first, second] }))
    if (url.pathname.endsWith('/contacts')) return Promise.resolve(response({ Items: [], Total: 0, Page: 1, Limit: 20 }))
    throw new Error('Unexpected request '+url.pathname)
  }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><RulesPage /></QueryClientProvider>)
  fireEvent.click(await screen.findByText(/规则A/))
  const create = await screen.findByRole('button', { name: '从此版本创建草稿' })
  fireEvent.click(create)
  await waitFor(() => expect(writes).toHaveLength(1))
  expect(writes[0]).toMatchObject({ bot_id: 1, scope_type: 'private', scope_id: 123, clone_from_id: 1 })
  fireEvent.click(screen.getByText(/规则B/))
  expect(reads).not.toContain('/api/v1/intent/rulesets/2')
  finish?.(response(created))
  await waitFor(() => expect(reads).toContain('/api/v1/intent/rulesets/3'))
  await waitFor(() => expect(screen.getByRole('button', { name: '添加条目' })).toBeEnabled())
  expect(reads).not.toContain('/api/v1/intent/rulesets/2')
  client.clear()
})
