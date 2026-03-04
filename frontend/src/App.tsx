import { type ReactNode, useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { BrowserRouter, Routes, Route, useLocation, useNavigate } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { ReactQueryDevtools } from '@tanstack/react-query-devtools'
import { queryClient } from '@/lib/query-client'
import { PageErrorBoundary, RouteErrorBoundary } from '@/components/error-boundary'
import { AuthProvider, useAuth } from '@/contexts/auth-context'
import { TenantProvider, useTenantContext } from '@/contexts/tenant-context'
import { useTenants } from '@/hooks/use-tenants'
import { ApiClientProvider } from '@/api/context'
import { ProtectedRoute, PlatformOnlyRoute } from '@/components/routing'
import { AppShell } from '@/components/layout/app-shell'
import { TooltipProvider } from '@/components/ui/tooltip'
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

// Placeholder page components - replaced as each page task is implemented
function PlaceholderPage({ title }: { title: string }) {
  return (
    <div className="p-6">
      <h1 className="text-2xl font-semibold">{title}</h1>
      <p className="mt-2 text-muted-foreground">Coming soon.</p>
    </div>
  )
}

function LoginPage() {
  const { login } = useAuth()
  const navigate = useNavigate()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

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
        <Route path="/accounts" element={guarded(<AccountsPage />)} />
        <Route path="/accounts/:accountId" element={guarded(<AccountDetailPage />)} />
        <Route path="/internal-accounts" element={guarded(<InternalAccountsPage />)} />
        <Route path="/internal-accounts/:accountId" element={guarded(<InternalAccountDetailPage />)} />
        <Route path="/payments" element={guarded(<PaymentsPage />)} />
        <Route path="/payments/:paymentOrderId" element={guarded(<PaymentDetailPage />)} />
        <Route path="/transactions" element={guarded(<TransactionsPage />)} />
        <Route path="/positions" element={guarded(<PositionsPage />)} />
        <Route path="/positions/:logId" element={guarded(<PositionDetailPage />)} />
        <Route path="/ledger" element={guarded(<LedgerPage />)} />
        <Route path="/ledger/:bookingLogId" element={guarded(<BookingLogDetailPage />)} />
        <Route path="/parties" element={guarded(<PartiesPage />)} />
        <Route path="/parties/:partyId" element={guarded(<PartyDetailPage />)} />
        <Route path="/reconciliation" element={guarded(<ReconciliationPage />)} />
        <Route path="/reconciliation/:runId" element={guarded(<ReconciliationDetailPage />)} />
        <Route
          path="/starlark-config"
          element={guarded(<StarlarkConfigPage isPlatformAdmin={isPlatformAdmin} />)}
        />
        <Route path="/starlark-config/:definitionId" element={guarded(<StarlarkDetailPage />)} />
        <Route path="/market-data" element={guarded(<MarketDataPage />)} />
        <Route path="/market-data/:datasetCode" element={guarded(<DatasetDetailPage />)} />
        <Route path="/forecasting" element={guarded(<ForecastingPage />)} />
        <Route path="/reference-data" element={guarded(<ReferenceDataHubPage />)} />
        <Route path="/reference-data/instruments" element={guarded(<InstrumentsPage />)} />
        <Route path="/reference-data/account-types" element={guarded(<AccountTypesPage />)} />
        <Route path="/reference-data/nodes" element={guarded(<NodesPage />)} />
        <Route path="/gateway-mappings" element={guarded(<MappingsPage />)} />
        <Route path="/gateway-mappings/:mappingId" element={guarded(<MappingDetailPage />)} />
        <Route path="/manifests" element={guarded(<ManifestsPage />)} />
        <Route path="/mcp-config" element={guarded(<McpConfigPage />)} />
        <Route path="/audit-log" element={guarded(<AuditLogPage />)} />

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
          <BrowserRouter>
            <Routes>
              <Route path="/login" element={<LoginPage />} />
              <Route
                path="/*"
                element={
                  <ProtectedRoute>
                    <AppShellLayout />
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
      </QueryClientProvider>
    </PageErrorBoundary>
  )
}

export default App
