import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { RulesPage, SimulateCard } from '../rules-page'

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
  expect(screen.getByText('只读规则模拟 · 当前编辑内容')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: '添加条目' }))
  const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
  fireEvent.click(screen.getByRole('button', { name: '关闭详情' }))
  expect(confirm).toHaveBeenCalledWith('放弃未保存的修改并关闭？')
  expect(screen.getByRole('dialog')).toBeVisible()
  expect(screen.getByRole('button', { name: '删除条目' })).toBeVisible()
  confirm.mockReturnValue(true)
  fireEvent.click(screen.getByRole('button', { name: '关闭详情' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  client.clear()
})

it.each([false, true])('confirms deletion and preserves the editor on failure=%s', async (fail) => {
  vi.stubGlobal('ResizeObserver', class { observe = vi.fn(); unobserve = vi.fn(); disconnect = vi.fn() })
  let removed = false
  let writes = 0
  const row = version(1, '删除测试', 123)
  vi.stubGlobal('fetch', vi.fn((input: string, init?: RequestInit) => {
    const path = new URL(String(input), 'http://localhost').pathname
    if (init?.method === 'DELETE') {
      writes++
      if (fail) return Promise.resolve(new Response(JSON.stringify({ code: 500, message: 'delete failed' }), { status: 500 }))
      removed = true
      return Promise.resolve(response({ ID: 1 }))
    }
    if (path.endsWith('/rulesets')) return Promise.resolve(response({ Items: removed ? [] : [row] }))
    if (path.endsWith('/rulesets/1')) return Promise.resolve(response(row))
    throw new Error('Unexpected request ' + path)
  }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><RulesPage /></QueryClientProvider>)
  fireEvent.click(await screen.findByText(/删除测试/))
  const remove = await screen.findByRole('button', { name: '删除版本' })
  fireEvent.click(remove)
  fireEvent.click(screen.getByRole('button', { name: '取消' }))
  expect(writes).toBe(0)
  fireEvent.click(remove)
  fireEvent.click(screen.getByRole('button', { name: '确认删除' }))
  await waitFor(() => expect(writes).toBe(1))
  if (fail) {
    await waitFor(() => expect(screen.getByRole('button', { name: '确认删除' })).toBeEnabled())
    fireEvent.click(screen.getByRole('button', { name: '取消' }))
    expect(screen.getByRole('dialog')).toBeVisible()
  } else {
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(await screen.findByText('还没有规则版本')).toBeVisible()
  }
  client.clear()
})

it('deletes from the version list without opening the editor', async () => {
  vi.stubGlobal('ResizeObserver', class { observe = vi.fn(); unobserve = vi.fn(); disconnect = vi.fn() })
  let removed = false
  let deleted = ''
  const row = version(1, '列表删除', 123)
  vi.stubGlobal('fetch', vi.fn((input: string, init?: RequestInit) => {
    const url = new URL(String(input), 'http://localhost')
    if (init?.method === 'DELETE') {
      deleted = url.pathname + url.search
      removed = true
      return Promise.resolve(response({ ID: 1 }))
    }
    if (url.pathname.endsWith('/rulesets')) return Promise.resolve(response({ Items: removed ? [] : [row] }))
    if (url.pathname.endsWith('/rulesets/1')) return Promise.resolve(response(row))
    throw new Error('Unexpected request ' + url.pathname)
  }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><RulesPage /></QueryClientProvider>)
  fireEvent.click(await screen.findByRole('button', { name: '删除规则 v1' }))
  expect(screen.queryByText(/规则编辑/)).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: '确认删除' }))
  await waitFor(() => expect(deleted).toBe('/api/v1/intent/rulesets/1?bot_id=1'))
  expect(await screen.findByText('还没有规则版本')).toBeVisible()
  client.clear()
})

it('resets the editor pane to editing after the version sheet closes', async () => {
  vi.stubGlobal('ResizeObserver', class { observe = vi.fn(); unobserve = vi.fn(); disconnect = vi.fn() })
  const row = version(1, '分栏复位', 123)
  vi.stubGlobal('fetch', vi.fn((input: string) => {
    const path = new URL(String(input), 'http://localhost').pathname
    if (path.endsWith('/rulesets')) return Promise.resolve(response({ Items: [row] }))
    if (path.endsWith('/rulesets/1')) return Promise.resolve(response(row))
    throw new Error('Unexpected request ' + path)
  }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><RulesPage /></QueryClientProvider>)
  fireEvent.click(await screen.findByText(/分栏复位/))
  fireEvent.click(await screen.findByRole('button', { name: '编辑测试' }))
  expect(screen.getByRole('button', { name: '编辑测试' })).toHaveAttribute('aria-current', 'page')
  fireEvent.click(screen.getByRole('button', { name: '关闭详情' }))
  await waitFor(() => expect(screen.queryByRole('button', { name: '规则编辑' })).toBeNull())
  fireEvent.click(screen.getByText(/分栏复位/))
  await waitFor(() => expect(screen.getByRole('button', { name: '规则编辑' })).toHaveAttribute('aria-current', 'page'))
  client.clear()
})

it('serializes unsaved rules and invalidates an old response when scope changes', async () => {
  let finish: ((value: Response) => void) | undefined
  let body: Record<string, unknown> | undefined
  vi.stubGlobal('fetch', vi.fn((_input: string, init?: RequestInit) => {
    body = JSON.parse(String(init?.body))
    return new Promise<Response>(resolve => { finish = resolve })
  }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const props = { scope: { botId: 1, scopeType: 'private' as const, scopeId: 123 }, entries: [{ priority: 0, match: {}, action: { type: 'reply' as const, reasonCode: 'preview_reason' } }], previewGlobal: false }
  const { rerender } = render(<QueryClientProvider client={client}><SimulateCard {...props} previewTargetsText="private:123" /></QueryClientProvider>)
  fireEvent.change(screen.getByPlaceholderText('输入一条消息'), { target: { value: 'hello' } })
  fireEvent.click(screen.getByRole('button', { name: '模拟' }))
  await waitFor(() => expect(body).toBeDefined())
  expect(body).toMatchObject({ entries: [{ action: { type: 'reply', reason_code: 'preview_reason' } }] })
  rerender(<QueryClientProvider client={client}><SimulateCard {...props} previewTargetsText="private:456" /></QueryClientProvider>)
  finish?.(response({ Blocked: false, Explicit: false, NeedSemantic: false, AlwaysReply: false, ForceReply: true, ReasonCode: 'preview_reason', RulesetVersion: 'draft' }))
  expect(await screen.findByText('内容已变化，请重新测试')).toBeVisible()
  expect(screen.queryByText('原因码：preview_reason')).toBeNull()
  expect(screen.getByRole('button', { name: '模拟' })).toBeDisabled()
  client.clear()
})
