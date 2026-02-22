import { Link, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { ChevronLeftIcon } from 'lucide-react'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { MoneyDisplay } from '@/components/shared/money-display'
import { StatusBadge } from '@/components/shared/status-badge'
import { TimeDisplay } from '@/components/shared/time-display'
import { SagaTimeline } from '@/components/shared/saga-timeline'
import { AuditTrail } from '@/components/shared/audit-trail'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { fetchPaymentDetail } from './payment-detail-query'

function PaymentDetailSkeleton() {
  return (
    <div data-testid="payment-detail-skeleton" className="p-6 space-y-6">
      <div className="h-5 w-32 animate-pulse rounded bg-muted" />
      <div className="h-8 w-64 animate-pulse rounded bg-muted" />
      <div className="h-48 w-full animate-pulse rounded-xl bg-muted" />
    </div>
  )
}

function DetailRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[180px_1fr] gap-4 py-2 border-b last:border-0">
      <span className="text-sm font-medium text-muted-foreground">{label}</span>
      <span className="text-sm">{children}</span>
    </div>
  )
}

export function PaymentDetailPage() {
  const { paymentOrderId } = useParams<{ paymentOrderId: string }>()
  const tenantSlug = useTenantSlug()
  const queryKey = tenantSlug && paymentOrderId
    ? tenantKeys.payment(tenantSlug, paymentOrderId)
    : ['payments', paymentOrderId]

  const { data, isLoading, isError } = useQuery({
    queryKey,
    queryFn: () => fetchPaymentDetail(paymentOrderId!),
    enabled: !!paymentOrderId,
  })

  if (isLoading) {
    return <PaymentDetailSkeleton />
  }

  if (isError || !data) {
    return (
      <div data-testid="payment-detail-error" className="p-6">
        <Link
          to="/payments"
          className="mb-4 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
        >
          <ChevronLeftIcon className="h-4 w-4" />
          Payments
        </Link>
        <p className="mt-4 text-sm text-destructive">Failed to load payment order details.</p>
      </div>
    )
  }

  return (
    <div className="p-6 space-y-6">
      {/* Back navigation */}
      <Link
        to="/payments"
        className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
      >
        <ChevronLeftIcon className="h-4 w-4" />
        Payments
      </Link>

      {/* Page header */}
      <div className="flex items-center gap-4">
        <h1 className="text-2xl font-semibold">{data.paymentOrderId}</h1>
        <StatusBadge status={data.status} />
      </div>

      {/* Tabs */}
      <Tabs defaultValue="overview">
        <TabsList>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="saga-steps">Saga Steps</TabsTrigger>
          <TabsTrigger value="audit-trail">Audit Trail</TabsTrigger>
        </TabsList>

        {/* Overview tab */}
        <TabsContent value="overview">
          <Card>
            <CardHeader>
              <CardTitle>Payment Details</CardTitle>
            </CardHeader>
            <CardContent>
              <div className="divide-y">
                <DetailRow label="Payment Order ID">{data.paymentOrderId}</DetailRow>
                <DetailRow label="Debtor Account">{data.debtorAccountId}</DetailRow>
                <DetailRow label="Creditor IBAN">{data.creditorIban}</DetailRow>
                <DetailRow label="Amount">
                  <MoneyDisplay amount={data.amount} currency={data.currency} />
                </DetailRow>
                <DetailRow label="Status">
                  <StatusBadge status={data.status} />
                </DetailRow>
                {data.reference && (
                  <DetailRow label="Reference">{data.reference}</DetailRow>
                )}
                <DetailRow label="Created">
                  <TimeDisplay timestamp={data.createdAt} format="both" />
                </DetailRow>
              </div>
            </CardContent>
          </Card>
        </TabsContent>

        {/* Saga Steps tab */}
        <TabsContent value="saga-steps">
          <Card>
            <CardHeader>
              <CardTitle>Saga Progression</CardTitle>
            </CardHeader>
            <CardContent>
              <SagaTimeline
                currentStatus={data.status}
                steps={data.sagaSteps}
                compensationSteps={data.compensationSteps}
              />
            </CardContent>
          </Card>
        </TabsContent>

        {/* Audit Trail tab */}
        <TabsContent value="audit-trail">
          <Card>
            <CardHeader>
              <CardTitle>Audit Trail</CardTitle>
            </CardHeader>
            <CardContent>
              <AuditTrail entityType="payment_order" entityId={data.paymentOrderId} />
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  )
}
