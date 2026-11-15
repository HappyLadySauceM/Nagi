import { describe, expect, it } from 'vitest'
import { ApiError } from '../client'
import {
  toExtractionJobPage,
  toExtractionSettings,
  toKnowledgePreview,
  toKnowledgeSummary,
} from '../adapters/knowledge'

const SUMMARY = {
  Memories: {
    Total: 19,
    Active: 12,
    Candidate: 5,
    Superseded: 2,
    ByVisibility: { bot_shared: 3, group: 8, private_user: 5, conversation: 3 },
  },
  Extraction: {
    Enabled: false,
    PendingJobs: 1,
    ProcessingJobs: 2,
    FailedJobs: 3,
    LastErrorCode: 'model_timeout',
  },
  Prompts: {
    'chat.reply': { ActiveVersion: 1, Source: 'builtin' },
    'intent.participation': { ActiveVersion: 3, Source: 'local' },
    'memory.extraction': { ActiveVersion: 1, Source: 'builtin' },
  },
}

const PREVIEW = {
  PreviewMode: 'real',
  Target: { Type: 'group', ID: 700, ConversationID: '' },
  Defaults: {
    ChatSystem: '你是 {{bot_name}}',
    ExtractionSystem: '抽取 {{scope_type}} 记忆',
  },
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
      Layers: [{ Layer: 'account_default', Version: 1, Source: 'builtin', Hash: 'sha256:hash' }],
      Content: { Provider: 'openai', OpenAI: { SystemPrompt: '你是 {{bot_name}}' } },
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
      EffectivePermissions: ['memory.detail', 'qq.reply.current'],
      ExplicitOverrides: {},
    },
    Memory: { Skipped: true, Reason: 'conversation_not_found', Items: [] },
    History: { Skipped: true, Reason: 'conversation_not_found', Messages: [] },
  },
}

const SETTINGS = {
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

const JOB = {
  ID: 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee',
  BotID: 998001,
  ConversationID: 'conv-1',
  ScopeType: 'group',
  ScopeID: 700,
  MessageID: 123,
  Status: 'failed',
  Attempts: 3,
  RoundAttempts: 1,
  NextRunAt: '2026-11-15T08:00:00Z',
  LastErrorCode: 'model_timeout',
  ManualRetries: 0,
  SourceAvailable: false,
  CreatedAt: '2026-11-15T07:00:00Z',
  UpdatedAt: '2026-11-15T07:30:00Z',
}

describe('knowledge adapter', () => {
  it('maps the count-only knowledge summary', () => {
    const summary = toKnowledgeSummary(SUMMARY)

    expect(summary.memories.total).toBe(19)
    expect(summary.memories.byVisibility.conversation).toBe(3)
    expect(summary.extraction.enabled).toBe(false)
    expect(summary.extraction.lastErrorCode).toBe('model_timeout')
    expect(summary.prompts['intent.participation']).toEqual({ activeVersion: 3, source: 'local' })
  })

  it('keeps skipped memory and history layers explicit', () => {
    const preview = toKnowledgePreview(PREVIEW)

    expect(preview.previewMode).toBe('real')
    expect(preview.target).toEqual({ type: 'group', id: 700, conversationId: '' })
    expect(preview.tools.policy.allowHistoryRead).toBe(false)
    expect(preview.memory.skipped).toBe(true)
    expect(preview.memory.reason).toBe('conversation_not_found')
    expect(preview.history.skipped).toBe(true)
    expect(preview.prompts['chat.reply'].layers[0].layer).toBe('account_default')
  })

  it('rejects an unknown preview mode', () => {
    expect(() => toKnowledgePreview({ ...PREVIEW, PreviewMode: 'live' })).toThrowError(ApiError)
  })

  it('maps global read-only extraction settings', () => {
    const settings = toExtractionSettings(SETTINGS)

    expect(settings.scope).toBe('global')
    expect(settings.enabled).toBe(false)
    expect(settings.batchMaxMessages).toBe(10)
    expect(settings.retryDelaysMs).toEqual([30000, 120000])
    expect(settings.editable).toBe(false)
  })

  it('maps the lowercase extraction job page and source availability', () => {
    const page = toExtractionJobPage(
      { items: [JOB], total: 1, page: 2, limit: 50 },
      { page: 1, limit: 20 },
    )

    expect(page.page).toBe(2)
    expect(page.limit).toBe(50)
    expect(page.items[0].status).toBe('failed')
    expect(page.items[0].sourceAvailable).toBe(false)
    expect(page.items[0].manualRetries).toBe(0)
  })

  it('falls back to the requested extraction job paging when the handler echoes zeros', () => {
    const page = toExtractionJobPage(
      { items: [], total: 0, page: 0, limit: 0 },
      { page: 3, limit: 50 },
    )

    expect(page.page).toBe(3)
    expect(page.limit).toBe(50)
  })
})
