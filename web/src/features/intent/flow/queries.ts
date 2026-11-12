import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiRequest } from '../../../lib/api/client'
import {
  toIntentFlow,
  toIntentCost,
  toIntentRun,
  toPromptDetail,
  toPromptList,
  toRecalculateResult,
} from '../../../lib/api/adapters/flow'
import type { IntentFlow, IntentRun, PromptDetail, PromptSummary } from '../../../lib/api/contracts'
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
  return useMutation({
    mutationFn: async (): Promise<ReplayModelResult> => {
      const raw = await apiRequest(`/api/v1/intent/decisions/${decisionId}/replay-model`, {
        method: 'POST',
      })
      const record = raw as Record<string, unknown>
      const cost = toIntentCost(record.Cost)
      return {
        run: toIntentRun(record.Run),
        mayIncur: cost?.mayIncur ?? true,
        costEstimate: cost?.costEstimate,
      }
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

export type { IntentFlow, PromptDetail, PromptSummary }
