import type { Interceptor } from '@connectrpc/connect'

export type TenantSlugGetter = () => string | null | undefined

export function createTenantInterceptor(getTenantSlug: TenantSlugGetter): Interceptor {
  return (next) => async (req) => {
    const slug = getTenantSlug()
    if (slug) {
      req.header.set('X-Tenant-Slug', slug)
    }
    return next(req)
  }
}
