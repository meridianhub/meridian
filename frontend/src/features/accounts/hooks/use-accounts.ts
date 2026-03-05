import { useQuery } from '@tanstack/react-query'
import { ConnectError, Code } from '@connectrpc/connect'
import { useApiClients } from '@/api/context'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { AccountStatus } from '@/api/gen/meridian/current_account/v1/current_account_pb'
import type { DataTableQueryParams, DataTableResult } from '@/shared/data-table'
import type { CurrentAccount, AccountStatus as AccountStatusType } from '../pages/types'

const ACCOUNT_STATUS_NAMES: Record<number, AccountStatusType> = {
  [AccountStatus.ACTIVE]: 'ACTIVE',
  [AccountStatus.FROZEN]: 'FROZEN',
  [AccountStatus.CLOSED]: 'CLOSED',
}

function toAccountStatus(status: number | undefined): AccountStatusType {
  return ACCOUNT_STATUS_NAMES[status ?? 0] ?? 'SUSPENDED'
}

/** Extract a display string from google.type.Money (units + nanos/1e9). */
function formatBalance(
  money:
    | { units?: bigint | number; nanos?: number; currencyCode?: string }
    | undefined
    | null,
): string | undefined {
  if (!money) return undefined
  const rawUnits =
    typeof money.units === 'bigint' ? money.units : BigInt(Math.trunc(money.units ?? 0))
  if (
    rawUnits > BigInt(Number.MAX_SAFE_INTEGER) ||
    rawUnits < BigInt(Number.MIN_SAFE_INTEGER)
  ) {
    return money.currencyCode
      ? `${money.currencyCode} ${rawUnits.toString()}`
      : rawUnits.toString()
  }
  const units = Number(rawUnits)
  const nanos = money.nanos ?? 0
  const value = units + nanos / 1e9
  if (money.currencyCode) {
    try {
      return new Intl.NumberFormat(undefined, {
        style: 'currency',
        currency: money.currencyCode,
      }).format(value)
    } catch {
      /* fall through for non-ISO codes */
    }
  }
  return value.toFixed(2)
}

/**
 * Fetches a paginated list of accounts for use with DataTable.
 * Returns the queryKey and queryFn ready to pass to DataTable.
 */
export function useAccountsTable() {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  const queryKey = tenantKeys.accounts(tenantSlug ?? '')

  async function queryFn(
    params: DataTableQueryParams,
  ): Promise<DataTableResult<CurrentAccount>> {
    if (!tenantSlug) return { items: [] }

    const statusFilter = params.filters?.status
    const parsedStatus =
      statusFilter !== undefined && statusFilter !== ''
        ? Number(statusFilter)
        : undefined
    const response = await clients.currentAccount.listCurrentAccounts({
      pageSize: params.pageSize,
      pageToken: params.pageToken ?? '',
      ...(parsedStatus !== undefined && Number.isInteger(parsedStatus) && parsedStatus >= 0
        ? { status: parsedStatus as AccountStatus }
        : {}),
    })

    const accounts: CurrentAccount[] = (response.accounts ?? []).map((a) => ({
      accountId: a.accountId ?? '',
      externalReference: a.externalIdentifier ?? '',
      status: toAccountStatus(a.accountStatus),
      instrumentCode: a.instrumentCode || '',
      availableBalance: '',
      createdAt: a.createdAt ?? undefined,
      updatedAt: a.updatedAt ?? undefined,
    }))

    return {
      items: accounts,
      nextPageToken: response.nextPageToken || undefined,
    }
  }

  return { queryKey, queryFn, tenantSlug }
}

/**
 * Fetches a single account by ID.
 */
export function useAccountDetail(accountId: string | undefined) {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  return useQuery({
    queryKey: tenantKeys.account(tenantSlug ?? '', accountId ?? ''),
    queryFn: async (): Promise<CurrentAccount | null> => {
      try {
        const response = await clients.currentAccount.retrieveCurrentAccount({
          accountId: accountId ?? '',
        })
        const f = response.facility
        if (!f) return null
        return {
          accountId: f.accountId,
          externalReference: f.externalIdentifier ?? '',
          status: toAccountStatus(f.accountStatus),
          instrumentCode: f.instrumentCode || '',
          availableBalance: formatBalance(f.currentBalance?.availableBalance?.amount) ?? '',
          createdAt: f.createdAt ?? undefined,
          updatedAt: f.updatedAt ?? undefined,
          partyId: f.orgPartyId || undefined,
        }
      } catch (err: unknown) {
        if (ConnectError.from(err).code === Code.NotFound) return null
        throw err
      }
    },
    enabled: Boolean(tenantSlug && accountId),
  })
}

/**
 * Fetches ledger postings (transactions) for an account.
 */
export function useAccountPostings(accountId: string | undefined) {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  return useQuery({
    queryKey: [...tenantKeys.account(tenantSlug ?? '', accountId ?? ''), 'postings'],
    queryFn: () =>
      clients.financialAccounting.listLedgerPostings({
        pagination: { pageSize: 50, pageToken: '' },
        accountId: accountId ?? '',
      }),
    enabled: Boolean(tenantSlug && accountId),
  })
}

/**
 * Fetches active amount blocks (liens) for an account.
 */
export function useAccountLiens(accountId: string | undefined) {
  const clients = useApiClients()
  const tenantSlug = useTenantSlug()

  return useQuery({
    queryKey: tenantKeys.liens(tenantSlug ?? '', accountId ?? ''),
    queryFn: () =>
      clients.currentAccount.getActiveAmountBlocks({ accountId: accountId ?? '' }),
    enabled: Boolean(tenantSlug && accountId),
  })
}
