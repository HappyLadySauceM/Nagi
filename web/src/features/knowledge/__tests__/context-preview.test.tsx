import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, apiRequest } from '../../../lib/api/client'
import { KnowledgePreviewCard } from '../components/context-preview'

vi.mock('../../../lib/api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api/client')>()
  return { ...actual, apiRequest: vi.fn() }
})

const mockedRequest = vi.mocked(apiRequest)

const preview = {
  PreviewMode: 'real',
  Target: { Type: 'group', ID: 700, ConversationID: '' },
  Defaults: { ChatSystem: '你是 Nagi', ExtractionSystem: '抽取记忆' },
  Prompts: {
    'chat.reply': {
      PromptID: 'chat.reply',
      Name: '聊天回复',
      Kind: 'chat_reply',
      Provider: 'openai',
      Version: 1,
      Source: 'builtin',
      Hash: 'sha256:hash',
      FinalHash: 'sha256:final',
      Layers: [{ Layer: 'account_default', Version: 1, Source: 'builtin' }],
      Content: { Provider: 'openai', OpenAI: { SystemPrompt: '你是 Nagi' } },
      Variables: { bot_name: 'Nagi', scope_type: 'group' },
    },
  },
  Context: {
    Tools: {
      Skipped: false,
      Reason: '',
      Policy: {
        Enabled: true,
        ReplyMode: 'intent_based',
        StoreMessages: true,
        AllowMemoryRead: true,
        AllowHistoryRead: false,
      },
      EffectivePermissions: ['memory.detail'],
      ExplicitOverrides: {},
    },
    Memory: { Skipped: true, Reason: 'conversation_not_found', Items: [] },
    History: { Skipped: true, Reason: 'conversation_not_found', Messages: [] },
  },
}

function renderCard() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <KnowledgePreviewCard botId={998001} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  mockedRequest.mockReset()
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('KnowledgePreviewCard', () => {
  it('shows a skipped 200 preview as incomplete instead of a full success', async () => {
    mockedRequest.mockResolvedValue(preview)
    renderCard()

    fireEvent.change(screen.getByLabelText('预览目标 ID'), { target: { value: '700' } })
    fireEvent.click(screen.getByRole('button', { name: '真实上下文预览' }))

    expect(await screen.findByText('接口返回 200，但这不是完整预览')).toBeVisible()
    expect(screen.getByText(/记忆已跳过/)).toBeVisible()
    expect(screen.getAllByText(/会话不存在或不属于该目标/).length).toBeGreaterThan(0)
    expect(screen.getByText(/Target.ConversationID 为空/)).toBeVisible()
    expect(mockedRequest).toHaveBeenCalledWith(
      '/api/v1/knowledge/preview',
      expect.objectContaining({
        method: 'POST',
        body: expect.objectContaining({
          bot_id: 998001,
          preview_mode: 'real',
          target_type: 'group',
          target_id: 700,
        }),
      }),
    )
  })

  it('marks a 400 real-preview rejection and never renders it as success', async () => {
    mockedRequest.mockRejectedValue(
      new ApiError({
        kind: 'invalid',
        endpoint: '/api/v1/knowledge/preview',
        status: 400,
        message: 'target_type must be group or private',
      }),
    )
    renderCard()

    fireEvent.change(screen.getByLabelText('预览目标 ID'), { target: { value: '700' } })
    fireEvent.click(screen.getByRole('button', { name: '真实上下文预览' }))

    expect(await screen.findByText('真实预览请求被拒绝（HTTP 400）')).toBeVisible()
    expect(screen.getByText(/必须提供 bot_id 与有效的 target_type/)).toBeVisible()
    expect(screen.queryByText('真实上下文预览：工具约束、记忆与历史均按当前授权读取并通过。')).toBeNull()
  })

  it('uses synthetic mode for the example button and sends no real target', async () => {
    mockedRequest.mockResolvedValue({
      ...preview,
      PreviewMode: 'example',
      Target: { Type: 'group', ID: 1000, ConversationID: '' },
      Context: {
        ...preview.Context,
        Memory: { Skipped: false, Reason: '', Items: [{ ID: 'm1', Visibility: 'private_user', OwnerKey: '1', Content: '示例记忆', Status: 'active' }] },
        History: { Skipped: false, Reason: '', Messages: [{ ID: 1, MessageID: 1, SenderID: 1, IsBot: false, Text: '示例消息' }] },
      },
    })
    renderCard()

    fireEvent.click(screen.getByRole('button', { name: '示例预览' }))

    expect(await screen.findByText('示例预览（合成数据）')).toBeVisible()
    expect(screen.getAllByText(/不读取真实内容/).length).toBeGreaterThan(0)
    expect(mockedRequest).toHaveBeenCalledWith(
      '/api/v1/knowledge/preview',
      expect.objectContaining({
        body: expect.objectContaining({ preview_mode: 'example', target_id: 0, conversation_id: '' }),
      }),
    )
  })
})
