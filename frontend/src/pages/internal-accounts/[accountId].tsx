import * as React from 'react'
import { Link, useParams } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { ChevronLeftIcon } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Button } from '@/components/ui/button'
import { StatusBadge } from '@/components/shared/status-badge'
import { TimeDisplay } from '@/components/shared/time-display'
import { MoneyDisplay } from '@/components/shared/money-display'
import { AuditTrail } from '@/components/shared'
import { ConnectError, Code } from '@connectrpc/connect'
import { useApiClients } from '@/api/context'
import { useTenantContext } from '@/contexts/tenant-context'
import { tenantKeys } from '@/lib/query-keys'
import { ControlAction } from '@/api/gen/meridian/internal_account/v1/internal_account_pb'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type InternalAccountStatusLabel = 'ACTIVE' | 'SUSPENDED' | 'CLOSED' | 'UNKNOWN'

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

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function accountStatusLabel(status: number): InternalAccountStatusLabel {
  switch (status) {
    case 1: return 'ACTIVE'
    case 2: return 'SUSPENDED'
    case 3: return 'CLOSED'
    default: return 'UNKNOWN'
  }
}

function getDirectionName(direction: unknown): string {
  if (typeof direction === 'string') return direction
  if (typeof direction === 'number') {
    const dirMap: Record<number, string> = { 0: 'UNSPECIFIED', 1: 'DEBIT', 2: 'CREDIT' }
    return dirMap[direction] ?? String(direction)
  }
  return String(direction ?? '')
}

function getTransactionStatusName(status: unknown): string {
  if (typeof status === 'string') return status
  if (typeof status === 'number') {
    const statusMap: Record<number, string> = { 0: 'UNSPECIFIED', 1: 'PENDING', 2: 'POSTED', 3: 'FAILED', 4: 'CANCELLED', 5: 'REVERSED' }
    return statusMap[status] ?? String(status)
  }
  return String(status ?? '')
}

// ---------------------------------------------------------------------------
// Skeleton
// ---------------------------------------------------------------------------

function InternalAccountDetailSkeleton() {
  return (
    <div data-testid="internal-account-detail-skeleton" className="animate-pulse space-y-6 p-6">
      <div className="flex items-center gap-3">
        <div className="h-4 w-32 rounded bg-muted" />
      </div>
      <div className="h-8 w-64 rounded bg-muted" />
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        {Array.from({ length: 4 }).map((_, i) => (
          <div key={i} className="h-20 rounded bg-muted" />
        ))}
      </div>
      <div className="h-64 rounded bg-muted" />
    </div>
  )
}

// ---------------------------------------------------------------------------
// Not found
// ---------------------------------------------------------------------------

function InternalAccountNotFound() {
  return (
    <div data-testid="internal-account-not-found" className="p-6">
      <Link
        to="/internal-accounts"
        className="mb-4 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
        aria-label="Back to Internal Accounts"
      >
        <ChevronLeftIcon className="h-4 w-4" />
        Internal Accounts
      </Link>
      <div className="mt-8 text-center">
        <h2 className="text-xl font-semibold">Account not found</h2>
        <p className="mt-2 text-sm text-muted-foreground">
          The internal account you are looking for does not exist or has been removed.
        </p>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Action buttons
// ---------------------------------------------------------------------------

interface InternalAccountActionsProps {
  accountId: string
  accountStatus: number
  queryKey: readonly unknown[]
}

function InternalAccountActions({ accountId, accountStatus, queryKey }: InternalAccountActionsProps) {
  const clients = useApiClients()
  const queryClient = useQueryClient()
  const [isPending, setIsPending] = React.useState(false)

  const statusLabel = accountStatusLabel(accountStatus)

  const controlMutation = useMutation({
    mutationFn: (action: ControlAction) =>
      clients.internalAccount.controlInternalAccount({
        accountId,
        controlAction: action,
        reason: action === ControlAction.CONTROL_ACTION_SUSPEND ? 'Suspended by operator' : 'Reactivated by operator',
      }),
    onMutate: () => setIsPending(true),
    onSettled: () => setIsPending(false),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey })
    },
  })

  if (statusLabel === 'CLOSED' || statusLabel === 'UNKNOWN') {
    return null
  }

  return (
    <div className="flex gap-2">
      {statusLabel === 'ACTIVE' && (
        <Button
          variant="outline"
          size="sm"
          disabled={isPending}
          onClick={() => controlMutation.mutate(ControlAction.CONTROL_ACTION_SUSPEND)}
        >
          Suspend
        </Button>
      )}
      {statusLabel === 'SUSPENDED' && (
        <Button
          variant="outline"
          size="sm"
          disabled={isPending}
          onClick={() => controlMutation.mutate(ControlAction.CONTROL_ACTION_ACTIVATE)}
        >
          Reactivate
        </Button>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Detail field
// ---------------------------------------------------------------------------

function DetailField({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-xs font-medium text-muted-foreground">{label}</dt>
      <dd className="mt-1 text-sm font-medium">{children}</dd>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Transactions tab
// ---------------------------------------------------------------------------

function InternalAccountTransactions({ accountId, instrumentCode }: { accountId: string; instrumentCode: string }) {
  const { tenantSlug } = useTenantContext()
  const clients = useApiClients()

  const { data, isLoading, isError } = useQuery({
    queryKey: [...tenantKeys.internalAccount(tenantSlug ?? '', accountId), 'postings'],
    queryFn: () =>
      clients.financialAccounting.listLedgerPostings({
        pagination: { pageSize: 50, pageToken: '' },
        accountId,
      }),
    enabled: !!accountId,
  })

  const postings = data?.ledgerPostings ?? []

  return (
    <Card>
      <CardHeader>
        <CardTitle>Transactions</CardTitle>
      </CardHeader>
      <CardContent>
        {isLoading && (
          <div className="animate-pulse space-y-2">
            {Array.from({ length: 3 }).map((_, i) => (
              <div key={i} className="h-8 rounded bg-muted" />
            ))}
          </div>
        )}
        {isError && (
          <p className="text-sm text-muted-foreground">Failed to load transactions.</p>
        )}
        {!isLoading && !isError && postings.length === 0 && (
          <p className="text-sm text-muted-foreground">No transactions found for this account.</p>
        )}
        {!isLoading && !isError && postings.length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b text-left text-xs font-medium text-muted-foreground">
                  <th className="pb-2 pr-4">Direction</th>
                  <th className="pb-2 pr-4">Amount</th>
                  <th className="pb-2 pr-4">Status</th>
                  <th className="pb-2">Created</th>
                </tr>
              </thead>
              <tbody>
                {postings.map((p) => (
                  <tr key={p.id} className="border-b last:border-0">
                    <td className="py-2 pr-4">
                      <StatusBadge status={getDirectionName(p.postingDirection)} />
                    </td>
                    <td className="py-2 pr-4 tabular-nums">
                      <MoneyDisplay
                        amount={p.postingAmount?.units}
                        currency={p.postingAmount?.currencyCode ?? instrumentCode}
                      />
                    </td>
                    <td className="py-2 pr-4">
                      <StatusBadge status={getTransactionStatusName(p.status)} />
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
      </CardContent>
    </Card>
  )
}

// ---------------------------------------------------------------------------
// Main page
// ---------------------------------------------------------------------------

export function InternalAccountDetailPage() {
  const { accountId } = useParams<{ accountId: string }>()
  const { tenantSlug } = useTenantContext()
  const clients = useApiClients()

  const queryKey = tenantKeys.internalAccount(tenantSlug ?? '', accountId ?? '')

  const { data: account, isLoading, isError } = useQuery({
    queryKey,
    queryFn: async (): Promise<InternalAccount | null> => {
      try {
        const response = await clients.internalAccount.retrieveInternalAccount({ accountId: accountId ?? '' })
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
    enabled: !!accountId,
  })

  if (isLoading) {
    return <InternalAccountDetailSkeleton />
  }

  if (isError || account === null || account === undefined) {
    return <InternalAccountNotFound />
  }

  const statusLabel = accountStatusLabel(account.accountStatus)

  return (
    <div className="p-6">
      {/* Back navigation */}
      <Link
        to="/internal-accounts"
        className="mb-4 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
        aria-label="Back to Internal Accounts"
      >
        <ChevronLeftIcon className="h-4 w-4" />
        Internal Accounts
      </Link>

      {/* Page header */}
      <div className="mt-4 flex flex-wrap items-start justify-between gap-4">
        <div>
          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-semibold font-mono">{account.accountCode}</h1>
            <StatusBadge status={statusLabel} />
          </div>
          <p className="mt-1 text-sm text-muted-foreground">{account.name}</p>
        </div>
        <InternalAccountActions
          accountId={account.accountId}
          accountStatus={account.accountStatus}
          queryKey={queryKey}
        />
      </div>

      {/* Summary card */}
      <Card className="mt-6">
        <CardContent>
          <dl className="grid grid-cols-2 gap-4 pt-2 md:grid-cols-4">
            <DetailField label="Account Code">
              <span className="font-mono">{account.accountCode}</span>
            </DetailField>
            <DetailField label="Name">{account.name}</DetailField>
            <DetailField label="Type">{account.behaviorClass}</DetailField>
            <DetailField label="Instrument">
              <span className="font-mono">{account.instrumentCode}</span>
            </DetailField>
          </dl>
        </CardContent>
      </Card>

      {/* Tabs */}
      <div className="mt-6">
        <Tabs defaultValue="overview">
          <TabsList>
            <TabsTrigger value="overview">Overview</TabsTrigger>
            <TabsTrigger value="transactions">Transactions</TabsTrigger>
            <TabsTrigger value="audit">Audit Trail</TabsTrigger>
          </TabsList>

          <TabsContent value="overview" className="mt-4">
            <Card>
              <CardHeader>
                <CardTitle>Account Details</CardTitle>
              </CardHeader>
              <CardContent>
                <dl className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <DetailField label="Account ID">{account.accountId}</DetailField>
                  <DetailField label="Account Code">
                    <span className="font-mono">{account.accountCode}</span>
                  </DetailField>
                  <DetailField label="Name">{account.name}</DetailField>
                  <DetailField label="Type (Behavior Class)">{account.behaviorClass}</DetailField>
                  <DetailField label="Instrument">
                    <span className="font-mono">{account.instrumentCode}</span>
                  </DetailField>
                  <DetailField label="Status">
                    <StatusBadge status={statusLabel} />
                  </DetailField>
                  {account.description && (
                    <DetailField label="Description">{account.description}</DetailField>
                  )}
                  <DetailField label="Created">
                    <TimeDisplay timestamp={account.createdAt} format="both" />
                  </DetailField>
                  <DetailField label="Last Updated">
                    <TimeDisplay timestamp={account.updatedAt} format="both" />
                  </DetailField>
                </dl>
              </CardContent>
            </Card>
          </TabsContent>

          <TabsContent value="transactions" className="mt-4">
            <InternalAccountTransactions
              accountId={account.accountId}
              instrumentCode={account.instrumentCode}
            />
          </TabsContent>

          <TabsContent value="audit" className="mt-4">
            <Card>
              <CardHeader>
                <CardTitle>Audit Trail</CardTitle>
              </CardHeader>
              <CardContent>
                <AuditTrail entityType="InternalAccount" entityId={account.accountId} />
              </CardContent>
            </Card>
          </TabsContent>
        </Tabs>
      </div>
    </div>
  )
}
