import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiRequest } from '../../lib/api/client'
import {
  toManagedPromptDetail,
  toManagedPromptDiff,
  toManagedPromptEffective,
  toManagedPromptList,
  toManagedPromptPreview,
  toManagedPromptPublishResult,
  toManagedPromptRestoreResult,
  toManagedPromptVersion,
} from '../../lib/api/adapters/prompts'
import type {
  ManagedPromptDetail,
  ManagedPromptEffective,
  ManagedPromptKind,
  ManagedPromptPreview,
  ManagedPromptProvider,
  ManagedPromptScope,
  ManagedPromptVersion,
} from '../../lib/api/contracts'

const PROMPTS_ENDPOINT = '/api/v1/prompts'

export function promptCatalogKey(botId: number | undefined) {
  return ['prompts', 'catalog', botId ?? null] as const
}

export function promptDetailKey(
  botId: number | undefined,
  promptId: string,
  scope: ManagedPromptScope,
  provider: ManagedPromptProvider,
) {
  return ['prompts', 'detail', botId ?? null, promptId, scope, provider] as const
}

export function usePromptCatalog(botId: number | undefined) {
  return useQuery({
    queryKey: promptCatalogKey(botId),
    enabled: botId !== undefined,
    queryFn: ({ signal }) =>
      apiRequest(`${PROMPTS_ENDPOINT}?bot_id=${botId}`, { signal }).then(toManagedPromptList),
  })
}

export function usePromptDetail(params: {
  botId: number | undefined
  promptId: string
  scope: ManagedPromptScope
  provider: ManagedPromptProvider
}) {
  const { botId, promptId, scope, provider } = params
  return useQuery({
    queryKey: promptDetailKey(botId, promptId, scope, provider),
    enabled: botId !== undefined,
    queryFn: ({ signal }): Promise<ManagedPromptDetail> =>
      apiRequest(
        `${PROMPTS_ENDPOINT}/${encodeURIComponent(promptId)}?bot_id=${botId}&scope_type=${scope}&scope_id=0&provider=${provider}`,
        { signal },
      ).then(toManagedPromptDetail),
  })
}

export interface CreateDraftInput {
  botId: number
  promptId: string
  scope: ManagedPromptScope
  provider: ManagedPromptProvider
  name: string
  base: 'auto' | 'builtin' | 'active' | 'empty' | 'version'
  baseVersionId?: number
  note?: string
}

export function useCreatePromptDraft() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: CreateDraftInput): Promise<ManagedPromptVersion> =>
      apiRequest(`${PROMPTS_ENDPOINT}/${encodeURIComponent(input.promptId)}/versions`, {
        method: 'POST',
        body: {
          bot_id: input.botId,
          scope_type: input.scope,
          scope_id: 0,
          provider: input.provider,
          name: input.name,
          base: input.base,
          base_version_id: input.baseVersionId ?? 0,
          note: input.note ?? '',
          source: 'local',
          source_ref: '',
        },
      }).then(toManagedPromptVersion),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['prompts', 'catalog'] })
      void client.invalidateQueries({ queryKey: ['prompts', 'detail'] })
    },
  })
}

export function useUpdatePromptDraft() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: { versionId: number; content: Record<string, unknown>; note: string }) =>
      apiRequest(`${PROMPTS_ENDPOINT}/versions/${input.versionId}`, {
        method: 'PATCH',
        body: { content: input.content, note: input.note },
      }).then(toManagedPromptVersion),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['prompts', 'catalog'] })
      void client.invalidateQueries({ queryKey: ['prompts', 'detail'] })
    },
  })
}

export interface PromptDiffParams {
  botId: number
  promptId: string
  scope: ManagedPromptScope
  provider: ManagedPromptProvider
  from?: 'builtin' | 'empty' | 'active' | 'version'
  fromVersionId?: number
  to?: 'draft' | 'active' | 'version'
  toVersionId?: number
}

export function usePromptDiff(params: PromptDiffParams | null) {
  return useQuery({
    queryKey: [
      'prompts',
      'diff',
      params?.botId ?? null,
      params?.promptId ?? '',
      params?.scope ?? '',
      params?.provider ?? '',
      params?.from ?? '',
      params?.fromVersionId ?? 0,
      params?.to ?? '',
      params?.toVersionId ?? 0,
    ],
    enabled: params !== null,
    queryFn: ({ signal }) => {
      const query = new URLSearchParams({
        bot_id: String(params!.botId),
        scope_type: params!.scope,
        scope_id: '0',
        provider: params!.provider,
      })
      if (params!.from) query.set('from', params!.from)
      if (params!.fromVersionId) query.set('from_version_id', String(params!.fromVersionId))
      if (params!.to) query.set('to', params!.to)
      if (params!.toVersionId) query.set('to_version_id', String(params!.toVersionId))
      return apiRequest(
        `${PROMPTS_ENDPOINT}/${encodeURIComponent(params!.promptId)}/diff?${query.toString()}`,
        { signal },
      ).then(toManagedPromptDiff)
    },
  })
}

export interface PreviewInput {
  botId: number
  promptId: string
  provider: ManagedPromptProvider
  mode: 'example' | 'real'
  versionId?: number
  targetType?: 'group' | 'private'
  targetId?: number
  senderId?: number
  conversationId?: string
}

export function usePromptPreview() {
  return useMutation({
    mutationFn: (input: PreviewInput): Promise<ManagedPromptPreview> =>
      apiRequest(`${PROMPTS_ENDPOINT}/${encodeURIComponent(input.promptId)}/preview`, {
        method: 'POST',
        body: {
          bot_id: input.botId,
          provider: input.provider,
          preview_mode: input.mode,
          ...(input.versionId ? { version_id: input.versionId } : {}),
          sample: { current_message: '在吗', recent: [] },
          ...(input.mode === 'real'
            ? {
                context: {
                  target_type: input.targetType,
                  target_id: input.targetId,
                  ...(input.senderId ? { sender_id: input.senderId } : {}),
                  conversation_id: input.conversationId ?? '',
                },
              }
            : {}),
        },
      }).then(toManagedPromptPreview),
  })
}

export function usePromptEffective(params: {
  botId: number | undefined
  promptId: string
  provider: ManagedPromptProvider
  targetType: 'group' | 'private'
  targetId: number | undefined
  senderId?: number
  conversationId?: string
  enabled: boolean
}) {
  return useQuery({
    queryKey: [
      'prompts',
      'effective',
      params.botId ?? null,
      params.promptId,
      params.provider,
      params.targetType,
      params.targetId ?? 0,
      params.senderId ?? 0,
      params.conversationId ?? '',
    ],
    enabled:
      params.enabled && params.botId !== undefined && (params.targetId ?? 0) > 0,
    queryFn: ({ signal }): Promise<ManagedPromptEffective> => {
      const query = new URLSearchParams({
        bot_id: String(params.botId),
        provider: params.provider,
        target_type: params.targetType,
        target_id: String(params.targetId),
      })
      if (params.senderId) query.set('sender_id', String(params.senderId))
      if (params.conversationId) query.set('conversation_id', params.conversationId)
      return apiRequest(
        `${PROMPTS_ENDPOINT}/${encodeURIComponent(params.promptId)}/effective?${query.toString()}`,
        { signal },
      ).then(toManagedPromptEffective)
    },
  })
}

export function usePublishPromptDraft() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (versionId: number) =>
      apiRequest(`${PROMPTS_ENDPOINT}/versions/${versionId}/publish`, { method: 'POST' }).then(
        toManagedPromptPublishResult,
      ),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['prompts', 'catalog'] })
      void client.invalidateQueries({ queryKey: ['prompts', 'detail'] })
    },
  })
}

export interface RollbackInput {
  botId: number
  promptId: string
  scope: ManagedPromptScope
  provider: ManagedPromptProvider
  versionId: number
}

export function useRollbackPromptVersion() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: RollbackInput) =>
      apiRequest(`${PROMPTS_ENDPOINT}/${encodeURIComponent(input.promptId)}/rollback`, {
        method: 'POST',
        body: {
          bot_id: input.botId,
          scope_type: input.scope,
          scope_id: 0,
          provider: input.provider,
          version_id: input.versionId,
        },
      }).then(toManagedPromptPublishResult),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['prompts', 'catalog'] })
      void client.invalidateQueries({ queryKey: ['prompts', 'detail'] })
    },
  })
}

export interface RestoreBuiltinInput {
  botId: number
  promptId: string
  scope: ManagedPromptScope
  provider: ManagedPromptProvider
}

export function useRestorePromptBuiltin() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: RestoreBuiltinInput) =>
      apiRequest(`${PROMPTS_ENDPOINT}/${encodeURIComponent(input.promptId)}/restore-builtin`, {
        method: 'POST',
        body: {
          bot_id: input.botId,
          scope_type: input.scope,
          scope_id: 0,
          provider: input.provider,
        },
      }).then(toManagedPromptRestoreResult),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['prompts', 'catalog'] })
      void client.invalidateQueries({ queryKey: ['prompts', 'detail'] })
    },
  })
}

export function useDeletePromptVersion() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (input: { botId: number; versionId: number }) =>
      apiRequest(
        `${PROMPTS_ENDPOINT}/versions/${input.versionId}?bot_id=${input.botId}`,
        { method: 'DELETE' },
      ),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ['prompts', 'catalog'] })
      void client.invalidateQueries({ queryKey: ['prompts', 'detail'] })
    },
  })
}

export type { ManagedPromptKind }
