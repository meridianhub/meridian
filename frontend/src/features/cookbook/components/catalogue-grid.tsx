import { Link } from 'react-router-dom'
import { Blocks, BookOpen, SearchX } from 'lucide-react'
import { Card, CardHeader, CardTitle, CardDescription, CardContent, CardFooter } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Tooltip, TooltipTrigger, TooltipContent } from '@/components/ui/tooltip'
import { EmptyState } from '@/components/ui/empty-state'
import type { CookbookItem, PatternMeta, ComponentMeta } from '../hooks/use-cookbook'
import { derivePatternKind } from '../hooks/use-filter-state'

interface CatalogueGridProps {
  items: CookbookItem[]
  hasActiveFilters?: boolean
}

function isPatternMeta(meta: PatternMeta | ComponentMeta | undefined): meta is PatternMeta {
  return meta !== undefined && 'complexity' in meta
}

function complexityLabel(score: number): string {
  if (score <= 2) return 'Simple'
  if (score <= 4) return 'Low'
  if (score <= 6) return 'Moderate'
  if (score <= 8) return 'High'
  return 'Very High'
}

function ComplexityIndicator({ score }: { score: number }) {
  const label = complexityLabel(score)
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          aria-label={`Complexity: ${score} — ${label}`}
          className="flex items-center gap-1"
          data-testid="complexity-indicator"
        >
          {Array.from({ length: 5 }, (_, i) => (
            <div
              key={i}
              className={`size-1.5 rounded-full ${
                i < Math.ceil(score / 2) ? 'bg-primary' : 'bg-muted'
              }`}
            />
          ))}
        </span>
      </TooltipTrigger>
      <TooltipContent side="top" sideOffset={4}>Complexity: {score} — {label}</TooltipContent>
    </Tooltip>
  )
}

function CookbookCard({ item }: { item: CookbookItem }) {
  const isPattern = item.type === 'registry:pattern'
  const meta = item.meta
  const patternMeta = isPatternMeta(meta) ? meta : undefined

  return (
    <Link to={`/cookbook/${item.name}`} className="rounded-xl outline-none focus-visible:ring-ring/50 focus-visible:ring-[3px]">
    <Card
      className="h-full cursor-pointer transition-colors hover:border-primary/50"
    >
      <CardHeader>
        <div className="flex items-start justify-between gap-2">
          <div className="flex items-center gap-2">
            {isPattern ? (
              <BookOpen className="size-4 text-primary shrink-0" />
            ) : (
              <Blocks className="size-4 text-muted-foreground shrink-0" />
            )}
            <CardTitle className="text-sm">{item.title}</CardTitle>
          </div>
          <div className="flex items-center gap-1 shrink-0">
            {isPattern && (() => {
              const kind = derivePatternKind(item)
              return kind ? (
                <Badge variant="outline" className="text-[10px] capitalize">
                  {kind}
                </Badge>
              ) : null
            })()}
            <Badge variant={isPattern ? 'default' : 'secondary'} className="text-[10px]">
              {isPattern ? 'Pattern' : 'UI'}
            </Badge>
          </div>
        </div>
        {item.description && (
          <CardDescription className="line-clamp-2 text-xs">
            {item.description}
          </CardDescription>
        )}
      </CardHeader>

      {item.categories && item.categories.length > 0 && (
        <CardContent>
          <div className="flex flex-wrap gap-1">
            {item.categories.map((cat) => (
              <Badge key={cat} variant="outline" className="text-[10px]">
                {cat}
              </Badge>
            ))}
          </div>
        </CardContent>
      )}

      {patternMeta?.complexity !== undefined && (
        <CardFooter>
          <ComplexityIndicator score={patternMeta.complexity} />
        </CardFooter>
      )}
    </Card>
    </Link>
  )
}

export function CatalogueGrid({ items, hasActiveFilters }: CatalogueGridProps) {
  if (items.length === 0) {
    return (
      <EmptyState
        icon={hasActiveFilters ? SearchX : BookOpen}
        title={hasActiveFilters ? 'No matching items' : 'No cookbook entries yet'}
        description={
          hasActiveFilters
            ? 'Try adjusting your filters or search terms.'
            : 'Cookbook entries will appear here once patterns and components are registered.'
        }
      />
    )
  }

  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
      {items.map((item) => (
        <CookbookCard key={item.name} item={item} />
      ))}
    </div>
  )
}
