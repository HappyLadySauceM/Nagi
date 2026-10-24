import { describe, expect, it } from 'vitest'
import { ApiError } from '../client'
import { toMetricsSnapshot, sumCounts, summarizeMemories } from '../adapters/metrics'
import { toAccountsSnapshot, toPingState, toStatusSnapshot } from '../adapters/system'

describe('toStatusSnapshot', () => {
  it('maps the snake_case status payload to camelCase', () => {
    const snapshot = toStatusSnapshot({
      version: '0.3.0',
      uptime_seconds: 7325,
      echo_enabled: true,
      onebot: {
        connected: true,
        self_id: 10001,
        heartbeat_seen: true,
        last_heartbeat: '2026-10-24T12:00:00Z',
        heartbeat_interval_ms: 30000,
      },
      handlers: ['ping', 'echo'],
      handled_events: 42,
    })

    expect(snapshot).toEqual({
      version: '0.3.0',
      uptimeSeconds: 7325,
      echoEnabled: true,
      onebot: {
        connected: true,
        selfId: 10001,
        heartbeatSeen: true,
        lastHeartbeat: '2026-10-24T12:00:00Z',
        heartbeatIntervalMs: 30000,
      },
      handlers: ['ping', 'echo'],
      handledEvents: 42,
    })
  })

  it('keeps the account id undefined before the first login info arrives', () => {
    const snapshot = toStatusSnapshot({
      version: '0.3.0',
      uptime_seconds: 10,
      echo_enabled: false,
      onebot: { connected: false },
      handlers: [],
      handled_events: 0,
    })

    expect(snapshot.onebot.selfId).toBeUndefined()
  })

  it('rejects a payload without a handlers array', () => {
    expect(() =>
      toStatusSnapshot({
        version: '0.3.0',
        uptime_seconds: 10,
        onebot: { connected: true },
        handled_events: 0,
      }),
    ).toThrowError(ApiError)
  })
})

describe('toAccountsSnapshot', () => {
  it('keeps a known bot id and the database flag', () => {
    const snapshot = toAccountsSnapshot({ connected: true, db_configured: true, bot_id: 10001 })

    expect(snapshot).toEqual({ connected: true, dbConfigured: true, botId: 10001 })
  })

  it('omits an unknown bot id', () => {
    const snapshot = toAccountsSnapshot({ connected: false, db_configured: false })

    expect(snapshot).toEqual({ connected: false, dbConfigured: false })
    expect(snapshot.botId).toBeUndefined()
  })
})

describe('toPingState', () => {
  it('reads the probe status', () => {
    expect(toPingState({ status: 'ready' }, '/readyz')).toEqual({ status: 'ready' })
  })
})

describe('toMetricsSnapshot', () => {
  it('orders known statuses, zero-fills missing ones and sorts decisions', () => {
    const snapshot = toMetricsSnapshot({
      messages_24h: 128,
      intent_decisions_24h: [
        { decision: 'silent', reason_code: 'intent_low', count: 3 },
        { decision: 'reply', reason_code: 'explicit_mention', count: 9 },
      ],
      message_jobs: { done: 12, pending: 2, retrying: 4 },
      memories: { active: 5 },
    })

    expect(snapshot.messages24h).toBe(128)
    expect(snapshot.intentDecisions24h.map((item) => item.decision)).toEqual(['reply', 'silent'])
    expect(snapshot.messageJobs).toEqual([
      { key: 'pending', count: 2 },
      { key: 'processing', count: 0 },
      { key: 'done', count: 12 },
      { key: 'failed', count: 0 },
      { key: 'cancelled', count: 0 },
      { key: 'retrying', count: 4 },
    ])
    expect(snapshot.memories).toEqual([
      { key: 'candidate', count: 0 },
      { key: 'active', count: 5 },
      { key: 'superseded', count: 0 },
      { key: 'deleted', count: 0 },
    ])
    expect(sumCounts(snapshot.messageJobs)).toBe(18)
  })

  it('rejects a payload with non-numeric counters', () => {
    expect(() =>
      toMetricsSnapshot({
        messages_24h: 1,
        intent_decisions_24h: [],
        message_jobs: { done: '12' },
        memories: {},
      }),
    ).toThrowError(ApiError)
  })

  it('returns undefined sums when metrics are unavailable', () => {
    expect(sumCounts(undefined)).toBeUndefined()
    expect(summarizeMemories(undefined)).toBeUndefined()
  })

  it('excludes deleted memories from the KPI total', () => {
    const summary = summarizeMemories([
      { key: 'candidate', count: 1 },
      { key: 'active', count: 4 },
      { key: 'superseded', count: 2 },
      { key: 'deleted', count: 3 },
    ])

    expect(summary).toEqual({ total: 7, excludedDeleted: 3 })
  })
})
