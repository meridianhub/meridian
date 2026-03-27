import * as React from 'react'
import { useParams } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { MoneyDisplay } from '@/shared/money-display'
import { StatusBadge } from '@/shared/status-badge'
import { Breadcrumbs, PageHeader, PageShell, ErrorState } from '@/shared'
import { tenantKeys } from '@/lib/query-keys'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { usePageTitle } from '@/hooks/use-page-title'
import { useInvoiceDetail, useInvoiceEmails } from '../api/hooks'
import { EmailDeliveryStatusBadge } from '../components/email-delivery-status-badge'
import { InvoiceActions } from '../components/invoice-actions'
import type { InvoiceEmail } from '../api/types'

function InvoiceDetailSkeleton() {
  return (
    <div data-testid="invoice-detail-skeleton" className="p-6 space-y-6">
      <div className="h-5 w-32 animate-pulse rounded bg-muted" />
      <div className="h-8 w-64 animate-pulse rounded bg-muted" />
      <div className="h-48 w-full animate-pulse rounded-xl bg-muted" />
    </div>
  )
}

function DetailRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-1 sm:grid-cols-[180px_1fr] gap-4 py-2 border-b last:border-0">
      <span className="text-sm font-medium text-muted-foreground">{label}</span>
      <span className="text-sm">{children}</span>
    </div>
  )
}

function formatDate(iso: string | undefined): string {
  if (!iso) return '-'
  // Parse YYYY-MM-DD as local midnight to avoid UTC-offset day shifts
  const parts = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso)
  const d = parts
    ? new Date(Number(parts[1]), Number(parts[2]) - 1, Number(parts[3]))
    : new Date(iso)
  if (isNaN(d.getTime())) return '-'
  return d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
}

function formatDateTime(iso: string | undefined): string {
  if (!iso) return '-'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return '-'
  return d.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

function EmailHistoryRow({ email }: { email: InvoiceEmail }) {
  const deliveryStatus = {
    status: email.status,
    sentAt: email.sentAt,
    deliveredAt: email.deliveredAt,
    bounceReason: email.bounceReason,
  }

  return (
    <div className="grid grid-cols-1 sm:grid-cols-[1fr_1fr_auto] gap-4 py-3 border-b last:border-0 items-center">
      <div>
        <p className="text-sm font-medium">{email.templateName}</p>
        <p className="text-xs text-muted-foreground">{email.toAddresses.join(', ')}</p>
      </div>
      <div className="text-sm text-muted-foreground">
        {email.sentAt ? formatDateTime(email.sentAt) : '-'}
      </div>
      <EmailDeliveryStatusBadge status={deliveryStatus} />
    </div>
  )
}

export function InvoiceDetailPage() {
  const { id } = useParams<{ id: string }>()
  const tenantSlug = useTenantSlug()
  const queryClient = useQueryClient()

  const { data, isLoading, isError } = useInvoiceDetail(id)
  const { data: emails = [], isLoading: emailsLoading, isError: emailsError } = useInvoiceEmails(id)

  usePageTitle(data ? `Invoice ${data.invoiceNumber}` : 'Invoice')

  const queryKey = tenantKeys.invoice(tenantSlug ?? '', id ?? '')

  function handleActionSuccess() {
    void queryClient.invalidateQueries({ queryKey, exact: true })
  }

  if (isLoading) {
    return <InvoiceDetailSkeleton />
  }

  if (isError) {
    return (
      <div data-testid="invoice-detail-error">
        <PageShell className="p-6">
          <Breadcrumbs
            items={[
              { label: 'Billing', href: '/billing' },
              { label: 'Invoices', href: '/billing/invoices' },
              { label: 'Error' },
            ]}
          />
          <ErrorState message="Failed to load invoice details." />
        </PageShell>
      </div>
    )
  }

  if (!data) {
    return (
      <div data-testid="invoice-detail-not-found">
        <PageShell className="p-6">
          <Breadcrumbs
            items={[
              { label: 'Billing', href: '/billing' },
              { label: 'Invoices', href: '/billing/invoices' },
              { label: 'Not found' },
            ]}
          />
          <ErrorState message="Invoice not found." />
        </PageShell>
      </div>
    )
  }

  return (
    <PageShell className="p-6">
      <Breadcrumbs
        items={[
          { label: 'Billing', href: '/billing' },
          { label: 'Invoices', href: '/billing/invoices' },
          { label: data.invoiceNumber },
        ]}
      />

      <PageHeader
        title={data.invoiceNumber}
        actions={
          <>
            <StatusBadge status={data.status} />
            <InvoiceActions
              invoiceId={data.id}
              status={data.status}
              onActionSuccess={handleActionSuccess}
            />
          </>
        }
      />

      <Tabs defaultValue="overview">
        <TabsList>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="line-items">Line Items</TabsTrigger>
          <TabsTrigger value="email-history">
            Email History
            {emails.length > 0 && (
              <span className="ml-1.5 rounded-full bg-muted px-1.5 py-0.5 text-xs font-medium">
                {emails.length}
              </span>
            )}
          </TabsTrigger>
        </TabsList>

        <TabsContent value="overview">
          <Card>
            <CardHeader>
              <CardTitle>Invoice Details</CardTitle>
            </CardHeader>
            <CardContent>
              <div className="divide-y">
                <DetailRow label="Invoice Number">{data.invoiceNumber}</DetailRow>
                <DetailRow label="Party">{data.partyId}</DetailRow>
                <DetailRow label="Billing Run">{data.billingRunId}</DetailRow>
                <DetailRow label="Status">
                  <StatusBadge status={data.status} />
                </DetailRow>
                <DetailRow label="Total Amount">
                  <MoneyDisplay
                    amount={String(data.subtotalCents)}
                    currency={data.currency}
                  />
                </DetailRow>
                <DetailRow label="Due Date">{formatDate(data.dueDate)}</DetailRow>
                <DetailRow label="Created">{formatDateTime(data.createdAt)}</DetailRow>
                <DetailRow label="Updated">{formatDateTime(data.updatedAt)}</DetailRow>
              </div>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="line-items">
          <Card>
            <CardHeader>
              <CardTitle>Line Items</CardTitle>
            </CardHeader>
            <CardContent className="p-0">
              {data.lineItems.length === 0 ? (
                <div className="flex items-center justify-center py-12 text-sm text-muted-foreground">
                  No line items
                </div>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Description</TableHead>
                      <TableHead className="text-right">Quantity</TableHead>
                      <TableHead className="text-right">Unit Price</TableHead>
                      <TableHead className="text-right">Amount</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {data.lineItems.map((item, index) => (
                      <TableRow key={index}>
                        <TableCell>{item.description}</TableCell>
                        <TableCell className="text-right">{item.quantity}</TableCell>
                        <TableCell className="text-right">
                          <MoneyDisplay
                            amount={String(item.unitPriceCents)}
                            currency={data.currency}
                          />
                        </TableCell>
                        <TableCell className="text-right">
                          <MoneyDisplay
                            amount={String(item.totalCents)}
                            currency={data.currency}
                          />
                        </TableCell>
                      </TableRow>
                    ))}
                    <TableRow className="font-medium">
                      <TableCell colSpan={3} className="text-right">
                        Total
                      </TableCell>
                      <TableCell className="text-right">
                        <MoneyDisplay
                          amount={String(data.subtotalCents)}
                          currency={data.currency}
                        />
                      </TableCell>
                    </TableRow>
                  </TableBody>
                </Table>
              )}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="email-history">
          <Card>
            <CardHeader>
              <CardTitle>Email History</CardTitle>
            </CardHeader>
            <CardContent>
              {emailsLoading ? (
                <div className="space-y-3">
                  {[1, 2].map((i) => (
                    <div key={i} className="h-12 animate-pulse rounded bg-muted" />
                  ))}
                </div>
              ) : emailsError ? (
                <div className="flex items-center justify-center py-12 text-sm text-destructive">
                  Failed to load email history.
                </div>
              ) : emails.length === 0 ? (
                <div className="flex items-center justify-center py-12 text-sm text-muted-foreground">
                  No email history
                </div>
              ) : (
                <div className="divide-y">
                  {emails.map((email) => (
                    <EmailHistoryRow key={email.idempotencyKey} email={email} />
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </PageShell>
  )
}
