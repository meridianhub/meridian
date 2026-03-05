import * as React from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { MoneyDisplay } from '@/shared/money-display'
import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay } from '@/shared/time-display'
import { SagaTimeline } from '@/features/sagas/components/saga-timeline'
import { AuditTrail } from '@/shared/audit-trail'
import { EntityLink, Breadcrumbs } from '@/shared'
import { tenantKeys } from '@/lib/query-keys'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { usePaymentDetail } from '../hooks'
import {
  InitiatePaymentDialog,
  CancelPaymentDialog,
  ReversePaymentDialog,
} from './dialogs'

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

const CANCELLABLE_STATUSES = new Set(['INITIATED', 'RESERVED', 'EXECUTING'])

function PaymentActions({
  paymentOrderId,
  status,
  onActionSuccess,
}: {
  paymentOrderId: string
  status: string
  onActionSuccess: () => void
}) {
  const [cancelOpen, setCancelOpen] = React.useState(false)
  const [reverseOpen, setReverseOpen] = React.useState(false)

  const showCancel = CANCELLABLE_STATUSES.has(status)
  const showReverse = status === 'COMPLETED'

  if (!showCancel && !showReverse) {
    return null
  }

  return (
    <>
      <div className="flex gap-2">
        {showCancel && (
          <Button
            variant="outline"
            size="sm"
            onClick={() => setCancelOpen(true)}
          >
            Cancel Payment
          </Button>
        )}
        {showReverse && (
          <Button
            variant="destructive"
            size="sm"
            onClick={() => setReverseOpen(true)}
          >
            Reverse Payment
          </Button>
        )}
      </div>

      <CancelPaymentDialog
        open={cancelOpen}
        onOpenChange={setCancelOpen}
        onSuccess={onActionSuccess}
        paymentOrderId={paymentOrderId}
        currentStatus={status}
      />
      <ReversePaymentDialog
        open={reverseOpen}
        onOpenChange={setReverseOpen}
        onSuccess={onActionSuccess}
        paymentOrderId={paymentOrderId}
        currentStatus={status}
      />
    </>
  )
}

export function PaymentDetailPage() {
  const { paymentOrderId } = useParams<{ paymentOrderId: string }>()
  const tenantSlug = useTenantSlug()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [initiateOpen, setInitiateOpen] = React.useState(false)

  const queryKey = tenantSlug && paymentOrderId
    ? tenantKeys.payment(tenantSlug, paymentOrderId)
    : ['payments', paymentOrderId]

  const { data, isLoading, isError } = usePaymentDetail(paymentOrderId)

  function handleActionSuccess() {
    void queryClient.invalidateQueries({ queryKey })
  }

  function handleInitiateSuccess(newPaymentOrderId: string) {
    if (newPaymentOrderId) {
      void navigate(`/payments/${newPaymentOrderId}`)
    }
  }

  if (isLoading) {
    return <PaymentDetailSkeleton />
  }

  if (isError || !data) {
    return (
      <div data-testid="payment-detail-error" className="p-6">
        <Breadcrumbs items={[{ label: 'Payments', href: '/payments' }, { label: 'Error' }]} />
        <p className="mt-4 text-sm text-destructive">Failed to load payment order details.</p>
      </div>
    )
  }

  return (
    <div className="p-6 space-y-6">
      {/* Breadcrumb navigation */}
      <Breadcrumbs
        items={[
          { label: 'Payments', href: '/payments' },
          { label: data.paymentOrderId },
        ]}
      />

      {/* Page header */}
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div className="flex items-center gap-4">
          <h1 className="text-2xl font-semibold">{data.paymentOrderId}</h1>
          <StatusBadge status={data.status} />
        </div>

        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => setInitiateOpen(true)}
          >
            <Plus className="mr-1 h-4 w-4" />
            New Payment
          </Button>

          <PaymentActions
            paymentOrderId={data.paymentOrderId}
            status={data.status}
            onActionSuccess={handleActionSuccess}
          />
        </div>
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
                <DetailRow label="Debtor Account">
                  <EntityLink type="account" id={data.debtorAccountId} />
                </DetailRow>
                <DetailRow label="Creditor Reference">{data.creditorReference}</DetailRow>
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

      <InitiatePaymentDialog
        open={initiateOpen}
        onOpenChange={setInitiateOpen}
        onSuccess={handleInitiateSuccess}
      />
    </div>
  )
}
