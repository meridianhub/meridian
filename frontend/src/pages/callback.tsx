import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useAuth } from '@/contexts/auth-context'
import { PKCE_VERIFIER_KEY, PKCE_STATE_KEY } from '@/hooks/use-oauth-flow'

/**
 * Synchronously validate the callback URL parameters and PKCE state.
 * Returns either a validated payload for code exchange or an error message.
 */
function validateCallbackParams(searchParams: URLSearchParams): {
  valid: false
  error: string
} | {
  valid: true
  code: string
  verifier: string
} {
  const errorParam = searchParams.get('error')
  const errorDescription = searchParams.get('error_description')
  if (errorParam) {
    return { valid: false, error: errorDescription ?? errorParam }
  }

  const code = searchParams.get('code')
  const state = searchParams.get('state')
  if (!code || !state) {
    return { valid: false, error: 'Missing authorization code or state parameter' }
  }

  const storedState = sessionStorage.getItem(PKCE_STATE_KEY)
  if (state !== storedState) {
    return { valid: false, error: 'Invalid state parameter - possible CSRF attack' }
  }

  const verifier = sessionStorage.getItem(PKCE_VERIFIER_KEY)
  if (!verifier) {
    return { valid: false, error: 'Missing PKCE verifier - please try signing in again' }
  }

  // Clean up stored PKCE values
  sessionStorage.removeItem(PKCE_STATE_KEY)
  sessionStorage.removeItem(PKCE_VERIFIER_KEY)

  return { valid: true, code, verifier }
}

/**
 * OAuth callback page that handles the authorization code exchange.
 * Validates state, retrieves PKCE verifier, and exchanges the code for tokens.
 */
export function CallbackPage() {
  const [searchParams] = useSearchParams()
  const navigate = useNavigate()
  const { login } = useAuth()
  const [exchangeError, setExchangeError] = useState<string | null>(null)

  const validation = useMemo(() => validateCallbackParams(searchParams), [searchParams])

  useEffect(() => {
    if (!validation.valid) return

    const { code, verifier } = validation

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
          setExchangeError(text.includes('invalid_grant') ? 'Authorization code expired or invalid' : 'Token exchange failed')
          return
        }

        const data = (await response.json()) as { id_token?: string; access_token?: string }
        const token = data.id_token ?? data.access_token
        if (!token) {
          setExchangeError('No token received from identity provider')
          return
        }

        login(token)
        navigate('/')
      } catch {
        setExchangeError('Unable to reach identity provider')
      }
    }

    void exchangeCode()
  }, [validation, login, navigate])

  const error = validation.valid ? exchangeError : validation.error

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
