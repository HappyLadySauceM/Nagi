import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { RulesPage, SimulateCard } from '../rules-page'

vi.mock('../../accounts/account-context', () => ({ useAccountContext: () => ({ selectedBotId: 1 }) }))
const response = (data: unknown) => new Response(JSON.stringify({ code: 200, message: 'success', data }), { headers: { 'Content-Type': 'application/json' } })
const version = (id: number, note: string, scopeId: number) => ({ ID: id, BotID: 1, ScopeType: 'private', ScopeID: scopeId, Version: 1, Status: 'published', Note: note, Entries: [], BuiltinRules: {}, Targets: [{ kind: 'private', id: scopeId }] })
const builtinCatalog = {
  Version: 'builtin-v1',
  Rules: [
    { RuleID: 'closing', Order: 10, Title: '告别结束', Condition: 'intent=closing', Action: 'silent', Editable: true, Source: 'builtin' },
    { RuleID: 'addressed_to_other', Order: 20, Title: '对他人说话优先抑制', Condition: 'addressed_to_other >= AddressedOtherMax', Action: 'silent', Editable: true, Source: 'builtin' },
    { RuleID: 'continuation', Order: 30, Title: '延续机器人话题', Condition: 'continues_bot_thread >= Continuation', Action: 'reply', Editable: true, Source: 'builtin' },
    { RuleID: 'interjection', Order: 40, Title: '自然插话', Condition: 'reply_mode=intent_based && appropriate_to_join >= Interjection', Action: 'reply', Editable: true, Source: 'builtin' },
    { RuleID: 'directed_to_bot', Order: 50, Title: '指向机器人', Condition: 'directed_to_bot >= Continuation', Action: 'reply', Editable: true, Source: 'builtin' },
  ],
}
const effectiveRules = {
  BuiltinRules: [
    { RuleID: 'closing', Order: 10, Title: '告别结束', Condition: 'intent=closing', Action: 'silent', Editable: true, Source: 'builtin', Enabled: false, Applicable: true, Status: 'not_applicable', Reason: 'disabled' },
    { RuleID: 'addressed_to_other', Order: 20, Title: '对他人说话优先抑制', Condition: 'addressed_to_other >= AddressedOtherMax', Action: 'silent', Editable: true, Source: 'builtin', Enabled: true, Applicable: true, Status: 'active', Reason: '' },
    { RuleID: 'continuation', Order: 30, Title: '延续机器人话题', Condition: 'continues_bot_thread >= Continuation', Action: 'reply', Editable: true, Source: 'builtin', Enabled: true, Applicable: true, Status: 'active', Reason: '' },
    { RuleID: 'interjection', Order: 40, Title: '自然插话', Condition: 'reply_mode=intent_based && appropriate_to_join >= Interjection', Action: 'reply', Editable: true, Source: 'builtin', Enabled: true, Applicable: true, Status: 'active', Reason: '' },
    { RuleID: 'directed_to_bot', Order: 50, Title: '指向机器人', Condition: 'directed_to_bot >= Continuation', Action: 'reply', Editable: true, Source: 'builtin', Enabled: true, Applicable: true, Status: 'active', Reason: '' },
  ],
  FixedConstraints: [],
  ActiveRuleset: null,
  Version: 'builtin-v1',
  BuiltinRulesVersion: 'builtin-v1',
  Source: 'builtin',
}
const draftQuestions = {
  directed_to_bot: { Type: 'noul', Instructions: '是否在跟我说话', Role: 'directed_to_bot', Weight: 1, Order: 50, Threshold: 0.8, Action: 'reply', Scope: 'group' },
  intent: { Type: 'choice', Instructions: '判断聊天意图', Role: 'intent', Criteria: { question: '提问', chat: '闲聊', command: '命令', closing: '告别', other: '其它' }, Order: 10, Action: 'silent', Scope: 'all', Match: 'closing' },
}
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
    if (url.pathname === '/api/v1/intent/builtin-rules') return Promise.resolve(response(builtinCatalog))
    if (url.pathname === '/api/v1/intent/rules/effective') return Promise.resolve(response(effectiveRules))
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
  await waitFor(() => expect(screen.getByRole('button', { name: '添加决策问题' })).toBeEnabled())
  expect(reads).not.toContain('/api/v1/intent/rulesets/2')
  expect(screen.getByText('只读规则预览 · 当前编辑内容')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: '添加决策问题' }))
  fireEvent.click(screen.getByRole('button', { name: '关闭详情' }))
  expect(await screen.findByRole('alertdialog')).toBeVisible()
  fireEvent.click(screen.getByRole('button', {name:'取消'}))
  await waitFor(()=>expect(screen.queryByRole('alertdialog')).toBeNull())
  expect(screen.getByRole('dialog')).toBeVisible()
  expect(screen.getByRole('button', { name: '删除决策问题 custom_1' })).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: '关闭详情' }))
  fireEvent.click(await screen.findByRole('button',{name:'确认'}))
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
    if (path === '/api/v1/intent/builtin-rules') return Promise.resolve(response(builtinCatalog))
    if (path === '/api/v1/intent/rules/effective') return Promise.resolve(response(effectiveRules))
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
    if (url.pathname === '/api/v1/intent/builtin-rules') return Promise.resolve(response(builtinCatalog))
    if (url.pathname === '/api/v1/intent/rules/effective') return Promise.resolve(response(effectiveRules))
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
    if (path === '/api/v1/intent/builtin-rules') return Promise.resolve(response(builtinCatalog))
    if (path === '/api/v1/intent/rules/effective') return Promise.resolve(response(effectiveRules))
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
  const props = { scope: { botId: 1, scopeType: 'private' as const, scopeId: 123 }, previewGlobal: false, questions: { directed_to_bot: { type: 'noul' as const, instructions: '是否在跟我说话', role: 'directed_to_bot' as const, weight: 1 } } }
  const { rerender } = render(<QueryClientProvider client={client}><SimulateCard {...props} previewTargetsText="private:123" /></QueryClientProvider>)
  fireEvent.change(screen.getByPlaceholderText('输入一条消息'), { target: { value: 'hello' } })
  fireEvent.click(screen.getByRole('button', { name: '运行规则预览' }))
  await waitFor(() => expect(body).toBeDefined())
  expect(body).not.toHaveProperty('entries')
  expect(body).not.toHaveProperty('questions')
  rerender(<QueryClientProvider client={client}><SimulateCard {...props} previewTargetsText="private:456" /></QueryClientProvider>)
  finish?.(response({ Blocked: false, Explicit: false, NeedSemantic: false, AlwaysReply: false, ForceReply: true, ReasonCode: 'preview_reason', RulesetVersion: 'draft' }))
  expect(await screen.findByText('内容已变化，请重新测试')).toBeVisible()
  expect(screen.queryByText('原因码：preview_reason')).toBeNull()
  expect(screen.getByRole('button', { name: '运行规则预览' })).toBeDisabled()
  client.clear()
})

it('allows rule-only saves with an empty question set and sends an empty questions map', async () => {
  vi.stubGlobal('ResizeObserver', class { observe = vi.fn(); unobserve = vi.fn(); disconnect = vi.fn() })
  const draft = { ...version(1, '空问题草稿', 123), Status: 'draft', Targets: [], Questions: {} }
  const writes: Record<string, unknown>[] = []
  vi.stubGlobal('fetch', vi.fn((input: string, init?: RequestInit) => {
    const path = new URL(String(input), 'http://localhost').pathname
    if (init?.method === 'PATCH') {
      writes.push(JSON.parse(String(init.body)))
      return Promise.resolve(response(draft))
    }
    if (path.endsWith('/rulesets')) return Promise.resolve(response({ Items: [draft] }))
    if (path.endsWith('/rulesets/1')) return Promise.resolve(response(draft))
    if (path.endsWith('/contacts') || path.endsWith('/groups')) return Promise.resolve(response({ Items: [], Total: 0, Page: 1, Limit: 20 }))
    if (path === '/api/v1/intent/builtin-rules') return Promise.resolve(response(builtinCatalog))
    if (path === '/api/v1/intent/rules/effective') return Promise.resolve(response(effectiveRules))
    throw new Error('Unexpected request ' + path)
  }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><RulesPage /></QueryClientProvider>)
  fireEvent.click(await screen.findByText(/空问题草稿/))
  expect(await screen.findByText('这个版本不做语义判断。')).toBeVisible()
  expect(screen.queryByRole('alert')).toBeNull()
  fireEvent.click(screen.getByRole('switch', { name: '适用于所有会话' }))
  fireEvent.change(screen.getByLabelText('规则适用会话'), { target: { value: 'private:123' } })
  const save = screen.getByRole('button', { name: '保存' })
  await waitFor(() => expect(save).toBeEnabled())
  fireEvent.click(save)
  await waitFor(() => expect(writes).toHaveLength(1))
  expect((writes[0] as { questions: unknown }).questions).toEqual({})
  expect(writes[0]).not.toHaveProperty('entries')
  expect(writes[0]).not.toHaveProperty('builtin_rules')
  client.clear()
})

it('blocks saving when a configured question set is invalid', async () => {
  vi.stubGlobal('ResizeObserver', class { observe = vi.fn(); unobserve = vi.fn(); disconnect = vi.fn() })
  const draft = {
    ...version(1, '非法问题草稿', 123),
    Status: 'draft',
    Targets: [],
    Questions: { directed_to_bot: { Type: 'noul', Instructions: '', Role: 'directed_to_bot', Weight: 1 } },
  }
  vi.stubGlobal('fetch', vi.fn((input: string) => {
    const path = new URL(String(input), 'http://localhost').pathname
    if (path.endsWith('/rulesets')) return Promise.resolve(response({ Items: [draft] }))
    if (path.endsWith('/rulesets/1')) return Promise.resolve(response(draft))
    if (path === '/api/v1/intent/builtin-rules') return Promise.resolve(response(builtinCatalog))
    if (path === '/api/v1/intent/rules/effective') return Promise.resolve(response(effectiveRules))
    throw new Error('Unexpected request ' + path)
  }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><RulesPage /></QueryClientProvider>)
  fireEvent.click(await screen.findByText(/非法问题草稿/))
  expect(await screen.findByLabelText('指向机器人 题目')).toBeVisible()
  expect(screen.getByRole('alert')).toHaveTextContent('指向机器人 的题目不能为空')
  expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
  client.clear()
})

it('adds a custom decision question that stays unsaved until its text is filled', async () => {
  vi.stubGlobal('ResizeObserver', class { observe = vi.fn(); unobserve = vi.fn(); disconnect = vi.fn() })
  const draft = { ...version(1, '内置五问草稿', 123), Status: 'draft', Targets: [], Questions: {} }
  vi.stubGlobal('fetch', vi.fn((input: string) => {
    const path = new URL(String(input), 'http://localhost').pathname
    if (path.endsWith('/rulesets')) return Promise.resolve(response({ Items: [draft] }))
    if (path.endsWith('/rulesets/1')) return Promise.resolve(response(draft))
    if (path === '/api/v1/intent/builtin-rules') return Promise.resolve(response(builtinCatalog))
    if (path === '/api/v1/intent/rules/effective') return Promise.resolve(response(effectiveRules))
    throw new Error('Unexpected request ' + path)
  }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><RulesPage /></QueryClientProvider>)
  fireEvent.click(await screen.findByText(/内置五问草稿/))
  expect(await screen.findByText('这个版本不做语义判断。')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: '添加决策问题' }))
  expect(await screen.findByLabelText('custom_1 题目')).toBeVisible()
  expect(screen.getByRole('alert')).toHaveTextContent('custom_1 的题目不能为空')
  client.clear()
})

it('saves edited decision questions and targets without entries or builtin switches', async () => {
  vi.stubGlobal('ResizeObserver', class { observe = vi.fn(); unobserve = vi.fn(); disconnect = vi.fn() })
  const draft = { ...version(1, '语义保存草稿', 123), Status: 'draft', Targets: [], Questions: draftQuestions }
  const writes: Record<string, unknown>[] = []
  vi.stubGlobal('fetch', vi.fn((input: string, init?: RequestInit) => {
    const path = new URL(String(input), 'http://localhost').pathname
    if (init?.method === 'PATCH') {
      writes.push(JSON.parse(String(init.body)))
      return Promise.resolve(response(draft))
    }
    if (path.endsWith('/rulesets')) return Promise.resolve(response({ Items: [draft] }))
    if (path.endsWith('/rulesets/1')) return Promise.resolve(response(draft))
    if (path === '/api/v1/intent/builtin-rules') return Promise.resolve(response(builtinCatalog))
    if (path === '/api/v1/intent/rules/effective') return Promise.resolve(response(effectiveRules))
    throw new Error('Unexpected request ' + path)
  }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><RulesPage /></QueryClientProvider>)
  fireEvent.click(await screen.findByText(/语义保存草稿/))
  const instructions = await screen.findByLabelText('指向机器人 题目')
  fireEvent.change(instructions, { target: { value: '更新后的指向描述' } })
  const save = screen.getByRole('button', { name: '保存' })
  await waitFor(() => expect(save).toBeEnabled())
  fireEvent.click(save)
  await waitFor(() => expect(writes).toHaveLength(1))
  const body = writes[0] as { targets: unknown; questions: Record<string, Record<string, unknown>> }
  expect(body).not.toHaveProperty('entries')
  expect(body).not.toHaveProperty('builtin_rules')
  expect(body).toHaveProperty('targets')
  expect(body.questions.directed_to_bot).toMatchObject({ Type: 'noul', Instructions: '更新后的指向描述', Role: 'directed_to_bot', Weight: 1, Order: 50, Threshold: 0.8, Action: 'reply', Scope: 'group' })
  expect(body.questions.intent).toMatchObject({ Type: 'choice', Role: 'intent', Criteria: { closing: '告别' }, Order: 10, Action: 'silent', Scope: 'all', Match: 'closing' })
  client.clear()
})

it('sends the current question set in the semantic simulate request body', async () => {
  let body: Record<string, unknown> | undefined
  vi.stubGlobal('fetch', vi.fn((_input: string, init?: RequestInit) => {
    if (init?.method === 'POST') {
      body = JSON.parse(String(init.body))
      return Promise.resolve(response({ Blocked: false, Explicit: false, NeedSemantic: false, AlwaysReply: false, ForceReply: true, ReasonCode: 'semantic', RulesetVersion: 'draft' }))
    }
    throw new Error('Unexpected request')
  }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const questions = {
    directed_to_bot: { type: 'noul' as const, instructions: '是否在跟我说话', role: 'directed_to_bot' as const, weight: 1 },
    intent: { type: 'choice' as const, instructions: '判断聊天意图', role: 'intent' as const, criteria: { question: '提问', chat: '闲聊' } },
  }
  render(
    <QueryClientProvider client={client}>
      <SimulateCard
        scope={{ botId: 1, scopeType: 'private', scopeId: 123 }}
        previewGlobal={false}
        previewTargetsText="private:123"
        questions={questions}
      />
    </QueryClientProvider>,
  )
  fireEvent.change(screen.getByLabelText('验证方式'), { target: { value: 'semantic' } })
  fireEvent.change(screen.getByPlaceholderText('输入一条消息'), { target: { value: 'hello' } })
  fireEvent.click(screen.getByRole('button', { name: '调用模型重放' }))
  await waitFor(() => expect(body).toBeDefined())
  expect((body as { questions: unknown }).questions).toEqual({
    directed_to_bot: { Type: 'noul', Instructions: '是否在跟我说话', Role: 'directed_to_bot', Weight: 1 },
    intent: { Type: 'choice', Instructions: '判断聊天意图', Role: 'intent', Criteria: { question: '提问', chat: '闲聊' } },
  })
  client.clear()
})
