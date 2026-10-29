import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiRequest } from '../../lib/api/client'
import { toSendingPolicy } from '../../lib/api/adapters/sending'
import type { ScopeType, SendingPolicy } from '../../lib/api/contracts'
import { queryKeys } from '../../lib/query-keys'

export interface SendScopeRef {
  botId: number
  scopeType: ScopeType
  scopeId: number
}

export function useSendingPolicy(scope: SendScopeRef | null) {
  return useQuery({
    queryKey: queryKeys.sendingPolicy(scope),
    enabled: scope !== null,
    queryFn: ({ signal }) =>
      apiRequest(
        `/api/v1/agent/sending-policy?bot_id=${scope!.botId}&scope_type=${scope!.scopeType}&scope_id=${scope!.scopeId}`,
        { signal },
      ).then(toSendingPolicy),
  })
}

export function useSaveSendingPolicy() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (policy: Omit<SendingPolicy, 'exists'>) =>
      apiRequest('/api/v1/agent/sending-policy', {
        method: 'PUT',
        body: {
          bot_id: policy.botId,
          scope_type: policy.scopeType,
          scope_id: policy.scopeId,
          allow_send_current: policy.allowSendCurrent,
          max_messages_per_run: policy.maxMessagesPerRun,
          max_chars_per_message: policy.maxCharsPerMessage,
          max_total_chars_per_run: policy.maxTotalCharsPerRun,
        },
      }),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ['agent', 'sending-policy'] }),
  })
}
