import { type ReactNode, lazy, Suspense, useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { BrowserRouter, Routes, Route, useLocation, useNavigate } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { ReactQueryDevtools } from '@tanstack/react-query-devtools'
import { queryClient } from '@/lib/query-client'
import { PageErrorBoundary, RouteErrorBoundary } from '@/components/error-boundary'
import { AuthProvider, useAuth } from '@/contexts/auth-context'
import { TenantProvider, useTenantContext } from '@/contexts/tenant-context'
import { useTenants } from '@/hooks/use-tenants'
import { useAuthProviders, type AuthProvider as AuthProviderType } from '@/hooks/use-auth-providers'
import { useOAuthFlow } from '@/hooks/use-oauth-flow'
import { getTenantSlugFromSubdomain } from '@/lib/tenant-utils'
import { ApiClientProvider } from '@/api/context'
import { ProtectedRoute, PlatformOnlyRoute, AdminOnlyRoute, TenantSubdomainEnforcer } from '@/components/routing'
import { FeatureGuard } from '@/components/feature-guard'
import { AppShell } from '@/components/layout/app-shell'
import { TooltipProvider } from '@/components/ui/tooltip'
import { Toaster } from 'sonner'
import { CallbackPage } from '@/pages/callback'
import { ProviderButton } from '@/components/auth/provider-button'
import { AuthDivider } from '@/components/auth/auth-divider'
import { AccountsPage, AccountDetailPage } from '@/features/accounts'
import { PaymentsPage, PaymentDetailPage } from '@/features/payments'
import { LedgerPage, BookingLogDetailPage } from '@/features/ledger'
import { PositionsPage, PositionDetailPage } from '@/features/positions'
import { PartiesPage, PartyDetailPage } from '@/features/parties'
import { TenantsPage, TenantDetailPage } from '@/features/tenants'
import { ReconciliationPage, ReconciliationDetailPage } from '@/features/reconciliation'
import { AuditLogPage } from '@/features/audit'
import { StarlarkConfigPage, StarlarkDetailPage } from '@/features/sagas'
import { MappingsPage, MappingDetailPage } from '@/features/mappings'
import { ReferenceDataHubPage, InstrumentsPage, AccountTypesPage, NodesPage } from '@/features/reference-data'
import { InternalAccountsPage, InternalAccountDetailPage } from '@/features/internal-accounts'
import { MarketDataPage, DatasetDetailPage } from '@/features/market-data'
import { ForecastingPage } from '@/features/forecasting'
import { DashboardPage } from '@/features/dashboard'
import { ManifestsPage } from '@/features/manifests'
import { McpConfigPage } from '@/features/mcp-config'
import { TransactionsPage } from '@/features/transactions'
import { UsersListPage, UserDetailPage } from '@/features/identity'
import { CookbookPage } from '@/features/cookbook'

const CookbookPatternsPage = lazy(() =>
  import('@/features/cookbook/pages/patterns').then((m) => ({ default: m.CookbookPatternsPage })),
)
const CookbookComponentsPage = lazy(() =>
  import('@/features/cookbook/pages/components').then((m) => ({ default: m.CookbookComponentsPage })),
)
const CookbookDetailPage = lazy(() =>
  import('@/features/cookbook/pages/detail').then((m) => ({ default: m.CookbookDetailPage })),
)
const CookbookGraphPage = lazy(() =>
  import('@/features/cookbook/pages/graph').then((m) => ({ default: m.CookbookGraphPage })),
)
import { ThemePreviewPanel } from '@/components/dev/theme-preview-panel'

// Placeholder page components - replaced as each page task is implemented
function PlaceholderPage({ title }: { title: string }) {
  return (
    <div className="p-6">
      <h1 className="text-2xl font-semibold">{title}</h1>
      <p className="mt-2 text-muted-foreground">Coming soon.</p>
    </div>
  )
}

function isBareDomain(): boolean {
  const hostname = window.location.hostname.toLowerCase()
  if (hostname === 'localhost' || hostname === '127.0.0.1') return false
  const baseDomain = (import.meta.env.VITE_BASE_DOMAIN ?? 'meridianhub.cloud').toLowerCase()
  return hostname === baseDomain
}

function LoginPage() {
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

  const handleDexLogin = useCallback(
    async (e: FormEvent) => {
      e.preventDefault()
      setError('')
      setLoading(true)

      try {
        const body = new URLSearchParams({
          grant_type: 'password',
          client_id: 'meridian-service',
          scope: 'openid email profile',
          username: email,
          password,
        })

        const response = await fetch('/dex/token', {
          method: 'POST',
          headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
          body: body.toString(),
        })

        if (!response.ok) {
          const text = await response.text()
          setError(text.includes('invalid_grant') ? 'Invalid email or password' : 'Authentication failed')
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

        {/* Dex login form - shown in demo mode and production */}
        {(import.meta.env.VITE_DEMO_MODE === 'true' || !import.meta.env.DEV) && (
          <form onSubmit={(e) => void handleDexLogin(e)} className="space-y-4">
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
            {error && <p className="text-sm text-destructive">{error}</p>}
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
            {(import.meta.env.VITE_DEMO_MODE === 'true' || !import.meta.env.DEV) && <AuthDivider />}
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

        {/* Dev-only fake JWT buttons */}
        {import.meta.env.DEV && (
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

function NotFoundPage() {
  return (
    <div className="p-6">
      <h1 className="text-2xl font-semibold">404 - Page Not Found</h1>
      <p className="mt-2 text-muted-foreground">The page you are looking for does not exist.</p>
    </div>
  )
}

/** Wraps a page element with a route-level error boundary. */
function guarded(element: ReactNode) {
  return <RouteErrorBoundary>{element}</RouteErrorBoundary>
}

/**
 * Layout wrapper that reads the current path from React Router
 * and passes it to AppShell for active nav highlighting.
 *
 * Each route is wrapped with RouteErrorBoundary so that a crash in one page
 * shows an inline error message instead of taking down the entire app.
 */
function AppShellLayout() {
  const { pathname } = useLocation()
  const { lens } = useAuth()
  const isPlatformAdmin = lens === 'platform'

  return (
    <AppShell currentPath={pathname}>
      <Routes>
        {/* Tenant-scoped routes */}
        <Route path="/" element={guarded(<DashboardPage />)} />
        <Route path="/accounts" element={<FeatureGuard feature="accounts">{guarded(<AccountsPage />)}</FeatureGuard>} />
        <Route path="/accounts/:accountId" element={<FeatureGuard feature="accounts">{guarded(<AccountDetailPage />)}</FeatureGuard>} />
        <Route path="/internal-accounts" element={<FeatureGuard feature="internal-accounts">{guarded(<InternalAccountsPage />)}</FeatureGuard>} />
        <Route path="/internal-accounts/:accountId" element={<FeatureGuard feature="internal-accounts">{guarded(<InternalAccountDetailPage />)}</FeatureGuard>} />
        <Route path="/payments" element={<FeatureGuard feature="payments">{guarded(<PaymentsPage />)}</FeatureGuard>} />
        <Route path="/payments/:paymentOrderId" element={<FeatureGuard feature="payments">{guarded(<PaymentDetailPage />)}</FeatureGuard>} />
        <Route path="/transactions" element={guarded(<TransactionsPage />)} />
        <Route path="/positions" element={<FeatureGuard feature="positions">{guarded(<PositionsPage />)}</FeatureGuard>} />
        <Route path="/positions/:logId" element={<FeatureGuard feature="positions">{guarded(<PositionDetailPage />)}</FeatureGuard>} />
        <Route path="/ledger" element={<FeatureGuard feature="ledger">{guarded(<LedgerPage />)}</FeatureGuard>} />
        <Route path="/ledger/:bookingLogId" element={<FeatureGuard feature="ledger">{guarded(<BookingLogDetailPage />)}</FeatureGuard>} />
        <Route path="/parties" element={<FeatureGuard feature="parties">{guarded(<PartiesPage />)}</FeatureGuard>} />
        <Route path="/parties/:partyId" element={<FeatureGuard feature="parties">{guarded(<PartyDetailPage />)}</FeatureGuard>} />
        <Route path="/reconciliation" element={<FeatureGuard feature="reconciliation">{guarded(<ReconciliationPage />)}</FeatureGuard>} />
        <Route path="/reconciliation/:runId" element={<FeatureGuard feature="reconciliation">{guarded(<ReconciliationDetailPage />)}</FeatureGuard>} />
        <Route
          path="/starlark-config"
          element={<FeatureGuard feature="sagas">{guarded(<StarlarkConfigPage isPlatformAdmin={isPlatformAdmin} />)}</FeatureGuard>}
        />
        <Route path="/starlark-config/:definitionId" element={<FeatureGuard feature="sagas">{guarded(<StarlarkDetailPage />)}</FeatureGuard>} />
        <Route path="/market-data" element={<FeatureGuard feature="market-data">{guarded(<MarketDataPage />)}</FeatureGuard>} />
        <Route path="/market-data/:datasetCode" element={<FeatureGuard feature="market-data">{guarded(<DatasetDetailPage />)}</FeatureGuard>} />
        <Route path="/forecasting" element={<FeatureGuard feature="forecasting">{guarded(<ForecastingPage />)}</FeatureGuard>} />
        <Route path="/reference-data" element={<FeatureGuard feature="reference-data">{guarded(<ReferenceDataHubPage />)}</FeatureGuard>} />
        <Route path="/reference-data/instruments" element={<FeatureGuard feature="reference-data">{guarded(<InstrumentsPage />)}</FeatureGuard>} />
        <Route path="/reference-data/account-types" element={<FeatureGuard feature="reference-data">{guarded(<AccountTypesPage />)}</FeatureGuard>} />
        <Route path="/reference-data/nodes" element={<FeatureGuard feature="reference-data">{guarded(<NodesPage />)}</FeatureGuard>} />
        <Route path="/gateway-mappings" element={<FeatureGuard feature="mappings">{guarded(<MappingsPage />)}</FeatureGuard>} />
        <Route path="/gateway-mappings/:mappingId" element={<FeatureGuard feature="mappings">{guarded(<MappingDetailPage />)}</FeatureGuard>} />
        <Route path="/manifests" element={<FeatureGuard feature="manifests">{guarded(<ManifestsPage />)}</FeatureGuard>} />
        <Route path="/mcp-config" element={<FeatureGuard feature="mcp-config">{guarded(<McpConfigPage />)}</FeatureGuard>} />
        <Route path="/cookbook" element={guarded(<CookbookPage />)} />
        <Route path="/cookbook/patterns" element={guarded(<Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}><CookbookPatternsPage /></Suspense>)} />
        <Route path="/cookbook/components" element={guarded(<Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}><CookbookComponentsPage /></Suspense>)} />
        <Route path="/cookbook/graph" element={guarded(<Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}><CookbookGraphPage /></Suspense>)} />
        <Route path="/cookbook/:name" element={guarded(<Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}><CookbookDetailPage /></Suspense>)} />
        <Route path="/audit-log" element={<FeatureGuard feature="audit">{guarded(<AuditLogPage />)}</FeatureGuard>} />

        {/* Admin-only routes */}
        <Route
          path="/users"
          element={
            <AdminOnlyRoute>
              {guarded(<UsersListPage />)}
            </AdminOnlyRoute>
          }
        />
        <Route
          path="/users/:userId"
          element={
            <AdminOnlyRoute>
              {guarded(<UserDetailPage />)}
            </AdminOnlyRoute>
          }
        />

        {/* Platform-only routes */}
        <Route
          path="/tenants"
          element={
            <PlatformOnlyRoute>
              {guarded(<TenantsPage />)}
            </PlatformOnlyRoute>
          }
        />
        <Route
          path="/tenants/:tenantId"
          element={
            <PlatformOnlyRoute>
              {guarded(<TenantDetailPage />)}
            </PlatformOnlyRoute>
          }
        />
        <Route
          path="/platform"
          element={
            <PlatformOnlyRoute>
              {guarded(<PlaceholderPage title="Platform Monitoring" />)}
            </PlatformOnlyRoute>
          }
        />

        {/* 404 */}
        <Route path="*" element={<NotFoundPage />} />
      </Routes>
    </AppShell>
  )
}

/**
 * Bridge component that reads tenantSlug from TenantProvider and passes it
 * to ApiClientProvider so API calls route to the correct tenant domain.
 * Must be rendered inside both AuthProvider and TenantProvider.
 */
function ApiClientBridge({ children }: { children: ReactNode }) {
  const { accessToken, logout } = useAuth()
  const { tenantSlug } = useTenantContext()

  const tokenRef = useRef(accessToken)
  const slugRef = useRef(tenantSlug)

  useEffect(() => {
    tokenRef.current = accessToken
  }, [accessToken])

  useEffect(() => {
    slugRef.current = tenantSlug
  }, [tenantSlug])

  const getToken = useCallback(() => tokenRef.current ?? '', [])
  const getTenantSlug = useCallback(() => slugRef.current, [])

  return (
    <ApiClientProvider
      tenantSlug={tenantSlug}
      getToken={getToken}
      getTenantSlug={getTenantSlug}
      onUnauthenticated={logout}
    >
      {children}
    </ApiClientProvider>
  )
}

/**
 * In dev/demo mode, auto-select the first real tenant for platform admins
 * so pages show data immediately after login.
 */
function DevTenantAutoSelector() {
  const { isPlatformAdmin, currentTenant, switchTenant } = useTenantContext()
  const { data: tenants } = useTenants()

  useEffect(() => {
    if (isPlatformAdmin && !currentTenant && tenants?.length) {
      // Pick the first tenant that has a slug (skip system tenants without one)
      const tenant = tenants.find((t) => t.slug) ?? tenants[0]
      if (tenant) {
        switchTenant({ id: tenant.tenantId, slug: tenant.slug, name: tenant.displayName })
      }
    }
  }, [isPlatformAdmin, currentTenant, tenants, switchTenant])

  return null
}

/**
 * Inner app that has access to auth and tenant contexts for ApiClientProvider.
 */
function AuthenticatedApp() {
  return (
    <TenantProvider>
      <ApiClientBridge>
        {(import.meta.env.DEV || import.meta.env.VITE_E2E_MODE === 'true' || import.meta.env.VITE_DEMO_MODE === 'true') && <DevTenantAutoSelector />}
        <TooltipProvider>
          <Toaster position="top-right" richColors closeButton />
          <BrowserRouter>
            <Routes>
              <Route path="/login" element={<LoginPage />} />
              <Route path="/callback" element={<CallbackPage />} />
              <Route
                path="/*"
                element={
                  <ProtectedRoute>
                    <TenantSubdomainEnforcer>
                      <AppShellLayout />
                    </TenantSubdomainEnforcer>
                  </ProtectedRoute>
                }
              />
            </Routes>
          </BrowserRouter>
        </TooltipProvider>
      </ApiClientBridge>
    </TenantProvider>
  )
}

export function App() {
  return (
    <PageErrorBoundary>
      <QueryClientProvider client={queryClient}>
        <AuthProvider>
          <AuthenticatedApp />
        </AuthProvider>
        {import.meta.env.DEV && <ReactQueryDevtools initialIsOpen={false} />}
        {import.meta.env.DEV && <ThemePreviewPanel />}
      </QueryClientProvider>
    </PageErrorBoundary>
  )
}

export default App
