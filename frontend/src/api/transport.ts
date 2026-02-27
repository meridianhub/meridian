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
  onUnauthenticated?: () => void,
): Transport {
  // In development or demo mode, keep the base URL and route via X-Tenant-Slug header
  // (the gateway's LOCAL_DEV_MODE resolves tenants from the header).
  // In production, use subdomain-based URL for tenant routing.
  const useHeaderRouting =
    import.meta.env.DEV || import.meta.env.VITE_DEMO_MODE === 'true'
  const baseUrl =
    tenantSlug && !useHeaderRouting ? buildTenantBaseUrl(tenantSlug) : apiConfig.baseUrl

  return createConnectTransport({
    baseUrl,
    interceptors: [
      createAuthInterceptor(getToken, onUnauthenticated),
      createTenantInterceptor(getTenantSlug),
    ],
    useBinaryFormat: apiConfig.useBinaryFormat,
  })
}
