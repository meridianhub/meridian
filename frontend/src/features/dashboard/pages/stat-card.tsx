import { type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'

interface StatCardProps {
  title: string
  value?: number
  description?: string
  icon?: ReactNode
  isLoading?: boolean
  error?: boolean
  showRecentQualifier?: boolean
  className?: string
  href?: string
}

export function StatCard({
  title,
  value,
  description,
  icon,
  isLoading,
  error,
  showRecentQualifier,
  className,
  href,
}: StatCardProps) {
  const cardContent = (
    <>
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <CardTitle className="text-sm font-medium">{title}</CardTitle>
        {icon && <div className="text-muted-foreground">{icon}</div>}
      </CardHeader>
      <CardContent>
        {isLoading ? (
          <div
            data-testid="stat-card-skeleton"
            className="h-8 w-24 animate-pulse rounded bg-muted"
          />
        ) : (
          <div className="text-2xl font-bold">
            {error ? (
              <span className="text-muted-foreground">—</span>
            ) : (
              <>
                {value ?? 0}
                {showRecentQualifier && (
                  <span className="ml-1 text-xs font-normal text-muted-foreground">recent</span>
                )}
              </>
            )}
          </div>
        )}
        {description && (
          <p className="mt-1 text-xs text-muted-foreground">{description}</p>
        )}
      </CardContent>
    </>
  )

  if (href) {
    return (
      <Link to={href} className="block">
        <Card
          className={cn(
            'transition-colors hover:bg-accent/50 cursor-pointer',
            className,
          )}
        >
          {cardContent}
        </Card>
      </Link>
    )
  }

  return <Card className={cn('', className)}>{cardContent}</Card>
}
