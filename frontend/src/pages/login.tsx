import { useState, useCallback, type FormEvent } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { useAuth } from '@/contexts/auth-context'
import { useAuthProviders, type AuthProvider as AuthProviderType } from '@/hooks/use-auth-providers'
import { useOAuthFlow } from '@/hooks/use-oauth-flow'
import { isBaseDomain } from '@/lib/tenant-utils'
import { ProviderButton } from '@/components/auth/provider-button'
import { AuthDivider } from '@/components/auth/auth-divider'

function isBareDomain(): boolean {
  return isBaseDomain(window.location.hostname)
}

export function LoginPage() {
  const { login } = useAuth()
  const navigate = useNavigate()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const { data: providers } = useAuthProviders()
  const { startFlow } = useOAuthFlow()

  const onBareDomain = isBareDomain()
  const externalProviders = providers?.filter((p: AuthProviderType) => p.type === 'oidc') ?? []

  const handleLogin = useCallback(
    async (e: FormEvent) => {
      e.preventDefault()
      setError('')
      setLoading(true)

      try {
        const response = await fetch('/api/auth/login', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ email, password }),
        })

        if (!response.ok) {
          const data = (await response.json().catch(() => null)) as { error?: string } | null
          setError(data?.error ?? 'Authentication failed')
          return
        }

        const data = (await response.json()) as { access_token?: string }
        const token = data.access_token
        if (!token) {
          setError('No token received from server')
          return
        }

        login(token)
        navigate('/')
      } catch {
        setError('Unable to reach authentication service')
      } finally {
        setLoading(false)
      }
    },
    [email, password, login, navigate],
  )

  const devLogin = useCallback(
    (role: 'platform-admin' | 'tenant-user') => {
      const header = btoa(JSON.stringify({ alg: 'none', typ: 'JWT' }))
      const payload = btoa(
        JSON.stringify({
          userId: 'dev-user',
          tenantId: role === 'tenant-user' ? 'dev-tenant' : undefined,
          roles: [role],
          scopes: ['read', 'write'],
          exp: Math.floor(Date.now() / 1000) + 86400,
          iss: 'meridian-dev',
          aud: 'meridian-console',
          sub: 'dev-user',
        }),
      )
      login(`${header}.${payload}.dev-signature`)
      navigate('/')
    },
    [login, navigate],
  )

  // On bare domain (no tenant subdomain) in production, show guidance
  if (onBareDomain && !import.meta.env.DEV) {
    const baseDomain = import.meta.env.VITE_BASE_DOMAIN ?? 'meridianhub.cloud'
    return (
      <div className="flex min-h-screen items-center justify-center">
        <div className="w-full max-w-sm space-y-6 px-4 text-center">
          <h1 className="text-2xl font-semibold">Meridian Operations Console</h1>
          <p className="mt-2 text-muted-foreground">
            Please access your organization&apos;s login page at:
          </p>
          <p className="font-mono text-sm text-muted-foreground">
            https://your-org.{baseDomain}
          </p>
        </div>
      </div>
    )
  }

  return (
    <div className="flex min-h-screen items-center justify-center">
      <div className="w-full max-w-sm space-y-6 px-4">
        <div className="text-center">
          <h1 className="text-2xl font-semibold">Meridian Operations Console</h1>
          <p className="mt-2 text-muted-foreground">Please sign in to continue.</p>
        </div>

        {/* Password login form - shown in production builds only */}
        {!import.meta.env.DEV && (
          <form onSubmit={(e) => void handleLogin(e)} className="space-y-4">
            <div>
              <label htmlFor="email" className="block text-sm font-medium mb-1">
                Email
              </label>
              <input
                id="email"
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
                autoComplete="email"
                className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                placeholder="admin@volterra.energy"
              />
            </div>
            <div>
              <label htmlFor="password" className="block text-sm font-medium mb-1">
                Password
              </label>
              <input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
                autoComplete="current-password"
                className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              />
            </div>
            {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
            <button
              type="submit"
              disabled={loading}
              className="w-full rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
            >
              {loading ? 'Signing in...' : 'Sign in'}
            </button>
          </form>
        )}

        {/* External auth provider buttons */}
        {externalProviders.length > 0 && (
          <>
            {!import.meta.env.DEV && <AuthDivider />}
            <div className="space-y-2">
              {externalProviders.map((provider: AuthProviderType) => (
                <ProviderButton
                  key={provider.id}
                  provider={provider}
                  onClick={() => startFlow(provider.id)}
                />
              ))}
            </div>
          </>
        )}

        {/* Registration link - shown in production builds only */}
        {!import.meta.env.DEV && (
          <p className="text-center text-sm text-muted-foreground">
            Don&apos;t have an account?{' '}
            <Link to="/register" className="text-primary underline-offset-4 hover:underline">
              Create one
            </Link>
          </p>
        )}

        {/* Dev-only fake JWT buttons (also shown in E2E mode) */}
        {(import.meta.env.DEV || import.meta.env.VITE_E2E_MODE === 'true') && (
          <div className="space-y-2">
            <p className="text-xs text-muted-foreground uppercase tracking-wider text-center">
              Development Login
            </p>
            <div className="flex gap-2 justify-center">
              <button
                onClick={() => devLogin('platform-admin')}
                className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
              >
                Platform Admin
              </button>
              <button
                onClick={() => devLogin('tenant-user')}
                className="rounded-md bg-secondary px-4 py-2 text-sm font-medium text-secondary-foreground hover:bg-secondary/80"
              >
                Tenant User
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
