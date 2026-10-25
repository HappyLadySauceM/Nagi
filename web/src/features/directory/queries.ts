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

export function useContacts(botId: number | undefined, params: PageParams, enabled = true) {
  return useQuery({
    queryKey: queryKeys.contacts(botId, params),
    queryFn: ({ signal }) =>
      apiRequest(
        `/api/v1/contacts${toQueryString({
          bot_id: botId,
          search: params.search,
          page: params.page,
          limit: params.limit,
        })}`,
        { signal },
      ).then(toContactPage),
    enabled: enabled && botId !== undefined,
    // Keep the previous page while paging, but never across an account switch.
    // 翻页时保留上一页，但切换账号时不复用其他账号的数据。
    placeholderData: (previousData, previousQuery) =>
      previousQuery?.queryKey[2] === (botId ?? null) ? previousData : undefined,
  })
}

export function useGroups(botId: number | undefined, params: PageParams, enabled = true) {
  return useQuery({
    queryKey: queryKeys.groups(botId, params),
    queryFn: ({ signal }) =>
      apiRequest(
        `/api/v1/groups${toQueryString({
          bot_id: botId,
          search: params.search,
          page: params.page,
          limit: params.limit,
        })}`,
        { signal },
      ).then(toGroupPage),
    enabled: enabled && botId !== undefined,
    // Keep the previous page while paging, but never across an account switch.
    // 翻页时保留上一页，但切换账号时不复用其他账号的数据。
    placeholderData: (previousData, previousQuery) =>
      previousQuery?.queryKey[2] === (botId ?? null) ? previousData : undefined,
  })
}

export function useGroupMembers(
  botId: number | undefined,
  groupId: number,
  params: PageParams,
  enabled = true,
) {
  return useQuery({
    queryKey: queryKeys.members(botId, groupId, params),
    queryFn: ({ signal }) =>
      apiRequest(
        `/api/v1/groups/${groupId}/members${toQueryString({
          bot_id: botId,
          search: params.search,
          page: params.page,
          limit: params.limit,
        })}`,
        { signal },
      ).then(toMemberPage),
    enabled: enabled && botId !== undefined && Number.isFinite(groupId) && groupId > 0,
    // Membership notices land in the database, so a light poll keeps the open
    // detail page in sync without a push channel.
    // 成员变更通知已落库，轻量轮询让打开的详情页保持同步（无推送通道）。
    refetchInterval: 30_000,
    placeholderData: (previousData, previousQuery) =>
      previousQuery?.queryKey[2] === (botId ?? null) ? previousData : undefined,
  })
}

/** Pulls friends and groups from OneBot for one account; requires it connected. */
export function useSyncDirectory(botId: number | undefined) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () => {
      if (botId === undefined) {
        return Promise.reject(new Error('尚未识别账号，无法同步目录'))
      }
      return apiRequest(`/api/v1/accounts/${botId}/sync`, {
        method: 'POST',
        timeoutMs: SYNC_TIMEOUT_MS,
      }).then(toDirectorySyncResult)
    },
    onSuccess: () => {
      for (const queryKey of DIRECTORY_QUERY_KEYS) {
        void queryClient.invalidateQueries({ queryKey })
      }
      void queryClient.invalidateQueries({ queryKey: queryKeys.accounts })
      void queryClient.invalidateQueries({ queryKey: queryKeys.status })
    },
  })
}

/** Refreshes one group's members on demand; the account must be identified. */
export function useRefreshGroupMembers(
  botId: number | undefined,
  groupId: number,
  params: PageParams,
) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () => {
      if (botId === undefined) {
        return Promise.reject(new Error('尚未识别账号，无法刷新成员'))
      }
      return apiRequest(
        `/api/v1/groups/${groupId}/members${toQueryString({
          bot_id: botId,
          search: params.search,
          page: params.page,
          limit: params.limit,
          refresh: 'true',
        })}`,
        { timeoutMs: SYNC_TIMEOUT_MS },
      ).then(toMemberPage)
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['directory', 'members', botId ?? null] })
      void queryClient.invalidateQueries({ queryKey: ['directory', 'groups'] })
    },
  })
}
