import { describe, expect, it } from 'vitest'
import {
  toIntentRun,
  toPromptSyncResult,
  toPromptSyncStatus,
  toReplayComparison,
  toReplayEstimate,
  toReplayEvaluation,
  toReplayEvaluationList,
} from '../adapters/flow'

const run = {
  DecisionID: 7,
  JobID: 9,
  RunID: 'run-1',
  FlowVersion: 'intent-flow-v1',
  Provider: 'jev',
  Model: 'decision-model',
  RulesetVersion: 'scope:1@1',
  SnapshotStatus: 'partial',
  MissingFields: [],
  Summary: { Decision: 'reply', ReasonCode: 'private_normal', ReasonText: '私聊正常回复' },
  Nodes: [],
  Edges: [],
  Delivery: { Outcome: 'completed', Reason: '', Denied: 0, Items: [] },
  Scores: {},
  LatencyMS: 10,
  CreatedAt: '2026-11-12T08:00:00Z',
}

describe('P1 prompt and replay adapters', () => {
  it('parses the prompt snapshot attached to a run', () => {
    const parsed = toIntentRun({
      ...run,
      Prompt: {
        ID: 'intent.participation',
        Name: '参与判断',
        Version: 2,
        Source: 'langfuse',
        SourceRef: 'production/v2',
        Hash: 'sha256:abc',
      },
    })
    expect(parsed.prompt).toEqual({
      id: 'intent.participation',
      name: '参与判断',
      version: 2,
      source: 'langfuse',
      sourceRef: 'production/v2',
      hash: 'sha256:abc',
    })
  })

  it('parses prompt sync status and candidates', () => {
    const parsed = toPromptSyncStatus({
      PromptID: 'intent.participation',
      Provider: 'jev',
      Configured: true,
      Status: 'ok',
      ReasonCode: '',
      Message: '同步成功',
      LastAttemptAt: '2026-11-12T08:00:00Z',
      LastSuccessAt: '2026-11-12T08:00:00Z',
      Active: { Version: 2, Source: 'langfuse', SourceRef: 'production', Status: 'published' },
      Candidates: [
        {
          ID: 9,
          Version: 3,
          Name: '远端候选',
          Hash: 'sha256:def',
          SourceRef: 'production/v3',
          CreatedAt: '2026-11-12T08:01:00Z',
        },
      ],
    })
    expect(parsed.status).toBe('ok')
    expect(parsed.active).toMatchObject({ version: 2, source: 'langfuse' })
    expect(parsed.candidates[0]).toMatchObject({ id: 9, version: 3, sourceRef: 'production/v3' })
  })

  it('parses a sync result candidate as a prompt version', () => {
    const parsed = toPromptSyncResult({
      PromptID: 'intent.participation',
      Provider: 'jev',
      RemoteName: '远端候选',
      RemoteVersion: 3,
      Status: 'candidate_created',
      Candidate: {
        ID: 9,
        PromptID: 'intent.participation',
        Name: '远端候选',
        Provider: 'jev',
        Version: 3,
        Status: 'draft',
        Source: 'langfuse',
        SourceRef: 'production/v3',
        Hash: 'sha256:def',
        Note: '',
        Content: {},
        CreatedAt: '2026-11-12T08:01:00Z',
        PublishedAt: '',
      },
    })
    expect(parsed.candidate).toMatchObject({ id: 9, status: 'draft', source: 'langfuse' })
  })

  it('parses a replay estimate with unknown cost', () => {
    const parsed = toReplayEstimate({
      DecisionID: 7,
      Provider: 'jev',
      Model: 'decision-model',
      SampleCount: 1,
      EstimatedExternalRequests: 1,
      TokenEstimate: { InputTokens: 100, OutputTokens: 40, TotalTokens: 140, Method: 'estimate' },
      Cost: { MayIncur: true, Known: false, Text: '费用未知' },
      ConfirmationToken: 'token-1',
      ExpiresAt: '2026-11-12T08:05:00Z',
      ParametersFingerprint: 'fingerprint-1',
    })
    expect(parsed).toMatchObject({
      decisionId: 7,
      estimatedExternalRequests: 1,
      tokenEstimate: { totalTokens: 140 },
      confirmationToken: 'token-1',
    })
    expect(parsed.cost?.known).toBe(false)
    expect(parsed.cost?.text).toBe('费用未知')
  })

  it('parses saved evaluations, list summaries and comparison differences', () => {
    const evaluation = toReplayEvaluation({
      ID: 1,
      DecisionID: 7,
      BotID: 998001,
      Mode: 'recalculate',
      Name: '评估 A',
      Note: '',
      Changed: true,
      Before: run,
      After: { ...run, Summary: { ...run.Summary, Decision: 'silent' } },
      Parameters: {
        Provider: 'jev', Model: 'decision-model', PromptID: 'intent.participation',
        PromptVersion: 2, PromptHash: 'sha256:abc', RulesetVersion: 'scope:1@1',
        SampleCount: 1, EstimatedExternalRequests: 0,
        TokenEstimate: { InputTokens: 20, OutputTokens: 0, TotalTokens: 20, Method: 'test' },
      },
      Cost: { MayIncur: false, Known: true, Text: '无外部调用' },
      Reproduction: { FullyReproduced: false, Reasons: ['prompt_current'] },
      CreatedAt: '2026-11-12T08:02:00Z',
    })
    expect(evaluation.changed).toBe(true)
    expect(evaluation.parameters).toMatchObject({
      promptVersion: 2,
      rulesetVersion: 'scope:1@1',
      estimatedExternalRequests: 0,
    })
    expect(evaluation.reproduction).toEqual({
      fullyReproduced: false,
      reasons: ['prompt_current'],
    })

    const list = toReplayEvaluationList({
      Items: [
        {
          ID: 1,
          DecisionID: 7,
          Mode: 'recalculate',
          Name: '评估 A',
          Changed: true,
          Summary: { Decision: 'silent', ReasonCode: '', ReasonText: '回复 → 沉默' },
          CreatedAt: '2026-11-12T08:02:00Z',
          Reproduction: { FullyReproduced: false, Reasons: ['prompt_current'] },
        },
      ],
      Limit: 20,
    })
    expect(list[0]).toMatchObject({ id: 1, mode: 'recalculate', summary: { decision: 'silent', reasonCode: '', reasonText: '回复 → 沉默' } })

    const comparison = toReplayComparison({
      EvaluationID: 2,
      Against: 'evaluation:1',
      Changed: true,
      Original: run,
      Replay: { ...run, Summary: { ...run.Summary, Decision: 'silent' } },
      Differences: [{ Field: 'Decision', Before: 'reply', After: 'silent' }],
      Reproduction: { FullyReproduced: true, Reasons: [] },
    })
    expect(comparison.differences).toEqual([{ field: 'Decision', before: 'reply', after: 'silent' }])
    expect(comparison.reproduction.fullyReproduced).toBe(true)
  })
})
