import { Link } from 'react-router-dom'
import { BookOpen, Blocks, ChevronRight, GitBranch } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { useCookbook } from '../hooks/use-cookbook'

interface CookbookHubCardProps {
  title: string
  description: string
  count: number
  href: string
  icon: React.ReactNode
}

function CookbookHubCard({ title, description, count, href, icon }: CookbookHubCardProps) {
  return (
    <Link to={href} className="group block focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring rounded-lg">
      <Card className="h-full transition-colors group-hover:border-primary/50 group-focus-visible:border-primary/50">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle className="text-sm font-medium">{title}</CardTitle>
          <div className="text-muted-foreground">{icon}</div>
        </CardHeader>
        <CardContent>
          <div className="mb-2 text-2xl font-bold">{count}</div>
          <p className="text-xs text-muted-foreground">{description}</p>
          <div className="mt-4 flex items-center text-xs font-medium text-primary">
            View all
            <ChevronRight className="ml-1 h-3.5 w-3.5 transition-transform group-hover:translate-x-0.5" />
          </div>
        </CardContent>
      </Card>
    </Link>
  )
}

export function CookbookPage() {
  const { items } = useCookbook()
  const patternCount = items.filter((i) => i.type === 'registry:pattern').length
  const componentCount = items.filter((i) => i.type === 'registry:ui').length

  const cards: CookbookHubCardProps[] = [
    {
      title: 'Economy Patterns',
      description: 'Saga definitions, manifest fragments, and instrument configurations for common business scenarios.',
      count: patternCount,
      href: '/cookbook/patterns',
      icon: <BookOpen className="h-4 w-4" />,
    },
    {
      title: 'UI Components',
      description: 'Reusable interface components for building Meridian-powered applications.',
      count: componentCount,
      href: '/cookbook/components',
      icon: <Blocks className="h-4 w-4" />,
    },
    {
      title: 'Composition Graph',
      description: 'Visual dependency graph showing how patterns relate to each other.',
      count: items.length,
      href: '/cookbook/graph',
      icon: <GitBranch className="h-4 w-4" />,
    },
  ]

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-3xl font-bold tracking-tight">Cookbook</h1>
        <p className="mt-2 text-muted-foreground">
          Browse economy patterns, UI components, and composition graphs.
        </p>
      </div>
      <div className="grid gap-4 md:grid-cols-3">
        {cards.map((card) => (
          <CookbookHubCard key={card.href} {...card} />
        ))}
      </div>
    </div>
  )
}
