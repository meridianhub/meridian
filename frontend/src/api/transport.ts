import { createConnectTransport } from '@connectrpc/connect-web'
import type { Transport } from '@connectrpc/connect'
import { createAuthInterceptor, type TokenGetter } from './interceptors/auth-interceptor'
import {
  createTenantInterceptor,
  type TenantSlugGetter,
} from './interceptors/tenant-interceptor'
import { apiConfig, buildTenantBaseUrl, isOnTenantSubdomain } from './config'

export function createTenantTransport(
  tenantSlug: string | null,
  getToken: TokenGetter,
  getTenantSlug: TenantSlugGetter,
  onUnauthenticated?: () => void,
): Transport {
  // If the browser is already on a tenant subdomain, API calls go to the same
  // origin (subdomain routing). Otherwise in dev/demo mode, route via
  // X-Tenant-Slug header (the gateway's LOCAL_DEV_MODE resolves from the header).
  const onSubdomain = isOnTenantSubdomain()
  const useHeaderRouting =
    !onSubdomain &&
    (import.meta.env.DEV || import.meta.env.VITE_DEMO_MODE === 'true')
  const configuredPath = new URL(apiConfig.baseUrl).pathname.replace(/\/$/, '')
  const baseUrl = onSubdomain
    ? `${window.location.origin}${configuredPath}`
    : tenantSlug && !useHeaderRouting
      ? buildTenantBaseUrl(tenantSlug)
      : apiConfig.baseUrl

  return createConnectTransport({
    baseUrl,
    interceptors: [
      createAuthInterceptor(getToken, onUnauthenticated),
      createTenantInterceptor(getTenantSlug),
    ],
    useBinaryFormat: apiConfig.useBinaryFormat,
  })
}
