import { useQuery } from '@tanstack/react-query'
import { useApiClients } from '@/api/context'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import type { DataTableQueryParams, DataTableResult } from '@/shared/data-table'
import type { FinancialBookingLog, LedgerPosting } from '../pages/types'

function getStatusName(status: unknown): string {
  if (typeof status === 'string') return status
  if (typeof status === 'number') {
    const statusMap: Record<number, string> = {
      0: 'UNSPECIFIED',
      1: 'PENDING',
      2: 'POSTED',
      3: 'FAILED',
      4: 'CANCELLED',
      5: 'REVERSED',
    }
    return statusMap[status] ?? String(status)
  }
  return String(status ?? '')
}

function getDirectionName(direction: unknown): string {
  if (typeof direction === 'string') return direction
  if (typeof direction === 'number') {
    const dirMap: Record<number, string> = {
      0: 'UNSPECIFIED',
      1: 'DEBIT',
      2: 'CREDIT',
    }
    return dirMap[direction] ?? String(direction)
  }
  return String(direction ?? '')
}

function getInstrumentCode(value: unknown): string {
  if (typeof value === 'string' && value) return value
  return ''
}

/**
 * Fetches a paginated list of booking logs for use with DataTable.
 */
export function useBookingLogsTable() {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  const queryKey = tenantKeys.bookingLogs(tenantSlug ?? '')

  async function queryFn(
    params: DataTableQueryParams,
  ): Promise<DataTableResult<FinancialBookingLog>> {
    if (!tenantSlug) return { items: [] }

    const statusFilter = params.filters?.status

    const response = await clients.financialAccounting.listFinancialBookingLogs({
      pagination: { pageSize: params.pageSize, pageToken: params.pageToken ?? '' },
      ...(statusFilter !== undefined && { status: statusFilter as never }),
    })

    const items = (response.financialBookingLogs ?? []).map((log) => ({
      id: log.id,
      financialAccountType: String(log.financialAccountType ?? ''),
      productServiceReference: String(log.productServiceReference ?? ''),
      businessUnitReference: String(log.businessUnitReference ?? ''),
      chartOfAccountsRules: String(log.chartOfAccountsRules ?? ''),
      instrumentCode: getInstrumentCode(log.baseInstrumentCode),
      status: getStatusName(log.status),
      createdAt: log.createdAt ?? null,
      updatedAt: log.updatedAt ?? null,
      postings: (log.postings ?? []) as FinancialBookingLog['postings'],
    })) as FinancialBookingLog[]

    const nextPageToken =
      typeof response.pagination?.nextPageToken === 'string'
        ? response.pagination.nextPageToken
        : undefined

    return {
      items,
      nextPageToken: nextPageToken || undefined,
    }
  }

  return { queryKey, queryFn, tenantSlug }
}

/**
 * Fetches a single booking log by ID with its postings.
 */
export function useBookingLogDetail(bookingLogId: string | undefined) {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  return useQuery({
    queryKey: tenantKeys.bookingLog(tenantSlug ?? '', bookingLogId ?? ''),
    queryFn: async (): Promise<FinancialBookingLog | null> => {
      const response = await clients.financialAccounting.retrieveFinancialBookingLog({
        id: bookingLogId ?? '',
      })

      const log = response.financialBookingLog
      if (!log) return null

      const postings: LedgerPosting[] = (log.postings ?? []).map((p) => ({
        id: p.id,
        financialBookingLogId: p.financialBookingLogId,
        postingDirection: getDirectionName(p.postingDirection),
        postingAmount: {
          currencyCode: typeof p.postingAmount?.currencyCode === 'string'
            ? p.postingAmount.currencyCode
            : '',
          units: (() => {
            const u = p.postingAmount?.units
            return typeof u === 'bigint' ? u : typeof u === 'number' && Number.isSafeInteger(u) ? BigInt(u) : 0n
          })(),
          nanos: p.postingAmount?.nanos ?? 0,
        },
        accountId: p.accountId,
        valueDate: p.valueDate ?? null,
        postingResult: p.postingResult ?? '',
        createdAt: p.createdAt ?? null,
        status: getStatusName(p.status),
      }))

      return {
        id: log.id,
        financialAccountType: String(log.financialAccountType ?? ''),
        productServiceReference: String(log.productServiceReference ?? ''),
        businessUnitReference: String(log.businessUnitReference ?? ''),
        chartOfAccountsRules: String(log.chartOfAccountsRules ?? ''),
        instrumentCode: getInstrumentCode(log.baseInstrumentCode),
        status: getStatusName(log.status),
        createdAt: log.createdAt ?? null,
        updatedAt: log.updatedAt ?? null,
        postings,
      }
    },
    enabled: Boolean(tenantSlug && bookingLogId),
  })
}

/**
 * Fetches ledger postings for a specific account.
 */
export function useLedgerPostings(accountId: string | undefined) {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  return useQuery({
    queryKey: tenantKeys.ledgerPostings(tenantSlug ?? '', accountId ?? ''),
    queryFn: () =>
      clients.financialAccounting.listLedgerPostings({
        pagination: { pageSize: 50, pageToken: '' },
        accountId: accountId ?? '',
      }),
    enabled: Boolean(tenantSlug && accountId),
  })
}
