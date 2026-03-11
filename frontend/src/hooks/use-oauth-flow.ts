import { useCallback } from 'react'

/**
 * Hook that initiates SSO login by redirecting to the BFF SSO endpoint.
 * The BFF handles PKCE, state, and token exchange server-side.
 */
export function useOAuthFlow() {
  const startFlow = useCallback((connectorId: string) => {
    const returnUrl = encodeURIComponent(window.location.pathname)
    window.location.href = `/api/auth/sso/${encodeURIComponent(connectorId)}?return_url=${returnUrl}`
  }, [])

  return { startFlow }
}
