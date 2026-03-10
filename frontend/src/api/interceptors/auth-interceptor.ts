import { ConnectError, Code, type Interceptor } from '@connectrpc/connect'
import { toast } from 'sonner'

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
      if (err instanceof ConnectError) {
        if (err.code === Code.Unauthenticated) {
          onUnauthenticated?.()
        } else if (err.code === Code.PermissionDenied) {
          toast.error('You do not have permission to perform this action.')
        }
      }
      throw err
    }
  }
}
