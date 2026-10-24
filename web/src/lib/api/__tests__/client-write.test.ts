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

describe('apiRequest write methods', () => {
  beforeEach(() => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('serialises the body and sets the JSON content type', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ code: 200, message: 'success', data: { id: 'abc' } }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(
      apiRequest('/api/v1/memories', {
        method: 'POST',
        body: { content: '值班交接', visibility: 'group' },
      }),
    ).resolves.toEqual({ id: 'abc' })

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(init.method).toBe('POST')
    expect(init.body).toBe(JSON.stringify({ content: '值班交接', visibility: 'group' }))
    expect(init.headers).toMatchObject({ 'Content-Type': 'application/json' })
  })

  it('sends no body or content type for a DELETE without a body', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ code: 200, message: 'success', data: { deleted: true } }))
    vi.stubGlobal('fetch', fetchMock)

    await apiRequest('/api/v1/memories/abc', { method: 'DELETE' })

    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(init.method).toBe('DELETE')
    expect(init.body).toBeUndefined()
    expect(init.headers).not.toHaveProperty('Content-Type')
  })

  it('classifies a 409 as a conflict', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(
          { code: 409, message: 'bot account not known yet; connect OneBot first' },
          409,
        ),
      ),
    )

    const error = await expectApiError(apiRequest('/api/v1/scopes/group/1', { method: 'PUT' }))

    expect(error.kind).toBe('conflict')
    expect(error.message).toContain('connect OneBot')
  })

  it('classifies a 400 as invalid input', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse({ code: 400, message: 'invalid scope policy' }, 400)),
    )

    const error = await expectApiError(apiRequest('/api/v1/scopes/group/1', { method: 'PUT' }))

    expect(error.kind).toBe('invalid')
  })
})
