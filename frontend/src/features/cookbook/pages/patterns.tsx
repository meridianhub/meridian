import { Breadcrumbs } from '@/shared/breadcrumbs'
import { useCookbook } from '../hooks/use-cookbook'
import { useFilterState, applyFilters } from '../hooks/use-filter-state'
import { CatalogueGrid } from '../components/catalogue-grid'
import { FilterBar } from '../components/filter-bar'

export function CookbookPatternsPage() {
  const { items } = useCookbook()
  const patterns = items.filter((i) => i.type === 'registry:pattern')
  const [filters, setFilters] = useFilterState()
  const filtered = applyFilters(patterns, filters)
  const hasActiveFilters = !!(filters.search || filters.category || filters.industry)

  return (
    <div className="space-y-6">
      <Breadcrumbs items={[{ label: 'Cookbook', href: '/cookbook' }, { label: 'Patterns' }]} />
      <div>
        <h1 className="text-2xl font-semibold">Economy Patterns</h1>
        <p className="text-muted-foreground">Saga definitions, manifest fragments, and instrument configurations</p>
      </div>
      <FilterBar items={patterns} filters={filters} onFilterChange={setFilters} hideTypeFilter />
      <CatalogueGrid items={filtered} hasActiveFilters={hasActiveFilters} />
    </div>
  )
}
