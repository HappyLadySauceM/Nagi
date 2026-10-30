import { describe, expect, it } from 'vitest'
import { DEFAULT_SCOPE_FORM } from '../../../lib/api/adapters/scope'
import { fromScopeFormValues, scopeFormSchema, toScopeFormValues } from '../schema'

const validValues = {
  maxMessagesPerRun: '0', maxCharsPerMessage: '4500', maxTotalCharsPerRun: '0', messageIntervalMs: '700',
  enabled: true,
  replyMode: 'mention_only',
  storeMessages: true,
  allowHistoryRead: false,
  allowMemoryWrite: false,
  minReplyIntervalMs: '10000',
  maxRepliesPerHour: '0',
  toolPermissions: {},
}

function messagesOf(input: unknown): string[] {
  const result = scopeFormSchema.safeParse(input)
  return result.success ? [] : result.error.issues.map((issue) => issue.message)
}

describe('scopeFormSchema', () => {
  it('accepts a complete policy', () => {
    expect(scopeFormSchema.safeParse(validValues).success).toBe(true)
  })

  it('accepts zero as unlimited for the sending limits', () => {
    expect(
      scopeFormSchema.safeParse({
        ...validValues,
        maxMessagesPerRun: '0',
        maxCharsPerMessage: '0',
        maxTotalCharsPerRun: '0',
        maxRepliesPerHour: '0',
      }).success,
    ).toBe(true)
    // An unlimited whole-round budget must not reject the single-message cap.
    expect(
      scopeFormSchema.safeParse({ ...validValues, maxCharsPerMessage: '4500', maxTotalCharsPerRun: '0' })
        .success,
    ).toBe(true)
  })

  it('rejects an empty numeric field instead of reading it as zero', () => {
    const messages = messagesOf({ ...validValues, minReplyIntervalMs: '' })
    expect(messages).toContain('最小回复间隔不能为空')
  })

  it('rejects decimals and negative numbers', () => {
    expect(messagesOf({ ...validValues, maxRepliesPerHour: '12.5' })).toContain(
      '每小时回复上限必须是非负整数',
    )
    expect(messagesOf({ ...validValues, maxMessagesPerRun: '-1' })).toContain(
      '每轮消息条数必须是非负整数',
    )
  })

  it('rejects values above the backend bound', () => {
    expect(messagesOf({ ...validValues, minReplyIntervalMs: '600001' })).toContain(
      '最小回复间隔不能超过 600000',
    )
  })

  it('rejects an unknown reply mode', () => {
    expect(scopeFormSchema.safeParse({ ...validValues, replyMode: 'sometimes' }).success).toBe(
      false,
    )
  })
})

describe('scope form conversion', () => {
  it('round-trips the default policy without losing values', () => {
    const values = toScopeFormValues(DEFAULT_SCOPE_FORM)
    expect(fromScopeFormValues(values)).toEqual(DEFAULT_SCOPE_FORM)
  })

  it('converts numeric strings on submit', () => {
    const form = fromScopeFormValues({
      ...toScopeFormValues(DEFAULT_SCOPE_FORM),
      maxMessagesPerRun: '5',
      minReplyIntervalMs: '5000',
      maxRepliesPerHour: '7',
    })

    expect(form.maxMessagesPerRun).toBe(5)
    expect(form.minReplyIntervalMs).toBe(5000)
    expect(form.maxRepliesPerHour).toBe(7)
    expect(form.maxCharsPerMessage).toBe(4500)
    expect(form.storeMessages).toBe(true)
  })

  it('keeps permission overrides across the form conversion', () => {
    const values = toScopeFormValues({
      ...DEFAULT_SCOPE_FORM,
      toolPermissions: { 'qq.group.read': false },
    })
    expect(fromScopeFormValues(values).toolPermissions).toEqual({ 'qq.group.read': false })
  })
})
