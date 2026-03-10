import { type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { AlertCircle, RefreshCw } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

interface StatCardProps {
  title: string
  value?: number
  description?: string
  icon?: ReactNode
  isLoading?: boolean
  error?: boolean
  onRetry?: () => void
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
  onRetry,
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
        ) : error ? (
          <div data-testid="stat-card-error" className="flex items-center gap-2">
            <AlertCircle className="h-4 w-4 text-destructive" />
            <span className="text-sm text-muted-foreground">Failed to load</span>
            {onRetry && (
              <Button
                variant="ghost"
                size="sm"
                className="ml-auto h-7 px-2"
                onClick={(e) => {
                  e.preventDefault()
                  onRetry()
                }}
              >
                <RefreshCw className="h-3 w-3" />
                <span className="sr-only">Retry {title}</span>
              </Button>
            )}
          </div>
        ) : (
          <div className="text-2xl font-bold">
            {value ?? 0}
            {showRecentQualifier && (
              <span className="ml-1 text-xs font-normal text-muted-foreground">recent</span>
            )}
          </div>
        )}
        {description && !error && (
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
