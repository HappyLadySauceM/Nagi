import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { type ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ScopeType } from '../../../lib/api/contracts'
import { queryKeys } from '../../../lib/query-keys'
import { useToolsCatalog } from '../queries'

const CATALOG = {
  Providers: ['builtin.qq'],
  Tools: [],
  Permissions: [],
}

function okResponse(): Response {
  return new Response(JSON.stringify({ code: 200, message: 'ok', data: CATALOG }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

function createQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function wrapperFor(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  }
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('useToolsCatalog', () => {
  it('requests the catalogue for the given scope kind', async () => {
    const fetchMock = vi.fn((_input: RequestInfo | URL) => Promise.resolve(okResponse()))
    vi.stubGlobal('fetch', fetchMock)
    const queryClient = createQueryClient()

    const { result } = renderHook(() => useToolsCatalog('group'), {
      wrapper: wrapperFor(queryClient),
    })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/tools?scope_kind=group')
  })

  it('caches each scope kind separately', async () => {
    const fetchMock = vi.fn((_input: RequestInfo | URL) => Promise.resolve(okResponse()))
    vi.stubGlobal('fetch', fetchMock)
    const queryClient = createQueryClient()

    const { result, rerender } = renderHook(
      ({ kind }: { kind: ScopeType }) => useToolsCatalog(kind),
      { wrapper: wrapperFor(queryClient), initialProps: { kind: 'group' } },
    )
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(queryClient.getQueryData(queryKeys.toolsCatalog('group'))).toBeDefined()
    expect(queryClient.getQueryData(queryKeys.toolsCatalog('private'))).toBeUndefined()

    rerender({ kind: 'private' })
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))

    expect(fetchMock.mock.calls[1][0]).toBe('/api/v1/tools?scope_kind=private')
    expect(queryClient.getQueryData(queryKeys.toolsCatalog('private'))).toBeDefined()
  })
})
