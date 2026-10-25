import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiRequest } from '../../lib/api/client'
import { toMemoryCreateResult, toMemoryPage } from '../../lib/api/adapters/memory'
import type { MemoryVisibility } from '../../lib/api/contracts'
import { toQueryString } from '../../lib/api/query-string'
import { queryKeys, type MemoryParams } from '../../lib/query-keys'

export function useMemories(params: MemoryParams) {
  return useQuery({
    queryKey: queryKeys.memories(params),
    queryFn: ({ signal }) =>
      apiRequest(
        `/api/v1/memories${toQueryString({
          bot_id: params.botId,
          visibility: params.visibility,
          owner_key: params.ownerKey,
          status: params.status,
          search: params.search,
          page: params.page,
          limit: params.limit,
        })}`,
        { signal },
      ).then((raw) => toMemoryPage(raw, { page: params.page, limit: params.limit })),
    // Paging keeps the previous page; switching accounts must not reuse it.
    // 翻页保留上一页，但切换账号时不得复用其他账号的数据。
    placeholderData: (previousData, previousQuery) =>
      previousQuery?.queryKey[2] &&
      (previousQuery.queryKey[2] as { botId?: number }).botId === params.botId
        ? previousData
        : undefined,
  })
}

function useInvalidateMemories() {
  const queryClient = useQueryClient()
  return () => {
    void queryClient.invalidateQueries({ queryKey: ['memories'] })
    void queryClient.invalidateQueries({ queryKey: ['metrics'] })
  }
}

export interface CreateMemoryVariables {
  botId: number
  visibility: MemoryVisibility
  ownerKey: string
  content: string
  source: string
  confidence: number
  approve: boolean
}

/**
 * Duplicates return the existing id with `duplicate: true` and are never
 * activated, even when `approve` is requested.
 * 重复内容返回既有 id 且标记 duplicate:true，即使请求 approve 也不会激活。
 */
export function useCreateMemory() {
  const invalidate = useInvalidateMemories()
  return useMutation({
    mutationFn: (variables: CreateMemoryVariables) =>
      apiRequest('/api/v1/memories', {
        method: 'POST',
        body: {
          bot_id: variables.botId,
          visibility: variables.visibility,
          owner_key: variables.ownerKey,
          content: variables.content,
          source: variables.source,
          confidence: variables.confidence,
          approve: variables.approve,
        },
      }).then(toMemoryCreateResult),
    onSuccess: invalidate,
  })
}

/** Only `active` and `deleted` actually change state on the backend. */
export function useApproveMemory() {
  const invalidate = useInvalidateMemories()
  return useMutation({
    mutationFn: (memoryId: string) =>
      apiRequest(`/api/v1/memories/${memoryId}`, {
        method: 'PATCH',
        body: { status: 'active' },
      }),
    onSuccess: invalidate,
  })
}

export interface UpdateMemoryVariables {
  id: string
  content?: string
  visibility?: MemoryVisibility
  ownerKey?: string
}

export function useUpdateMemory() {
  const invalidate = useInvalidateMemories()
  return useMutation({
    mutationFn: ({ id, ...changes }: UpdateMemoryVariables) => {
      const body: Record<string, unknown> = {}
      if (changes.content !== undefined) {
        body.content = changes.content
      }
      if (changes.visibility !== undefined) {
        body.visibility = changes.visibility
      }
      if (changes.ownerKey !== undefined) {
        body.owner_key = changes.ownerKey
      }
      return apiRequest(`/api/v1/memories/${id}`, { method: 'PATCH', body })
    },
    onSuccess: invalidate,
  })
}

/** Soft delete: the row stays in the database but disappears from retrieval. */
export function useDeleteMemory() {
  const invalidate = useInvalidateMemories()
  return useMutation({
    mutationFn: (memoryId: string) =>
      apiRequest(`/api/v1/memories/${memoryId}`, { method: 'DELETE' }),
    onSuccess: invalidate,
  })
}
