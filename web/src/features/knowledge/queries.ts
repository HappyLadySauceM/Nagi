import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiRequest } from '../../lib/api/client'
import {
  toExtractionJobPage,
  toExtractionSettings,
  toKnowledgePreview,
  toKnowledgeSummary,
} from '../../lib/api/adapters/knowledge'
import type {
  ExtractionJob,
  ExtractionJobStatus,
  ExtractionSettings,
  KnowledgePreview,
  KnowledgeSummary,
  PageResult,
} from '../../lib/api/contracts'
import { toQueryString } from '../../lib/api/query-string'

export interface ExtractionJobParams {
  status: ExtractionJobStatus | ''
  conversationId: string
  page: number
  limit: number
}

export function useKnowledgeSummary(botId: number | undefined) {
  return useQuery({
    queryKey: ['knowledge', 'summary', botId ?? null] as const,
    enabled: botId !== undefined,
    queryFn: ({ signal }): Promise<KnowledgeSummary> =>
      apiRequest(`/api/v1/knowledge/summary?bot_id=${botId}`, { signal }).then(toKnowledgeSummary),
  })
}

export interface KnowledgePreviewInput {
  botId: number
  mode: 'example' | 'real'
  targetType: 'group' | 'private'
  targetId?: number
  conversationId?: string
  speakerId?: string
  query?: string
  subjectType?: 'unknown' | 'user' | 'group'
  subjectId?: string
  allowRewrite?: boolean
}

/** One explicit read-only preview request; skipped layers remain visible to the caller.
 * 一次显式只读预览请求；被跳过的层会完整返回给调用方展示。
 */
export function useKnowledgePreview() {
  return useMutation({
    mutationFn: (input: KnowledgePreviewInput): Promise<KnowledgePreview> =>
      apiRequest('/api/v1/knowledge/preview', {
        method: 'POST',
        body: {
          bot_id: input.botId,
          preview_mode: input.mode,
          target_type: input.targetType,
          target_id: input.targetId ?? 0,
          conversation_id: input.conversationId ?? '',
          query: input.query ?? '',
          speaker_type: input.speakerId ? 'user' : '',
          speaker_id: input.speakerId ?? '',
          allow_rewrite: input.allowRewrite ?? false,
          subject_type: input.subjectType ?? 'unknown',
          subject_id: input.subjectId ?? '',
          subject_source: input.subjectId ? 'admin_explicit' : 'unknown',
        },
      }).then(toKnowledgePreview),
  })
}

export function useExtractionSettings() {
  return useQuery({
    queryKey: ['knowledge', 'extraction', 'settings'] as const,
    queryFn: ({ signal }): Promise<ExtractionSettings> =>
      apiRequest('/api/v1/memory-extraction/settings', { signal }).then(toExtractionSettings),
  })
}

export function useSaveExtractionSettings() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: ExtractionSettings): Promise<ExtractionSettings> =>
      apiRequest('/api/v1/memory-extraction/settings', {
        method: 'PUT',
        body: {
          Version: input.version,
          Enabled: input.enabled,
          Concurrency: input.concurrency,
          BatchMaxMessages: input.batchMaxMessages,
          BatchMaxRunes: input.batchMaxRunes,
          MaxWaitMS: input.maxWaitMs,
          PollIntervalMS: input.pollIntervalMs,
          LeaseDurationMS: input.leaseDurationMs,
          MaxAttempts: input.maxAttempts,
          RetryDelaysMS: input.retryDelaysMs,
        },
      }).then(toExtractionSettings),
    onSuccess: (updated) => {
      queryClient.setQueryData(['knowledge', 'extraction', 'settings'], updated)
      void queryClient.invalidateQueries({ queryKey: ['knowledge', 'extraction', 'jobs'] })
      void queryClient.invalidateQueries({ queryKey: ['knowledge', 'summary'] })
    },
  })
}

export function useExtractionJobs(botId: number | undefined, params: ExtractionJobParams) {
  return useQuery({
    queryKey: ['knowledge', 'extraction', 'jobs', botId ?? null, params] as const,
    enabled: botId !== undefined,
    queryFn: ({ signal }): Promise<PageResult<ExtractionJob>> =>
      apiRequest(
        `/api/v1/memory-extraction/jobs${toQueryString({
          bot_id: botId,
          status: params.status,
          conversation_id: params.conversationId,
          page: params.page,
          limit: params.limit,
        })}`,
        { signal },
      ).then((raw) => toExtractionJobPage(raw, { page: params.page, limit: params.limit })),
  })
}

export function useRetryExtractionJob() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (jobId: string) =>
      apiRequest(`/api/v1/memory-extraction/jobs/${jobId}/retry`, { method: 'POST' }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['knowledge', 'extraction', 'jobs'] })
      void queryClient.invalidateQueries({ queryKey: ['knowledge', 'summary'] })
    },
  })
}
