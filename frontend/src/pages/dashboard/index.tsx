import { useQuery } from '@tanstack/react-query'
import { CreditCard, FileText, BarChart3, ArrowRight } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { useApiClients } from '@/api/context'
import { useTenantContext } from '@/contexts/tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { StatCard } from './stat-card'
import { ActivityFeed, type ActivityItem } from './activity-feed'
import { QuickActions, type QuickAction } from './quick-actions'

function useDashboardStats(tenantSlug: string | null) {
  const clients = useApiClients()

  const paymentsQuery = useQuery({
    queryKey: [...tenantKeys.all(tenantSlug ?? ''), 'dashboard', 'payments'],
    queryFn: () =>
      clients.paymentOrder.listPaymentOrders({
        pagination: { pageSize: 1, pageToken: '' },
      }),
    enabled: !!tenantSlug,
  })

  const bookingLogsQuery = useQuery({
    queryKey: [...tenantKeys.all(tenantSlug ?? ''), 'dashboard', 'bookingLogs'],
    queryFn: () =>
      clients.financialAccounting.listFinancialBookingLogs({
        pagination: { pageSize: 1, pageToken: '' },
      }),
    enabled: !!tenantSlug,
  })

  const ledgerPostingsQuery = useQuery({
    queryKey: [...tenantKeys.all(tenantSlug ?? ''), 'dashboard', 'ledgerPostings'],
    queryFn: () =>
      clients.financialAccounting.listLedgerPostings({
        pagination: { pageSize: 1, pageToken: '' },
      }),
    enabled: !!tenantSlug,
  })

  return { paymentsQuery, bookingLogsQuery, ledgerPostingsQuery }
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
  const { tenantSlug } = useTenantContext()
  const { paymentsQuery, bookingLogsQuery, ledgerPostingsQuery } = useDashboardStats(tenantSlug)

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

  // Build recent activity feed from payment orders data
  const activityItems: ActivityItem[] = (paymentsQuery.data?.paymentOrders ?? [])
    .slice(0, 10)
    .map((po) => ({
      id: po.id ?? String(Math.random()),
      type: 'payment' as const,
      title: `Payment order ${po.id ?? ''}`,
      description: po.id ? `ID: ${po.id}` : undefined,
      timestamp: po.createdAt ?? null,
      status: po.status?.toString(),
    }))

  const quickActions: QuickAction[] = [
    {
      id: 'view-payments',
      label: 'View Payment Orders',
      description: 'Browse all payment orders',
      icon: <CreditCard className="h-4 w-4" />,
      onClick: () => {},
    },
    {
      id: 'view-booking-logs',
      label: 'View Booking Logs',
      description: 'Browse financial booking logs',
      icon: <FileText className="h-4 w-4" />,
      onClick: () => {},
    },
    {
      id: 'view-ledger',
      label: 'View Ledger Postings',
      description: 'Browse double-entry ledger',
      icon: <BarChart3 className="h-4 w-4" />,
      onClick: () => {},
    },
    {
      id: 'view-reconciliations',
      label: 'Reconciliations',
      description: 'Check reconciliation status',
      icon: <ArrowRight className="h-4 w-4" />,
      onClick: () => {},
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
      <div className="grid gap-4 md:grid-cols-3">
        <StatCard
          title="Payment Orders"
          value={paymentsCount?.count}
          isLoading={paymentsQuery.isLoading}
          error={paymentsQuery.isError}
          showRecentQualifier={!!(paymentsCount?.isEstimate && !paymentsQuery.isLoading && !paymentsQuery.isError)}
          description="Active payment orders"
          icon={<CreditCard className="h-4 w-4" />}
        />
        <StatCard
          title="Booking Logs"
          value={bookingLogsCount?.count}
          isLoading={bookingLogsQuery.isLoading}
          error={bookingLogsQuery.isError}
          showRecentQualifier={!!(bookingLogsCount?.isEstimate && !bookingLogsQuery.isLoading && !bookingLogsQuery.isError)}
          description="Financial booking logs"
          icon={<FileText className="h-4 w-4" />}
        />
        <StatCard
          title="Ledger Postings"
          value={ledgerPostingsCount?.count}
          isLoading={ledgerPostingsQuery.isLoading}
          error={ledgerPostingsQuery.isError}
          showRecentQualifier={!!(ledgerPostingsCount?.isEstimate && !ledgerPostingsQuery.isLoading && !ledgerPostingsQuery.isError)}
          description="Double-entry postings"
          icon={<BarChart3 className="h-4 w-4" />}
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
            <ActivityFeed
              items={activityItems}
              isLoading={paymentsQuery.isLoading}
            />
          </CardContent>
        </Card>

        {/* Quick Actions - takes 1/3 width */}
        <Card>
          <CardHeader>
            <CardTitle>Quick Actions</CardTitle>
          </CardHeader>
          <CardContent>
            <QuickActions actions={quickActions} />
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
