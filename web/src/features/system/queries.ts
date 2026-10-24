import { useQuery } from '@tanstack/react-query'
import { ApiError, apiRequest } from '../../lib/api/client'
import type { AccountSnapshot } from '../../lib/api/contracts'
import { queryKeys } from '../../lib/query-keys'
import { toAccountsSnapshot, toPingState, toStatusSnapshot } from '../../lib/api/adapters/system'
import { toMetricsSnapshot } from '../../lib/api/adapters/metrics'

const STATUS_INTERVAL_MS = 15_000
const METRICS_INTERVAL_MS = 30_000

export function useLivez() {
  return useQuery({
    queryKey: queryKeys.livez,
    queryFn: ({ signal }) => apiRequest('/livez', { signal }).then((raw) => toPingState(raw, '/livez')),
    refetchInterval: STATUS_INTERVAL_MS,
  })
}

export function useReadyz() {
  return useQuery({
    queryKey: queryKeys.readyz,
    queryFn: ({ signal }) => apiRequest('/readyz', { signal }).then((raw) => toPingState(raw, '/readyz')),
    refetchInterval: STATUS_INTERVAL_MS,
    retry: false,
  })
}

export function useStatus() {
  return useQuery({
    queryKey: queryKeys.status,
    queryFn: ({ signal }) => apiRequest('/api/v1/status', { signal }).then(toStatusSnapshot),
    refetchInterval: STATUS_INTERVAL_MS,
  })
}

export function useAccounts() {
  return useQuery({
    queryKey: queryKeys.accounts,
    queryFn: ({ signal }) => apiRequest('/api/v1/accounts', { signal }).then(toAccountsSnapshot),
    refetchInterval: STATUS_INTERVAL_MS,
  })
}

export function useMetrics() {
  return useQuery({
    queryKey: queryKeys.metrics,
    queryFn: ({ signal }) => apiRequest('/api/v1/metrics', { signal }).then(toMetricsSnapshot),
    refetchInterval: METRICS_INTERVAL_MS,
  })
}

interface AccountSnapshotQuery {
  data?: AccountSnapshot
  isLoading: boolean
  isFetching: boolean
  isError: boolean
  error: unknown
  /** True when `/accounts` is not registered and `/status` answered instead. */
  isFallback: boolean
  refetch: () => void
}

/**
 * Single source of truth for connection facts. `/accounts` wins; `/status` is
 * used only when the backend does not register the accounts route (an older
 * build, where that route was gated behind the database). Auth, network and
 * contract failures are surfaced instead of being masked by a fallback.
 */
export function useAccountSnapshot(): AccountSnapshotQuery {
  const accounts = useAccounts()
  const status = useStatus()

  const isFallback =
    accounts.error instanceof ApiError &&
    accounts.error.kind === 'not-found' &&
    accounts.error.detail === 'unregistered'

  let data: AccountSnapshot | undefined
  if (accounts.data) {
    data = {
      connected: accounts.data.connected,
      dbConfigured: accounts.data.dbConfigured,
      source: 'accounts',
    }
    if (accounts.data.botId !== undefined) {
      data.botId = accounts.data.botId
    }
  } else if (isFallback && status.data) {
    data = {
      connected: status.data.onebot.connected,
      source: 'status',
    }
    if (status.data.onebot.selfId !== undefined) {
      data.botId = status.data.onebot.selfId
    }
  }

  const isLoading = accounts.isLoading || (isFallback && status.isLoading)
  const isError = data === undefined && (isFallback ? status.isError : accounts.isError)

  return {
    data,
    isLoading,
    isFetching: accounts.isFetching || (isFallback && status.isFetching),
    isError,
    error: isFallback ? status.error : accounts.error,
    isFallback,
    refetch: () => {
      void accounts.refetch()
      if (isFallback) {
        void status.refetch()
      }
    },
  }
}

/** Keys invalidated by the manual refresh control. */
export const SYSTEM_QUERY_KEYS = [queryKeys.livez, queryKeys.readyz, queryKeys.status, queryKeys.accounts, queryKeys.metrics]
