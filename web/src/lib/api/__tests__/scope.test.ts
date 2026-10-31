import { describe, expect, it } from 'vitest'
import { ApiError } from '../client'
import {
  isScopeEqual,
  toScopeForm,
  toScopePolicy,
  toScopePolicyList,
  toScopePutBody,
} from '../adapters/scope'

const POLICY = {
  ScopeType: 'group',
  ScopeID: 555,
  Enabled: true,
  ReplyMode: 'mention_only',
  StoreMessages: true,
  AllowMemoryWrite: false,
  AllowHistoryRead: true,
  MinReplyIntervalMS: 10000,
  MaxRepliesPerHour: 0,
  ToolPermissions: { 'qq.group.read': false },
}

describe('scope adapter', () => {
  it('reads a default template and its version', () => {
    expect(toScopePolicy({ ...POLICY, ScopeID: 0, Source: 'default', Version: 4, TemplateVersion: 4 })).toMatchObject({
      scopeId: 0, source: 'default', version: 4, templateVersion: 4,
    })
  })

  it('maps the PascalCase effective policy', () => {
    expect(toScopePolicy(POLICY)).toEqual({
      scopeType: 'group',
      scopeId: 555,
      maxMessagesPerRun: 0, maxCharsPerMessage: 4500, maxTotalCharsPerRun: 0, messageIntervalMs: 700,
      source: 'independent',
      version: 0,
      templateVersion: 0,
      enabled: true,
      replyMode: 'mention_only',
      storeMessages: true,
      allowMemoryWrite: false,
      allowHistoryRead: true,
      minReplyIntervalMs: 10000,
      maxRepliesPerHour: 0,
      toolPermissions: { 'qq.group.read': false },
    })
  })

  it('treats a nil override map as "no overrides"', () => {
    expect(toScopePolicy({ ...POLICY, ToolPermissions: null }).toolPermissions).toEqual({})
  })

  it('rejects non-boolean overrides instead of guessing', () => {
    expect(() =>
      toScopePolicy({ ...POLICY, ToolPermissions: { 'qq.group.read': 'no' } }),
    ).toThrowError(ApiError)
  })

  it('mirrors WithDefaults for a legacy row with an empty mode and zero numbers', () => {
    const policy = toScopePolicy({
      ...POLICY,
      ReplyMode: '',
      MinReplyIntervalMS: 0,
      MaxRepliesPerHour: 0,
    })

    expect(policy.replyMode).toBe('mention_only')
    expect(policy.minReplyIntervalMs).toBe(10000)
    expect(policy.maxRepliesPerHour).toBe(0)
  })

  it('preserves explicit zero cooldown and hourly limit', () => {
    const policy = toScopePolicy({ ...POLICY, MinReplyIntervalMS: 0, MaxRepliesPerHour: 0 })
    expect(policy.minReplyIntervalMs).toBe(0)
    expect(policy.maxRepliesPerHour).toBe(0)
    expect(toScopePutBody(toScopeForm(policy))).toMatchObject({ min_reply_interval_ms: 0, max_replies_per_hour: 0 })
  })

  it('forces message storage on even when a stale row says off', () => {
    const policy = toScopePolicy({ ...POLICY, StoreMessages: false })
    expect(policy.storeMessages).toBe(true)
    expect(toScopePutBody(toScopeForm(policy)).store_messages).toBe(true)
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
      maxRepliesPerHour: 0,
      toolPermissions: {},
    })

    expect(Object.keys(body).sort()).toEqual(
      [
        'allow_history_read',
        'allow_memory_write',
        'enabled',
        'max_replies_per_hour',
        'max_messages_per_run', 'max_chars_per_message', 'max_total_chars_per_run', 'message_interval_ms',
        'min_reply_interval_ms',
        'reply_mode',
        'store_messages',
        'tool_permissions',
      ].sort(),
    )
    expect(body.reply_mode).toBe('observe_only')
    expect(body.store_messages).toBe(true)
    expect(body.max_chars_per_message).toBe(4500)
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

  it('treats permission overrides as part of form equality', () => {
    const base = toScopeForm(toScopePolicy(POLICY))
    expect(isScopeEqual(base, { ...base, toolPermissions: { 'qq.group.read': true } })).toBe(false)
  })


})
