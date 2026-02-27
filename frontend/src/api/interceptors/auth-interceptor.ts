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
        try {
          onUnauthenticated?.()
        } catch {
          // Preserve original auth error for downstream handling
        }
      }
      throw err
    }
  }
}
