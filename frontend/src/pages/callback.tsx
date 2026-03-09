import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useAuth } from '@/contexts/auth-context'
import { PKCE_VERIFIER_KEY, PKCE_STATE_KEY } from '@/hooks/use-oauth-flow'

/**
 * OAuth callback page that handles the authorization code exchange.
 * Validates state, retrieves PKCE verifier, and exchanges the code for tokens.
 */
export function CallbackPage() {
  const [searchParams] = useSearchParams()
  const navigate = useNavigate()
  const { login } = useAuth()
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const code = searchParams.get('code')
    const state = searchParams.get('state')
    const errorParam = searchParams.get('error')
    const errorDescription = searchParams.get('error_description')

    if (errorParam) {
      setError(errorDescription ?? errorParam)
      return
    }

    if (!code || !state) {
      setError('Missing authorization code or state parameter')
      return
    }

    const storedState = sessionStorage.getItem(PKCE_STATE_KEY)
    if (state !== storedState) {
      setError('Invalid state parameter - possible CSRF attack')
      return
    }

    const verifier = sessionStorage.getItem(PKCE_VERIFIER_KEY)
    if (!verifier) {
      setError('Missing PKCE verifier - please try signing in again')
      return
    }

    // Clean up stored PKCE values
    sessionStorage.removeItem(PKCE_STATE_KEY)
    sessionStorage.removeItem(PKCE_VERIFIER_KEY)

    const exchangeCode = async () => {
      try {
        const body = new URLSearchParams({
          grant_type: 'authorization_code',
          client_id: 'meridian-service',
          code,
          redirect_uri: `${window.location.origin}/callback`,
          code_verifier: verifier,
        })

        const response = await fetch('/dex/token', {
          method: 'POST',
          headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
          body: body.toString(),
        })

        if (!response.ok) {
          const text = await response.text()
          setError(text.includes('invalid_grant') ? 'Authorization code expired or invalid' : 'Token exchange failed')
          return
        }

        const data = (await response.json()) as { id_token?: string; access_token?: string }
        const token = data.id_token ?? data.access_token
        if (!token) {
          setError('No token received from identity provider')
          return
        }

        login(token)
        navigate('/')
      } catch {
        setError('Unable to reach identity provider')
      }
    }

    void exchangeCode()
  }, [searchParams, login, navigate])

  if (error) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <div className="w-full max-w-sm space-y-4 px-4 text-center">
          <h1 className="text-2xl font-semibold">Authentication Failed</h1>
          <p className="text-sm text-destructive">{error}</p>
          <button
            onClick={() => navigate('/login')}
            className="w-full rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
          >
            Return to Login
          </button>
        </div>
      </div>
    )
  }

  return (
    <div className="flex min-h-screen items-center justify-center">
      <div className="text-center">
        <p className="text-muted-foreground">Completing sign in...</p>
      </div>
    </div>
  )
}
