import { useQuery } from '@tanstack/react-query';
import { ClockIcon } from 'lucide-react';
import { cn } from '@/lib/utils';
import { TimeDisplay } from './time-display';
import { EmptyState } from '@/components/ui/empty-state';
import { Button } from '@/components/ui/button';
import { useApiClients } from '@/api/context';
import { AuditOperation as AuditOperationEnum } from '@/api/gen/meridian/audit/v1/audit_events_pb';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export type AuditOperation = 'INSERT' | 'UPDATE' | 'DELETE';

export interface AuditEntry {
  entryId: string;
  operation: AuditOperation;
  changedBy: string;
  timestamp: { seconds: bigint | number; nanos?: number } | null | undefined;
  oldValues: object | null;
  newValues: object | null;
}

export interface AuditTrailProps {
  entityType: string;
  entityId: string;
}

// ---------------------------------------------------------------------------
// Enum helpers
// ---------------------------------------------------------------------------

const OPERATION_NAMES: Record<number, AuditOperation> = {
  [AuditOperationEnum.INSERT]: 'INSERT',
  [AuditOperationEnum.UPDATE]: 'UPDATE',
  [AuditOperationEnum.DELETE]: 'DELETE',
};

function toOperationName(op: unknown): AuditOperation {
  if (typeof op === 'string') return op as AuditOperation;
  if (typeof op === 'number') return OPERATION_NAMES[op] ?? 'UPDATE';
  return 'UPDATE';
}

// ---------------------------------------------------------------------------
// JsonDiffViewer
// ---------------------------------------------------------------------------

interface JsonDiffViewerProps {
  oldValue: object | null;
  newValue: object | null;
}

export function JsonDiffViewer({ oldValue, newValue }: JsonDiffViewerProps) {
  if (!oldValue && newValue) {
    // INSERT - show new values in green
    return (
      <pre
        data-testid="diff-inserted"
        className={cn(
          'rounded-md bg-green-50 p-3 text-xs text-green-800',
          'max-h-48 overflow-auto whitespace-pre-wrap break-all',
        )}
      >
        {JSON.stringify(newValue, null, 2)}
      </pre>
    );
  }

  if (oldValue && !newValue) {
    // DELETE - show old values in red
    return (
      <pre
        data-testid="diff-deleted"
        className={cn(
          'rounded-md bg-red-50 p-3 text-xs text-red-800',
          'max-h-48 overflow-auto whitespace-pre-wrap break-all',
        )}
      >
        {JSON.stringify(oldValue, null, 2)}
      </pre>
    );
  }

  if (oldValue && newValue) {
    // UPDATE - show side-by-side before/after
    return (
      <div className="grid grid-cols-2 gap-2">
        <div>
          <p className="mb-1 text-xs font-medium text-muted-foreground">Before</p>
          <pre
            data-testid="diff-before"
            className={cn(
              'rounded-md bg-red-50 p-3 text-xs text-red-800',
              'max-h-48 overflow-auto whitespace-pre-wrap break-all',
            )}
          >
            {JSON.stringify(oldValue, null, 2)}
          </pre>
        </div>
        <div>
          <p className="mb-1 text-xs font-medium text-muted-foreground">After</p>
          <pre
            data-testid="diff-after"
            className={cn(
              'rounded-md bg-green-50 p-3 text-xs text-green-800',
              'max-h-48 overflow-auto whitespace-pre-wrap break-all',
            )}
          >
            {JSON.stringify(newValue, null, 2)}
          </pre>
        </div>
      </div>
    );
  }

  // null/null — no changes to display
  return null;
}

// ---------------------------------------------------------------------------
// Operation badge
// ---------------------------------------------------------------------------

const OPERATION_STYLES: Record<AuditOperation, string> = {
  INSERT: 'bg-green-100 text-green-800 border-green-200',
  UPDATE: 'bg-blue-100 text-blue-800 border-blue-200',
  DELETE: 'bg-red-100 text-red-800 border-red-200',
};

function OperationBadge({ operation }: { operation: string }) {
  const style = OPERATION_STYLES[operation as AuditOperation] ?? 'bg-gray-100 text-gray-800 border-gray-200';
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium',
        style,
      )}
    >
      {operation}
    </span>
  );
}

// ---------------------------------------------------------------------------
// AuditTrailSkeleton
// ---------------------------------------------------------------------------

export function AuditTrailSkeleton() {
  return (
    <div data-testid="audit-trail-skeleton" className="space-y-4">
      {Array.from({ length: 3 }).map((_, i) => (
        <div key={i} className="flex gap-4">
          <div className="h-3 w-3 shrink-0 animate-pulse rounded-full bg-muted mt-1.5" />
          <div className="flex-1 space-y-2 rounded-lg border p-4">
            <div className="flex items-center gap-2">
              <div className="h-5 w-14 animate-pulse rounded-full bg-muted" />
              <div className="h-4 w-24 animate-pulse rounded bg-muted" />
              <div className="h-4 w-32 animate-pulse rounded bg-muted" />
            </div>
            <div className="h-16 w-full animate-pulse rounded-md bg-muted" />
          </div>
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// AuditEntry item
// ---------------------------------------------------------------------------

function AuditEntryItem({ entry }: { entry: AuditEntry }) {
  return (
    <div
      data-testid="audit-entry"
      className="flex gap-4"
    >
      {/* Timeline dot */}
      <div
        className="mt-5 h-2.5 w-2.5 shrink-0 rounded-full border-2 border-primary bg-background"
        aria-hidden="true"
      />

      <div className="flex-1 rounded-lg border p-4">
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <OperationBadge operation={entry.operation} />
          <span className="text-muted-foreground">by</span>
          <span className="font-medium">{entry.changedBy}</span>
          <span className="text-muted-foreground">
            <TimeDisplay timestamp={entry.timestamp} format="both" />
          </span>
        </div>

        {(entry.oldValues !== null || entry.newValues !== null) && (
          <div className="mt-3">
            <JsonDiffViewer oldValue={entry.oldValues} newValue={entry.newValues} />
          </div>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// AuditTrail (main component)
// ---------------------------------------------------------------------------

export function AuditTrail({ entityType, entityId }: AuditTrailProps) {
  const clients = useApiClients();

  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ['audit', entityType, entityId],
    queryFn: async () => {
      const response = await clients.audit.listAuditEntries({
        tableName: entityType,
        recordId: entityId,
        pageSize: 50,
      });
      return response;
    },
    staleTime: 0,
  });

  if (isLoading) {
    return <AuditTrailSkeleton />;
  }

  if (isError) {
    return (
      <div
        data-testid="audit-trail-error"
        className="rounded-lg border border-dashed p-6 text-center"
      >
        <p className="text-sm text-muted-foreground">
          Unable to load audit history.
        </p>
        <Button
          variant="outline"
          size="sm"
          className="mt-3"
          onClick={() => void refetch()}
        >
          Retry
        </Button>
      </div>
    );
  }

  const entries: AuditEntry[] = (data?.entries ?? []).map((e) => ({
    entryId: e.entryId,
    operation: toOperationName(e.operation),
    changedBy: e.changedBy,
    timestamp: e.timestamp ?? null,
    oldValues: e.oldValues?.fields ? (e.oldValues as unknown as object) : null,
    newValues: e.newValues?.fields ? (e.newValues as unknown as object) : null,
  }));

  if (entries.length === 0) {
    return (
      <div data-testid="audit-trail-empty">
        <EmptyState
          icon={ClockIcon}
          title="No audit history"
          description="No audit entries have been recorded for this entity yet."
        />
      </div>
    );
  }

  // Sort most-recent first
  const sorted = [...entries].sort((a, b) => {
    const aTime = typeof a.timestamp?.seconds === 'bigint'
      ? Number(a.timestamp.seconds)
      : (a.timestamp?.seconds ?? 0);
    const bTime = typeof b.timestamp?.seconds === 'bigint'
      ? Number(b.timestamp.seconds)
      : (b.timestamp?.seconds ?? 0);
    return bTime - aTime;
  });

  return (
    <div className="relative space-y-2">
      {/* Vertical timeline line */}
      <div
        className="absolute left-[5px] top-6 bottom-6 w-px bg-border"
        aria-hidden="true"
      />
      {sorted.map((entry) => (
        <AuditEntryItem key={entry.entryId} entry={entry} />
      ))}
    </div>
  );
}
