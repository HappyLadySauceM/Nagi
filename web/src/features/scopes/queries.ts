import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiRequest } from '../../lib/api/client'
import {
  toScopePolicy,
  toScopePolicyList,
  toScopePutBody,
} from '../../lib/api/adapters/scope'
import type { ScopeForm, ScopePolicy, ScopeType } from '../../lib/api/contracts'
import { queryKeys } from '../../lib/query-keys'

/** Index of stored policies; screens use it to mark unknown scopes as default-deny. */
export function useScopeIndex() {
  return useQuery({
    queryKey: queryKeys.scopeIndex,
    queryFn: ({ signal }) => apiRequest('/api/v1/scopes', { signal }).then(toScopePolicyList),
    staleTime: 30_000,
  })
}

/** Effective policy; unknown scopes resolve to the backend default (all off). */
export function useScope(scopeType: ScopeType, scopeId: number) {
  return useQuery({
    queryKey: queryKeys.scope(scopeType, scopeId),
    queryFn: ({ signal }) =>
      apiRequest(`/api/v1/scopes/${scopeType}/${scopeId}`, { signal }).then(toScopePolicy),
    enabled: Number.isFinite(scopeId) && scopeId > 0,
  })
}

interface SaveScopeVariables {
  scopeType: ScopeType
  scopeId: number
  form: ScopeForm
}

/**
 * `PUT` replaces the whole policy: all nine fields are sent, then the stored row
 * is read back so the screen reflects the server state rather than optimism.
 * PUT 是整份覆盖：提交全部九个字段，随后回读服务端状态而不是乐观更新。
 */
export function useSaveScope() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ scopeType, scopeId, form }: SaveScopeVariables) =>
      apiRequest(`/api/v1/scopes/${scopeType}/${scopeId}`, {
        method: 'PUT',
        body: toScopePutBody(form),
      }).then(toScopePolicy),
    onSuccess: (policy) => {
      queryClient.setQueryData<ScopePolicy>(
        queryKeys.scope(policy.scopeType, policy.scopeId),
        policy,
      )
      void queryClient.invalidateQueries({ queryKey: queryKeys.scopeIndex })
    },
  })
}
