import { BrowserRouter, Routes, Route, useLocation } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { ReactQueryDevtools } from '@tanstack/react-query-devtools'
import { queryClient } from '@/lib/query-client'
import { PageErrorBoundary } from '@/components/error-boundary'
import { AuthProvider } from '@/contexts/auth-context'
import { TenantProvider } from '@/contexts/tenant-context'
import { ProtectedRoute, PlatformOnlyRoute } from '@/components/routing'
import { AppShell } from '@/components/layout/app-shell'
import { AuditLogPage } from '@/pages/audit'
import { StarlarkConfigPage } from '@/pages/starlark/index'
import { StarlarkDetailPage } from '@/pages/starlark/detail'
import { useAuth } from '@/contexts/auth-context'

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
  const { lens } = useAuth()
  const isPlatformAdmin = lens === 'platform'

  return (
    <AppShell currentPath={pathname}>
      <Routes>
        {/* Tenant-scoped routes */}
        <Route path="/" element={<PlaceholderPage title="Dashboard" />} />
        <Route path="/accounts" element={<PlaceholderPage title="Accounts" />} />
        <Route
          path="/internal-accounts"
          element={<PlaceholderPage title="Internal Accounts" />}
        />
        <Route path="/payments" element={<PlaceholderPage title="Payments" />} />
        <Route path="/transactions" element={<PlaceholderPage title="Transactions" />} />
        <Route path="/positions" element={<PlaceholderPage title="Positions" />} />
        <Route path="/ledger" element={<PlaceholderPage title="Ledger" />} />
        <Route path="/parties" element={<PlaceholderPage title="Parties" />} />
        <Route path="/reconciliation" element={<PlaceholderPage title="Reconciliation" />} />
        <Route
          path="/starlark-config"
          element={<StarlarkConfigPage isPlatformAdmin={isPlatformAdmin} />}
        />
        <Route path="/starlark-config/:definitionId" element={<StarlarkDetailPage />} />
        <Route path="/reference-data" element={<PlaceholderPage title="Reference Data" />} />
        <Route
          path="/gateway-mappings"
          element={<PlaceholderPage title="Gateway Mappings" />}
        />
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
          </TenantProvider>
        </AuthProvider>
        {import.meta.env.DEV && <ReactQueryDevtools initialIsOpen={false} />}
      </QueryClientProvider>
    </PageErrorBoundary>
  )
}

export default App
