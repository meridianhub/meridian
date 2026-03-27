import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { ConnectError, Code } from '@connectrpc/connect'
import { useApiClients } from '@/api/context'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import type { DataTableQueryParams, DataTableResult } from '@/shared/data-table'
import type { BillingRun, Invoice, InvoiceEmail, EmailDeliveryStatus } from './types'
import type { Timestamp } from '@bufbuild/protobuf/wkt'

function timestampToISO(ts?: Timestamp): string {
  if (!ts) return ''
  return new Date(Number(ts.seconds) * 1000).toISOString()
}

function billingRunStatus(raw: unknown): BillingRun['status'] {
  const s = String(raw).replace('BILLING_RUN_STATUS_', '')
  return s as BillingRun['status']
}

function invoiceStatus(raw: unknown): Invoice['status'] {
  const s = String(raw).replace('INVOICE_STATUS_', '')
  return s as Invoice['status']
}

function emailStatus(raw: unknown): EmailDeliveryStatus['status'] {
  const s = String(raw).replace('EMAIL_STATUS_', '')
  return s as EmailDeliveryStatus['status']
}

function generateIdempotencyKey(): string {
  return `${Date.now()}-${Math.random().toString(36).slice(2)}`
}

/**
 * Fetches a paginated list of billing runs for use with DataTable.
 */
export function useBillingRunsTable() {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  const queryKey = tenantKeys.billingRuns(tenantSlug ?? '')

  async function queryFn(
    params: DataTableQueryParams,
  ): Promise<DataTableResult<BillingRun>> {
    if (!tenantSlug) return { items: [] }

    try {
      const response = await clients.billing.listBillingRuns({
        pagination: {
          pageSize: params.pageSize,
          pageToken: params.pageToken ?? '',
        },
      })

      const items: BillingRun[] = (response.billingRuns ?? []).map((run) => ({
        id: run.id ?? '',
        billingPeriod: {
          start: timestampToISO(run.periodStart),
          end: timestampToISO(run.periodEnd),
        },
        status: billingRunStatus(run.status),
        dunningLevel: run.dunningLevel ?? 0,
        invoiceCount: run.invoiceCount ?? 0,
        totalAmountCents: Number(run.totalAmountCents ?? 0),
        currency: run.currency ?? '',
        createdAt: timestampToISO(run.createdAt),
      }))

      return {
        items,
        nextPageToken: response.pagination?.nextPageToken || undefined,
      }
    } catch (error) {
      if (
        error instanceof ConnectError &&
        (error.code === Code.NotFound || error.code === Code.Unimplemented)
      ) {
        return { items: [] }
      }
      throw error
    }
  }

  return { queryKey, queryFn, tenantSlug }
}

/**
 * Fetches a paginated list of invoices for use with DataTable.
 */
export function useInvoicesTable() {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  const queryKey = tenantKeys.invoices(tenantSlug ?? '')

  async function queryFn(
    params: DataTableQueryParams,
  ): Promise<DataTableResult<Invoice>> {
    if (!tenantSlug) return { items: [] }

    try {
      const response = await clients.billing.listInvoices({
        pagination: {
          pageSize: params.pageSize,
          pageToken: params.pageToken ?? '',
        },
        ...(params.filters?.billing_run_id ? { billingRunId: params.filters.billing_run_id } : {}),
        ...(params.filters?.party_id ? { partyId: params.filters.party_id } : {}),
      })

      const items: Invoice[] = (response.invoices ?? []).map((inv) => ({
        id: inv.id ?? '',
        billingRunId: inv.billingRunId ?? '',
        partyId: inv.partyId ?? '',
        invoiceNumber: inv.invoiceNumber ?? '',
        lineItems: (inv.lineItems ?? []).map((li) => ({
          description: li.description ?? '',
          quantity: li.quantity ?? '',
          unitPriceCents: Number(li.unitPriceCents ?? 0),
          totalCents: Number(li.totalCents ?? 0),
          valuationAnalysis: li.valuationAnalysis as Record<string, unknown> | undefined,
        })),
        subtotalCents: Number(inv.subtotalCents ?? 0),
        currency: inv.currency ?? '',
        status: invoiceStatus(inv.status),
        dueDate: inv.dueDate || undefined,
        createdAt: timestampToISO(inv.createdAt),
        updatedAt: timestampToISO(inv.updatedAt),
      }))

      return {
        items,
        nextPageToken: response.pagination?.nextPageToken || undefined,
      }
    } catch (error) {
      if (
        error instanceof ConnectError &&
        (error.code === Code.NotFound || error.code === Code.Unimplemented)
      ) {
        return { items: [] }
      }
      throw error
    }
  }

  return { queryKey, queryFn, tenantSlug }
}

/**
 * Fetches a single invoice by ID.
 */
export function useInvoiceDetail(invoiceId: string | undefined) {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  return useQuery({
    queryKey: tenantKeys.invoice(tenantSlug ?? '', invoiceId ?? ''),
    queryFn: async (): Promise<Invoice | null> => {
      const response = await clients.billing.getInvoice({ id: invoiceId ?? '' })
      const inv = response.invoice
      if (!inv) return null
      return {
        id: inv.id ?? '',
        billingRunId: inv.billingRunId ?? '',
        partyId: inv.partyId ?? '',
        invoiceNumber: inv.invoiceNumber ?? '',
        lineItems: (inv.lineItems ?? []).map((li) => ({
          description: li.description ?? '',
          quantity: li.quantity ?? '',
          unitPriceCents: Number(li.unitPriceCents ?? 0),
          totalCents: Number(li.totalCents ?? 0),
          valuationAnalysis: li.valuationAnalysis as Record<string, unknown> | undefined,
        })),
        subtotalCents: Number(inv.subtotalCents ?? 0),
        currency: inv.currency ?? '',
        status: invoiceStatus(inv.status),
        dueDate: inv.dueDate || undefined,
        createdAt: timestampToISO(inv.createdAt),
        updatedAt: timestampToISO(inv.updatedAt),
      }
    },
    enabled: Boolean(tenantSlug && invoiceId),
  })
}

/**
 * Fetches email audit log entries for an invoice.
 */
export function useInvoiceEmails(invoiceId: string | undefined) {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  return useQuery({
    queryKey: tenantKeys.invoiceEmails(tenantSlug ?? '', invoiceId ?? ''),
    queryFn: async (): Promise<InvoiceEmail[]> => {
      const response = await clients.billing.listInvoiceEmails({
        invoiceId: invoiceId ?? '',
        pagination: { pageSize: 100, pageToken: '' },
      })
      return (response.emails ?? []).map((email) => ({
        idempotencyKey: email.idempotencyKey ?? '',
        templateName: email.templateName ?? '',
        toAddresses: email.toAddresses ?? [],
        status: emailStatus(email.status),
        sentAt: email.sentAt ? timestampToISO(email.sentAt) : undefined,
        deliveredAt: email.deliveredAt ? timestampToISO(email.deliveredAt) : undefined,
        bounceReason: email.bounceReason || undefined,
      }))
    },
    enabled: Boolean(tenantSlug && invoiceId),
  })
}

/**
 * Resends the invoice email. Returns a mutation to trigger resend.
 */
export function useResendInvoiceEmail() {
  const clients = useApiClients()
  const queryClient = useQueryClient()
  const tenantSlug = useTenantSlug()

  return useMutation({
    mutationFn: async (invoiceId: string) => {
      const response = await clients.billing.resendInvoiceEmail({
        invoiceId,
        idempotencyKey: { key: generateIdempotencyKey() },
      })
      return response.email
    },
    onSuccess: (_, invoiceId) => {
      if (tenantSlug) {
        void queryClient.invalidateQueries({
          queryKey: tenantKeys.invoiceEmails(tenantSlug, invoiceId),
        })
      }
    },
  })
}

/**
 * Marks an invoice as paid.
 */
export function useMarkInvoicePaid() {
  const clients = useApiClients()
  const queryClient = useQueryClient()
  const tenantSlug = useTenantSlug()

  return useMutation({
    mutationFn: async (invoiceId: string) => {
      const response = await clients.billing.markInvoicePaid({
        invoiceId,
        idempotencyKey: { key: generateIdempotencyKey() },
      })
      return response.invoice
    },
    onSuccess: (_, invoiceId) => {
      if (tenantSlug) {
        void queryClient.invalidateQueries({
          queryKey: tenantKeys.invoice(tenantSlug, invoiceId),
        })
        void queryClient.invalidateQueries({
          queryKey: tenantKeys.invoices(tenantSlug),
        })
      }
    },
  })
}

/**
 * Voids an invoice and cancels pending email deliveries.
 */
export function useVoidInvoice() {
  const clients = useApiClients()
  const queryClient = useQueryClient()
  const tenantSlug = useTenantSlug()

  return useMutation({
    mutationFn: async (invoiceId: string) => {
      const response = await clients.billing.voidInvoice({
        invoiceId,
        idempotencyKey: { key: generateIdempotencyKey() },
      })
      return response.invoice
    },
    onSuccess: (_, invoiceId) => {
      if (tenantSlug) {
        void queryClient.invalidateQueries({
          queryKey: tenantKeys.invoice(tenantSlug, invoiceId),
        })
        void queryClient.invalidateQueries({
          queryKey: tenantKeys.invoices(tenantSlug),
        })
      }
    },
  })
}
