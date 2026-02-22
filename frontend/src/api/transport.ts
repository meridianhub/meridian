import { createConnectTransport } from '@connectrpc/connect-web'
import type { Transport } from '@connectrpc/connect'
import { createAuthInterceptor, type TokenGetter } from './interceptors/auth-interceptor'
import { apiConfig, buildTenantBaseUrl } from './config'

export function createTenantTransport(
  tenantSlug: string | null,
  getToken: TokenGetter,
): Transport {
  const baseUrl = tenantSlug ? buildTenantBaseUrl(tenantSlug) : apiConfig.baseUrl

  return createConnectTransport({
    baseUrl,
    interceptors: [createAuthInterceptor(getToken)],
    useBinaryFormat: apiConfig.useBinaryFormat,
  })
}
