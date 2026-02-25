import * as React from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { StatusBadge } from '@/components/shared/status-badge'
import { CELEditor } from '@/components/shared/cel-editor'
import {
  VarianceDetail,
  type Variance,
} from '@/components/reconciliation/variance-detail'
import { cn } from '@/lib/utils'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface ReconciliationRunDetail {
  runId: string
  accountId: string
  scope: string
  settlementType: string
  status: string
  varianceCount: number
  periodStart: string
  periodEnd: string
}

export type DisputeStatus = 'OPEN' | 'RESOLVED' | 'REJECTED'

export interface Dispute {
  disputeId: string
  varianceId: string
  status: DisputeStatus
  raisedBy: string
  raisedAt: string
  resolvedBy?: string
  resolvedAt?: string
  resolutionNotes?: string
}

export interface BalanceAssertion {
  assertionId: string
  name: string
  expression: string
  enabled: boolean
  lastResult?: 'PASS' | 'FAIL' | null
}

// ---------------------------------------------------------------------------
// API helpers
// ---------------------------------------------------------------------------

async function fetchRunDetail(runId: string): Promise<ReconciliationRunDetail> {
  const res = await fetch(`/v1/reconciliation/runs/${runId}`)
  if (!res.ok) throw new Error(`Failed to fetch run detail: ${res.status}`)
  return res.json() as Promise<ReconciliationRunDetail>
}

async function fetchVariances(runId: string): Promise<Variance[]> {
  const res = await fetch(`/v1/reconciliation/runs/${runId}/variances`)
  if (!res.ok) throw new Error(`Failed to fetch variances: ${res.status}`)
  const data = (await res.json()) as { variances: Variance[]; nextPageToken?: string; totalCount?: number }
  return data.variances ?? []
}

async function fetchDisputes(runId: string): Promise<Dispute[]> {
  const res = await fetch(`/v1/reconciliation/runs/${runId}/disputes`)
  if (!res.ok) throw new Error(`Failed to fetch disputes: ${res.status}`)
  const data = (await res.json()) as { items: Dispute[] }
  return data.items
}

async function updateDisputeStatus(
  runId: string,
  disputeId: string,
  status: DisputeStatus,
  resolutionNotes: string,
): Promise<void> {
  const res = await fetch(
    `/v1/reconciliation/runs/${runId}/disputes/${disputeId}`,
    {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ status, resolutionNotes }),
    },
  )
  if (!res.ok) throw new Error(`Failed to update dispute: ${res.status}`)
}

async function fetchBalanceAssertions(runId: string): Promise<BalanceAssertion[]> {
  const res = await fetch(`/v1/reconciliation/runs/${runId}/assertions`)
  if (!res.ok) throw new Error(`Failed to fetch assertions: ${res.status}`)
  const data = (await res.json()) as { items: BalanceAssertion[] }
  return data.items
}

async function saveBalanceAssertion(
  runId: string,
  assertion: { name: string; expression: string },
): Promise<void> {
  const res = await fetch(`/v1/reconciliation/runs/${runId}/assertions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(assertion),
  })
  if (!res.ok) throw new Error(`Failed to save assertion: ${res.status}`)
}

// ---------------------------------------------------------------------------
// Variances tab
// ---------------------------------------------------------------------------

function VariancesTab({ runId }: { runId: string }) {
  const { data: variances, isLoading, isError } = useQuery({
    queryKey: ['reconciliation-variances', runId],
    queryFn: () => fetchVariances(runId),
  })

  if (isLoading) {
    return (
      <div className="space-y-3">
        {[1, 2, 3].map((i) => (
          <div
            key={i}
            data-testid="variance-skeleton"
            className="h-24 animate-pulse rounded-lg bg-muted"
          />
        ))}
      </div>
    )
  }

  if (isError) {
    return (
      <div className="rounded-lg border border-destructive p-4 text-destructive text-sm">
        Failed to load variances.
      </div>
    )
  }

  if (!variances || variances.length === 0) {
    return (
      <div
        data-testid="variances-empty"
        className="rounded-lg border p-8 text-center text-sm text-muted-foreground"
      >
        No variances detected.
      </div>
    )
  }

  return (
    <div className="space-y-3">
      {variances.map((v) => (
        <VarianceDetail key={v.varianceId} variance={v} />
      ))}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Disputes tab
// ---------------------------------------------------------------------------

const DISPUTE_STATUS_TRANSITIONS: Record<DisputeStatus, DisputeStatus[]> = {
  OPEN: ['RESOLVED', 'REJECTED'],
  RESOLVED: [],
  REJECTED: [],
}

function DisputeCard({
  dispute,
  runId,
}: {
  dispute: Dispute
  runId: string
}) {
  const qc = useQueryClient()
  const [notes, setNotes] = React.useState('')
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const [_pendingStatus, setPendingStatus] = React.useState<DisputeStatus | null>(null)

  const mutation = useMutation({
    mutationFn: (status: DisputeStatus) =>
      updateDisputeStatus(runId, dispute.disputeId, status, notes),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['reconciliation-disputes', runId] })
      setPendingStatus(null)
      setNotes('')
    },
  })

  const transitions = DISPUTE_STATUS_TRANSITIONS[dispute.status]

  return (
    <div
      data-testid="dispute-card"
      className="rounded-lg border bg-card p-4 shadow-sm"
    >
      <div className="mb-2 flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <span className="font-mono text-xs text-muted-foreground">
            {dispute.disputeId}
          </span>
          <Badge
            variant={
              dispute.status === 'OPEN'
                ? 'default'
                : dispute.status === 'RESOLVED'
                  ? 'secondary'
                  : 'outline'
            }
          >
            {dispute.status}
          </Badge>
        </div>
        <span className="text-xs text-muted-foreground">
          Variance: {dispute.varianceId}
        </span>
      </div>

      <div className="text-sm text-muted-foreground mb-2">
        Raised by <strong>{dispute.raisedBy}</strong> on {dispute.raisedAt.slice(0, 10)}
      </div>

      {dispute.resolutionNotes && (
        <div className="mt-2 rounded-md bg-muted p-2 text-sm">
          {dispute.resolutionNotes}
        </div>
      )}

      {transitions.length > 0 && (
        <div className="mt-3 flex flex-col gap-2">
          <Input
            aria-label="Resolution notes"
            placeholder="Resolution notes…"
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
            className="text-sm"
          />
          <div className="flex gap-2">
            {transitions.map((status) => (
              <Button
                key={status}
                variant={status === 'RESOLVED' ? 'default' : 'outline'}
                size="sm"
                disabled={mutation.isPending}
                onClick={() => {
                  setPendingStatus(status)
                  mutation.mutate(status)
                }}
              >
                {status === 'RESOLVED' ? 'Resolve' : 'Reject'}
              </Button>
            ))}
          </div>
          {mutation.isError && (
            <p className="text-xs text-destructive">Failed to update dispute.</p>
          )}
        </div>
      )}
    </div>
  )
}

type DisputeFilter = 'ALL' | DisputeStatus

function DisputesTab({ runId }: { runId: string }) {
  const [filter, setFilter] = React.useState<DisputeFilter>('ALL')

  const { data: disputes, isLoading, isError } = useQuery({
    queryKey: ['reconciliation-disputes', runId],
    queryFn: () => fetchDisputes(runId),
  })

  const filtered = React.useMemo(() => {
    if (!disputes) return []
    if (filter === 'ALL') return disputes
    return disputes.filter((d) => d.status === filter)
  }, [disputes, filter])

  const filters: DisputeFilter[] = ['ALL', 'OPEN', 'RESOLVED', 'REJECTED']

  if (isLoading) {
    return (
      <div className="space-y-3">
        {[1, 2].map((i) => (
          <div
            key={i}
            data-testid="dispute-skeleton"
            className="h-20 animate-pulse rounded-lg bg-muted"
          />
        ))}
      </div>
    )
  }

  if (isError) {
    return (
      <div className="rounded-lg border border-destructive p-4 text-destructive text-sm">
        Failed to load disputes.
      </div>
    )
  }

  return (
    <div className="space-y-3">
      <div className="flex gap-2 flex-wrap" role="group" aria-label="Dispute status filter">
        {filters.map((f) => (
          <button
            key={f}
            onClick={() => setFilter(f)}
            className={cn(
              'rounded-md border px-3 py-1 text-sm transition-colors',
              filter === f
                ? 'bg-primary text-primary-foreground border-primary'
                : 'bg-background hover:bg-accent',
            )}
            aria-pressed={filter === f}
          >
            {f}
          </button>
        ))}
      </div>

      {filtered.length === 0 ? (
        <div
          data-testid="disputes-empty"
          className="rounded-lg border p-8 text-center text-sm text-muted-foreground"
        >
          No disputes.
        </div>
      ) : (
        filtered.map((d) => (
          <DisputeCard key={d.disputeId} dispute={d} runId={runId} />
        ))
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Balance Assertions tab (CEL editor)
// ---------------------------------------------------------------------------

function AssertionCard({ assertion }: { assertion: BalanceAssertion }) {
  return (
    <div
      data-testid="assertion-card"
      className="rounded-lg border bg-card p-4 shadow-sm"
    >
      <div className="mb-2 flex items-center justify-between gap-2">
        <span className="font-medium text-sm">{assertion.name}</span>
        <div className="flex items-center gap-2">
          {assertion.lastResult != null && (
            <Badge variant={assertion.lastResult === 'PASS' ? 'secondary' : 'destructive'}>
              {assertion.lastResult}
            </Badge>
          )}
          <Badge variant={assertion.enabled ? 'default' : 'outline'}>
            {assertion.enabled ? 'Enabled' : 'Disabled'}
          </Badge>
        </div>
      </div>
      <pre className="rounded-md bg-muted p-2 text-xs font-mono overflow-x-auto">
        {assertion.expression}
      </pre>
    </div>
  )
}

function BalanceAssertionsTab({ runId }: { runId: string }) {
  const qc = useQueryClient()
  const [name, setName] = React.useState('')
  const [expression, setExpression] = React.useState('')
  const [saveError, setSaveError] = React.useState<string | null>(null)

  const { data: assertions, isLoading, isError } = useQuery({
    queryKey: ['reconciliation-assertions', runId],
    queryFn: () => fetchBalanceAssertions(runId),
  })

  const saveMutation = useMutation({
    mutationFn: () => saveBalanceAssertion(runId, { name, expression }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['reconciliation-assertions', runId] })
      setName('')
      setExpression('')
      setSaveError(null)
    },
    onError: (err: Error) => {
      setSaveError(err.message)
    },
  })

  function handleSave(e: React.FormEvent) {
    e.preventDefault()
    if (!name.trim() || !expression.trim()) {
      setSaveError('Name and expression are required.')
      return
    }
    setSaveError(null)
    saveMutation.mutate()
  }

  if (isLoading) {
    return (
      <div className="space-y-3">
        {[1, 2].map((i) => (
          <div
            key={i}
            data-testid="assertion-skeleton"
            className="h-20 animate-pulse rounded-lg bg-muted"
          />
        ))}
      </div>
    )
  }

  if (isError) {
    return (
      <div className="rounded-lg border border-destructive p-4 text-destructive text-sm">
        Failed to load balance assertions.
      </div>
    )
  }

  return (
    <div className="space-y-4">
      {/* Existing assertions */}
      {assertions && assertions.length > 0 ? (
        <div className="space-y-3">
          {assertions.map((a) => (
            <AssertionCard key={a.assertionId} assertion={a} />
          ))}
        </div>
      ) : (
        <div
          data-testid="assertions-empty"
          className="rounded-lg border p-8 text-center text-sm text-muted-foreground"
        >
          No balance assertions defined.
        </div>
      )}

      {/* Add new assertion */}
      <div className="rounded-lg border p-4">
        <h3 className="mb-3 text-sm font-semibold">Add Balance Assertion</h3>
        <form onSubmit={handleSave} className="space-y-3" data-testid="assertion-form">
          <div>
            <label htmlFor="assertion-name" className="mb-1 block text-xs font-medium">
              Name
            </label>
            <Input
              id="assertion-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. Non-negative balance"
            />
          </div>
          <div>
            <label htmlFor="assertion-expression" className="mb-1 block text-xs font-medium">
              CEL Expression
            </label>
            <CELEditor
              value={expression}
              onChange={setExpression}
              context="validation"
            />
            <p className="mt-1 text-xs text-muted-foreground">
              Expression saved for audit trail. Assertions use strict equality (e.g. total debits == total credits).
            </p>
          </div>
          {saveError && (
            <p data-testid="assertion-error" className="text-xs text-destructive">
              {saveError}
            </p>
          )}
          <Button
            type="submit"
            size="sm"
            disabled={saveMutation.isPending}
            data-testid="save-assertion-btn"
          >
            {saveMutation.isPending ? 'Saving…' : 'Save Assertion'}
          </Button>
        </form>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// ReconciliationDetailPage
// ---------------------------------------------------------------------------

function formatDate(iso: string): string {
  if (!iso) return '—'
  return iso.slice(0, 10)
}

export function ReconciliationDetailPage() {
  const { runId } = useParams<{ runId: string }>()
  const navigate = useNavigate()

  const { data: run, isLoading, isError } = useQuery({
    queryKey: ['reconciliation-run', runId],
    queryFn: () => fetchRunDetail(runId!),
    enabled: !!runId,
  })

  if (!runId) return null

  if (isLoading) {
    return (
      <div className="p-6 space-y-4">
        <div className="h-8 w-64 animate-pulse rounded bg-muted" />
        <div className="h-4 w-96 animate-pulse rounded bg-muted" />
      </div>
    )
  }

  if (isError || !run) {
    return (
      <div className="p-6">
        <p className="text-destructive text-sm">Failed to load reconciliation run.</p>
      </div>
    )
  }

  return (
    <div className="p-6">
      {/* Header */}
      <div className="mb-6">
        <button
          onClick={() => void navigate('/reconciliation')}
          className="mb-2 text-sm text-muted-foreground hover:text-foreground transition-colors"
          aria-label="Back to reconciliation list"
        >
          ← Reconciliation
        </button>
        <div className="flex items-center gap-3">
          <h1 className="text-2xl font-semibold font-mono">{run.runId}</h1>
          <StatusBadge status={run.status} />
          {run.varianceCount > 0 && (
            <Badge variant="destructive">{run.varianceCount} variances</Badge>
          )}
        </div>
        <div className="mt-1 flex flex-wrap gap-4 text-sm text-muted-foreground">
          <span>Account: <strong className="text-foreground font-mono">{run.accountId}</strong></span>
          <span>Scope: <strong className="text-foreground">{run.scope}</strong></span>
          <span>Settlement: <strong className="text-foreground">{run.settlementType}</strong></span>
          <span>
            Period: <strong className="text-foreground">
              {formatDate(run.periodStart)} – {formatDate(run.periodEnd)}
            </strong>
          </span>
        </div>
      </div>

      {/* Tabs */}
      <Tabs defaultValue="variances">
        <TabsList>
          <TabsTrigger value="variances">Variances</TabsTrigger>
          <TabsTrigger value="disputes">Disputes</TabsTrigger>
          <TabsTrigger value="assertions">Balance Assertions</TabsTrigger>
        </TabsList>

        <TabsContent value="variances" className="mt-4">
          <VariancesTab runId={runId} />
        </TabsContent>

        <TabsContent value="disputes" className="mt-4">
          <DisputesTab runId={runId} />
        </TabsContent>

        <TabsContent value="assertions" className="mt-4">
          <BalanceAssertionsTab runId={runId} />
        </TabsContent>
      </Tabs>
    </div>
  )
}
