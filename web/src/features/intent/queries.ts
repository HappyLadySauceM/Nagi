import { useQuery } from '@tanstack/react-query'
import { apiRequest } from '../../lib/api/client'
import { toDecisionDetail, toDecisionList } from '../../lib/api/adapters/intent'
import { toQueryString } from '../../lib/api/query-string'
import { queryKeys } from '../../lib/query-keys'

/**
 * The decision log is account-scoped: switching accounts must never reuse the
 * previous account's rows, so the bot id is part of the query key.
 * 决策记录按账号隔离：切换账号不得复用上一个账号的数据，
 * 因此 bot id 是 query key 的一部分。
 */
export function useDecisions(botId: number | undefined, conversationId: string, limit = 20, beforeId = 0, reasonCode = '') {
  return useQuery({
    queryKey: [...queryKeys.decisions(botId, conversationId), limit, beforeId, reasonCode],
    enabled: typeof botId === 'number' && botId > 0,
    queryFn: ({ signal }) =>
      apiRequest(
        `/api/v1/intent/decisions${toQueryString({
          bot_id: botId,
          conversation_id: conversationId || undefined,
          limit,
 before_id: beforeId || undefined,
 reason_code:reasonCode || undefined,
        })}`,
        { signal },
      ).then(toDecisionList),
  })
}

export function useDecision(id: number | null) {
  return useQuery({
    queryKey: queryKeys.decision(id ?? 0),
    enabled: typeof id === 'number' && id > 0,
    queryFn: ({ signal }) =>
      apiRequest(`/api/v1/intent/decisions/${id}`, { signal }).then(toDecisionDetail),
  })
}
