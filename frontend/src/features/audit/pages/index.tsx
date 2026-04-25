import { useState, useCallback } from 'react'
import type { ColumnDef } from '@tanstack/react-table'
import { ClipboardList } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { DataTable, type DataTableQueryParams, type DataTableResult, type FilterConfig } from '@/shared/data-table'
import { TimeDisplay } from '@/shared/time-display'
import { JsonDiffViewer } from '@/shared/audit-trail'
import { PageShell, PageHeader } from '@/shared'
import { usePageTitle } from '@/hooks/use-page-title'
import { useApiClients } from '@/api/context'
import { AuditOperation as AuditOperationEnum } from '@/api/gen/meridian/audit/v1/audit_events_pb'
import { cn } from '@/lib/utils'

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export type AuditOperation = 'INSERT' | 'UPDATE' | 'DELETE' | 'UNKNOWN'

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

// ---------------------------------------------------------------------------
// Enum helpers
// ---------------------------------------------------------------------------

const OPERATION_NAMES: Record<number, AuditOperation> = {
  [AuditOperationEnum.INSERT]: 'INSERT',
  [AuditOperationEnum.UPDATE]: 'UPDATE',
  [AuditOperationEnum.DELETE]: 'DELETE',
}

const VALID_OPERATIONS = new Set<string>(['INSERT', 'UPDATE', 'DELETE', 'UNKNOWN'])

function toOperationName(op: unknown): AuditOperation {
  if (typeof op === 'string' && VALID_OPERATIONS.has(op)) return op as AuditOperation
  if (typeof op === 'number') return OPERATION_NAMES[op] ?? 'UNKNOWN'
  return 'UNKNOWN'
}

// ---------------------------------------------------------------------------
// Struct helpers
// ---------------------------------------------------------------------------

/** Convert a value to a plain object, or null if falsy.
 *  Connect-es deserializes google.protobuf.Struct to JsonObject (plain JS objects),
 *  so this is just a type narrowing helper. */
function toObject(value: unknown): object | null {
  if (!value || typeof value !== 'object') return null
  return value as object
}

// ---------------------------------------------------------------------------
// Operation Badge Component
// ---------------------------------------------------------------------------

const OPERATION_STYLES: Record<AuditOperation, string> = {
  INSERT: 'bg-success-muted text-success-foreground border-success/30',
  UPDATE: 'bg-info-muted text-info-foreground border-info/30',
  DELETE: 'bg-destructive/10 text-destructive border-destructive/30',
  UNKNOWN: 'bg-muted text-muted-foreground border-border',
}

function OperationBadge({ operation }: { operation: AuditOperation }) {
  const style = OPERATION_STYLES[operation] ?? 'bg-muted text-muted-foreground border-border'
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
        className="fixed inset-0 bg-overlay transition-opacity"
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
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
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
// Audit Empty State
// ---------------------------------------------------------------------------

function AuditEmptyState() {
  return (
    <div
      data-testid="empty-state"
      className="flex flex-col items-center justify-center gap-3 py-12 px-4 text-center"
    >
      <ClipboardList className="size-10 text-muted-foreground" />
      <div className="flex flex-col gap-1.5 max-w-sm">
        <p className="text-sm font-medium text-foreground">No audit events yet</p>
        <p className="text-sm text-muted-foreground">
          Audit entries appear here when you create parties, update accounts, or run sagas.
          Try creating a party to see your first event.
        </p>
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
  usePageTitle('Audit Log')
  const [selectedEntry, setSelectedEntry] = useState<AuditLogEntry | null>(null)
  const clients = useApiClients()

  const fetchAuditEntries = useCallback(async (params: DataTableQueryParams): Promise<DataTableResult<AuditLogEntry>> => {
    const operationFilter = params.filters?.operation
    const parsedOperation =
      operationFilter && operationFilter !== ''
        ? (AuditOperationEnum[operationFilter as keyof typeof AuditOperationEnum] ?? 0)
        : 0

    const response = await clients.audit.listAuditEntries({
      tableName: params.filters?.tableName ?? '',
      recordId: params.filters?.recordId ?? '',
      changedBy: params.filters?.changedBy ?? '',
      operation: parsedOperation as AuditOperationEnum,
      pageSize: params.pageSize,
      pageToken: params.pageToken ?? '',
    })

    const entries: AuditLogEntry[] = (response.entries ?? []).map((e) => ({
      entryId: e.entryId ?? '',
      timestamp: e.timestamp ?? null,
      tableName: e.tableName ?? '',
      operation: toOperationName(e.operation),
      recordId: e.recordId ?? '',
      changedBy: e.changedBy ?? '',
      oldValues: toObject(e.oldValues),
      newValues: toObject(e.newValues),
    }))

    return { items: entries, nextPageToken: response.nextPageToken || undefined }
  }, [clients])

  return (
    <PageShell>
      <PageHeader
        title="Audit Log"
        description="Browse and review all audit trail entries for your tenant"
      />

      <Card>
        <DataTable<AuditLogEntry>
          queryKey={['audit-log']}
          queryFn={fetchAuditEntries}
          columns={columns}
          filters={filters}
          pageSize={25}
          onRowClick={setSelectedEntry}
          emptyState={<AuditEmptyState />}
          className="w-full"
        />
      </Card>

      <AuditDetailPanel entry={selectedEntry} onClose={() => setSelectedEntry(null)} />
    </PageShell>
  )
}
