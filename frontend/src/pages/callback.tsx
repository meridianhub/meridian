import { useEffect, useMemo } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useAuth } from '@/contexts/auth-context'

function getFragmentToken(): string | null {
  const hash = window.location.hash.substring(1)
  const params = new URLSearchParams(hash)
  return params.get('access_token')
}

/**
 * OAuth callback page that receives the JWT from the BFF.
 * The BFF redirects here with the token in the URL fragment: /callback#access_token=<jwt>
 */
export function CallbackPage() {
  const [searchParams] = useSearchParams()
  const navigate = useNavigate()
  const { login } = useAuth()

  // Compute token and error synchronously from URL on mount
  const { token, error } = useMemo(() => {
    const fragmentToken = getFragmentToken()
    if (fragmentToken) {
      return { token: fragmentToken, error: null }
    }

    const errorParam = searchParams.get('error')
    if (errorParam) {
      return { token: null, error: searchParams.get('error_description') ?? errorParam }
    }

    return { token: null, error: 'No authentication token received' }
  }, [searchParams])

  useEffect(() => {
    if (!token) return

    // Clear fragment from URL for security (don't leave JWT in browser history)
    window.history.replaceState(null, '', window.location.pathname)
    login(token)

    // Navigate to the return_url if the BFF passed one through, otherwise go home
    const returnUrl = searchParams.get('return_url')
    navigate(returnUrl || '/', { replace: true })
  }, [token, login, navigate, searchParams])

  if (error) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <div className="w-full max-w-sm space-y-4 px-4 text-center">
          <h1 className="text-2xl font-semibold">Authentication Failed</h1>
          <p className="text-sm text-destructive">{error}</p>
          <button
            onClick={() => navigate('/login', { replace: true })}
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
