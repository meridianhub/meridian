import { useQuery } from '@tanstack/react-query'
import { ConnectError, Code } from '@connectrpc/connect'
import { useApiClients } from '@/api/context'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import type { DataTableQueryParams, DataTableResult } from '@/shared/data-table'

interface InternalAccountRow {
  accountId: string
  accountCode: string
  name: string
  behaviorClass: string
  accountStatus: number
  instrumentCode: string
  createdAt?: { seconds: bigint | number; nanos?: number } | null
}

interface InternalAccount {
  accountId: string
  accountCode: string
  name: string
  behaviorClass: string
  instrumentCode: string
  accountStatus: number
  description: string
  createdAt?: { seconds: bigint | number; nanos?: number } | null
  updatedAt?: { seconds: bigint | number; nanos?: number } | null
}

/**
 * Fetches a paginated list of internal accounts for use with DataTable.
 */
export function useInternalAccountsTable() {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  const queryKey = tenantKeys.internalAccounts(tenantSlug ?? '')

  async function queryFn(
    params: DataTableQueryParams,
  ): Promise<DataTableResult<InternalAccountRow>> {
    if (!tenantSlug) return { items: [] }

    const statusFilter = params.filters?.status ? parseInt(params.filters.status, 10) : 0

    const res = await clients.internalAccount.listInternalAccounts({
      behaviorClassFilter: params.filters?.behaviorClass ?? '',
      statusFilter,
      pagination: { pageToken: params.pageToken ?? '', pageSize: params.pageSize },
    })

    return {
      items: res.facilities.map((f) => ({
        accountId: f.accountId,
        accountCode: f.accountCode,
        name: f.name,
        behaviorClass: f.behaviorClass,
        accountStatus: f.accountStatus,
        instrumentCode: f.instrumentCode,
        createdAt: f.createdAt ?? null,
      })),
      nextPageToken: res.pagination?.nextPageToken || undefined,
    }
  }

  return { queryKey, queryFn, tenantSlug }
}

/**
 * Fetches a single internal account by ID.
 */
export function useInternalAccountDetail(accountId: string | undefined) {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  const queryKey = tenantKeys.internalAccount(tenantSlug ?? '', accountId ?? '')

  return useQuery({
    queryKey,
    queryFn: async (): Promise<InternalAccount | null> => {
      try {
        const response = await clients.internalAccount.retrieveInternalAccount({
          accountId: accountId ?? '',
        })
        const f = response.facility
        if (!f) return null
        return {
          accountId: f.accountId,
          accountCode: f.accountCode,
          name: f.name,
          behaviorClass: f.behaviorClass,
          instrumentCode: f.instrumentCode,
          accountStatus: f.accountStatus,
          description: f.description,
          createdAt: f.createdAt ?? null,
          updatedAt: f.updatedAt ?? null,
        }
      } catch (err: unknown) {
        if (ConnectError.from(err).code === Code.NotFound) return null
        throw err
      }
    },
    enabled: Boolean(tenantSlug && accountId),
  })
}
