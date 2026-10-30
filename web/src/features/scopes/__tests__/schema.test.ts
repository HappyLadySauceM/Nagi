import { describe, expect, it } from 'vitest'
import { DEFAULT_SCOPE_FORM } from '../../../lib/api/adapters/scope'
import { fromScopeFormValues, scopeFormSchema, toScopeFormValues } from '../schema'

const validValues = {
 maxMessagesPerRun: '3', maxCharsPerMessage: '500', maxTotalCharsPerRun: '1500', messageIntervalMs: '700',
  enabled: true,
  replyMode: 'mention_only',
  storeMessages: false,
  allowHistoryRead: false,
  allowMemoryWrite: false,
  minReplyIntervalMs: '10000',
  maxRepliesPerHour: '20',
  retentionDays: '30',
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

  it('rejects an empty numeric field instead of reading it as zero', () => {
    const messages = messagesOf({ ...validValues, minReplyIntervalMs: '' })
    expect(messages).toContain('最小回复间隔不能为空')
  })

  it('rejects decimals and negative numbers', () => {
    expect(messagesOf({ ...validValues, maxRepliesPerHour: '12.5' })).toContain(
      '每小时回复上限必须是非负整数',
    )
    expect(messagesOf({ ...validValues, retentionDays: '-1' })).toContain(
      '留存天数必须是非负整数',
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
      minReplyIntervalMs: '5000',
      maxRepliesPerHour: '7',
      retentionDays: '14',
    })

    expect(form.minReplyIntervalMs).toBe(5000)
    expect(form.maxRepliesPerHour).toBe(7)
    expect(form.retentionDays).toBe(14)
  })

  it('keeps permission overrides across the form conversion', () => {
    const values = toScopeFormValues({
      ...DEFAULT_SCOPE_FORM,
      toolPermissions: { 'qq.group.read': false },
    })
    expect(fromScopeFormValues(values).toolPermissions).toEqual({ 'qq.group.read': false })
  })
})
