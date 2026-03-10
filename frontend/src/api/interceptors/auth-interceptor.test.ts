import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ConnectError, Code } from '@connectrpc/connect'
import { createAuthInterceptor } from './auth-interceptor'

function createMockNext(error?: Error) {
  return vi.fn().mockImplementation(() => {
    if (error) throw error
    return Promise.resolve({ message: 'ok' })
  })
}

function createMockReq() {
  return {
    header: new Headers(),
    method: {},
    url: 'http://localhost/test',
  }
}

describe('createAuthInterceptor', () => {
  const getToken = vi.fn<() => string | null>()
  const onUnauthenticated = vi.fn()

  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('sets Authorization header when token is present', async () => {
    getToken.mockReturnValue('my-token')
    const interceptor = createAuthInterceptor(getToken, onUnauthenticated)
    const next = createMockNext()
    const req = createMockReq()

    await interceptor(next)(req as never)

    expect(req.header.get('Authorization')).toBe('Bearer my-token')
  })

  it('does not set Authorization header when token is null', async () => {
    getToken.mockReturnValue(null)
    const interceptor = createAuthInterceptor(getToken, onUnauthenticated)
    const next = createMockNext()
    const req = createMockReq()

    await interceptor(next)(req as never)

    expect(req.header.get('Authorization')).toBeNull()
  })

  it('calls onUnauthenticated on 401 (Unauthenticated)', async () => {
    getToken.mockReturnValue('token')
    const error = new ConnectError('unauthenticated', Code.Unauthenticated)
    const interceptor = createAuthInterceptor(getToken, onUnauthenticated)
    const next = createMockNext(error)
    const req = createMockReq()

    await expect(interceptor(next)(req as never)).rejects.toThrow(error)

    expect(onUnauthenticated).toHaveBeenCalledOnce()
  })

  it('rethrows 403 (PermissionDenied) without calling onUnauthenticated', async () => {
    getToken.mockReturnValue('token')
    const error = new ConnectError('forbidden', Code.PermissionDenied)
    const interceptor = createAuthInterceptor(getToken, onUnauthenticated)
    const next = createMockNext(error)
    const req = createMockReq()

    await expect(interceptor(next)(req as never)).rejects.toThrow(error)

    expect(onUnauthenticated).not.toHaveBeenCalled()
  })

  it('does not call onUnauthenticated for other errors', async () => {
    getToken.mockReturnValue('token')
    const error = new ConnectError('not found', Code.NotFound)
    const interceptor = createAuthInterceptor(getToken, onUnauthenticated)
    const next = createMockNext(error)
    const req = createMockReq()

    await expect(interceptor(next)(req as never)).rejects.toThrow(error)

    expect(onUnauthenticated).not.toHaveBeenCalled()
  })

  it('rethrows the original error in all cases', async () => {
    getToken.mockReturnValue('token')
    const error = new ConnectError('internal', Code.Internal)
    const interceptor = createAuthInterceptor(getToken, onUnauthenticated)
    const next = createMockNext(error)
    const req = createMockReq()

    await expect(interceptor(next)(req as never)).rejects.toThrow(error)
  })
})
