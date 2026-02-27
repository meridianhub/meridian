import { useState, useCallback } from 'react'
import type { ColumnDef } from '@tanstack/react-table'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { DataTable, type DataTableQueryParams, type DataTableResult, type FilterConfig } from '@/components/shared/data-table'
import { TimeDisplay } from '@/components/shared/time-display'
import { JsonDiffViewer } from '@/components/shared/audit-trail'
import { useAuthenticatedFetch } from '@/hooks/use-authenticated-fetch'
import { cn } from '@/lib/utils'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export type AuditOperation = 'INSERT' | 'UPDATE' | 'DELETE'

export interface AuditLogEntry {
  entryId: string
  timestamp: { seconds: bigint | number; nanos?: number } | null | undefined
  tableName: string
  operation: AuditOperation
  recordId: string
  changedBy: string
  oldValues: object | null
  newValues: object | null
}

export interface AuditLogResponse {
  entries: AuditLogEntry[]
  nextPageToken?: string
}

// ---------------------------------------------------------------------------
// Operation Badge Component
// ---------------------------------------------------------------------------

const OPERATION_STYLES: Record<AuditOperation, string> = {
  INSERT: 'bg-green-100 text-green-800 border-green-200',
  UPDATE: 'bg-blue-100 text-blue-800 border-blue-200',
  DELETE: 'bg-red-100 text-red-800 border-red-200',
}

function OperationBadge({ operation }: { operation: AuditOperation }) {
  const style = OPERATION_STYLES[operation] ?? 'bg-gray-100 text-gray-800 border-gray-200'
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium',
        style,
      )}
    >
      {operation}
    </span>
  )
}

// ---------------------------------------------------------------------------
// Detail Side Panel
// ---------------------------------------------------------------------------

interface AuditDetailPanelProps {
  entry: AuditLogEntry | null
  onClose: () => void
}

function AuditDetailPanel({ entry, onClose }: AuditDetailPanelProps) {
  if (!entry) return null

  return (
    <div className="fixed inset-0 z-50 overflow-y-auto">
      {/* Backdrop */}
      <div
        className="fixed inset-0 bg-black/50 transition-opacity"
        onClick={onClose}
        data-testid="detail-panel-backdrop"
      />

      {/* Panel */}
      <div className="flex justify-end min-h-screen">
        <div
          className="relative w-full max-w-2xl bg-background shadow-lg"
          onClick={(e) => e.stopPropagation()}
        >
          {/* Header */}
          <div className="sticky top-0 border-b bg-background/95 backdrop-blur px-6 py-4 flex items-center justify-between">
            <h2 className="text-lg font-semibold">Audit Entry Details</h2>
            <Button variant="ghost" size="sm" onClick={onClose}>
              ✕
            </Button>
          </div>

          {/* Content */}
          <div className="p-6 space-y-6">
            {/* Metadata */}
            <div className="grid grid-cols-2 gap-4">
              <div>
                <p className="text-sm font-medium text-muted-foreground">Table</p>
                <p className="mt-1 text-sm font-mono">{entry.tableName}</p>
              </div>
              <div>
                <p className="text-sm font-medium text-muted-foreground">Record ID</p>
                <p className="mt-1 text-sm font-mono">{entry.recordId}</p>
              </div>
              <div>
                <p className="text-sm font-medium text-muted-foreground">Operation</p>
                <p className="mt-1">
                  <OperationBadge operation={entry.operation} />
                </p>
              </div>
              <div>
                <p className="text-sm font-medium text-muted-foreground">Timestamp</p>
                <p className="mt-1 text-sm">
                  <TimeDisplay timestamp={entry.timestamp} format="absolute" />
                </p>
              </div>
              <div>
                <p className="text-sm font-medium text-muted-foreground">Changed By</p>
                <p className="mt-1 text-sm">{entry.changedBy}</p>
              </div>
            </div>

            {/* JSON Diff */}
            {(entry.oldValues !== null || entry.newValues !== null) && (
              <div className="border-t pt-6">
                <h3 className="text-sm font-semibold mb-3">Changes</h3>
                <JsonDiffViewer oldValue={entry.oldValues} newValue={entry.newValues} />
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Audit Log Page
// ---------------------------------------------------------------------------

const TABLE_OPTIONS = [
  { label: 'Current Account', value: 'current_account' },
  { label: 'Payment Order', value: 'payment_order' },
  { label: 'Financial Accounting', value: 'financial_accounting' },
  { label: 'Position Keeping', value: 'position_keeping' },
  { label: 'Account Reconciliation', value: 'account_reconciliation' },
  { label: 'Party', value: 'party' },
  { label: 'Reference Data', value: 'reference_data' },
  { label: 'Internal Account', value: 'internal_account' },
]

const OPERATION_OPTIONS = [
  { label: 'Insert', value: 'INSERT' },
  { label: 'Update', value: 'UPDATE' },
  { label: 'Delete', value: 'DELETE' },
]

const filters: FilterConfig[] = [
  {
    field: 'tableName',
    label: 'Table',
    type: 'select',
    options: TABLE_OPTIONS,
  },
  {
    field: 'operation',
    label: 'Operation',
    type: 'select',
    options: OPERATION_OPTIONS,
  },
  {
    field: 'changedBy',
    label: 'User',
    type: 'text',
  },
  {
    field: 'recordId',
    label: 'Record ID',
    type: 'text',
  },
]

const columns: ColumnDef<AuditLogEntry>[] = [
  {
    accessorKey: 'timestamp',
    header: 'Timestamp',
    cell: (row) => <TimeDisplay timestamp={row.row.original.timestamp} format="relative" />,
    size: 150,
  },
  {
    accessorKey: 'tableName',
    header: 'Table',
    size: 140,
  },
  {
    accessorKey: 'operation',
    header: 'Operation',
    cell: (row) => <OperationBadge operation={row.row.original.operation} />,
    size: 100,
  },
  {
    accessorKey: 'recordId',
    header: 'Record ID',
    size: 180,
  },
  {
    accessorKey: 'changedBy',
    header: 'Changed By',
    size: 140,
  },
]

export function AuditLogPage() {
  const [selectedEntry, setSelectedEntry] = useState<AuditLogEntry | null>(null)
  const authFetch = useAuthenticatedFetch()

  const fetchAuditEntries = useCallback(async (params: DataTableQueryParams): Promise<DataTableResult<AuditLogEntry>> => {
    const searchParams = new URLSearchParams()
    if (params.pageToken) searchParams.set('pageToken', params.pageToken)
    searchParams.set('pageSize', String(params.pageSize))
    if (params.filters) {
      Object.entries(params.filters).forEach(([key, value]) => {
        if (value) searchParams.set(key, value)
      })
    }
    const response = await authFetch(`/meridian.audit.v1.AuditService/ListAuditEntries?${searchParams}`, {
      method: 'POST',
    })
    if (!response.ok) {
      if (response.status === 501 || response.status === 503) {
        return { items: [], nextPageToken: undefined }
      }
      throw new Error(`Failed to fetch audit entries: ${response.status}`)
    }
    const data = (await response.json()) as AuditLogResponse
    return { items: data.entries ?? [], nextPageToken: data.nextPageToken }
  }, [authFetch])

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-3xl font-bold tracking-tight">Audit Log</h1>
        <p className="mt-2 text-muted-foreground">
          Browse and review all audit trail entries for your tenant
        </p>
      </div>

      <Card className="p-6">
        <DataTable<AuditLogEntry>
          queryKey={['audit-log']}
          queryFn={fetchAuditEntries}
          columns={columns}
          filters={filters}
          pageSize={25}
          onRowClick={setSelectedEntry}
          className="w-full"
        />
      </Card>

      <AuditDetailPanel entry={selectedEntry} onClose={() => setSelectedEntry(null)} />
    </div>
  )
}
