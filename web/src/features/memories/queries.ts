import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiRequest } from '../../lib/api/client'
import {
  toMemoryCreateResult,
  toMemoryManagement,
  toMemoryPage,
  toMemorySupersedeResult,
} from '../../lib/api/adapters/memory'
import type { MemorySubjectType, MemoryVisibility } from '../../lib/api/contracts'
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
  subjectType: MemorySubjectType
  subjectId: string
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
          subject_type: variables.subjectType,
          subject_id: variables.subjectType === 'unknown' ? '' : variables.subjectId,
          content: variables.content,
          source: variables.source,
          confidence: variables.confidence,
          approve: variables.approve,
        },
      }).then(toMemoryCreateResult),
    onSuccess: invalidate,
  })
}

export function useMemoryManagement(memoryId: string | null, botId?: number) {
  return useQuery({
    queryKey: queryKeys.memoryManagement(botId, memoryId),
    enabled: memoryId !== null && botId !== undefined,
    queryFn: ({ signal }) =>
      apiRequest(`/api/v1/memories/${memoryId}/management?bot_id=${botId}`, { signal }).then(
        toMemoryManagement,
      ),
  })
}

export interface SupersedeMemoryVariables {
  candidateId: string
  oldMemoryId: string
  reason: string
  idempotencyKey: string
}

/**
 * Explicitly supersedes one active memory with one candidate. The backend
 * validates same bot, visibility, owner and subject; the UI never infers the
 * old memory from similar content.
 * 显式用候选替代一条 active 记忆；后端校验同 bot、可见性、归属与主体，
 * 前端绝不按相似正文推断旧记忆。
 */
export function useSupersedeMemory() {
  const invalidate = useInvalidateMemories()
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (variables: SupersedeMemoryVariables) =>
      apiRequest(`/api/v1/memories/${variables.candidateId}/supersede`, {
        method: 'POST',
        body: {
          old_memory_id: variables.oldMemoryId,
          reason: variables.reason,
          idempotency_key: variables.idempotencyKey,
        },
      }).then(toMemorySupersedeResult),
    onSuccess: () => {
      invalidate()
      void queryClient.invalidateQueries({ queryKey: ['knowledge', 'summary'] })
    },
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

/**
 * Permanently deletes the memory row; the list is invalidated on success.
 * 永久删除该记忆行；成功后刷新列表。
 */
export function useDeleteMemory() {
  const invalidate = useInvalidateMemories()
  return useMutation({
    mutationFn: (memoryId: string) =>
      apiRequest(`/api/v1/memories/${memoryId}`, { method: 'DELETE' }),
    onSuccess: invalidate,
  })
}
