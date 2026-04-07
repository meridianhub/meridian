import { useState, useEffect } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useAuth } from '@/contexts/auth-context'
import { ConsentCard, type ConsentInfo } from '@/components/consent-card'

interface ConsentResponse {
  redirect_url: string
}

export function OAuthConsentPage() {
  const { isAuthenticated, accessToken } = useAuth()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()

  const mcpState = searchParams.get('mcp_state') ?? ''
  const clientId = searchParams.get('client_id') ?? ''

  const [consentInfo, setConsentInfo] = useState<ConsentInfo | null>(null)
  const [fetchError, setFetchError] = useState('')
  const [loading, setLoading] = useState(false)

  // Redirect to login if not authenticated
  useEffect(() => {
    if (!isAuthenticated) {
      const returnUrl = `/auth/mcp-consent?mcp_state=${encodeURIComponent(mcpState)}&client_id=${encodeURIComponent(clientId)}`
      navigate(`/login?return_url=${encodeURIComponent(returnUrl)}`, { replace: true })
    }
  }, [isAuthenticated, navigate, mcpState, clientId])

  // Fetch consent info once authenticated, with both params present
  useEffect(() => {
    if (!isAuthenticated || !clientId || !mcpState) return

    const params = new URLSearchParams({ client_id: clientId, mcp_state: mcpState })
    fetch(`/mcp/consent-info?${params.toString()}`)
      .then(async (res) => {
        if (!res.ok) {
          const data = (await res.json().catch(() => null)) as { error?: string } | null
          setFetchError(data?.error ?? 'Failed to load consent information')
          return
        }
        const data = (await res.json()) as ConsentInfo
        setConsentInfo(data)
      })
      .catch(() => {
        setFetchError('Unable to reach the authorization service')
      })
  }, [isAuthenticated, clientId, mcpState])

  const handleConsent = async (action: 'approve' | 'deny') => {
    if (!accessToken || !mcpState || !clientId) return

    setLoading(true)
    try {
      const res = await fetch('/api/auth/mcp-consent', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${accessToken}`,
        },
        body: JSON.stringify({ mcp_state: mcpState, client_id: clientId, action }),
      })

      if (!res.ok) {
        const data = (await res.json().catch(() => null)) as { error?: string } | null
        setFetchError(data?.error ?? 'Authorization request failed')
        return
      }

      const data = (await res.json()) as ConsentResponse
      window.location.href = data.redirect_url
    } catch {
      setFetchError('Unable to reach the authorization service')
    } finally {
      setLoading(false)
    }
  }

  if (!isAuthenticated) {
    return null
  }

  if (!mcpState || !clientId) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <div className="w-full max-w-md space-y-4 px-4">
          <p role="alert" className="rounded-md bg-destructive/10 px-4 py-3 text-sm text-destructive">
            Missing required authorization parameters.
          </p>
        </div>
      </div>
    )
  }

  if (fetchError) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <div className="w-full max-w-md space-y-4 px-4">
          <p role="alert" className="rounded-md bg-destructive/10 px-4 py-3 text-sm text-destructive">
            {fetchError}
          </p>
        </div>
      </div>
    )
  }

  if (!consentInfo) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <div className="h-8 w-8 animate-spin rounded-full border-2 border-primary border-t-transparent" />
      </div>
    )
  }

  return (
    <div className="flex min-h-screen items-center justify-center px-4">
      <ConsentCard
        consentInfo={consentInfo}
        onApprove={() => void handleConsent('approve')}
        onDeny={() => void handleConsent('deny')}
        loading={loading}
      />
    </div>
  )
}
