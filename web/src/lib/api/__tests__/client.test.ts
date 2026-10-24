import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, apiRequest } from '../client'

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

async function expectApiError(promise: Promise<unknown>): Promise<ApiError> {
  try {
    await promise
  } catch (error) {
    if (error instanceof ApiError) {
      return error
    }
    throw error
  }
  throw new Error('expected the request to fail')
}

describe('apiRequest', () => {
  beforeEach(() => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('unwraps the data field of a successful envelope', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse({ code: 200, message: 'success', data: { status: 'alive' } }),
      ),
    )

    await expect(apiRequest('/livez')).resolves.toEqual({ status: 'alive' })
  })

  it('classifies a JSON 401 as unauthorized', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse({ code: 401, message: 'unauthorized' }, 401)),
    )

    const error = await expectApiError(apiRequest('/api/v1/status'))

    expect(error.kind).toBe('unauthorized')
    expect(error.status).toBe(401)
  })

  it('classifies a plain-text 404 as an unregistered route', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response('404 page not found', {
          status: 404,
          headers: { 'Content-Type': 'text/plain; charset=utf-8' },
        }),
      ),
    )

    const error = await expectApiError(apiRequest('/api/v1/accounts'))

    expect(error.kind).toBe('not-found')
    expect(error.detail).toBe('unregistered')
  })

  it('keeps the backend message of a 503 envelope', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse({ code: 503, message: 'onebot connection not ready' }, 503),
      ),
    )

    const error = await expectApiError(apiRequest('/readyz'))

    expect(error.kind).toBe('unavailable')
    expect(error.message).toBe('onebot connection not ready')
  })

  it('classifies a transport failure as a network error', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))

    const error = await expectApiError(apiRequest('/livez'))

    expect(error.kind).toBe('network')
  })

  it('classifies an aborted request as a timeout', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockRejectedValue(new DOMException('The operation timed out', 'TimeoutError')),
    )

    const error = await expectApiError(apiRequest('/api/v1/metrics'))

    expect(error.kind).toBe('timeout')
  })

  it('rejects a 200 response that is not a JSON envelope', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('<html></html>', { status: 200 })))

    const error = await expectApiError(apiRequest('/api/v1/metrics'))

    expect(error.kind).toBe('contract')
  })

  it('treats a non-200 envelope code on a 2xx response as an error', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse({ code: 500, message: 'collect metrics failed' })),
    )

    const error = await expectApiError(apiRequest('/api/v1/metrics'))

    expect(error.kind).toBe('server')
    expect(error.message).toBe('collect metrics failed')
  })
})
