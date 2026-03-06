import { useNavigate } from 'react-router-dom'
import { Blocks, BookOpen, SearchX } from 'lucide-react'
import { Card, CardHeader, CardTitle, CardDescription, CardContent, CardFooter } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { EmptyState } from '@/components/ui/empty-state'
import type { CookbookItem, PatternMeta, ComponentMeta } from '../hooks/use-cookbook'

interface CatalogueGridProps {
  items: CookbookItem[]
  hasActiveFilters?: boolean
}

function isPatternMeta(meta: PatternMeta | ComponentMeta | undefined): meta is PatternMeta {
  return meta !== undefined && 'complexity' in meta
}

function ComplexityIndicator({ score }: { score: number }) {
  return (
    <div className="flex items-center gap-1" title={`Complexity: ${score}/10`}>
      {Array.from({ length: 5 }, (_, i) => (
        <div
          key={i}
          className={`size-1.5 rounded-full ${
            i < Math.ceil(score / 2) ? 'bg-primary' : 'bg-muted'
          }`}
        />
      ))}
    </div>
  )
}

function CookbookCard({ item }: { item: CookbookItem }) {
  const navigate = useNavigate()
  const isPattern = item.type === 'registry:pattern'
  const meta = item.meta
  const patternMeta = isPatternMeta(meta) ? meta : undefined

  return (
    <Card
      role="link"
      tabIndex={0}
      className="cursor-pointer transition-colors hover:border-primary/50 focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px] outline-none"
      onClick={() => navigate(`/cookbook/${item.name}`)}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          navigate(`/cookbook/${item.name}`)
        }
      }}
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
          <Badge variant={isPattern ? 'default' : 'secondary'} className="text-[10px] shrink-0">
            {isPattern ? 'Pattern' : 'UI'}
          </Badge>
        </div>
        {item.description && (
          <CardDescription className="line-clamp-2 text-xs">
            {item.description}
          </CardDescription>
        )}
      </CardHeader>

      {(item.categories?.length || patternMeta?.complexity) && (
        <CardContent>
          <div className="flex flex-wrap gap-1">
            {item.categories?.map((cat) => (
              <Badge key={cat} variant="outline" className="text-[10px]">
                {cat}
              </Badge>
            ))}
          </div>
        </CardContent>
      )}

      {patternMeta?.complexity !== undefined && (
        <CardFooter className="justify-between">
          <ComplexityIndicator score={patternMeta.complexity} />
          {patternMeta.design_pattern && (
            <span className="text-[10px] text-muted-foreground">{patternMeta.design_pattern}</span>
          )}
        </CardFooter>
      )}
    </Card>
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
