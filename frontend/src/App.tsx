import { type ReactNode } from 'react'
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
import { TooltipProvider } from '@/components/ui/tooltip'
import { TenantsPage } from '@/pages/tenants/index'
import { TenantDetailPage } from '@/pages/tenants/[tenantId]'
import { AccountsPage } from '@/pages/accounts'
import { AccountDetailPage } from '@/pages/accounts/[accountId]'
import { PaymentsPage } from '@/pages/payments'
import { PaymentDetailPage } from '@/pages/payments/payment-detail'
import { PartiesPage } from '@/pages/parties'
import { PartyDetailPage } from '@/pages/parties/[partyId]'
import { AuditLogPage } from '@/pages/audit'
import { PositionsPage } from '@/pages/positions'
import { PositionDetailPage } from '@/pages/positions/detail'
import { InternalAccountsPage } from '@/pages/internal-accounts'
import { MarketDataPage } from '@/pages/market-data'
import { DatasetDetailPage } from '@/pages/market-data/[datasetCode]'
import { ForecastingPage } from '@/pages/forecasting'
import { LedgerPage } from '@/pages/ledger'
import { BookingLogDetailPage } from '@/pages/ledger/booking-log-detail'
import { ReconciliationPage } from '@/pages/reconciliation'
import { ReconciliationDetailPage } from '@/pages/reconciliation/detail'

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
        <Route
          path="/gateway-mappings"
          element={<PlaceholderPage title="Gateway Mappings" />}
        />
        <Route path="/audit-log" element={<PlaceholderPage title="Audit Log" />} />

        {/* Platform-only routes */}
        <Route
          path="/tenants"
          element={
            <PlatformOnlyRoute>
              <TenantsPage />
            </PlatformOnlyRoute>
          }
        />
        <Route
          path="/tenants/:tenantId"
          element={
            <PlatformOnlyRoute>
              <TenantDetailPage />
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

/**
 * Bridge component that reads tenantSlug from TenantProvider and passes it
 * to ApiClientProvider so API calls route to the correct tenant domain.
 * Must be rendered inside both AuthProvider and TenantProvider.
 */
function ApiClientBridge({ children }: { children: ReactNode }) {
  const { accessToken } = useAuth()
  const { tenantSlug } = useTenantContext()
  const getToken = () => accessToken ?? ''

  return (
    <ApiClientProvider tenantSlug={tenantSlug} getToken={getToken}>
      {children}
    </ApiClientProvider>
  )
}

/**
 * Inner app that has access to auth and tenant contexts for ApiClientProvider.
 */
function AuthenticatedApp() {
  return (
    <TenantProvider>
      <ApiClientBridge>
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
