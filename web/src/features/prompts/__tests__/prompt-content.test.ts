import { describe, expect, it } from 'vitest'
import {
  JEV_QUESTIONS,
  extractVariables,
  normaliseContentForScope,
  promptContentToWire,
  validatePromptContent,
} from '../prompt-content'
import type { ManagedPromptContent } from '../../../lib/api/contracts'

const jevContent: ManagedPromptContent = {
  provider: 'jev',
  systemOne: {
    questions: Object.fromEntries(
      JEV_QUESTIONS.map((question) => [
        question.key,
        {
          type: question.type,
          instructions: '',
          ...(question.criteria ? { criteria: question.criteria } : {}),
        },
      ]),
    ),
  },
}

describe('prompt content helpers', () => {
  it('extracts only explicit variables', () => {
    expect(extractVariables('你好 {{bot_name}}，当前 {{scope_type}} {{unknown}}')).toEqual([
      'bot_name',
      'scope_type',
      'unknown',
    ])
  })

  it('serialises supplement Jev content without Type or Criteria', () => {
    const supplement = normaliseContentForScope('intent_participation', 'jev', 'group', jevContent)
    const wire = promptContentToWire(supplement, 'group')
    const systemOne = wire.SystemOne as { Questions: Record<string, Record<string, unknown>> }
    for (const question of JEV_QUESTIONS) {
      expect(systemOne.Questions[question.key]).toEqual({
        Instructions: '',
      })
    }
    expect(systemOne.Questions.intent.Criteria).toBeUndefined()
    expect(systemOne.Questions.intent.Type).toBeUndefined()
  })

  it('seeds the default five Jev questions when an empty account draft is opened', () => {
    const normalised = normaliseContentForScope('intent_participation', 'jev', 'account', {
      provider: 'jev',
      systemOne: { questions: {} },
    })
    const questions = normalised.systemOne?.questions ?? {}
    expect(Object.keys(questions)).toEqual(JEV_QUESTIONS.map((question) => question.key))
    expect(questions.intent.criteria).toEqual(JEV_QUESTIONS[4].criteria)
    expect(questions.directed_to_bot.type).toBe('noul')
  })

  it('keeps account Jev Type and Criteria in the wire shape', () => {
    const wire = promptContentToWire(jevContent, 'account')
    const systemOne = wire.SystemOne as { Questions: Record<string, Record<string, unknown>> }
    expect(systemOne.Questions.intent.Type).toBe('choice')
    expect(systemOne.Questions.intent.Criteria).toEqual(JEV_QUESTIONS[4].criteria)
  })

  it('rejects an empty Jev account question set and forbidden supplement variables', () => {
    const missing = { ...jevContent, systemOne: { questions: {} } }
    expect(validatePromptContent('intent_participation', 'jev', 'account', missing)).toEqual(
      expect.arrayContaining(['至少需要 1 个问题']),
    )
    const supplement: ManagedPromptContent = {
      provider: 'jev',
      systemOne: {
        questions: {
          directed_to_bot: { type: 'noul', instructions: '{{state}} 不应出现在补充层' },
        },
      },
    }
    expect(validatePromptContent('intent_participation', 'jev', 'group', supplement)).toEqual(
      expect.arrayContaining(['补充层不能使用 {{state}}']),
    )
  })

  it('keeps stored questions and infers missing Role and Weight', () => {
    const stored: ManagedPromptContent = {
      provider: 'jev',
      systemOne: {
        questions: {
          directed_to_bot: { type: 'noul', instructions: '是否对机器人说' },
          addressed_to_other: { type: 'noul', instructions: '是否在和别人说' },
          my_topic: { type: 'noul', instructions: '自定义主题' },
          intent: { type: 'choice', instructions: '聊天功能', criteria: { closing: '告别' } },
        },
      },
    }
    const normalised = normaliseContentForScope('intent_participation', 'jev', 'account', stored)
    const questions = normalised.systemOne?.questions ?? {}
    expect(Object.keys(questions)).toEqual(['directed_to_bot', 'addressed_to_other', 'my_topic', 'intent'])
    expect(questions.directed_to_bot.role).toBe('directed_to_bot')
    expect(questions.directed_to_bot.weight).toBe(1)
    expect(questions.addressed_to_other.weight).toBe(-1)
    expect(questions.my_topic.role).toBe('custom')
    expect(questions.my_topic.weight).toBe(1)
    expect(questions.intent.role).toBe('intent')
    expect(questions.intent.weight).toBeUndefined()
    expect(questions.intent.criteria).toEqual({ closing: '告别', question: '求助或提问', chat: '普通闲聊', command: '要求执行动作', other: '其它' })
  })

  it('serialises Role and Weight for account questions', () => {
    const wire = promptContentToWire(
      normaliseContentForScope('intent_participation', 'jev', 'account', {
        provider: 'jev',
        systemOne: { questions: { directed_to_bot: { type: 'noul', instructions: 'x' } } },
      }),
      'account',
    )
    const systemOne = wire.SystemOne as { Questions: Record<string, Record<string, unknown>> }
    expect(systemOne.Questions.directed_to_bot).toMatchObject({ Role: 'directed_to_bot', Weight: 1, Type: 'noul' })
    expect(systemOne.Questions.directed_to_bot).not.toHaveProperty('Criteria')
  })

  it('validates question count, unique roles, weight range and intent criteria', () => {
    const baseQuestion = { type: 'noul' as const, instructions: 'x', role: 'directed_to_bot' as const, weight: 1 }
    const duplicate: ManagedPromptContent = {
      provider: 'jev',
      systemOne: {
        questions: {
          directed_to_bot: baseQuestion,
          directed_again: { ...baseQuestion, role: 'directed_to_bot' },
        },
      },
    }
    expect(validatePromptContent('intent_participation', 'jev', 'account', duplicate)).toEqual(
      expect.arrayContaining(['Role 指向机器人 最多只能有一个问题']),
    )

    const badWeight: ManagedPromptContent = {
      provider: 'jev',
      systemOne: { questions: { directed_to_bot: { ...baseQuestion, weight: 11 } } },
    }
    expect(validatePromptContent('intent_participation', 'jev', 'account', badWeight)).toEqual(
      expect.arrayContaining(['题目 指向机器人 的 Weight 必须在 -10 到 10 之间']),
    )

    const intentWithoutClosing: ManagedPromptContent = {
      provider: 'jev',
      systemOne: {
        questions: { intent: { type: 'choice', instructions: '功能', role: 'intent', criteria: { chat: '闲聊' } } },
      },
    }
    expect(validatePromptContent('intent_participation', 'jev', 'account', intentWithoutClosing)).toEqual(
      expect.arrayContaining(['intent 问题必须包含 closing 分类']),
    )

    const tooMany: ManagedPromptContent = {
      provider: 'jev',
      systemOne: {
        questions: Object.fromEntries(
          Array.from({ length: 21 }, (_, index) => [`q_${index}`, { type: 'noul' as const, instructions: 'x', role: 'custom' as const }]),
        ),
      },
    }
    expect(validatePromptContent('intent_participation', 'jev', 'account', tooMany)).toEqual(
      expect.arrayContaining(['问题数量不能超过 20 个']),
    )
  })

  it('validates required templates exactly once', () => {
    const content: ManagedPromptContent = {
      provider: 'openai',
      openai: {
        systemPrompt: '你是 {{bot_name}}',
        userTemplate: '{{state}} {{question}}',
        responseShape: 'single-letter',
      },
    }
    expect(validatePromptContent('intent_participation', 'openai', 'account', content)).toEqual([])
    const bad = {
      ...content,
      openai: { ...content.openai, userTemplate: '{{state}} {{state}}' },
    }
    expect(validatePromptContent('intent_participation', 'openai', 'account', bad)).toEqual(
      expect.arrayContaining(['UserTemplate 必须且只能包含一次 {{question}}']),
    )
    const extraction: ManagedPromptContent = {
      provider: 'openai',
      openai: { systemPrompt: '抽取', userTemplate: '消息：{{messages_json}}' },
    }
    expect(validatePromptContent('memory_extraction', 'openai', 'account', extraction)).toEqual([])
  })

  it('allows the runtime variables target_id, sender_id and bot_id in System Prompt', () => {
    const content: ManagedPromptContent = {
      provider: 'openai',
      openai: { systemPrompt: '{{bot_name}} {{scope_type}} {{target_id}} {{sender_id}} {{bot_id}}' },
    }
    expect(validatePromptContent('chat_reply', 'openai', 'account', content)).toEqual([])
    const unknown = {
      ...content,
      openai: { systemPrompt: '{{bot_name}} {{chat_id}}' },
    }
    expect(validatePromptContent('chat_reply', 'openai', 'account', unknown)).toEqual([
      'System Prompt 包含未识别变量 {{chat_id}}',
    ])
  })

  it('allows custom variables in System Prompt only when passed as extraVariables', () => {
    const content: ManagedPromptContent = {
      provider: 'openai',
      openai: { systemPrompt: '你好 {{bot_name}}，用户 {{user_name}}' },
    }
    expect(validatePromptContent('chat_reply', 'openai', 'account', content)).toEqual([
      'System Prompt 包含未识别变量 {{user_name}}',
    ])
    expect(validatePromptContent('chat_reply', 'openai', 'account', content, ['user_name'])).toEqual([])
  })
})
