import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiRequest } from '../../lib/api/client'
import { toRuleset, toRulesetList, toRuleSimulation } from '../../lib/api/adapters/rules'
import type { RuleEntry, ScopeType } from '../../lib/api/contracts'
import { queryKeys } from '../../lib/query-keys'

export interface ScopeRef {
  botId: number
  scopeType: ScopeType
  scopeId: number
}

export function useRulesets(scope: ScopeRef | null) {
  return useQuery({
    queryKey: queryKeys.rulesets(scope),
    enabled: scope !== null,
    queryFn: ({ signal }) =>
      apiRequest(
        `/api/v1/intent/rulesets?bot_id=${scope!.botId}&scope_type=${scope!.scopeType}&scope_id=${scope!.scopeId}`,
        { signal },
      ).then(toRulesetList),
  })
}

export function useRuleset(id: number | null) {
  return useQuery({
    queryKey: queryKeys.ruleset(id ?? 0),
    enabled: typeof id === 'number' && id > 0,
    queryFn: ({ signal }) => apiRequest(`/api/v1/intent/rulesets/${id}`, { signal }).then(toRuleset),
  })
}

function useInvalidateRules() {
  const queryClient = useQueryClient()
  return () => void queryClient.invalidateQueries({ queryKey: ['intent', 'rulesets'] })
}

export function useCreateDraft(scope: ScopeRef) {
  const invalidate = useInvalidateRules()
  return useMutation({
    mutationFn: (variables: { note: string; cloneActive: boolean }) =>
      apiRequest('/api/v1/intent/rulesets', {
        method: 'POST',
        body: {
          bot_id: scope.botId,
          scope_type: scope.scopeType,
          scope_id: scope.scopeId,
          note: variables.note,
          clone_active: variables.cloneActive,
        },
      }).then(toRuleset),
    onSuccess: invalidate,
  })
}

export function useSaveEntries() {
  const invalidate = useInvalidateRules()
  return useMutation({
    mutationFn: (variables: { id: number; entries: RuleEntry[] }) =>
      apiRequest(`/api/v1/intent/rulesets/${variables.id}`, {
        method: 'PATCH',
        body: { entries: variables.entries },
      }).then(toRuleset),
    onSuccess: invalidate,
  })
}

export function usePublishRuleset() {
  const invalidate = useInvalidateRules()
  return useMutation({
    mutationFn: (id: number) =>
      apiRequest(`/api/v1/intent/rulesets/${id}/publish`, { method: 'POST' }),
    onSuccess: invalidate,
  })
}

export function useRollbackRuleset(scope: ScopeRef) {
  const invalidate = useInvalidateRules()
  return useMutation({
    mutationFn: (version: number) =>
      apiRequest('/api/v1/intent/rulesets/0/rollback', {
        method: 'POST',
        body: {
          bot_id: scope.botId,
          scope_type: scope.scopeType,
          scope_id: scope.scopeId,
          version,
        },
      }),
    onSuccess: invalidate,
  })
}

export function useSimulate(scope: ScopeRef) {
  return useMutation({
    mutationFn: (variables: { kind: 'group' | 'private'; replyMode: string; explicit: boolean; text: string }) =>
      apiRequest('/api/v1/intent/simulate', {
        method: 'POST',
        body: {
          bot_id: scope.botId,
          scope_type: scope.scopeType,
          scope_id: scope.scopeId,
          kind: variables.kind,
          reply_mode: variables.replyMode,
          explicit: variables.explicit,
          text: variables.text,
        },
      }).then(toRuleSimulation),
  })
}
