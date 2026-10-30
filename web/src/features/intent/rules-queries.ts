import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiRequest } from '../../lib/api/client'
import { toRuleset, toRulesetList, toRuleSimulation, toRuleEntriesRequest } from '../../lib/api/adapters/rules'
import type { RuleEntry, RuleTarget, ScopeType } from '../../lib/api/contracts'
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
        `/api/v1/intent/rulesets?bot_id=${scope!.botId}${scope!.scopeId === -1 ? '' : `&scope_type=${scope!.scopeType}&scope_id=${scope!.scopeId}`}`,
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
  return () => Promise.all([queryClient.invalidateQueries({ queryKey: ['intent', 'rulesets'] }), queryClient.invalidateQueries({ queryKey: ['intent', 'ruleset'] })])
}

export function useCreateDraft(scope: ScopeRef) {
  const invalidate = useInvalidateRules()
  return useMutation({
    mutationFn: (variables: { note: string; cloneActive: boolean; cloneFromId?: number }) =>
      apiRequest('/api/v1/intent/rulesets', {
        method: 'POST',
        body: {
          bot_id: scope.botId,
          scope_type: scope.scopeType,
          scope_id: scope.scopeId,
          note: variables.note,
          clone_active: variables.cloneActive,
 clone_from_id: variables.cloneFromId,
        },
      }).then(toRuleset),
    onSuccess: invalidate,
  })
}

export function useSaveEntries() {
  const queryClient = useQueryClient()
  const invalidate = useInvalidateRules()
  return useMutation({
    mutationFn: (variables: { id: number; entries: RuleEntry[]; builtinRules?: Record<string, boolean>; targets?: RuleTarget[] }) =>
      apiRequest(`/api/v1/intent/rulesets/${variables.id}`, {
        method: 'PATCH',
        body: { entries: toRuleEntriesRequest(variables.entries), builtin_rules: variables.builtinRules ?? {}, targets: variables.targets },
      }).then(toRuleset),
    onSuccess: (data) => { queryClient.setQueryData(queryKeys.ruleset(data.id), data); return invalidate() },
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

export function useRollbackRuleset() {
  const invalidate = useInvalidateRules()
  return useMutation({
    mutationFn: (variables: { version: number; scope: ScopeRef }) =>
      apiRequest('/api/v1/intent/rulesets/0/rollback', {
        method: 'POST',
        body: {
          bot_id: variables.scope.botId,
          scope_type: variables.scope.scopeType,
          scope_id: variables.scope.scopeId,
          version: variables.version,
        },
      }),
    onSuccess: invalidate,
  })
}

export function useSimulate(scope: ScopeRef) {
  return useMutation({
    mutationFn: (variables: { mode?: 'rules' | 'semantic'; builtinRules?: Record<string, boolean>; previewGlobal?: boolean; kind: 'group' | 'private'; replyMode: string; explicit: boolean; text: string; entries?: RuleEntry[]; rulesetId?: number; secondsSinceReply?: number }) =>
      apiRequest('/api/v1/intent/simulate', {
        method: 'POST',
        body: {
          bot_id: scope.botId,
          scope_type: scope.scopeType,
          scope_id: scope.scopeId,
          mode: variables.mode ?? 'rules',
          builtin_rules: variables.builtinRules,
          preview_global: variables.previewGlobal,
          kind: variables.kind,
          reply_mode: variables.replyMode,
          explicit: variables.explicit,
          text: variables.text,
          entries: variables.entries ? toRuleEntriesRequest(variables.entries) : undefined,
          ruleset_id: variables.rulesetId,
          seconds_since_reply: variables.secondsSinceReply,
        },
      }).then(toRuleSimulation),
  })
}
