import { BrowserRouter, Routes, Route, useLocation } from 'react-router-dom'
import { useCallback } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { ReactQueryDevtools } from '@tanstack/react-query-devtools'
import { queryClient } from '@/lib/query-client'
import { PageErrorBoundary } from '@/components/error-boundary'
import { AuthProvider, useAuth } from '@/contexts/auth-context'
import { TenantProvider, useTenantContext } from '@/contexts/tenant-context'
import { ApiClientProvider } from '@/api/context'
import { ProtectedRoute, PlatformOnlyRoute } from '@/components/routing'
import { AppShell } from '@/components/layout/app-shell'
import { AccountsPage } from '@/pages/accounts'
import { AccountDetailPage } from '@/pages/accounts/[accountId]'
import { PaymentsPage } from '@/pages/payments'
import { PaymentDetailPage } from '@/pages/payments/payment-detail'
import { PartiesPage } from '@/pages/parties'
import { PartyDetailPage } from '@/pages/parties/[partyId]'
import { AuditLogPage } from '@/pages/audit'
import { PositionsPage } from '@/pages/positions'
import { PositionDetailPage } from '@/pages/positions/detail'
import { MappingsPage } from '@/pages/mappings'
import { MappingDetailPage } from '@/pages/mappings/[mappingId]'
import { InternalAccountsPage } from '@/pages/internal-accounts'
import { MarketDataPage } from '@/pages/market-data'
import { DatasetDetailPage } from '@/pages/market-data/[datasetCode]'
import { ForecastingPage } from '@/pages/forecasting'
import { LedgerPage } from '@/pages/ledger'
import { BookingLogDetailPage } from '@/pages/ledger/booking-log-detail'
import { ReconciliationPage } from '@/pages/reconciliation'
import { ReconciliationDetailPage } from '@/pages/reconciliation/detail'

/**
 * Bridges Auth and Tenant context to ApiClientProvider.
 * This component reads the token and tenant slug from context,
 * then provides them to the API client layer.
 */
function ApiClientBridge({ children }: { children: React.ReactNode }) {
  const { accessToken } = useAuth()
  const { tenantSlug } = useTenantContext()
  const getToken = useCallback(() => Promise.resolve(accessToken ?? ''), [accessToken])
  return (
    <ApiClientProvider tenantSlug={tenantSlug} getToken={getToken}>
      {children}
    </ApiClientProvider>
  )
}

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
  return (
    <div className="flex min-h-screen items-center justify-center">
      <div className="text-center">
        <h1 className="text-2xl font-semibold">Meridian Operations Console</h1>
        <p className="mt-2 text-muted-foreground">Please sign in to continue.</p>
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

/**
 * Layout wrapper that reads the current path from React Router
 * and passes it to AppShell for active nav highlighting.
 */
function AppShellLayout() {
  const { pathname } = useLocation()
  return (
    <AppShell currentPath={pathname}>
      <Routes>
        {/* Tenant-scoped routes */}
        <Route path="/" element={<PlaceholderPage title="Dashboard" />} />
        <Route path="/accounts" element={<AccountsPage />} />
        <Route path="/accounts/:accountId" element={<AccountDetailPage />} />
        <Route path="/internal-accounts" element={<InternalAccountsPage />} />
        <Route path="/internal-accounts/:accountId" element={<PlaceholderPage title="Internal Account Detail" />} />
        <Route path="/payments" element={<PaymentsPage />} />
        <Route path="/payments/:paymentOrderId" element={<PaymentDetailPage />} />
        <Route path="/transactions" element={<PlaceholderPage title="Transactions" />} />
        <Route path="/positions" element={<PositionsPage />} />
        <Route path="/positions/:logId" element={<PositionDetailPage />} />
        <Route path="/ledger" element={<LedgerPage />} />
        <Route path="/ledger/:bookingLogId" element={<BookingLogDetailPage />} />
        <Route path="/parties" element={<PartiesPage />} />
        <Route path="/parties/:partyId" element={<PartyDetailPage />} />
        <Route path="/reconciliation" element={<ReconciliationPage />} />
        <Route path="/reconciliation/:runId" element={<ReconciliationDetailPage />} />
        <Route
          path="/starlark-config"
          element={<PlaceholderPage title="Starlark Configuration" />}
        />
        <Route path="/reference-data" element={<PlaceholderPage title="Reference Data" />} />
        <Route path="/gateway-mappings" element={<MappingsPage />} />
        <Route path="/gateway-mappings/:mappingId" element={<MappingDetailPage />} />
        <Route path="/audit-log" element={<AuditLogPage />} />

        {/* Platform-only routes */}
        <Route
          path="/tenants"
          element={
            <PlatformOnlyRoute>
              <PlaceholderPage title="Tenant Management" />
            </PlatformOnlyRoute>
          }
        />
        <Route
          path="/platform"
          element={
            <PlatformOnlyRoute>
              <PlaceholderPage title="Platform Monitoring" />
            </PlatformOnlyRoute>
          }
        />

        {/* 404 */}
        <Route path="*" element={<NotFoundPage />} />
      </Routes>
    </AppShell>
  )
}

export function App() {
  return (
    <PageErrorBoundary>
      <QueryClientProvider client={queryClient}>
        <AuthProvider>
          <TenantProvider>
            <BrowserRouter>
              <ApiClientBridge>
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
              </ApiClientBridge>
            </BrowserRouter>
          </TenantProvider>
        </AuthProvider>
        {import.meta.env.DEV && <ReactQueryDevtools initialIsOpen={false} />}
      </QueryClientProvider>
    </PageErrorBoundary>
  )
}

export default App
