import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiRequest } from '../../lib/api/client'
import {
  toContactPage,
  toDirectorySyncResult,
  toGroupPage,
  toMemberPage,
} from '../../lib/api/adapters/directory'
import { toQueryString } from '../../lib/api/query-string'
import { DIRECTORY_QUERY_KEYS, queryKeys, type PageParams } from '../../lib/query-keys'

/** The backend caps sync and member refresh at 30s; leave headroom for the proxy. */
const SYNC_TIMEOUT_MS = 35_000

export function useContacts(params: PageParams) {
  return useQuery({
    queryKey: queryKeys.contacts(params),
    queryFn: ({ signal }) =>
      apiRequest(
        `/api/v1/contacts${toQueryString({
          search: params.search,
          page: params.page,
          limit: params.limit,
        })}`,
        { signal },
      ).then(toContactPage),
    placeholderData: (previousData) => previousData,
  })
}

export function useGroups(params: PageParams) {
  return useQuery({
    queryKey: queryKeys.groups(params),
    queryFn: ({ signal }) =>
      apiRequest(
        `/api/v1/groups${toQueryString({
          search: params.search,
          page: params.page,
          limit: params.limit,
        })}`,
        { signal },
      ).then(toGroupPage),
    placeholderData: (previousData) => previousData,
  })
}

export function useGroupMembers(groupId: number, params: PageParams, enabled = true) {
  return useQuery({
    queryKey: queryKeys.members(groupId, params),
    queryFn: ({ signal }) =>
      apiRequest(
        `/api/v1/groups/${groupId}/members${toQueryString({
          search: params.search,
          page: params.page,
          limit: params.limit,
        })}`,
        { signal },
      ).then(toMemberPage),
    enabled: enabled && Number.isFinite(groupId) && groupId > 0,
    placeholderData: (previousData) => previousData,
  })
}

/** Pulls friends and groups from OneBot; requires a connected bot. */
export function useSyncDirectory() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () =>
      apiRequest('/api/v1/directory/sync', {
        method: 'POST',
        timeoutMs: SYNC_TIMEOUT_MS,
      }).then(toDirectorySyncResult),
    onSuccess: () => {
      for (const queryKey of DIRECTORY_QUERY_KEYS) {
        void queryClient.invalidateQueries({ queryKey })
      }
      void queryClient.invalidateQueries({ queryKey: queryKeys.accounts })
      void queryClient.invalidateQueries({ queryKey: queryKeys.status })
    },
  })
}

/**
 * Refreshes one group's members on demand. The backend resolves the bot id from
 * the last sync held in memory, so this fails after a process restart until the
 * directory is synced again.
 * 按需刷新群成员；后端依赖内存中最近一次同步的机器人账号，
 * 进程重启后需要重新同步目录。
 */
export function useRefreshGroupMembers(groupId: number, params: PageParams) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () =>
      apiRequest(
        `/api/v1/groups/${groupId}/members${toQueryString({
          search: params.search,
          page: params.page,
          limit: params.limit,
          refresh: 'true',
        })}`,
        { timeoutMs: SYNC_TIMEOUT_MS },
      ).then(toMemberPage),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['directory', 'members', groupId] })
      void queryClient.invalidateQueries({ queryKey: queryKeys.groups(params) })
    },
  })
}
