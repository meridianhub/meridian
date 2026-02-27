import { useCallback } from 'react'
import { useAuth } from '@/contexts/auth-context'
import { useTenantContext } from '@/contexts/tenant-context'

/**
 * Returns a fetch wrapper that automatically includes Authorization and
 * X-Tenant-Slug headers. Use this for pages that call REST/gRPC-web
 * endpoints via raw fetch() instead of typed Connect-RPC clients.
 */
export function useAuthenticatedFetch() {
  const { accessToken } = useAuth()
  const { tenantSlug } = useTenantContext()

  return useCallback(
    (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      const headers = new Headers(init?.headers)
      if (accessToken) {
        headers.set('Authorization', `Bearer ${accessToken}`)
      }
      if (tenantSlug) {
        headers.set('X-Tenant-Slug', tenantSlug)
      }
      if (!headers.has('Content-Type')) {
        headers.set('Content-Type', 'application/json')
      }
      return fetch(input, { ...init, headers })
    },
    [accessToken, tenantSlug],
  )
}
