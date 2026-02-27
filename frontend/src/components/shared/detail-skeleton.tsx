import * as React from 'react'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

export interface DetailSkeletonProps {
  /** Number of stat/summary fields to show in the top grid */
  fieldCount?: number
  /** Number of tabs to show */
  tabCount?: number
  /** Whether to show a back-nav skeleton */
  showBackNav?: boolean
  className?: string
}

/**
 * Reusable full-page loading skeleton for detail pages.
 * Renders a back-nav placeholder, title, stats grid, and optional tabs.
 */
export function DetailSkeleton({
  fieldCount = 4,
  tabCount = 3,
  showBackNav = true,
  className,
}: DetailSkeletonProps) {
  return (
    <div
      data-testid="detail-skeleton"
      className={cn('animate-pulse space-y-6 p-6', className)}
    >
      {showBackNav && (
        <Skeleton className="h-4 w-24" />
      )}

      {/* Title + badge */}
      <div className="flex items-center gap-3">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-6 w-20 rounded-full" />
      </div>

      {/* Summary stats grid */}
      <div className={cn(
        'grid gap-4',
        fieldCount <= 2 ? 'grid-cols-2' : 'grid-cols-2 md:grid-cols-4'
      )}>
        {Array.from({ length: fieldCount }).map((_, i) => (
          <Skeleton key={i} className="h-20 rounded-lg" />
        ))}
      </div>

      {/* Tabs bar */}
      {tabCount > 0 && (
        <div className="flex gap-2">
          {Array.from({ length: tabCount }).map((_, i) => (
            <Skeleton key={i} className="h-9 w-24 rounded-md" />
          ))}
        </div>
      )}

      {/* Tab content area */}
      <Skeleton className="h-64 rounded-lg" />
    </div>
  )
}
