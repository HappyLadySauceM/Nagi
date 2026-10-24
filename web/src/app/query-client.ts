import { QueryClient } from '@tanstack/react-query'
import { ApiError } from '../lib/api/client'

/**
 * Shared client. Refetching is explicit and modest: 15–30s intervals per query,
 * never a global 1s poll, and never while the tab is hidden.
 */
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 15_000,
      gcTime: 5 * 60_000,
      refetchOnWindowFocus: false,
      refetchIntervalInBackground: false,
      retry: (failureCount, error) => {
        if (
          error instanceof ApiError &&
          (error.kind === 'unauthorized' ||
            error.kind === 'not-found' ||
            error.kind === 'contract' ||
            error.kind === 'cancelled')
        ) {
          return false
        }
        return failureCount < 1
      },
    },
  },
})
