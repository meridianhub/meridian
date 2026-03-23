import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { CreditCard, FileText, BarChart3, ArrowRight, RefreshCw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { useApiClients } from '@/api/context'
import { useTenantContext } from '@/contexts/tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { usePageTitle } from '@/hooks/use-page-title'
import { StatCard } from './stat-card'
import { ActivityFeed, type ActivityItem } from './activity-feed'
import { QuickActions, type QuickAction } from './quick-actions'
import { McpConnectionCard } from './mcp-connection-card'

// Cache dashboard stats for 60s to reduce unnecessary refetches on navigation
const DASHBOARD_STALE_TIME = 60_000

function useDashboardStats(tenantSlug: string | null) {
  const clients = useApiClients()

  // pageSize: 1 for stat queries — we only need totalCount from pagination metadata
  const paymentsQuery = useQuery({
    queryKey: [...tenantKeys.all(tenantSlug ?? ''), 'dashboard', 'payments'],
    queryFn: () =>
      clients.paymentOrder.listPaymentOrders({
        pagination: { pageSize: 1, pageToken: '' },
      }),
    enabled: !!tenantSlug,
    staleTime: DASHBOARD_STALE_TIME,
  })

  const bookingLogsQuery = useQuery({
    queryKey: [...tenantKeys.all(tenantSlug ?? ''), 'dashboard', 'bookingLogs'],
    queryFn: () =>
      clients.financialAccounting.listFinancialBookingLogs({
        pagination: { pageSize: 1, pageToken: '' },
      }),
    enabled: !!tenantSlug,
    staleTime: DASHBOARD_STALE_TIME,
  })

  const ledgerPostingsQuery = useQuery({
    queryKey: [...tenantKeys.all(tenantSlug ?? ''), 'dashboard', 'ledgerPostings'],
    queryFn: () =>
      clients.financialAccounting.listLedgerPostings({
        pagination: { pageSize: 1, pageToken: '' },
      }),
    enabled: !!tenantSlug,
    staleTime: DASHBOARD_STALE_TIME,
  })

  // Separate query for activity feed — uses larger page to populate the list
  const activityQuery = useQuery({
    queryKey: [...tenantKeys.all(tenantSlug ?? ''), 'dashboard', 'activity'],
    queryFn: () =>
      clients.paymentOrder.listPaymentOrders({
        pagination: { pageSize: 10, pageToken: '' },
      }),
    enabled: !!tenantSlug,
    staleTime: DASHBOARD_STALE_TIME,
  })

  return { paymentsQuery, bookingLogsQuery, ledgerPostingsQuery, activityQuery }
}

function getCountFromPagination(
  pagination: { totalCount?: bigint | string | number | null } | null | undefined,
  fallbackLength: number,
): { count: number; isEstimate: boolean } {
  if (!pagination) return { count: fallbackLength, isEstimate: true }

  const total =
    typeof pagination.totalCount === 'bigint'
      ? Number(pagination.totalCount)
      : typeof pagination.totalCount === 'string'
        ? parseInt(pagination.totalCount, 10)
        : (pagination.totalCount ?? -1)

  if (total === -1 || total === null || total === undefined || isNaN(total as number)) {
    return { count: fallbackLength, isEstimate: true }
  }
  return { count: total as number, isEstimate: false }
}

export function DashboardPage() {
  usePageTitle('Dashboard')
  const navigate = useNavigate()
  const { tenantSlug } = useTenantContext()
  const { paymentsQuery, bookingLogsQuery, ledgerPostingsQuery, activityQuery } =
    useDashboardStats(tenantSlug)

  const paymentsCount = paymentsQuery.data
    ? getCountFromPagination(
        paymentsQuery.data.pagination,
        paymentsQuery.data.paymentOrders.length,
      )
    : null

  const bookingLogsCount = bookingLogsQuery.data
    ? getCountFromPagination(
        bookingLogsQuery.data.pagination,
        bookingLogsQuery.data.financialBookingLogs.length,
      )
    : null

  const ledgerPostingsCount = ledgerPostingsQuery.data
    ? getCountFromPagination(
        ledgerPostingsQuery.data.pagination,
        ledgerPostingsQuery.data.ledgerPostings.length,
      )
    : null

  // Build recent activity feed from dedicated activity query (pageSize: 10)
  const activityItems: ActivityItem[] = (activityQuery.data?.paymentOrders ?? []).map((po, idx) => ({
    id: po.paymentOrderId || `activity-${idx}`,
    type: 'payment' as const,
    title: `Payment order ${po.paymentOrderId || ''}`,
    description: po.paymentOrderId ? `ID: ${po.paymentOrderId}` : undefined,
    timestamp: po.createdAt ?? null,
    status: po.status?.toString(),
    href: po.paymentOrderId ? `/payments/${po.paymentOrderId}` : undefined,
  }))

  const quickActions: QuickAction[] = [
    {
      id: 'view-payments',
      label: 'View Payment Orders',
      description: 'Browse all payment orders',
      icon: <CreditCard className="h-4 w-4" />,
      onClick: () => navigate('/payments'),
    },
    {
      id: 'view-booking-logs',
      label: 'View Booking Logs',
      description: 'Browse financial booking logs',
      icon: <FileText className="h-4 w-4" />,
      onClick: () => navigate('/ledger'),
    },
    {
      id: 'view-ledger',
      label: 'View Ledger Postings',
      description: 'Browse double-entry ledger',
      icon: <BarChart3 className="h-4 w-4" />,
      onClick: () => navigate('/ledger'),
    },
    {
      id: 'view-reconciliations',
      label: 'Reconciliations',
      description: 'Check reconciliation status',
      icon: <ArrowRight className="h-4 w-4" />,
      onClick: () => navigate('/reconciliation'),
    },
  ]

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Dashboard</h1>
        <p className="text-muted-foreground">
          {tenantSlug ? `Overview for ${tenantSlug}` : 'Tenant overview'}
        </p>
      </div>

      {/* Stat Cards */}
      <div data-testid="stat-cards" className="grid gap-4 md:grid-cols-3">
        <StatCard
          title="Payment Orders"
          value={paymentsCount?.count}
          isLoading={paymentsQuery.isLoading}
          error={paymentsQuery.isError}
          onRetry={() => void paymentsQuery.refetch()}
          showRecentQualifier={!!(paymentsCount?.isEstimate && !paymentsQuery.isLoading && !paymentsQuery.isError)}
          description="Active payment orders"
          icon={<CreditCard className="h-4 w-4" />}
          href="/payments"
        />
        <StatCard
          title="Booking Logs"
          value={bookingLogsCount?.count}
          isLoading={bookingLogsQuery.isLoading}
          error={bookingLogsQuery.isError}
          onRetry={() => void bookingLogsQuery.refetch()}
          showRecentQualifier={!!(bookingLogsCount?.isEstimate && !bookingLogsQuery.isLoading && !bookingLogsQuery.isError)}
          description="Financial booking logs"
          icon={<FileText className="h-4 w-4" />}
          href="/ledger"
        />
        <StatCard
          title="Ledger Postings"
          value={ledgerPostingsCount?.count}
          isLoading={ledgerPostingsQuery.isLoading}
          error={ledgerPostingsQuery.isError}
          onRetry={() => void ledgerPostingsQuery.refetch()}
          showRecentQualifier={!!(ledgerPostingsCount?.isEstimate && !ledgerPostingsQuery.isLoading && !ledgerPostingsQuery.isError)}
          description="Double-entry postings"
          icon={<BarChart3 className="h-4 w-4" />}
          href="/ledger"
        />
      </div>

      {/* Main content area */}
      <div className="grid gap-4 md:grid-cols-3">
        {/* Recent Activity - takes 2/3 width */}
        <Card className="md:col-span-2">
          <CardHeader>
            <CardTitle>Recent Activity</CardTitle>
          </CardHeader>
          <CardContent>
            {activityQuery.isError ? (
              <div className="py-8 text-center">
                <p className="text-sm text-muted-foreground">
                  Unable to load recent activity.
                </p>
                <Button
                  variant="outline"
                  size="sm"
                  className="mt-3"
                  onClick={() => void activityQuery.refetch()}
                >
                  <RefreshCw className="h-3 w-3" />
                  Retry
                </Button>
              </div>
            ) : (
              <ActivityFeed items={activityItems} isLoading={activityQuery.isLoading} />
            )}
          </CardContent>
        </Card>

        {/* Right column: Quick Actions + MCP Connection */}
        <div className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle>Quick Actions</CardTitle>
            </CardHeader>
            <CardContent>
              <QuickActions actions={quickActions} />
            </CardContent>
          </Card>
          <McpConnectionCard />
        </div>
      </div>
    </div>
  )
}
