import { useMemo, useState } from 'react'
import type { ColumnDef } from '@tanstack/react-table'
import type { DiffAction } from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'
import { StatusBadge } from '@/shared/status-badge'

const CHANGE_LABELS: Record<string, string> = {
  CREATE: 'Added',
  UPDATE: 'Modified',
  DELETE: 'Removed',
  NO_CHANGE: 'Unchanged',
}

const columns: ColumnDef<DiffAction>[] = [
  {
    accessorKey: 'resourceType',
    header: 'Resource Type',
    cell: ({ row }) => (
      <span className="font-medium capitalize">
        {row.original.resourceType.replace(/_/g, ' ')}
      </span>
    ),
  },
  {
    accessorKey: 'resourceCode',
    header: 'Resource ID',
    cell: ({ row }) => (
      <code className="text-sm bg-muted px-1.5 py-0.5 rounded">
        {row.original.resourceCode}
      </code>
    ),
  },
  {
    accessorKey: 'action',
    header: 'Change Type',
    cell: ({ row }) => {
      const label = CHANGE_LABELS[row.original.action] ?? row.original.action
      return <StatusBadge status={label} />
    },
  },
  {
    accessorKey: 'description',
    header: 'Description',
    cell: ({ row }) => (
      <span className="text-sm text-muted-foreground">
        {row.original.description || '\u2014'}
      </span>
    ),
  },
  {
    accessorKey: 'breaking',
    header: 'Breaking',
    cell: ({ row }) =>
      row.original.breaking ? (
        <span className="text-sm font-medium text-destructive">Yes</span>
      ) : (
        <span className="text-sm text-muted-foreground">No</span>
      ),
  },
]

export interface ManifestDiffTableProps {
  actions: DiffAction[]
  summary?: {
    totalActions: number
    creates: number
    updates: number
    deletes: number
    noChanges: number
    hasBreakingChanges: boolean
  }
}

export function ManifestDiffTable({ actions, summary }: ManifestDiffTableProps) {
  const [resourceTypeFilter, setResourceTypeFilter] = useState<string>('all')
  const [changeTypeFilter, setChangeTypeFilter] = useState<string>('all')

  const resourceTypes = useMemo(() => {
    const types = new Set(actions.map((a) => a.resourceType))
    return Array.from(types).sort()
  }, [actions])

  const changeTypes = useMemo(() => {
    const types = new Set(actions.map((a) => a.action))
    return Array.from(types).sort()
  }, [actions])

  const filteredActions = useMemo(() => {
    return actions.filter((a) => {
      if (resourceTypeFilter !== 'all' && a.resourceType !== resourceTypeFilter) return false
      if (changeTypeFilter !== 'all' && a.action !== changeTypeFilter) return false
      return true
    })
  }, [actions, resourceTypeFilter, changeTypeFilter])

  return (
    <div data-testid="manifest-diff-table">
      {summary && (
        <div className="flex gap-4 pb-4 text-sm" data-testid="diff-summary">
          <span>
            Total: <strong>{summary.totalActions}</strong>
          </span>
          {summary.creates > 0 && (
            <span className="text-green-600">
              +{summary.creates} added
            </span>
          )}
          {summary.updates > 0 && (
            <span className="text-yellow-600">
              ~{summary.updates} modified
            </span>
          )}
          {summary.deletes > 0 && (
            <span className="text-red-600">
              -{summary.deletes} removed
            </span>
          )}
          {summary.noChanges > 0 && (
            <span className="text-muted-foreground">
              {summary.noChanges} unchanged
            </span>
          )}
          {summary.hasBreakingChanges && (
            <span className="font-medium text-destructive" data-testid="breaking-warning">
              Breaking changes detected
            </span>
          )}
        </div>
      )}

      <div className="flex gap-3 pb-4" data-testid="diff-filters">
        <select
          value={resourceTypeFilter}
          onChange={(e) => setResourceTypeFilter(e.target.value)}
          className="rounded-md border border-input bg-background px-3 py-2 text-sm"
          data-testid="resource-type-filter"
          aria-label="Filter by resource type"
        >
          <option value="all">All resource types</option>
          {resourceTypes.map((type) => (
            <option key={type} value={type}>
              {type.replace(/_/g, ' ')}
            </option>
          ))}
        </select>

        <select
          value={changeTypeFilter}
          onChange={(e) => setChangeTypeFilter(e.target.value)}
          className="rounded-md border border-input bg-background px-3 py-2 text-sm"
          data-testid="change-type-filter"
          aria-label="Filter by change type"
        >
          <option value="all">All changes</option>
          {changeTypes.map((type) => (
            <option key={type} value={type}>
              {CHANGE_LABELS[type] ?? type}
            </option>
          ))}
        </select>
      </div>

      <div className="rounded-md border">
        <table className="w-full">
          <thead>
            <tr className="border-b bg-muted/50">
              {columns.map((col) => (
                <th
                  key={col.accessorKey as string}
                  className="px-4 py-3 text-left text-sm font-medium text-muted-foreground"
                >
                  {col.header as string}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {filteredActions.length === 0 ? (
              <tr>
                <td
                  colSpan={columns.length}
                  className="px-4 py-8 text-center text-sm text-muted-foreground"
                  data-testid="empty-state"
                >
                  No changes match the selected filters.
                </td>
              </tr>
            ) : (
              filteredActions.map((action, idx) => (
                <tr
                  key={`${action.resourceType}-${action.resourceCode}-${idx}`}
                  className="border-b last:border-b-0 hover:bg-muted/50"
                  data-testid={`diff-row-${action.resourceType}-${action.resourceCode}`}
                >
                  {columns.map((col) => {
                    const CellFn = col.cell
                    return (
                      <td key={col.accessorKey as string} className="px-4 py-3 text-sm">
                        {typeof CellFn === 'function'
                          ? CellFn({
                              row: { original: action },
                              getValue: () =>
                                action[col.accessorKey as keyof DiffAction],
                            } as Parameters<typeof CellFn>[0])
                          : String(action[col.accessorKey as keyof DiffAction] ?? '')}
                      </td>
                    )
                  })}
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
