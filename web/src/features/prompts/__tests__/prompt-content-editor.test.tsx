import { fireEvent, render, screen } from '@testing-library/react'
import type { MutableRefObject } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { PromptEditor, PromptContentView, type InsertToken } from '../prompt-content-editor'
import { JEV_QUESTIONS } from '../prompt-content'
import type { ManagedPromptContent } from '../../../lib/api/contracts'

const jevContent: ManagedPromptContent = {
  provider: 'jev',
  systemOne: {
    questions: Object.fromEntries(
      JEV_QUESTIONS.map((question) => [
        question.key,
        { type: question.type, instructions: '', ...(question.criteria ? { criteria: question.criteria } : {}) },
      ]),
    ),
  },
}

describe('PromptEditor', () => {
  it('renders the default five Jev questions and edits their descriptions', () => {
    const onChange = vi.fn()
    render(
      <PromptEditor
        kind="intent_participation"
        provider="jev"
        scope="account"
        content={jevContent}
        onChange={onChange}
      />,
    )
    for (const question of JEV_QUESTIONS) {
      expect(screen.getByText(question.label)).toBeVisible()
      expect(screen.getByLabelText(`${question.label} Instructions`)).toBeVisible()
    }
    const criteria = screen.getByLabelText('聊天功能 question 描述')
    fireEvent.change(criteria, { target: { value: '需要帮助或提出问题时' } })
    expect(onChange).toHaveBeenCalled()
    expect(screen.queryByLabelText('directed_to_bot key')).toBeNull()
  })

  it('adds a custom question and deletes it without a builtin warning', () => {
    const onChange = vi.fn()
    const content: ManagedPromptContent = {
      provider: 'jev',
      systemOne: { questions: { directed_to_bot: { type: 'noul', instructions: '是否对机器人说' } } },
    }
    const { rerender } = render(
      <PromptEditor kind="intent_participation" provider="jev" scope="account" content={content} onChange={onChange} />,
    )
    fireEvent.change(screen.getByLabelText('添加问题类型'), { target: { value: 'custom' } })
    fireEvent.click(screen.getByRole('button', { name: '添加' }))
    expect(onChange).toHaveBeenCalled()
    const withCustom = onChange.mock.calls[0][0] as ManagedPromptContent
    expect(Object.keys(withCustom.systemOne?.questions ?? {})).toContain('custom_1')

    onChange.mockClear()
    rerender(
      <PromptEditor
        kind="intent_participation"
        provider="jev"
        scope="account"
        content={withCustom}
        onChange={onChange}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: '删除 自定义问题（custom_1）' }))
    const afterDelete = onChange.mock.calls[0][0] as ManagedPromptContent
    expect(Object.keys(afterDelete.systemOne?.questions ?? {})).not.toContain('custom_1')
    expect(screen.queryByText(/^删除「/)).toBeNull()
  })

  it('warns before deleting a standard role', () => {
    const onChange = vi.fn()
    render(
      <PromptEditor
        kind="intent_participation"
        provider="jev"
        scope="account"
        content={jevContent}
        onChange={onChange}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: '删除 指向机器人' }))
    expect(screen.getByText('删除「指向机器人」后对应内置规则不再生效。')).toBeVisible()
    expect(onChange).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: '确认删除' }))
    const next = onChange.mock.calls[0][0] as ManagedPromptContent
    expect(next.systemOne?.questions?.directed_to_bot).toBeUndefined()
  })

  it('renames a custom question key', () => {
    const onChange = vi.fn()
    const content: ManagedPromptContent = {
      provider: 'jev',
      systemOne: { questions: { custom_1: { type: 'noul', instructions: '', role: 'custom', weight: 1 } } },
    }
    render(
      <PromptEditor kind="intent_participation" provider="jev" scope="account" content={content} onChange={onChange} />,
    )
    fireEvent.change(screen.getByLabelText('自定义问题 key'), { target: { value: 'my_topic' } })
    const next = onChange.mock.calls[0][0] as ManagedPromptContent
    expect(Object.keys(next.systemOne?.questions ?? {})).toEqual(['my_topic'])
  })

  it('limits chat supplement editing to append-only system text', () => {
    render(
      <PromptEditor
        kind="chat_reply"
        provider="openai"
        scope="group"
        content={{ provider: 'openai', openai: { systemPrompt: '' } }}
        onChange={() => undefined}
      />,
    )
    expect(screen.getByText('System Prompt 补充文本')).toBeVisible()
    expect(screen.queryByLabelText('UserTemplate')).toBeNull()
    expect(screen.getByText(/不覆盖账号默认内容/)).toBeVisible()
  })

  it('inserts a token into the focused textarea through insertRef', () => {
    const insertRef: MutableRefObject<InsertToken | null> = { current: null }
    const onChange = vi.fn()
    render(
      <PromptEditor
        kind="chat_reply"
        provider="openai"
        scope="account"
        content={{ provider: 'openai', openai: { systemPrompt: '你好 ' } }}
        onChange={onChange}
        insertRef={insertRef}
      />,
    )
    fireEvent.focus(screen.getByLabelText('System Prompt'))
    expect(insertRef.current).not.toBeNull()
    expect(insertRef.current!('{{user_name}}')).toBe(true)
    expect(onChange).toHaveBeenCalled()
    const next = onChange.mock.calls[0][0] as ManagedPromptContent
    expect(next.openai?.systemPrompt).toContain('{{user_name}}')
  })

  it('routes messages_json into UserTemplate even when System Prompt is focused', () => {
    const insertRef: MutableRefObject<InsertToken | null> = { current: null }
    const onChange = vi.fn()
    render(
      <PromptEditor
        kind="memory_extraction"
        provider="openai"
        scope="account"
        content={{ provider: 'openai', openai: { systemPrompt: '抽取 {{bot_name}}', userTemplate: '' } }}
        onChange={onChange}
        insertRef={insertRef}
      />,
    )
    fireEvent.focus(screen.getByLabelText('System Prompt'))
    expect(insertRef.current!('{{messages_json}}')).toBe(true)
    const next = onChange.mock.calls[0][0] as ManagedPromptContent
    expect(next.openai?.userTemplate).toContain('{{messages_json}}')
    expect(next.openai?.systemPrompt).toBe('抽取 {{bot_name}}')
  })
})

describe('PromptContentView', () => {
  it('shows all five fixed question keys in read-only mode', () => {
    render(<PromptContentView kind="intent_participation" provider="jev" content={jevContent} />)
    for (const question of JEV_QUESTIONS) {
      expect(screen.getByText(question.key)).toBeVisible()
    }
  })
})
