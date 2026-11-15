import { describe, expect, it } from 'vitest'
import {
  toManagedPromptDetail,
  toManagedPromptDiff,
  toManagedPromptList,
  toManagedPromptPreview,
} from '../../../lib/api/adapters/prompts'

const version = {
  ID: 12,
  PromptID: 'chat.reply',
  Kind: 'chat_reply',
  Name: '聊天回复',
  Provider: 'openai',
  BotID: 998001,
  ScopeType: 'account',
  ScopeID: 0,
  Version: 2,
  Status: 'published',
  Source: 'local',
  SourceRef: '',
  Hash: 'sha256:abc',
  Note: '',
  Content: { Provider: 'openai', OpenAI: { SystemPrompt: '你好' } },
  CreatedAt: '2026-11-15T08:00:00Z',
  PublishedAt: '2026-11-15T08:01:00Z',
  ArchivedAt: null,
}

describe('managed prompt adapters', () => {
  it('parses list layers and nullable provider fields', () => {
    const items = toManagedPromptList({
      Items: [
        {
          PromptID: 'chat.reply',
          Kind: 'chat_reply',
          Name: '聊天回复',
          Provider: 'openai',
          BuiltinVersion: 1,
          Layers: [
            {
              BotID: 998001,
              ScopeType: 'account',
              ScopeID: 0,
              ActiveVersion: 2,
              ActiveVersionID: 12,
              ActiveSource: 'local',
              Status: 'published',
              UpdatedAt: '2026-11-15T08:01:00Z',
            },
          ],
        },
      ],
    })
    expect(items[0].layers[0]).toMatchObject({ activeVersion: 2, activeSource: 'local' })
  })

  it('parses detail with builtin, active and archived timestamps', () => {
    const detail = toManagedPromptDetail({
      PromptID: 'chat.reply',
      Kind: 'chat_reply',
      Provider: 'openai',
      Builtin: {
        Version: 1,
        Status: 'builtin',
        Source: 'builtin',
        Hash: 'sha256:builtin',
        Content: { Provider: 'openai', OpenAI: { SystemPrompt: '内置' } },
      },
      Active: version,
      Versions: [
        version,
        {
          ...version,
          ID: 10,
          Version: 1,
          Status: 'archived',
          PublishedAt: '2026-11-01T08:00:00Z',
          ArchivedAt: null,
        },
      ],
    })
    expect(detail.builtin.hash).toBe('sha256:builtin')
    expect(detail.active?.id).toBe(12)
    expect(detail.versions[1].archivedAt).toBeNull()
  })

  it('parses diff lines and preview warnings', () => {
    const diff = toManagedPromptDiff({
      PromptID: 'chat.reply',
      Provider: 'openai',
      From: { Source: 'builtin', Version: 1, Status: 'builtin', Hash: 'a' },
      To: { ID: 13, Version: 3, Status: 'draft', Hash: 'b' },
      Changed: true,
      UnifiedDiff: '',
      Lines: [{ Op: '-', Text: '旧' }, { Op: '+', Text: '新' }],
    })
    expect(diff.lines.map((line) => line.op)).toEqual(['-', '+'])
    const preview = toManagedPromptPreview({
      PromptID: 'chat.reply',
      Kind: 'chat_reply',
      Provider: 'openai',
      PreviewMode: 'example',
      VersionID: 13,
      Layers: [],
      Final: { Content: { Provider: 'openai', OpenAI: { SystemPrompt: '你好' } }, Variables: { bot_name: 'Nagi' } },
      Warnings: ['示例数据'],
      PreviewHash: 'sha256:preview',
    })
    expect(preview.previewMode).toBe('example')
    expect(preview.final.variables.bot_name).toBe('Nagi')
    expect(preview.warnings).toEqual(['示例数据'])
  })
})
