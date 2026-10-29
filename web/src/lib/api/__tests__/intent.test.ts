import { describe, expect, it } from 'vitest'
import { ApiError } from '../client'
import { toDecisionDetail, toDecisionList } from '../adapters/intent'

const LIST = {
  Items: [
    {
      ID: 12,
      JobID: 34,
      BotID: 1341939164,
      ConversationID: '9a838f09-2909-4157-ac5c-3a07ca0c11a1',
      Decision: 'reply',
      ReasonCode: 'always_reply',
      Provider: 'jev',
      Model: 'jev-latest',
      RulesetVersion: 'builtin-v1',
      LatencyMS: 42,
      CreatedAt: '2026-10-29T12:00:00Z',
    },
  ],
  Limit: 20,
}

const DETAIL = {
  ...LIST.Items[0],
  Scores: { directed_to_bot: 0.72 },
  Steps: [
    { Index: 0, Phase: 'policy', ReasonCode: '', Detail: { reply_mode: 'always_reply' } },
    { Index: 1, Phase: 'final', ReasonCode: 'always_reply', Detail: { decision: 'reply' } },
  ],
}

describe('decision adapters', () => {
  it('parses a page of decisions', () => {
    const page = toDecisionList(LIST)
    expect(page.limit).toBe(20)
    expect(page.items).toHaveLength(1)
    expect(page.items[0]).toMatchObject({
      id: 12,
      jobId: 34,
      botId: 1341939164,
      decision: 'reply',
      reasonCode: 'always_reply',
      latencyMs: 42,
    })
  })

  it('parses detail with steps and scores', () => {
    const detail = toDecisionDetail(DETAIL)
    expect(detail.scores.directed_to_bot).toBeCloseTo(0.72)
    expect(detail.steps).toHaveLength(2)
    expect(detail.steps[0].detail.reply_mode).toBe('always_reply')
  })

  it('treats a missing step list as empty for legacy decisions', () => {
    const legacy = { ...LIST.Items[0], Steps: undefined, Scores: undefined }
    const detail = toDecisionDetail(legacy)
    expect(detail.steps).toEqual([])
    expect(detail.scores).toEqual({})
  })

  it('rejects an unknown decision kind instead of guessing', () => {
    const broken = { ...LIST, Items: [{ ...LIST.Items[0], Decision: 'shout' }] }
    expect(() => toDecisionList(broken)).toThrow(ApiError)
  })
})
