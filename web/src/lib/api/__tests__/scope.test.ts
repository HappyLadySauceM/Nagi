import { describe, expect, it } from 'vitest'
import { ApiError } from '../client'
import {
  DEFAULT_SCOPE_FORM,
  isScopeEqual,
  scopeDangerFlags,
  toScopeForm,
  toScopePolicy,
  toScopePolicyList,
  toScopePutBody,
} from '../adapters/scope'
import type { ScopeForm } from '../contracts'

const POLICY = {
  ScopeType: 'group',
  ScopeID: 555,
  Enabled: true,
  ReplyMode: 'mention_only',
  StoreMessages: true,
  AllowMemoryWrite: false,
  AllowHistoryRead: true,
  MinReplyIntervalMS: 10000,
  MaxRepliesPerHour: 20,
  RetentionDays: 30,
}

describe('scope adapter', () => {
  it('maps the PascalCase effective policy', () => {
    expect(toScopePolicy(POLICY)).toEqual({
      scopeType: 'group',
      scopeId: 555,
      enabled: true,
      replyMode: 'mention_only',
      storeMessages: true,
      allowMemoryWrite: false,
      allowHistoryRead: true,
      minReplyIntervalMs: 10000,
      maxRepliesPerHour: 20,
      retentionDays: 30,
    })
  })

  it('mirrors WithDefaults for a legacy row with an empty mode and zero numbers', () => {
    const policy = toScopePolicy({
      ...POLICY,
      ReplyMode: '',
      MinReplyIntervalMS: 0,
      MaxRepliesPerHour: 0,
      RetentionDays: 0,
    })

    expect(policy.replyMode).toBe('mention_only')
    expect(policy.minReplyIntervalMs).toBe(10000)
    expect(policy.maxRepliesPerHour).toBe(20)
    expect(policy.retentionDays).toBe(30)
  })

  it('rejects an unknown scope type', () => {
    expect(() => toScopePolicy({ ...POLICY, ScopeType: 'channel' })).toThrowError(ApiError)
  })

  it('rejects a policy without identity', () => {
    expect(() => toScopePolicy({ ...POLICY, ScopeType: '', ScopeID: 0 })).toThrowError(ApiError)
  })

  it('reads the stored policy list', () => {
    const list = toScopePolicyList([POLICY])
    expect(list).toHaveLength(1)
    expect(list[0].scopeId).toBe(555)
  })

  it('always sends every snake_case field on PUT', () => {
    const body = toScopePutBody({
      enabled: false,
      replyMode: 'observe_only',
      storeMessages: false,
      allowHistoryRead: false,
      allowMemoryWrite: false,
      minReplyIntervalMs: 10000,
      maxRepliesPerHour: 20,
      retentionDays: 30,
    })

    expect(Object.keys(body).sort()).toEqual(
      [
        'allow_history_read',
        'allow_memory_write',
        'enabled',
        'max_replies_per_hour',
        'min_reply_interval_ms',
        'reply_mode',
        'retention_days',
        'store_messages',
      ].sort(),
    )
    expect(body.reply_mode).toBe('observe_only')
  })

  it('round-trips a policy into the form shape', () => {
    const form = toScopeForm(toScopePolicy(POLICY))
    expect(form.replyMode).toBe('mention_only')
    expect(form.storeMessages).toBe(true)
  })

  it('detects unchanged and changed forms', () => {
    const base = toScopeForm(toScopePolicy(POLICY))
    expect(isScopeEqual(base, { ...base })).toBe(true)
    expect(isScopeEqual(base, { ...base, replyMode: 'always_reply' })).toBe(false)
  })

  it('lists the risky capabilities that need confirmation', () => {
    const baseline: ScopeForm = { ...DEFAULT_SCOPE_FORM }
    const flags = scopeDangerFlags({ ...baseline, storeMessages: true }, baseline)

    expect(flags).toHaveLength(1)
    expect(flags[0]).toContain('消息内容')
  })
})
