import { createConnectTransport } from '@connectrpc/connect-web'
import type { Transport } from '@connectrpc/connect'
import { createAuthInterceptor, type TokenGetter } from './interceptors/auth-interceptor'
import {
  createTenantInterceptor,
  type TenantSlugGetter,
} from './interceptors/tenant-interceptor'
import { apiConfig, buildTenantBaseUrl } from './config'

export function createTenantTransport(
  tenantSlug: string | null,
  getToken: TokenGetter,
  getTenantSlug: TenantSlugGetter,
): Transport {
  // In development, keep the base URL and route via X-Tenant-Slug header
  // (the gateway's LOCAL_DEV_MODE resolves tenants from the header).
  // In production, use subdomain-based URL for tenant routing.
  const baseUrl =
    tenantSlug && !import.meta.env.DEV ? buildTenantBaseUrl(tenantSlug) : apiConfig.baseUrl

  return createConnectTransport({
    baseUrl,
    interceptors: [createAuthInterceptor(getToken), createTenantInterceptor(getTenantSlug)],
    useBinaryFormat: apiConfig.useBinaryFormat,
  })
}
