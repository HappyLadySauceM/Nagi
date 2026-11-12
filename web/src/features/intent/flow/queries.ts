import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiRequest } from '../../../lib/api/client'
import {
  toIntentFlow,
  toIntentCost,
  toIntentRun,
  toPromptDetail,
  toPromptList,
  toPromptSyncResult,
  toPromptSyncStatus,
  toRecalculateResult,
  toReplayComparison,
  toReplayEstimate,
  toReplayEvaluation,
  toReplayEvaluationList,
} from '../../../lib/api/adapters/flow'
import type {
  IntentFlow,
  IntentRun,
  PromptDetail,
  PromptSummary,
  PromptSyncStatus,
  PromptSyncResult,
  ReplayComparison,
  ReplayEstimate,
  ReplayEvaluation,
  ReplayEvaluationMode,
  ReplayEvaluationSummary,
} from '../../../lib/api/contracts'
import { queryKeys } from '../../../lib/query-keys'

/** Loads the fixed read-only execution flow definition. */
export function useIntentFlow() {
  return useQuery({
    queryKey: queryKeys.flow,
    queryFn: ({ signal }) => apiRequest('/api/v1/intent/flow', { signal }).then(toIntentFlow),
  })
}

export interface ReplayModelResult {
  run: IntentRun
  mayIncur: boolean
  costEstimate?: string
  evaluationId?: number
}

export interface ReplayModelVariables {
  confirmationToken: string
  name?: string
  note?: string
}

/** Recalculates a stored decision from its immutable snapshot without model calls. */
export function useRecalculateDecision(decisionId: number | null) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: () =>
      apiRequest(`/api/v1/intent/decisions/${decisionId}/recalculate`, {
        method: 'POST',
      }).then(toRecalculateResult),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['intent', 'decisions'] })
      if (decisionId !== null) {
        void client.invalidateQueries({ queryKey: queryKeys.decision(decisionId) })
      }
    },
  })
}

/** Replays one stored input against the current semantic model; never sends. */
export function useReplayDecisionModel(decisionId: number | null) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (variables: ReplayModelVariables): Promise<ReplayModelResult> => {
      const raw = await apiRequest(`/api/v1/intent/decisions/${decisionId}/replay-model`, {
        method: 'POST',
        body: {
          ConfirmationToken: variables.confirmationToken,
          ...(variables.name ? { Name: variables.name } : {}),
          ...(variables.note ? { Note: variables.note } : {}),
        },
      })
      const record = raw as Record<string, unknown>
      const cost = toIntentCost(record.Cost)
      return {
        run: toIntentRun(record.Run),
        mayIncur: cost?.mayIncur ?? true,
        costEstimate: cost?.costEstimate ?? cost?.text,
        evaluationId: typeof record.EvaluationID === 'number' ? record.EvaluationID : undefined,
      }
    },
    onSuccess: () => {
      if (decisionId !== null) {
        void client.invalidateQueries({ queryKey: queryKeys.replayEvaluations(decisionId) })
      }
    },
  })
}

/** Quotes the server-side cost of a model replay before any external call. */
export function useReplayEstimate(decisionId: number | null) {
  return useMutation({
    mutationFn: (): Promise<ReplayEstimate> =>
      apiRequest(`/api/v1/intent/decisions/${decisionId}/replay-model/estimate`, {
        method: 'POST',
      }).then(toReplayEstimate),
  })
}

export function useSaveReplayEvaluation(decisionId: number | null) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (variables: {
      mode: ReplayEvaluationMode
      confirmationToken?: string
      name?: string
      note?: string
    }): Promise<ReplayEvaluation> =>
      apiRequest(`/api/v1/intent/decisions/${decisionId}/replay-evaluations`, {
        method: 'POST',
        body: {
          Mode: variables.mode,
          ...(variables.confirmationToken ? { ConfirmationToken: variables.confirmationToken } : {}),
          ...(variables.name ? { Name: variables.name } : {}),
          ...(variables.note ? { Note: variables.note } : {}),
        },
      }).then(toReplayEvaluation),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['intent', 'replay-evaluations', decisionId] })
      if (decisionId !== null) {
        void client.invalidateQueries({ queryKey: queryKeys.decision(decisionId) })
      }
    },
  })
}

export function useReplayEvaluations(decisionId: number | null) {
  return useQuery({
    queryKey: queryKeys.replayEvaluations(decisionId),
    enabled: decisionId !== null && decisionId > 0,
    queryFn: ({ signal }): Promise<ReplayEvaluationSummary[]> =>
      apiRequest(`/api/v1/intent/replay-evaluations?decision_id=${decisionId}&limit=20`, {
        signal,
      }).then(toReplayEvaluationList),
  })
}

export function useReplayEvaluation(id: number | null) {
  return useQuery({
    queryKey: queryKeys.replayEvaluation(id),
    enabled: id !== null && id > 0,
    queryFn: ({ signal }): Promise<ReplayEvaluation> =>
      apiRequest(`/api/v1/intent/replay-evaluations/${id}`, { signal }).then(toReplayEvaluation),
  })
}

export function useReplayComparison(id: number | null, againstId: number | null) {
  return useQuery({
    queryKey: queryKeys.replayComparison(id, againstId),
    enabled: id !== null && id > 0 && againstId !== null && againstId > 0,
    queryFn: ({ signal }): Promise<ReplayComparison> =>
      apiRequest(
        `/api/v1/intent/replay-evaluations/${id}/comparison?against_evaluation_id=${againstId}`,
        { signal },
      ).then(toReplayComparison),
  })
}

export function usePromptSyncStatus(promptId: string | null, provider: string) {
  return useQuery({
    queryKey: queryKeys.promptSyncStatus(promptId ?? '', provider),
    enabled: Boolean(promptId && provider),
    queryFn: ({ signal }): Promise<PromptSyncStatus> =>
      apiRequest(
        `/api/v1/intent/prompts/sync-status?prompt_id=${encodeURIComponent(promptId!)}&provider=${encodeURIComponent(provider)}`,
        { signal },
      ).then(toPromptSyncStatus),
  })
}

export function useSyncPrompt() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (variables: {
      promptId: string
      provider: string
      version?: number
      label?: string
    }): Promise<PromptSyncResult> =>
      apiRequest(`/api/v1/intent/prompts/${encodeURIComponent(variables.promptId)}/sync`, {
        method: 'POST',
        body: {
          Provider: variables.provider,
          ...(variables.version !== undefined ? { Version: variables.version } : {}),
          ...(variables.label ? { Label: variables.label } : {}),
        },
      }).then(toPromptSyncResult),
    onSuccess: (_result, variables) => {
      void client.invalidateQueries({ queryKey: ['intent', 'prompts'] })
      void client.invalidateQueries({ queryKey: ['intent', 'prompt', variables.promptId] })
      void client.invalidateQueries({
        queryKey: queryKeys.promptSyncStatus(variables.promptId, variables.provider),
      })
    },
  })
}

export function usePrompts() {
  return useQuery({
    queryKey: queryKeys.prompts,
    queryFn: ({ signal }) => apiRequest('/api/v1/intent/prompts', { signal }).then(toPromptList),
  })
}

export function usePromptDetail(promptId: string | null) {
  return useQuery({
    queryKey: queryKeys.prompt(promptId ?? ''),
    enabled: Boolean(promptId),
    queryFn: ({ signal }) =>
      apiRequest(`/api/v1/intent/prompts/${encodeURIComponent(promptId!)}`, { signal }).then(
        toPromptDetail,
      ),
  })
}

export function usePublishPromptVersion() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (versionId: number) =>
      apiRequest(`/api/v1/intent/prompts/versions/${versionId}/publish`, { method: 'POST' }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['intent', 'prompts'] })
      void client.invalidateQueries({ queryKey: ['intent', 'prompt'] })
    },
  })
}

export function useRollbackPromptVersion(promptId: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (version: number) =>
      apiRequest(`/api/v1/intent/prompts/${encodeURIComponent(promptId)}/rollback`, {
        method: 'POST',
        body: { version },
      }),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['intent', 'prompts'] })
      void client.invalidateQueries({ queryKey: ['intent', 'prompt'] })
    },
  })
}

export type { IntentFlow, PromptDetail, PromptSummary, PromptSyncStatus, PromptSyncResult }
