import { Link } from 'react-router-dom'
import { StatusBadge } from '@/shared/status-badge'
import { TimeDisplay } from '@/shared/time-display'
import { cn } from '@/lib/utils'

export interface ActivityItem {
  id: string
  type: 'payment' | 'account' | 'reconciliation' | 'saga' | 'system'
  title: string
  description?: string
  timestamp: { seconds: bigint | number; nanos?: number } | null | undefined
  status?: string
  href?: string
}

interface ActivityFeedProps {
  items: ActivityItem[]
  isLoading?: boolean
  className?: string
}

function ActivitySkeleton() {
  return (
    <div data-testid="activity-skeleton" className="flex items-start gap-3 py-3">
      <div className="mt-1 h-2 w-2 animate-pulse rounded-full bg-muted" />
      <div className="flex-1 space-y-1">
        <div className="h-4 w-48 animate-pulse rounded bg-muted" />
        <div className="h-3 w-32 animate-pulse rounded bg-muted" />
      </div>
      <div className="h-3 w-16 animate-pulse rounded bg-muted" />
    </div>
  )
}

const TYPE_COLORS: Record<ActivityItem['type'], string> = {
  payment: 'bg-info',
  account: 'bg-success',
  reconciliation: 'bg-warning',
  saga: 'bg-primary',
  system: 'bg-muted-foreground',
}

export function ActivityFeed({ items, isLoading, className }: ActivityFeedProps) {
  if (isLoading) {
    return (
      <div className={cn('divide-y', className)}>
        {Array.from({ length: 5 }).map((_, i) => (
          <ActivitySkeleton key={i} />
        ))}
      </div>
    )
  }

  if (items.length === 0) {
    return (
      <div className={cn('py-8 text-center text-sm text-muted-foreground', className)}>
        No recent activity
      </div>
    )
  }

  return (
    <div className={cn('divide-y', className)}>
      {items.map((item) => {
        const itemContent = (
          <>
            <div
              role="img"
              aria-label={item.type}
              className={cn(
                'mt-2 h-2 w-2 flex-shrink-0 rounded-full',
                TYPE_COLORS[item.type] ?? 'bg-muted-foreground',
              )}
            />
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2">
                <span className="truncate text-sm font-medium">{item.title}</span>
                {item.status && <StatusBadge status={item.status} />}
              </div>
              {item.description && (
                <p className="mt-0.5 truncate text-xs text-muted-foreground">{item.description}</p>
              )}
            </div>
            <div className="flex-shrink-0 text-xs text-muted-foreground">
              <TimeDisplay timestamp={item.timestamp} format="relative" />
            </div>
          </>
        )

        if (item.href) {
          return (
            <Link
              key={item.id}
              to={item.href}
              className="flex items-start gap-3 py-3 transition-colors hover:bg-accent/50 rounded-sm"
            >
              {itemContent}
            </Link>
          )
        }

        return (
          <div key={item.id} className="flex items-start gap-3 py-3">
            {itemContent}
          </div>
        )
      })}
    </div>
  )
}
