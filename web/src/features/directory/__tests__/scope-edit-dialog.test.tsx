import { useState } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { ScopeEditDialog } from '../components/scope-edit-dialog'

const response = (data: unknown) => new Response(JSON.stringify({ code: 200, message: 'success', data }), { headers: { 'Content-Type': 'application/json' } })
const policy = {
  ScopeType: 'private', ScopeID: 123, Source: 'default', TemplateVersion: 1, Version: 1,
  Enabled: false, ReplyMode: 'mention_only', StoreMessages: false, AllowHistoryRead: false, AllowMemoryWrite: false,
  MinReplyIntervalMS: 10000, MaxRepliesPerHour: 20, RetentionDays: 30, ToolPermissions: {},
  MaxMessagesPerRun: 3, MaxCharsPerMessage: 500, MaxTotalCharsPerRun: 1500, MessageIntervalMS: 700,
}
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

it('saves an unchanged inherited template as an independent snapshot and freezes pending changes', async () => {
  vi.stubGlobal('ResizeObserver', class { observe = vi.fn(); unobserve = vi.fn(); disconnect = vi.fn() })
  let finish: ((value: Response) => void) | undefined
  const writes: Record<string, unknown>[] = []
  vi.stubGlobal('fetch', vi.fn((input: string, init?: RequestInit) => {
    if (init?.method === 'PUT') {
      writes.push(JSON.parse(String(init.body)))
      return new Promise<Response>(resolve => { finish = resolve })
    }
    if (String(input).includes('/tools')) return Promise.resolve(response({ Providers: [], Tools: [], Permissions: [], AllowSideEffects: false }))
    return Promise.resolve(response({ ...policy, ScopeID: String(input).includes('/default') ? 0 : 123 }))
  }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  function Harness() {
    const [open, setOpen] = useState(true)
    return <ScopeEditDialog open={open} onOpenChange={setOpen} botId={1} scopeType="private" scopeId={123} targetLabel="测试好友" />
  }
  render(<QueryClientProvider client={client}><Harness /></QueryClientProvider>)
  const save = screen.getByRole('button', { name: '保存策略' })
  await waitFor(() => expect(save).toBeEnabled())
  fireEvent.click(save)
  await waitFor(() => expect(writes).toHaveLength(1))
  expect(writes[0]).toMatchObject({ enabled: false, expected_template_version: 1, max_messages_per_run: 3, message_interval_ms: 700 })
  expect(screen.getByRole('switch', { name: '启用该作用域' })).toBeDisabled()
  expect(window.dispatchEvent(new Event('nagi:account-changing', { cancelable: true }))).toBe(false)
  finish?.(response({ ...policy, Source: 'independent', Version: 1 }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  client.clear()
})

it('shows permission changes before restoring the default template', async () => {
  vi.stubGlobal('ResizeObserver', class { observe = vi.fn(); unobserve = vi.fn(); disconnect = vi.fn() })
  vi.stubGlobal('fetch', vi.fn((input: string) => {
    if (String(input).includes('/tools')) return Promise.resolve(response({ Providers: [], Tools: [], Permissions: [], AllowSideEffects: false }))
    return Promise.resolve(response({ ...policy, ScopeID: String(input).includes('/default') ? 0 : 123, Source: 'independent', StoreMessages: String(input).includes('/default') }))
  }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><ScopeEditDialog open onOpenChange={() => {}} botId={1} scopeType="private" scopeId={123} targetLabel="测试好友" savedPolicy={{ source: 'independent' } as never} /></QueryClientProvider>)
  const restore = screen.getByRole('button', { name: '恢复跟随默认' })
  await waitFor(() => expect(restore).toBeEnabled())
  fireEvent.click(restore)
  expect(screen.getByText('保存消息内容：关闭 → 允许')).toBeVisible()
  client.clear()
})
