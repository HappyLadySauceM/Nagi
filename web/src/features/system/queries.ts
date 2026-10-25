import { useQuery } from '@tanstack/react-query'
import { apiRequest } from '../../lib/api/client'
import { toQueryString } from '../../lib/api/query-string'
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

/** Configured accounts; available even without a database. */
export function useAccounts() {
  return useQuery({
    queryKey: queryKeys.accounts,
    queryFn: ({ signal }) => apiRequest('/api/v1/accounts', { signal }).then(toAccountsSnapshot),
    refetchInterval: STATUS_INTERVAL_MS,
  })
}

/** Aggregate counters of one account, or of every account when botId is unset. */
export function useMetrics(botId?: number) {
  return useQuery({
    queryKey: queryKeys.metrics(botId),
    queryFn: ({ signal }) =>
      apiRequest(`/api/v1/metrics${toQueryString({ bot_id: botId })}`, { signal }).then(
        toMetricsSnapshot,
      ),
    refetchInterval: METRICS_INTERVAL_MS,
  })
}

/** Keys invalidated by the manual refresh control. */
export const SYSTEM_QUERY_KEYS = [
  queryKeys.livez,
  queryKeys.readyz,
  queryKeys.status,
  queryKeys.accounts,
  ['metrics'],
] as const
