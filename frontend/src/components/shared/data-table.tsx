import * as React from 'react'
import {
  flexRender,
  getCoreRowModel,
  useReactTable,
  type ColumnDef,
  type QueryKey,
} from '@tanstack/react-table'
import { useQuery } from '@tanstack/react-query'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

export interface FilterOption {
  label: string
  value: string
}

export interface FilterConfig {
  key: string
  label: string
  type: 'text' | 'select'
  options?: FilterOption[]
}

export interface DataTableQueryParams {
  pageToken?: string
  pageSize: number
  filters?: Record<string, string>
}

export interface DataTableResult<T> {
  items: T[]
  nextPageToken?: string
}

export interface DataTableProps<T> {
  queryKey: QueryKey
  queryFn: (params: DataTableQueryParams) => Promise<DataTableResult<T>>
  columns: ColumnDef<T>[]
  pageSize?: number
  filters?: FilterConfig[]
  onRowClick?: (row: T) => void
  className?: string
}

function SkeletonRow({ colCount }: { colCount: number }) {
  return (
    <TableRow data-testid="skeleton-row">
      {Array.from({ length: colCount }).map((_, i) => (
        <TableCell key={i}>
          <div className="h-4 w-full animate-pulse rounded bg-muted" />
        </TableCell>
      ))}
    </TableRow>
  )
}

function EmptyState() {
  return (
    <TableRow>
      <TableCell colSpan={99} className="h-32 text-center">
        <div data-testid="empty-state" className="flex flex-col items-center gap-2 text-muted-foreground">
          <span className="text-sm font-medium">No results</span>
          <span className="text-xs">Try adjusting your filters.</span>
        </div>
      </TableCell>
    </TableRow>
  )
}

function ErrorState({ onRetry }: { onRetry: () => void }) {
  return (
    <TableRow>
      <TableCell colSpan={99} className="h-32 text-center">
        <div className="flex flex-col items-center gap-3 text-destructive">
          <span className="text-sm font-medium">Failed to load data</span>
          <Button variant="outline" size="sm" onClick={onRetry}>
            Retry
          </Button>
        </div>
      </TableCell>
    </TableRow>
  )
}

function FilterBar({
  filters,
  values,
  onChange,
}: {
  filters: FilterConfig[]
  values: Record<string, string>
  onChange: (key: string, value: string) => void
}) {
  return (
    <div className="flex flex-wrap gap-2 pb-3">
      {filters.map((f) => {
        if (f.type === 'select' && f.options) {
          return (
            <div key={f.key} className="flex flex-col gap-1">
              <label htmlFor={`filter-${f.key}`} className="sr-only">
                {f.label}
              </label>
              <select
                id={`filter-${f.key}`}
                aria-label={f.label}
                role="combobox"
                value={values[f.key] ?? ''}
                onChange={(e) => onChange(f.key, e.target.value)}
                className={cn(
                  'h-9 min-w-[140px] rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs outline-none',
                  'focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]',
                )}
              >
                <option value="">All {f.label}</option>
                {f.options.map((opt) => (
                  <option key={opt.value} value={opt.value}>
                    {opt.label}
                  </option>
                ))}
              </select>
            </div>
          )
        }

        return (
          <Input
            key={f.key}
            placeholder={`Filter by ${f.label}`}
            value={values[f.key] ?? ''}
            onChange={(e) => onChange(f.key, e.target.value)}
            className="h-9 w-[200px]"
          />
        )
      })}
    </div>
  )
}

function PaginationBar({
  hasNext,
  hasPrev,
  onNext,
  onPrev,
}: {
  hasNext: boolean
  hasPrev: boolean
  onNext: () => void
  onPrev: () => void
}) {
  if (!hasNext && !hasPrev) return null

  return (
    <div className="flex items-center justify-end gap-2 pt-3">
      {hasPrev && (
        <Button variant="outline" size="sm" onClick={onPrev}>
          Previous
        </Button>
      )}
      {hasNext && (
        <Button variant="outline" size="sm" onClick={onNext}>
          Next
        </Button>
      )}
    </div>
  )
}

export function DataTable<T>({
  queryKey,
  queryFn,
  columns,
  pageSize = 25,
  filters,
  onRowClick,
  className,
}: DataTableProps<T>) {
  const [activeFilters, setActiveFilters] = React.useState<Record<string, string>>({})
  const [pageToken, setPageToken] = React.useState<string | undefined>(undefined)
  const [hasPrev, setHasPrev] = React.useState(false)

  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: [...(queryKey as unknown[]), { pageToken, ...activeFilters }],
    queryFn: () => queryFn({ pageToken, pageSize, filters: activeFilters }),
  })

  const table = useReactTable({
    data: data?.items ?? [],
    columns,
    getCoreRowModel: getCoreRowModel(),
    manualPagination: true,
  })

  function handleFilterChange(key: string, value: string) {
    setActiveFilters((prev) => {
      const next = { ...prev }
      if (value) {
        next[key] = value
      } else {
        delete next[key]
      }
      return next
    })
    // Reset pagination on filter change
    setPageToken(undefined)
    setHasPrev(false)
  }

  function handleNextPage() {
    if (data?.nextPageToken) {
      setPageToken(data.nextPageToken)
      setHasPrev(true)
    }
  }

  function handlePrevPage() {
    setPageToken(undefined)
    setHasPrev(false)
  }

  const rows = table.getRowModel().rows

  return (
    <div className={cn('w-full', className)}>
      {filters && filters.length > 0 && (
        <FilterBar filters={filters} values={activeFilters} onChange={handleFilterChange} />
      )}

      <Table>
        <TableHeader>
          {table.getHeaderGroups().map((headerGroup) => (
            <TableRow key={headerGroup.id}>
              {headerGroup.headers.map((header) => (
                <TableHead key={header.id}>
                  {header.isPlaceholder
                    ? null
                    : flexRender(header.column.columnDef.header, header.getContext())}
                </TableHead>
              ))}
            </TableRow>
          ))}
        </TableHeader>

        <TableBody>
          {isLoading ? (
            Array.from({ length: pageSize }).map((_, i) => (
              <SkeletonRow key={i} colCount={columns.length} />
            ))
          ) : isError ? (
            <ErrorState onRetry={() => void refetch()} />
          ) : rows.length === 0 ? (
            <EmptyState />
          ) : (
            rows.map((row) => (
              <TableRow
                key={row.id}
                onClick={() => onRowClick?.(row.original)}
                className={onRowClick ? 'cursor-pointer' : undefined}
              >
                {row.getVisibleCells().map((cell) => (
                  <TableCell key={cell.id}>
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </TableCell>
                ))}
              </TableRow>
            ))
          )}
        </TableBody>
      </Table>

      <PaginationBar
        hasNext={!!data?.nextPageToken}
        hasPrev={hasPrev}
        onNext={handleNextPage}
        onPrev={handlePrevPage}
      />
    </div>
  )
}
