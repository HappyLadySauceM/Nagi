import { useQuery } from '@tanstack/react-query'
import { apiRequest } from '../../lib/api/client'
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

/** Keys invalidated by the manual refresh control. */
export const SYSTEM_QUERY_KEYS = [queryKeys.livez, queryKeys.readyz, queryKeys.status, queryKeys.accounts, queryKeys.metrics]
