import { PageErrorBoundary } from '@/components/error-boundary'
import { DashboardPage } from '@/features/dashboard'
import { LedgerPage } from '@/pages/ledger'
import { BookingLogDetailPage } from '@/pages/ledger/booking-log-detail'

export interface Route {
  path: string
  name: string
  component: React.ComponentType
}

const routes: Route[] = [
  { path: '/', name: 'Dashboard', component: DashboardPage },
  { path: '/accounts', name: 'Accounts', component: () => <div>Accounts</div> },
  {
    path: '/internal-accounts',
    name: 'Internal Accounts',
    component: () => <div>Internal Accounts</div>,
  },
  { path: '/payments', name: 'Payments', component: () => <div>Payments</div> },
  { path: '/transactions', name: 'Transactions', component: () => <div>Transactions</div> },
  { path: '/positions', name: 'Positions', component: () => <div>Positions</div> },
  { path: '/ledger', name: 'Ledger', component: LedgerPage },
  { path: '/ledger/:bookingLogId', name: 'Booking Log Detail', component: BookingLogDetailPage },
  { path: '/parties', name: 'Parties', component: () => <div>Parties</div> },
  {
    path: '/reconciliation',
    name: 'Reconciliation',
    component: () => <div>Reconciliation</div>,
  },
  {
    path: '/starlark-config',
    name: 'Starlark Configuration',
    component: () => <div>Starlark Configuration</div>,
  },
  {
    path: '/reference-data',
    name: 'Reference Data',
    component: () => <div>Reference Data</div>,
  },
  {
    path: '/gateway-mappings',
    name: 'Gateway Mappings',
    component: () => <div>Gateway Mappings</div>,
  },
  { path: '/audit-log', name: 'Audit Log', component: () => <div>Audit Log</div> },
  {
    path: '/tenants',
    name: 'Tenant Management',
    component: () => <div>Tenant Management</div>,
  },
  {
    path: '/platform',
    name: 'Platform Monitoring',
    component: () => <div>Platform Monitoring</div>,
  },
]

export function getRoutes(): Route[] {
  return routes
}

export function wrapRouteWithErrorBoundary(component: React.ComponentType): React.ComponentType {
  return function WrappedRoute() {
    const Component = component
    return (
      <PageErrorBoundary>
        <Component />
      </PageErrorBoundary>
    )
  }
}

export function createRouteHandler(
  route: Route,
): { path: string; name: string; component: React.ComponentType } {
  return {
    path: route.path,
    name: route.name,
    component: wrapRouteWithErrorBoundary(route.component),
  }
}

export function getRouteHandlers() {
  return getRoutes().map(createRouteHandler)
}
