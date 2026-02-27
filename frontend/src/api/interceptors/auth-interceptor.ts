import { ConnectError, Code, type Interceptor } from '@connectrpc/connect'

export type TokenGetter = () => string | null | undefined

export function createAuthInterceptor(
  getToken: TokenGetter,
  onUnauthenticated?: () => void,
): Interceptor {
  return (next) => async (req) => {
    const token = getToken()
    if (token) {
      req.header.set('Authorization', `Bearer ${token}`)
    }
    try {
      return await next(req)
    } catch (err) {
      if (err instanceof ConnectError && err.code === Code.Unauthenticated) {
        // Defer logout so the error propagates to React Query first,
        // preventing a redirect before the component can handle the error.
        queueMicrotask(() => {
          try {
            onUnauthenticated?.()
          } catch {
            // Preserve original auth error for downstream handling
          }
        })
      }
      throw err
    }
  }
}
