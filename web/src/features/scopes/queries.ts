import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiRequest } from '../../lib/api/client'
import { toQueryString } from '../../lib/api/query-string'
import {
  toScopePolicy,
  toScopePolicyList,
  toScopePutBody,
} from '../../lib/api/adapters/scope'
import type { ScopeForm, ScopePolicy, ScopeType } from '../../lib/api/contracts'
import { queryKeys } from '../../lib/query-keys'

/** Index of the account's stored policies; unknown scopes stay default-deny. */
export function useScopeIndex(botId: number | undefined) {
  return useQuery({
    queryKey: queryKeys.scopeIndex(botId),
    queryFn: ({ signal }) =>
      apiRequest(`/api/v1/scopes${toQueryString({ bot_id: botId })}`, { signal }).then(
        toScopePolicyList,
      ),
    enabled: botId !== undefined,
    staleTime: 30_000,
  })
}

/** Effective policy; unknown scopes resolve to the backend default (all off). */
export function useScope(botId: number | undefined, scopeType: ScopeType, scopeId: number) {
  return useQuery({
    queryKey: queryKeys.scope(botId, scopeType, scopeId),
    queryFn: ({ signal }) =>
      apiRequest(
        `/api/v1/scopes/${scopeType}/${scopeId}${toQueryString({ bot_id: botId })}`,
        { signal },
      ).then(toScopePolicy),
    enabled: botId !== undefined && Number.isFinite(scopeId) && scopeId > 0,
  })
}

interface SaveScopeVariables {
  botId: number
  scopeType: ScopeType
  scopeId: number
  form: ScopeForm
}

/**
 * `PUT` replaces the whole policy: every field is sent, then the stored row is
 * read back so the screen reflects the server state rather than optimism.
 * PUT 是整份覆盖：提交全部字段，随后回读服务端状态而不是乐观更新。
 */
export function useSaveScope() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ botId, scopeType, scopeId, form }: SaveScopeVariables) =>
      apiRequest(`/api/v1/scopes/${scopeType}/${scopeId}${toQueryString({ bot_id: botId })}`, {
        method: 'PUT',
        body: toScopePutBody(form),
      }).then(toScopePolicy),
    onSuccess: (policy, variables) => {
      queryClient.setQueryData<ScopePolicy>(
        queryKeys.scope(variables.botId, policy.scopeType, policy.scopeId),
        policy,
      )
      // The PUT response is the server truth: write it straight into the index
      // cache so badges refresh instantly instead of waiting for a refetch.
      // PUT 响应即服务端真相：直接回写 index 缓存，徽章立即刷新而无需等重拉。
      queryClient.setQueryData<ScopePolicy[]>(
        queryKeys.scopeIndex(variables.botId),
        (old) => [
          ...(old ?? []).filter(
            (row) => !(row.scopeType === policy.scopeType && row.scopeId === policy.scopeId),
          ),
          policy,
        ],
      )
      void queryClient.invalidateQueries({ queryKey: ['metrics'] })
    },
  })
}

interface DeleteScopeVariables {
  botId: number
  scopeType: ScopeType
  scopeId: number
}

/**
 * Deletes a stored policy. The backend evicts its cache entry, so the scope
 * falls back to default-deny immediately.
 * 删除已保存策略。后端会驱逐缓存，该作用域立即回到默认拒绝。
 */
export function useDeleteScope() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ botId, scopeType, scopeId }: DeleteScopeVariables) =>
      apiRequest(`/api/v1/scopes/${scopeType}/${scopeId}${toQueryString({ bot_id: botId })}`, {
        method: 'DELETE',
      }),
    onSuccess: (_data, variables) => {
      queryClient.removeQueries({
        queryKey: queryKeys.scope(variables.botId, variables.scopeType, variables.scopeId),
      })
      // Mirror the deletion in the index cache so status badges flip back to
      // "default deny" at once.
      // 同步从 index 缓存移除，状态徽章立即回到默认拒绝。
      queryClient.setQueryData<ScopePolicy[]>(
        queryKeys.scopeIndex(variables.botId),
        (old) =>
          (old ?? []).filter(
            (row) => !(row.scopeType === variables.scopeType && row.scopeId === variables.scopeId),
          ),
      )
      void queryClient.invalidateQueries({ queryKey: ['metrics'] })
    },
  })
}
