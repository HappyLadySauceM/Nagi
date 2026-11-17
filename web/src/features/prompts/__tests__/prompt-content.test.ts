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

  it('fills the fixed five Jev questions when an empty account draft is opened', () => {
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

  it('rejects a missing fixed Jev question and forbidden supplement variables', () => {
    const missing = { ...jevContent, systemOne: { questions: {} } }
    expect(validatePromptContent('intent_participation', 'jev', 'account', missing)).toEqual(
      expect.arrayContaining(['缺少固定题目 指向机器人']),
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
