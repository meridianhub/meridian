import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ConnectError, Code } from '@connectrpc/connect'
import { toast } from 'sonner'
import { createAuthInterceptor } from './auth-interceptor'

vi.mock('sonner', () => ({
  toast: { error: vi.fn() },
}))

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

  it('calls onUnauthenticated on 401 (Unauthenticated) and does not toast', async () => {
    getToken.mockReturnValue('token')
    const error = new ConnectError('unauthenticated', Code.Unauthenticated)
    const interceptor = createAuthInterceptor(getToken, onUnauthenticated)
    const next = createMockNext(error)
    const req = createMockReq()

    await expect(interceptor(next)(req as never)).rejects.toThrow(error)

    expect(onUnauthenticated).toHaveBeenCalledOnce()
    expect(toast.error).not.toHaveBeenCalled()
  })

  it('shows toast on 403 (PermissionDenied) and does not call onUnauthenticated', async () => {
    getToken.mockReturnValue('token')
    const error = new ConnectError('forbidden', Code.PermissionDenied)
    const interceptor = createAuthInterceptor(getToken, onUnauthenticated)
    const next = createMockNext(error)
    const req = createMockReq()

    await expect(interceptor(next)(req as never)).rejects.toThrow(error)

    expect(onUnauthenticated).not.toHaveBeenCalled()
    expect(toast.error).toHaveBeenCalledWith(
      'You do not have permission to perform this action.',
    )
  })

  it('does not call onUnauthenticated or toast for other errors', async () => {
    getToken.mockReturnValue('token')
    const error = new ConnectError('not found', Code.NotFound)
    const interceptor = createAuthInterceptor(getToken, onUnauthenticated)
    const next = createMockNext(error)
    const req = createMockReq()

    await expect(interceptor(next)(req as never)).rejects.toThrow(error)

    expect(onUnauthenticated).not.toHaveBeenCalled()
    expect(toast.error).not.toHaveBeenCalled()
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
