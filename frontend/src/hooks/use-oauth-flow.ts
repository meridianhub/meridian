import { useCallback } from 'react'
import { generateCodeVerifier, generateCodeChallenge, generateState } from '@/lib/pkce'

const PKCE_VERIFIER_KEY = 'meridian_pkce_verifier'
const PKCE_STATE_KEY = 'meridian_pkce_state'

/**
 * Hook that initiates an OAuth authorization code flow with PKCE.
 * Generates PKCE parameters, stores them in sessionStorage, and redirects to Dex.
 */
export function useOAuthFlow() {
  const startFlow = useCallback(async (connectorId: string) => {
    const verifier = generateCodeVerifier()
    const challenge = await generateCodeChallenge(verifier)
    const state = generateState()

    sessionStorage.setItem(PKCE_VERIFIER_KEY, verifier)
    sessionStorage.setItem(PKCE_STATE_KEY, state)

    const params = new URLSearchParams({
      client_id: 'meridian-service',
      response_type: 'code',
      scope: 'openid email profile',
      redirect_uri: `${window.location.origin}/callback`,
      code_challenge: challenge,
      code_challenge_method: 'S256',
      state,
      connector_id: connectorId,
    })

    window.location.href = `/dex/auth?${params.toString()}`
  }, [])

  return { startFlow }
}

export { PKCE_VERIFIER_KEY, PKCE_STATE_KEY }
