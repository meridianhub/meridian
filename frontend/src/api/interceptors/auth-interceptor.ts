import type { Interceptor } from '@connectrpc/connect'

export type TokenGetter = () => string | null | undefined

export function createAuthInterceptor(getToken: TokenGetter): Interceptor {
  return (next) => async (req) => {
    const token = getToken()
    if (token) {
      req.header.set('Authorization', `Bearer ${token}`)
    }
    return await next(req)
  }
}
