import { describe, it, expect, vi } from 'vitest'
import { ConnectError, Code } from '@connectrpc/connect'
import { createAuthInterceptor } from '@/api/interceptors/auth-interceptor'
import type { UnaryRequest } from '@connectrpc/connect'

function makeRequest(headers: Headers = new Headers()): UnaryRequest {
  return {
    header: headers,
    url: 'https://example.com/test',
    init: {},
    signal: new AbortController().signal,
    service: {} as UnaryRequest['service'],
    method: {} as UnaryRequest['method'],
    message: {},
    contextValues: undefined as unknown as UnaryRequest['contextValues'],
  }
}

describe('createAuthInterceptor', () => {
  it('adds Authorization header when token is present', async () => {
    const getToken = vi.fn(() => 'test-token-123')
    const interceptor = createAuthInterceptor(getToken)
    const req = makeRequest()
    const next = vi.fn(async (_r: UnaryRequest) => ({ header: new Headers(), message: {} } as never))

    await interceptor(next)(req)

    expect(req.header.get('Authorization')).toBe('Bearer test-token-123')
    expect(next).toHaveBeenCalledOnce()
  })

  it('does not add Authorization header when token is null', async () => {
    const getToken = vi.fn(() => null)
    const interceptor = createAuthInterceptor(getToken)
    const req = makeRequest()
    const next = vi.fn(async (_r: UnaryRequest) => ({ header: new Headers(), message: {} } as never))

    await interceptor(next)(req)

    expect(req.header.get('Authorization')).toBeNull()
  })

  it('does not add Authorization header when token is undefined', async () => {
    const getToken = vi.fn(() => undefined)
    const interceptor = createAuthInterceptor(getToken)
    const req = makeRequest()
    const next = vi.fn(async (_r: UnaryRequest) => ({ header: new Headers(), message: {} } as never))

    await interceptor(next)(req)

    expect(req.header.get('Authorization')).toBeNull()
  })

  it('calls next with the modified request', async () => {
    const getToken = vi.fn(() => 'my-token')
    const interceptor = createAuthInterceptor(getToken)
    const req = makeRequest()
    const mockResponse = { header: new Headers(), message: {} }
    const next = vi.fn(async () => mockResponse as never)

    const result = await interceptor(next)(req)

    expect(next).toHaveBeenCalledWith(req)
    expect(result).toBe(mockResponse)
  })

  it('uses Bearer prefix for the token', async () => {
    const getToken = vi.fn(() => 'eyJhbGciOiJSUzI1NiJ9.payload.signature')
    const interceptor = createAuthInterceptor(getToken)
    const req = makeRequest()
    const next = vi.fn(async () => ({ header: new Headers(), message: {} } as never))

    await interceptor(next)(req)

    expect(req.header.get('Authorization')).toBe(
      'Bearer eyJhbGciOiJSUzI1NiJ9.payload.signature',
    )
  })

  it('calls onUnauthenticated and re-throws on Unauthenticated error', async () => {
    const getToken = vi.fn(() => 'token')
    const onUnauthenticated = vi.fn()
    const interceptor = createAuthInterceptor(getToken, onUnauthenticated)
    const req = makeRequest()
    const next = vi.fn(async () => {
      throw new ConnectError('token expired', Code.Unauthenticated)
    })

    await expect(interceptor(next)(req)).rejects.toThrow(ConnectError)
    expect(onUnauthenticated).toHaveBeenCalledOnce()
  })

  it('does not call onUnauthenticated on non-auth errors', async () => {
    const getToken = vi.fn(() => 'token')
    const onUnauthenticated = vi.fn()
    const interceptor = createAuthInterceptor(getToken, onUnauthenticated)
    const req = makeRequest()
    const next = vi.fn(async () => {
      throw new ConnectError('not found', Code.NotFound)
    })

    await expect(interceptor(next)(req)).rejects.toThrow(ConnectError)
    expect(onUnauthenticated).not.toHaveBeenCalled()
  })
})
