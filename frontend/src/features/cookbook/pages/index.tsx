import { useCookbook } from '../hooks/use-cookbook'
import { useFilterState, applyFilters } from '../hooks/use-filter-state'
import { CatalogueGrid } from '../components/catalogue-grid'
import { FilterBar } from '../components/filter-bar'

export function CookbookPage() {
  const { items } = useCookbook()
  const [filters, setFilters] = useFilterState()
  const filtered = applyFilters(items, filters)
  const hasActiveFilters = !!(filters.search || filters.type || filters.category || filters.industry)

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">Cookbook</h1>
        <p className="text-muted-foreground">Browse economy patterns and UI components</p>
      </div>
      <FilterBar items={items} filters={filters} onFilterChange={setFilters} />
      <CatalogueGrid items={filtered} hasActiveFilters={hasActiveFilters} />
    </div>
  )
}
