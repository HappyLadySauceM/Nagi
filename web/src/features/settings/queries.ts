import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiRequest } from '../../lib/api/client'
import { toSettingsPutBody, toSettingsView } from '../../lib/api/adapters/settings'
import type { SystemSettings, SystemSettingsView } from '../../lib/api/contracts'
import { queryKeys } from '../../lib/query-keys'

const SETTINGS_ENDPOINT = '/api/v1/settings'

/** Current revision, baseline and credential state of the system settings. */
export function useSystemSettings() {
  return useQuery({
    queryKey: queryKeys.settings,
    queryFn: ({ signal }) =>
      apiRequest(SETTINGS_ENDPOINT, { signal }).then(toSettingsView),
    staleTime: 15_000,
  })
}

interface SaveVariables {
  settings: SystemSettings
  version: number
}

/**
 * Replaces the whole configuration. The response is the stored revision, so the
 * screen shows server truth instead of an optimistic guess.
 * 整份覆盖配置。响应即服务端已存储的修订，界面展示服务端真相而不是乐观猜测。
 */
export function useSaveSystemSettings() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ settings, version }: SaveVariables) =>
      apiRequest(SETTINGS_ENDPOINT, {
        method: 'PUT',
        body: toSettingsPutBody(settings, version),
      }).then(toSettingsView),
    onSuccess: (view) => {
      queryClient.setQueryData<SystemSettingsView>(queryKeys.settings, view)
      // The catalogue mirrors enabled providers and pipeline state.
      // 工具清单镜像启用的提供方与流水线状态，需要重新拉取。
      void queryClient.invalidateQueries({ queryKey: ['tools'] })
    },
  })
}

/** Restores the configuration-file baseline as a new revision. */
export function useResetSystemSettings() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () =>
      apiRequest(`${SETTINGS_ENDPOINT}/reset`, { method: 'POST' }).then(toSettingsView),
    onSuccess: (view) => {
      queryClient.setQueryData<SystemSettingsView>(queryKeys.settings, view)
      void queryClient.invalidateQueries({ queryKey: ['tools'] })
    },
  })
}
