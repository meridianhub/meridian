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
import { isBaseDomain } from '@/lib/tenant-utils'
import { ApiClientProvider } from '@/api/context'
import { ProtectedRoute, PlatformOnlyRoute, AdminOnlyRoute, TenantSubdomainEnforcer } from '@/components/routing'
import { FeatureGuard } from '@/components/feature-guard'
import { AppShell } from '@/components/layout/app-shell'
import { TooltipProvider } from '@/components/ui/tooltip'
import { Toaster } from 'sonner'
import { CallbackPage } from '@/pages/callback'
import { ProviderButton } from '@/components/auth/provider-button'
import { AuthDivider } from '@/components/auth/auth-divider'
import { DashboardPage } from '@/features/dashboard'

// Feature pages (lazy-loaded: split per route for faster initial load)
const AccountsPage = lazy(() =>
  import('@/features/accounts/pages/index').then((m) => ({ default: m.AccountsPage })),
)
const AccountDetailPage = lazy(() =>
  import('@/features/accounts/pages/[accountId]').then((m) => ({ default: m.AccountDetailPage })),
)
const PaymentsPage = lazy(() =>
  import('@/features/payments/pages/index').then((m) => ({ default: m.PaymentsPage })),
)
const PaymentDetailPage = lazy(() =>
  import('@/features/payments/pages/payment-detail').then((m) => ({ default: m.PaymentDetailPage })),
)
const LedgerPage = lazy(() =>
  import('@/features/ledger/pages/index').then((m) => ({ default: m.LedgerPage })),
)
const BookingLogDetailPage = lazy(() =>
  import('@/features/ledger/pages/booking-log-detail').then((m) => ({ default: m.BookingLogDetailPage })),
)
const PositionsPage = lazy(() =>
  import('@/features/positions/pages/index').then((m) => ({ default: m.PositionsPage })),
)
const PositionDetailPage = lazy(() =>
  import('@/features/positions/pages/detail').then((m) => ({ default: m.PositionDetailPage })),
)
const PartiesPage = lazy(() =>
  import('@/features/parties/pages/index').then((m) => ({ default: m.PartiesPage })),
)
const PartyDetailPage = lazy(() =>
  import('@/features/parties/pages/[partyId]').then((m) => ({ default: m.PartyDetailPage })),
)
const TenantsPage = lazy(() =>
  import('@/features/tenants/pages/index').then((m) => ({ default: m.TenantsPage })),
)
const TenantDetailPage = lazy(() =>
  import('@/features/tenants/pages/[tenantId]').then((m) => ({ default: m.TenantDetailPage })),
)
const ReconciliationPage = lazy(() =>
  import('@/features/reconciliation/pages/index').then((m) => ({ default: m.ReconciliationPage })),
)
const ReconciliationDetailPage = lazy(() =>
  import('@/features/reconciliation/pages/detail').then((m) => ({ default: m.ReconciliationDetailPage })),
)
const AuditLogPage = lazy(() =>
  import('@/features/audit/pages/index').then((m) => ({ default: m.AuditLogPage })),
)
const StarlarkConfigPage = lazy(() =>
  import('@/features/sagas/pages/index').then((m) => ({ default: m.StarlarkConfigPage })),
)
const StarlarkDetailPage = lazy(() =>
  import('@/features/sagas/pages/detail').then((m) => ({ default: m.StarlarkDetailPage })),
)
const MappingsPage = lazy(() =>
  import('@/features/mappings/pages/index').then((m) => ({ default: m.MappingsPage })),
)
const MappingDetailPage = lazy(() =>
  import('@/features/mappings/pages/[mappingId]').then((m) => ({ default: m.MappingDetailPage })),
)
const InternalAccountsPage = lazy(() =>
  import('@/features/internal-accounts/pages/index').then((m) => ({ default: m.InternalAccountsPage })),
)
const InternalAccountDetailPage = lazy(() =>
  import('@/features/internal-accounts/pages/[accountId]').then((m) => ({ default: m.InternalAccountDetailPage })),
)
const McpConfigPage = lazy(() =>
  import('@/features/mcp-config/pages/index').then((m) => ({ default: m.McpConfigPage })),
)
const TransactionsPage = lazy(() =>
  import('@/features/transactions/pages/index').then((m) => ({ default: m.TransactionsPage })),
)
const UsersListPage = lazy(() =>
  import('@/features/identity/pages/users-list-page').then((m) => ({ default: m.UsersListPage })),
)
const UserDetailPage = lazy(() =>
  import('@/features/identity/pages/user-detail-page').then((m) => ({ default: m.UserDetailPage })),
)
const CookbookPage = lazy(() =>
  import('@/features/cookbook/pages/index').then((m) => ({ default: m.CookbookPage })),
)

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

// Economy pages (lazy-loaded: graph rendering with ELK/Cytoscape)
const EconomyOverviewPage = lazy(() =>
  import('@/features/economy/pages/economy-overview-page').then((m) => ({ default: m.EconomyOverviewPage })),
)
const EconomyCreatePage = lazy(() =>
  import('@/features/economy/pages/economy-create-page').then((m) => ({ default: m.EconomyCreatePage })),
)
const EconomyEditPage = lazy(() =>
  import('@/features/economy/pages/economy-edit-page').then((m) => ({ default: m.EconomyEditPage })),
)
const EconomyExplorePage = lazy(() =>
  import('@/features/economy/pages/economy-explore-page').then((m) => ({ default: m.EconomyExplorePage })),
)
const EconomyDraftPage = lazy(() =>
  import('@/features/economy/pages/economy-draft-page').then((m) => ({ default: m.EconomyDraftPage })),
)

// Market data pages (lazy-loaded: chart rendering)
const MarketDataPage = lazy(() =>
  import('@/features/market-data/pages/index').then((m) => ({ default: m.MarketDataPage })),
)
const DatasetDetailPage = lazy(() =>
  import('@/features/market-data/pages/[datasetCode]').then((m) => ({ default: m.DatasetDetailPage })),
)

// Forecasting page (lazy-loaded: data visualization)
const ForecastingPage = lazy(() =>
  import('@/features/forecasting/pages/index').then((m) => ({ default: m.ForecastingPage })),
)

// Reference data pages (lazy-loaded: large component trees)
const ReferenceDataHubPage = lazy(() =>
  import('@/features/reference-data/pages/index').then((m) => ({ default: m.ReferenceDataHubPage })),
)
const InstrumentsPage = lazy(() =>
  import('@/features/reference-data/pages/instruments/index').then((m) => ({ default: m.InstrumentsPage })),
)
const AccountTypesPage = lazy(() =>
  import('@/features/reference-data/pages/account-types/index').then((m) => ({ default: m.AccountTypesPage })),
)
const NodesPage = lazy(() =>
  import('@/features/reference-data/pages/nodes/index').then((m) => ({ default: m.NodesPage })),
)
const ValuationRulesPage = lazy(() =>
  import('@/features/reference-data/pages/valuation-rules/index').then((m) => ({ default: m.ValuationRulesPage })),
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
  return isBaseDomain(window.location.hostname)
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

        {/* Dex login form - shown in demo mode and production */}
        {(import.meta.env.VITE_DEMO_MODE === 'true' || !import.meta.env.DEV) && (
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
        <Route path="/accounts" element={<FeatureGuard feature="accounts"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<AccountsPage />)}</Suspense></FeatureGuard>} />
        <Route path="/accounts/:accountId" element={<FeatureGuard feature="accounts"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<AccountDetailPage />)}</Suspense></FeatureGuard>} />
        <Route path="/internal-accounts" element={<FeatureGuard feature="internal-accounts"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<InternalAccountsPage />)}</Suspense></FeatureGuard>} />
        <Route path="/internal-accounts/:accountId" element={<FeatureGuard feature="internal-accounts"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<InternalAccountDetailPage />)}</Suspense></FeatureGuard>} />
        <Route path="/payments" element={<FeatureGuard feature="payments"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<PaymentsPage />)}</Suspense></FeatureGuard>} />
        <Route path="/payments/:paymentOrderId" element={<FeatureGuard feature="payments"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<PaymentDetailPage />)}</Suspense></FeatureGuard>} />
        <Route path="/transactions" element={<Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<TransactionsPage />)}</Suspense>} />
        <Route path="/positions" element={<FeatureGuard feature="positions"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<PositionsPage />)}</Suspense></FeatureGuard>} />
        <Route path="/positions/:logId" element={<FeatureGuard feature="positions"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<PositionDetailPage />)}</Suspense></FeatureGuard>} />
        <Route path="/ledger" element={<FeatureGuard feature="ledger"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<LedgerPage />)}</Suspense></FeatureGuard>} />
        <Route path="/ledger/:bookingLogId" element={<FeatureGuard feature="ledger"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<BookingLogDetailPage />)}</Suspense></FeatureGuard>} />
        <Route path="/parties" element={<FeatureGuard feature="parties"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<PartiesPage />)}</Suspense></FeatureGuard>} />
        <Route path="/parties/:partyId" element={<FeatureGuard feature="parties"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<PartyDetailPage />)}</Suspense></FeatureGuard>} />
        <Route path="/reconciliation" element={<FeatureGuard feature="reconciliation"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<ReconciliationPage />)}</Suspense></FeatureGuard>} />
        <Route path="/reconciliation/:runId" element={<FeatureGuard feature="reconciliation"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<ReconciliationDetailPage />)}</Suspense></FeatureGuard>} />
        <Route
          path="/starlark-config"
          element={<FeatureGuard feature="sagas"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<StarlarkConfigPage isPlatformAdmin={isPlatformAdmin} />)}</Suspense></FeatureGuard>}
        />
        <Route path="/starlark-config/:definitionId" element={<FeatureGuard feature="sagas"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<StarlarkDetailPage />)}</Suspense></FeatureGuard>} />
        <Route path="/market-data" element={<FeatureGuard feature="market-data"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<MarketDataPage />)}</Suspense></FeatureGuard>} />
        <Route path="/market-data/:datasetCode" element={<FeatureGuard feature="market-data"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<DatasetDetailPage />)}</Suspense></FeatureGuard>} />
        <Route path="/forecasting" element={<FeatureGuard feature="forecasting"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<ForecastingPage />)}</Suspense></FeatureGuard>} />
        <Route path="/reference-data" element={<FeatureGuard feature="reference-data"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<ReferenceDataHubPage />)}</Suspense></FeatureGuard>} />
        <Route path="/reference-data/instruments" element={<FeatureGuard feature="reference-data"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<InstrumentsPage />)}</Suspense></FeatureGuard>} />
        <Route path="/reference-data/account-types" element={<FeatureGuard feature="reference-data"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<AccountTypesPage />)}</Suspense></FeatureGuard>} />
        <Route path="/reference-data/nodes" element={<FeatureGuard feature="reference-data"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<NodesPage />)}</Suspense></FeatureGuard>} />
        <Route path="/reference-data/valuation-rules" element={<FeatureGuard feature="reference-data"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<ValuationRulesPage />)}</Suspense></FeatureGuard>} />
        <Route path="/gateway-mappings" element={<FeatureGuard feature="mappings"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<MappingsPage />)}</Suspense></FeatureGuard>} />
        <Route path="/gateway-mappings/:mappingId" element={<FeatureGuard feature="mappings"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<MappingDetailPage />)}</Suspense></FeatureGuard>} />
        <Route path="/economy" element={<FeatureGuard feature="economy"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<EconomyOverviewPage />)}</Suspense></FeatureGuard>} />
        <Route path="/economy/create" element={<FeatureGuard feature="economy"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<EconomyCreatePage />)}</Suspense></FeatureGuard>} />
        <Route path="/economy/edit" element={<FeatureGuard feature="economy"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<EconomyEditPage />)}</Suspense></FeatureGuard>} />
        <Route path="/economy/explore" element={<FeatureGuard feature="economy"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<EconomyExplorePage />)}</Suspense></FeatureGuard>} />
        <Route path="/economy/draft" element={<FeatureGuard feature="economy"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<EconomyDraftPage />)}</Suspense></FeatureGuard>} />
        <Route path="/mcp-config" element={<FeatureGuard feature="mcp-config"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<McpConfigPage />)}</Suspense></FeatureGuard>} />
        <Route path="/cookbook" element={<Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<CookbookPage />)}</Suspense>} />
        <Route path="/cookbook/patterns" element={guarded(<Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}><CookbookPatternsPage /></Suspense>)} />
        <Route path="/cookbook/components" element={guarded(<Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}><CookbookComponentsPage /></Suspense>)} />
        <Route path="/cookbook/graph" element={guarded(<Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}><CookbookGraphPage /></Suspense>)} />
        <Route path="/cookbook/:name" element={guarded(<Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}><CookbookDetailPage /></Suspense>)} />
        <Route path="/audit-log" element={<FeatureGuard feature="audit"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<AuditLogPage />)}</Suspense></FeatureGuard>} />

        {/* Admin-only routes */}
        <Route
          path="/users"
          element={
            <AdminOnlyRoute>
              <Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>
                {guarded(<UsersListPage />)}
              </Suspense>
            </AdminOnlyRoute>
          }
        />
        <Route
          path="/users/:userId"
          element={
            <AdminOnlyRoute>
              <Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>
                {guarded(<UserDetailPage />)}
              </Suspense>
            </AdminOnlyRoute>
          }
        />

        {/* Platform-only routes */}
        <Route
          path="/tenants"
          element={
            <PlatformOnlyRoute>
              <Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>
                {guarded(<TenantsPage />)}
              </Suspense>
            </PlatformOnlyRoute>
          }
        />
        <Route
          path="/tenants/:tenantId"
          element={
            <PlatformOnlyRoute>
              <Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>
                {guarded(<TenantDetailPage />)}
              </Suspense>
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
