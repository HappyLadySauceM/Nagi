import { useState } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { ScopeEditDialog } from '../components/scope-edit-dialog'

const response = (data: unknown) => new Response(JSON.stringify({ code: 200, message: 'success', data }), { headers: { 'Content-Type': 'application/json' } })
const policy = {
  ScopeType: 'private', ScopeID: 123, Source: 'default', TemplateVersion: 1, Version: 1,
  Enabled: false, ReplyMode: 'mention_only', StoreMessages: false, AllowHistoryRead: false, AllowMemoryRead: false, AllowMemoryWrite: false,
  MinReplyIntervalMS: 10000, MaxRepliesPerHour: 20, ToolPermissions: {},
  MaxMessagesPerRun: 3, MaxCharsPerMessage: 500, MaxTotalCharsPerRun: 1500, MessageIntervalMS: 700,
}
beforeEach(() => { vi.stubGlobal('ResizeObserver', class { observe = vi.fn(); unobserve = vi.fn(); disconnect = vi.fn() }) })
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

it('saves an unchanged inherited template as an independent snapshot and freezes pending changes', async () => {
  let finish: ((value: Response) => void) | undefined
  const writes: Record<string, unknown>[] = []
  vi.stubGlobal('fetch', vi.fn((input: string, init?: RequestInit) => {
    if (init?.method === 'PUT') {
      writes.push(JSON.parse(String(init.body)))
      return new Promise<Response>(resolve => { finish = resolve })
    }
    if(String(input).includes('/models')) return Promise.resolve(response({Catalog:{Profiles:[],Defaults:{},Selections:{},HasBilibiliCredential:false},Version:1}))
 if (String(input).includes('/tools')) return Promise.resolve(response({ Providers: [], Tools: [], Permissions: [], }))
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
  vi.stubGlobal('fetch', vi.fn((input: string) => {
    if(String(input).includes('/models')) return Promise.resolve(response({Catalog:{Profiles:[],Defaults:{},Selections:{},HasBilibiliCredential:false},Version:1}))
 if (String(input).includes('/tools')) return Promise.resolve(response({ Providers: [], Tools: [], Permissions: [], }))
    return Promise.resolve(response({ ...policy, ScopeID: String(input).includes('/default') ? 0 : 123, Source: 'independent', AllowHistoryRead: String(input).includes('/default') }))
  }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><ScopeEditDialog open onOpenChange={() => {}} botId={1} scopeType="private" scopeId={123} targetLabel="测试好友" savedPolicy={{ source: 'independent' } as never} /></QueryClientProvider>)
  const restore = screen.getByRole('button', { name: '恢复跟随默认' })
  await waitFor(() => expect(restore).toBeEnabled())
  fireEvent.click(restore)
  expect(screen.getByText('读取聊天历史：关闭 → 允许')).toBeVisible()
  client.clear()
})

it('keeps edits across workspace sections and asks before closing', async () => {
  vi.stubGlobal('fetch', vi.fn((input: string) => Promise.resolve(response(
    String(input).includes('/models') ? {Catalog:{Profiles:[],Defaults:{},Selections:{},HasBilibiliCredential:false},Version:1} : String(input).includes('/tools') ? { Providers: [], Tools: [], Permissions: [] } : policy,
  ))))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  function Harness() {
    const [open, setOpen] = useState(true)
    return <ScopeEditDialog open={open} onOpenChange={setOpen} botId={1} scopeType="private" scopeId={123} targetLabel="测试好友" />
  }
  render(<QueryClientProvider client={client}><Harness /></QueryClientProvider>)
  await waitFor(() => expect(screen.getByRole('button', { name: '保存策略' })).toBeEnabled())
  fireEvent.click(screen.getByRole('button', { name: '发送设置' }))
  const interval = screen.getByLabelText('气泡间隔')
  fireEvent.change(interval, { target: { value: '900' } })
  fireEvent.click(screen.getByRole('button', { name: '参与控制' }))
  fireEvent.click(screen.getByRole('button', { name: '发送设置' }))
  expect(screen.getByLabelText('气泡间隔')).toHaveValue('900')
  fireEvent.click(screen.getByRole('button', { name: '关闭详情' }))
  expect(await screen.findByText('放弃未保存的修改？')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: '放弃并关闭' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  client.clear()
})

it('shows loading, error, retry and empty feedback in the capability section', async () => {
  let finish: ((value: Response) => void) | undefined
  let attempts = 0
  vi.stubGlobal('fetch', vi.fn((input: string) => {
    if(String(input).includes('/models')) return Promise.resolve(response({Catalog:{Profiles:[],Defaults:{},Selections:{},HasBilibiliCredential:false},Version:1}))
 if (String(input).includes('/tools')) {
      attempts++
      if (attempts === 1) return new Promise<Response>(resolve => { finish = resolve })
      return Promise.resolve(response({ Providers: [], Tools: [], Permissions: [] }))
    }
    return Promise.resolve(response(policy))
  }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><ScopeEditDialog open onOpenChange={() => {}} botId={1} scopeType="private" scopeId={123} targetLabel="测试好友" /></QueryClientProvider>)
  fireEvent.click(screen.getByRole('button', { name: '能力授权' }))
  expect(await screen.findByText('正在加载能力列表…')).toBeVisible()
  finish?.(new Response(JSON.stringify({ code: 500, message: 'internal error' }), { status: 500 }))
  expect(await screen.findByRole('alert')).toBeVisible()
  expect(screen.getByRole('button', { name: '保存策略' })).toBeDisabled()
  fireEvent.click(screen.getByRole('button', { name: '重试' }))
  expect(await screen.findByText('当前会话没有可授权的能力。')).toBeVisible()
  expect(screen.queryByRole('alert')).toBeNull()
  client.clear()
})

it.each([['private', 123], ['group', 123], ['private', 0], ['group', 0]] as const)('saves and reloads specific capability grants for %s/%s without widening other grants', async (scopeType, scopeId) => {
  const tools = {
    Providers: ['builtin.qq'],
    Tools: [{ ID: 'qq.send_msg', Provider: 'builtin.qq', Description: '发送消息', Category: 'reply', Permission: 'qq.reply.current', Applicable: true, ReadOnly: false, SideEffect: true, DefaultGranted: true }],
    Permissions: [
      { ID: 'qq.reply.current', Tools: ['qq.send_msg'], DefaultGranted: true },
      { ID: 'qq.cross_conversation', Tools: ['qq.send_msg'], DefaultGranted: false },
    ],
  }
  let stored = { ...policy, ScopeType: scopeType, ScopeID: scopeId, ToolPermissions: {} }
  let body: Record<string, unknown> | undefined
  vi.stubGlobal('fetch', vi.fn((input: string, init?: RequestInit) => {
    if(String(input).includes('/models')) return Promise.resolve(response({Catalog:{Profiles:[],Defaults:{},Selections:{},HasBilibiliCredential:false},Version:1}))
 if (String(input).includes('/tools')) return Promise.resolve(response(tools))
    if (String(input).includes('/members')) return Promise.resolve(response({ Items: [], Total: 0, Page: 1, Limit: 20 }))
    if (init?.method === 'PUT') {
      body = JSON.parse(String(init.body))
      stored = { ...stored, Source: 'independent', ToolPermissions: body!.tool_permissions as Record<string, boolean> }
    }
    return Promise.resolve(response(stored))
  }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  function Harness() {
    const [open, setOpen] = useState(true)
    return <><button onClick={() => setOpen(true)}>重新编辑</button><ScopeEditDialog open={open} onOpenChange={setOpen} botId={1} scopeType={scopeType} scopeId={scopeId} targetLabel="测试会话" /></>
  }
  render(<QueryClientProvider client={client}><Harness /></QueryClientProvider>)
  fireEvent.click(screen.getByRole('button', { name: '能力授权' }))
  fireEvent.click(await screen.findByRole('checkbox', { name: '发送消息' }))
  fireEvent.click(screen.getByRole('button', { name: '发送设置' }))
  fireEvent.click(screen.getByRole('button', { name: '能力授权' }))
  expect(screen.getByRole('checkbox', { name: '发送消息' })).not.toBeChecked()
  expect(screen.getByRole('checkbox', { name: '跨会话操作' })).not.toBeChecked()
  fireEvent.click(screen.getByRole('button', { name: '保存策略' }))
  expect(screen.queryByRole('alertdialog')).toBeNull()
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  expect(body?.tool_permissions).toEqual({ 'qq.reply.current': false })
  fireEvent.click(screen.getByRole('button', { name: '重新编辑' }))
  fireEvent.click(screen.getByRole('button', { name: '能力授权' }))
  expect(await screen.findByRole('checkbox', { name: '发送消息' })).not.toBeChecked()
  expect(screen.getByRole('checkbox', { name: '跨会话操作' })).not.toBeChecked()
  fireEvent.click(screen.getByRole('checkbox', { name: '发送消息' }))
  fireEvent.click(screen.getByRole('button', { name: '放弃修改' }))
  expect(screen.getByRole('checkbox', { name: '发送消息' })).not.toBeChecked()
  client.clear()
})

function groupTokenPolicy() {
  return { ...policy, ScopeType: 'group', ScopeID: 700100, Source: 'independent' }
}

const groupRoleTools = {
  Providers: ['builtin.qq'],
  Tools: [{ ID: 'qq.mute_group_member', Provider: 'builtin.qq', Description: '禁言群成员', Category: 'group', Permission: 'qq.group.admin.mute', Applicable: true, ReadOnly: false, SideEffect: true, DefaultGranted: false }],
  Permissions: [{ ID: 'qq.group.admin.mute', Tools: ['qq.mute_group_member'], DefaultGranted: false, RequiresGroupRole: 'admin' }],
}

function stubGroupRoleFetch(role: string | null, requests: URL[]) {
  vi.stubGlobal('fetch', vi.fn((input: string) => {
    const url = new URL(String(input), 'http://localhost')
    if (url.pathname === '/api/v1/models') return Promise.resolve(response({ Catalog: { Profiles: [], Defaults: {}, Selections: {}, HasBilibiliCredential: false }, Version: 1 }))
    if (url.pathname === '/api/v1/tools') return Promise.resolve(response(groupRoleTools))
    if (url.pathname === '/api/v1/groups/700100/members') {
      requests.push(url)
      if (role === null) return Promise.resolve(new Response(JSON.stringify({ code: 409, message: 'bot account is not connected' }), { status: 409 }))
      return Promise.resolve(response({ Items: [{ GroupID: 700100, UserID: 1, Nickname: '机器人', Card: '', Role: role }], Total: 1, Page: 1, Limit: 20 }))
    }
    return Promise.resolve(response(groupTokenPolicy()))
  }))
}

it.each(['admin', 'member'] as const)('refreshes the bot role before gating group admin capabilities: %s', async (role) => {
  const requests: URL[] = []
  stubGroupRoleFetch(role, requests)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><ScopeEditDialog open onOpenChange={() => {}} botId={1} scopeType="group" scopeId={700100} targetLabel="测试群" /></QueryClientProvider>)
  fireEvent.click(screen.getByRole('button', { name: '能力授权' }))
  const control = await screen.findByRole('checkbox', { name: '禁言群成员' })
  await waitFor(() => expect(requests).toHaveLength(1))
  expect(requests[0].searchParams.get('refresh')).toBe('true')
  expect(requests[0].searchParams.get('search')).toBe('1')
  if (role === 'admin') {
    expect(control).toBeEnabled()
  } else {
    expect(control).toBeDisabled()
    expect(screen.getByText('机器人不是该群管理员，无法开启。')).toBeVisible()
  }
  client.clear()
})

it('treats an unavailable role refresh as unknown instead of a plain member', async () => {
  const requests: URL[] = []
  stubGroupRoleFetch(null, requests)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<QueryClientProvider client={client}><ScopeEditDialog open onOpenChange={() => {}} botId={1} scopeType="group" scopeId={700100} targetLabel="测试群" /></QueryClientProvider>)
  fireEvent.click(screen.getByRole('button', { name: '能力授权' }))
  expect(await screen.findByText('无法确认机器人在该群的角色，暂不能开启需要管理员的能力。')).toBeVisible()
  expect(screen.getByRole('checkbox', { name: '禁言群成员' })).toBeDisabled()
  client.clear()
})
