import { Breadcrumbs } from '@/shared/breadcrumbs'
import { useCookbook } from '../hooks/use-cookbook'
import { useFilterState, applyFilters } from '../hooks/use-filter-state'
import { CatalogueGrid } from '../components/catalogue-grid'
import { FilterBar } from '../components/filter-bar'

export function CookbookComponentsPage() {
  const { items } = useCookbook()
  const components = items.filter((i) => i.type === 'registry:ui')
  const [filters, setFilters] = useFilterState()
  const effectiveFilters = { ...filters, type: '' }
  const filtered = applyFilters(components, effectiveFilters)
  const hasActiveFilters = !!(filters.search || filters.category || filters.industry)

  return (
    <div className="space-y-6">
      <Breadcrumbs items={[{ label: 'Cookbook', href: '/cookbook' }, { label: 'UI Components' }]} />
      <div>
        <h1 className="text-2xl font-semibold">UI Components</h1>
        <p className="text-muted-foreground">Reusable interface components for Meridian-powered applications</p>
      </div>
      <FilterBar items={components} filters={filters} onFilterChange={setFilters} hideTypeFilter />
      <CatalogueGrid items={filtered} hasActiveFilters={hasActiveFilters} />
    </div>
  )
}
