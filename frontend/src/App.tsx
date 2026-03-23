import { type ReactNode, lazy, Suspense, useCallback, useEffect, useRef } from 'react'
import { BrowserRouter, Routes, Route, useLocation } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { ReactQueryDevtools } from '@tanstack/react-query-devtools'
import { queryClient } from '@/lib/query-client'
import { PageErrorBoundary, RouteErrorBoundary } from '@/components/error-boundary'
import { AuthProvider, useAuth } from '@/contexts/auth-context'
import { TenantProvider, useTenantContext } from '@/contexts/tenant-context'
import { useTenants } from '@/hooks/use-tenants'
import { ApiClientProvider } from '@/api/context'
import { ProtectedRoute, PlatformOnlyRoute, AdminOnlyRoute, TenantSubdomainEnforcer } from '@/components/routing'
import { FeatureGuard } from '@/components/feature-guard'
import { AppShell } from '@/components/layout/app-shell'
import { TooltipProvider } from '@/components/ui/tooltip'
import { Toaster } from 'sonner'
import { CallbackPage } from '@/pages/callback'
import { LoginPage } from '@/pages/login'
import { RegisterPage } from '@/features/registration/pages/register-page'
import { DashboardPage } from '@/features/dashboard'
import { AccountDetailPage } from '@/features/accounts/pages/[accountId]'
import { PaymentDetailPage } from '@/features/payments/pages/payment-detail'
import { BookingLogDetailPage } from '@/features/ledger/pages/booking-log-detail'
import { PositionDetailPage } from '@/features/positions/pages/detail'
import { PartyDetailPage } from '@/features/parties/pages/[partyId]'
import { TenantDetailPage } from '@/features/tenants/pages/[tenantId]'
import { ReconciliationDetailPage } from '@/features/reconciliation/pages/detail'
import { StarlarkDetailPage } from '@/features/sagas/pages/detail'
import { MappingDetailPage } from '@/features/mappings/pages/[mappingId]'
import { InternalAccountDetailPage } from '@/features/internal-accounts/pages/[accountId]'
import { UserDetailPage } from '@/features/identity/pages/user-detail-page'

// List pages (lazy-loaded: split per route for faster initial load)
const AccountsPage = lazy(() =>
  import('@/features/accounts/pages/index').then((m) => ({ default: m.AccountsPage })),
)
const PaymentsPage = lazy(() =>
  import('@/features/payments/pages/index').then((m) => ({ default: m.PaymentsPage })),
)
const LedgerPage = lazy(() =>
  import('@/features/ledger/pages/index').then((m) => ({ default: m.LedgerPage })),
)
const PositionsPage = lazy(() =>
  import('@/features/positions/pages/index').then((m) => ({ default: m.PositionsPage })),
)
const PartiesPage = lazy(() =>
  import('@/features/parties/pages/index').then((m) => ({ default: m.PartiesPage })),
)
const TenantsPage = lazy(() =>
  import('@/features/tenants/pages/index').then((m) => ({ default: m.TenantsPage })),
)
const ReconciliationPage = lazy(() =>
  import('@/features/reconciliation/pages/index').then((m) => ({ default: m.ReconciliationPage })),
)
const AuditLogPage = lazy(() =>
  import('@/features/audit/pages/index').then((m) => ({ default: m.AuditLogPage })),
)
const StarlarkConfigPage = lazy(() =>
  import('@/features/sagas/pages/index').then((m) => ({ default: m.StarlarkConfigPage })),
)
const MappingsPage = lazy(() =>
  import('@/features/mappings/pages/index').then((m) => ({ default: m.MappingsPage })),
)
const InternalAccountsPage = lazy(() =>
  import('@/features/internal-accounts/pages/index').then((m) => ({ default: m.InternalAccountsPage })),
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
const ManifestDiffPage = lazy(() =>
  import('@/features/manifests/pages/manifest-diff-page').then((m) => ({ default: m.ManifestDiffPage })),
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
  return (
    <AppShell currentPath={pathname}>
      <Routes>
        {/* Tenant-scoped routes */}
        <Route path="/" element={guarded(<DashboardPage />)} />
        <Route path="/accounts" element={<FeatureGuard feature="accounts"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<AccountsPage />)}</Suspense></FeatureGuard>} />
        <Route path="/accounts/:accountId" element={<FeatureGuard feature="accounts">{guarded(<AccountDetailPage />)}</FeatureGuard>} />
        <Route path="/internal-accounts" element={<FeatureGuard feature="internal-accounts"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<InternalAccountsPage />)}</Suspense></FeatureGuard>} />
        <Route path="/internal-accounts/:accountId" element={<FeatureGuard feature="internal-accounts">{guarded(<InternalAccountDetailPage />)}</FeatureGuard>} />
        <Route path="/payments" element={<FeatureGuard feature="payments"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<PaymentsPage />)}</Suspense></FeatureGuard>} />
        <Route path="/payments/:paymentOrderId" element={<FeatureGuard feature="payments">{guarded(<PaymentDetailPage />)}</FeatureGuard>} />
        <Route path="/transactions" element={<Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<TransactionsPage />)}</Suspense>} />
        <Route path="/positions" element={<FeatureGuard feature="positions"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<PositionsPage />)}</Suspense></FeatureGuard>} />
        <Route path="/positions/:logId" element={<FeatureGuard feature="positions">{guarded(<PositionDetailPage />)}</FeatureGuard>} />
        <Route path="/ledger" element={<FeatureGuard feature="ledger"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<LedgerPage />)}</Suspense></FeatureGuard>} />
        <Route path="/ledger/:bookingLogId" element={<FeatureGuard feature="ledger">{guarded(<BookingLogDetailPage />)}</FeatureGuard>} />
        <Route path="/parties" element={<FeatureGuard feature="parties"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<PartiesPage />)}</Suspense></FeatureGuard>} />
        <Route path="/parties/:partyId" element={<FeatureGuard feature="parties">{guarded(<PartyDetailPage />)}</FeatureGuard>} />
        <Route path="/reconciliation" element={<FeatureGuard feature="reconciliation"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<ReconciliationPage />)}</Suspense></FeatureGuard>} />
        <Route path="/reconciliation/:runId" element={<FeatureGuard feature="reconciliation">{guarded(<ReconciliationDetailPage />)}</FeatureGuard>} />
        <Route
          path="/starlark-config"
          element={<FeatureGuard feature="sagas"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<StarlarkConfigPage />)}</Suspense></FeatureGuard>}
        />
        <Route path="/starlark-config/:sagaName" element={<FeatureGuard feature="sagas">{guarded(<StarlarkDetailPage />)}</FeatureGuard>} />
        <Route path="/market-data" element={<FeatureGuard feature="market-data"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<MarketDataPage />)}</Suspense></FeatureGuard>} />
        <Route path="/market-data/:datasetCode" element={<FeatureGuard feature="market-data"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<DatasetDetailPage />)}</Suspense></FeatureGuard>} />
        <Route path="/forecasting" element={<FeatureGuard feature="forecasting"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<ForecastingPage />)}</Suspense></FeatureGuard>} />
        <Route path="/reference-data" element={<FeatureGuard feature="reference-data"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<ReferenceDataHubPage />)}</Suspense></FeatureGuard>} />
        <Route path="/reference-data/instruments" element={<FeatureGuard feature="reference-data"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<InstrumentsPage />)}</Suspense></FeatureGuard>} />
        <Route path="/reference-data/account-types" element={<FeatureGuard feature="reference-data"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<AccountTypesPage />)}</Suspense></FeatureGuard>} />
        <Route path="/reference-data/nodes" element={<FeatureGuard feature="reference-data"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<NodesPage />)}</Suspense></FeatureGuard>} />
        <Route path="/reference-data/valuation-rules" element={<FeatureGuard feature="reference-data"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<ValuationRulesPage />)}</Suspense></FeatureGuard>} />
        <Route path="/gateway-mappings" element={<FeatureGuard feature="mappings"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<MappingsPage />)}</Suspense></FeatureGuard>} />
        <Route path="/gateway-mappings/:mappingId" element={<FeatureGuard feature="mappings">{guarded(<MappingDetailPage />)}</FeatureGuard>} />
        <Route path="/economy" element={<FeatureGuard feature="economy"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<EconomyOverviewPage />)}</Suspense></FeatureGuard>} />
        <Route path="/economy/create" element={<FeatureGuard feature="economy"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<EconomyCreatePage />)}</Suspense></FeatureGuard>} />
        <Route path="/economy/edit" element={<FeatureGuard feature="economy"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<EconomyEditPage />)}</Suspense></FeatureGuard>} />
        <Route path="/economy/explore" element={<FeatureGuard feature="economy"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<EconomyExplorePage />)}</Suspense></FeatureGuard>} />
        <Route path="/economy/draft" element={<FeatureGuard feature="economy"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<EconomyDraftPage />)}</Suspense></FeatureGuard>} />
        <Route path="/economy/manifests/diff/:v1/:v2" element={<FeatureGuard feature="economy"><Suspense fallback={<div className="h-96 animate-pulse rounded bg-muted" />}>{guarded(<ManifestDiffPage />)}</Suspense></FeatureGuard>} />
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
              {guarded(<UserDetailPage />)}
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
 * In DEV and E2E mode, auto-select the first real tenant for platform admins
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
        {(import.meta.env.DEV || import.meta.env.VITE_E2E_MODE === 'true') && <DevTenantAutoSelector />}
        <TooltipProvider>
          <Toaster position="top-right" richColors closeButton />
          <BrowserRouter>
            <Routes>
              <Route path="/login" element={<LoginPage />} />
              <Route path="/register" element={<RegisterPage />} />
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
