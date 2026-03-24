import * as React from 'react'
import { useQuery } from '@tanstack/react-query'
import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay } from '@/shared/time-display'
import { MoneyDisplay } from '@/shared/money-display'
import { EntityLink } from '@/shared/entity-link'
import { useClients } from '@/api/context'
import { useTenantSlug } from '@/hooks/use-tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { PartyType } from '@/api/gen/meridian/party/v1/party_pb'

interface TransactionsTabProps {
  partyId: string
  /** Party type passed from the parent page to avoid a duplicate fetch */
  partyType?: number | string
}

function getDirectionName(direction: unknown): string {
  if (typeof direction === 'string') return direction
  if (typeof direction === 'number') {
    const dirMap: Record<number, string> = { 0: 'UNSPECIFIED', 1: 'DEBIT', 2: 'CREDIT' }
    return dirMap[direction] ?? String(direction)
  }
  return String(direction ?? '')
}

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

export function TransactionsTab({ partyId, partyType }: TransactionsTabProps) {
  const clients = useClients()
  const tenantSlug = useTenantSlug()
  const [selectedAccount, setSelectedAccount] = React.useState<string>('')

  const isOrganization =
    partyType === PartyType.ORGANIZATION ||
    partyType === 'PARTY_TYPE_ORGANIZATION' ||
    partyType === 'ORGANIZATION'

  // Step 1: Resolve all account IDs for this party (paginate all pages)
  const accountIdsQuery = useQuery({
    queryKey: [...tenantKeys.party(tenantSlug ?? '', partyId), 'transaction-account-ids', isOrganization],
    queryFn: async (): Promise<string[]> => {
      const ids: string[] = []
      let pageToken = ''
      do {
        const resp = await clients.currentAccount.listCurrentAccounts({
          pageSize: 100,
          pageToken,
          ...(isOrganization ? { orgPartyId: partyId } : { partyId }),
        })
        for (const acct of resp.accounts ?? []) {
          ids.push(acct.accountId)
        }
        pageToken = resp.nextPageToken || ''
      } while (pageToken)
      return ids
    },
    enabled: Boolean(tenantSlug && partyId),
  })

  // Step 2: Fetch ledger postings for all resolved account IDs (batch by 100, paginate each batch)
  const postingsQuery = useQuery({
    queryKey: [...tenantKeys.party(tenantSlug ?? '', partyId), 'transactions', accountIdsQuery.data],
    queryFn: async () => {
      const accountIds = accountIdsQuery.data ?? []
      if (accountIds.length === 0) return []

      const allPostings: Awaited<ReturnType<typeof clients.financialAccounting.listLedgerPostings>>['ledgerPostings'] =
        []
      for (let i = 0; i < accountIds.length; i += 100) {
        const batch = accountIds.slice(i, i + 100)
        let pageToken = ''
        do {
          const resp = await clients.financialAccounting.listLedgerPostings({
            pagination: { pageSize: 100, pageToken },
            accountIds: batch,
          })
          allPostings.push(...(resp.ledgerPostings ?? []))
          pageToken = resp.pagination?.nextPageToken || ''
        } while (pageToken)
      }
      return allPostings
    },
    enabled: Boolean(tenantSlug && accountIdsQuery.data && accountIdsQuery.data.length > 0),
  })

  const isLoading = accountIdsQuery.isLoading || postingsQuery.isLoading
  const isError = accountIdsQuery.isError || postingsQuery.isError

  const allPostings = postingsQuery.data ?? []

  // Build account filter options from resolved account IDs
  const accountIds = accountIdsQuery.data ?? []

  // Filter displayed postings by selected account
  const displayedPostings = selectedAccount
    ? allPostings.filter((p) => p.accountId === selectedAccount)
    : allPostings

  return (
    <div className="space-y-4">
      {/* Account filter */}
      {accountIds.length > 1 && (
        <div className="flex items-center gap-2">
          <label htmlFor="account-filter" className="text-sm font-medium text-muted-foreground">
            Filter by account:
          </label>
          <select
            id="account-filter"
            value={selectedAccount}
            onChange={(e) => setSelectedAccount(e.target.value)}
            className="rounded-md border border-input bg-background px-3 py-1.5 text-sm shadow-sm focus:outline-none focus:ring-1 focus:ring-ring"
          >
            <option value="">All accounts</option>
            {accountIds.map((id) => (
              <option key={id} value={id}>
                {id}
              </option>
            ))}
          </select>
        </div>
      )}

      {isLoading && (
        <div className="animate-pulse space-y-2">
          {Array.from({ length: 5 }).map((_, i) => (
            <div key={i} className="h-8 rounded bg-muted" />
          ))}
        </div>
      )}

      {isError && (
        <p className="text-sm text-muted-foreground">Failed to load transactions.</p>
      )}

      {!isLoading && !isError && displayedPostings.length === 0 && (
        <p className="text-sm text-muted-foreground">No transactions found for this party.</p>
      )}

      {!isLoading && !isError && displayedPostings.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b text-left text-xs font-medium text-muted-foreground">
                <th className="pb-2 pr-4">Direction</th>
                <th className="pb-2 pr-4">Amount</th>
                <th className="pb-2 pr-4">Account</th>
                <th className="pb-2 pr-4">Status</th>
                <th className="pb-2">Created</th>
              </tr>
            </thead>
            <tbody>
              {displayedPostings.map((p) => (
                <tr key={p.id} className="border-b last:border-0">
                  <td className="py-2 pr-4">
                    <StatusBadge status={getDirectionName(p.postingDirection)} />
                  </td>
                  <td className="py-2 pr-4 tabular-nums">
                    <MoneyDisplay
                      amount={p.postingAmount?.units}
                      currency={p.postingAmount?.currencyCode ?? ''}
                    />
                  </td>
                  <td className="py-2 pr-4">
                    <EntityLink type="account" id={p.accountId} />
                  </td>
                  <td className="py-2 pr-4">
                    <StatusBadge status={getStatusName(p.status)} />
                  </td>
                  <td className="py-2">
                    <TimeDisplay timestamp={p.createdAt} format="relative" />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
