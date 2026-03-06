import { Search, X } from 'lucide-react'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import type { CookbookItem, PatternMeta } from '../hooks/use-cookbook'
import type { FilterState } from '../hooks/use-filter-state'

function getUniqueCategories(items: CookbookItem[]): string[] {
  const set = new Set<string>()
  for (const item of items) {
    for (const cat of item.categories ?? []) {
      set.add(cat)
    }
  }
  return Array.from(set).sort()
}

function getUniqueIndustries(items: CookbookItem[]): string[] {
  const set = new Set<string>()
  for (const item of items) {
    const meta = item.meta as PatternMeta | undefined
    for (const ind of meta?.industries ?? []) {
      set.add(ind)
    }
  }
  return Array.from(set).sort()
}

interface FilterBarProps {
  items: CookbookItem[]
  filters: FilterState
  onFilterChange: (patch: Partial<FilterState>) => void
  hideTypeFilter?: boolean
}

export function FilterBar({ items, filters, onFilterChange, hideTypeFilter }: FilterBarProps) {
  const categories = getUniqueCategories(items)
  const industries = getUniqueIndustries(items)
  const hasActiveFilters = filters.search || filters.type || filters.category || filters.industry

  return (
    <div className="space-y-3">
      <div className="relative">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 size-4 text-muted-foreground" />
        <Input
          placeholder="Search patterns and components..."
          value={filters.search}
          onChange={(e) => onFilterChange({ search: e.target.value })}
          className="pl-9"
        />
      </div>

      <div className="flex flex-wrap gap-2">
        {!hideTypeFilter && (
          <FilterChipGroup
            label="Type"
            options={[
              { value: 'pattern', label: 'Patterns' },
              { value: 'ui', label: 'UI Components' },
            ]}
            value={filters.type}
            onChange={(value) => onFilterChange({ type: value })}
          />
        )}

        {categories.length > 0 && (
          <FilterChipGroup
            label="Category"
            options={categories.map((c) => ({ value: c, label: c }))}
            value={filters.category}
            onChange={(value) => onFilterChange({ category: value })}
          />
        )}

        {industries.length > 0 && (
          <FilterChipGroup
            label="Industry"
            options={industries.map((i) => ({ value: i, label: i }))}
            value={filters.industry}
            onChange={(value) => onFilterChange({ industry: value })}
          />
        )}

        {hasActiveFilters && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => onFilterChange({ search: '', type: '', category: '', industry: '' })}
            className="h-7 text-xs"
          >
            <X className="size-3 mr-1" />
            Clear
          </Button>
        )}
      </div>
    </div>
  )
}

interface FilterChipGroupProps {
  label: string
  options: { value: string; label: string }[]
  value: string
  onChange: (value: string) => void
}

function FilterChipGroup({ label, options, value, onChange }: FilterChipGroupProps) {
  return (
    <div className="flex items-center gap-1" role="group" aria-label={`${label} filter`}>
      <span className="text-xs text-muted-foreground mr-1">{label}:</span>
      {options.map((opt) => (
        <button
          key={opt.value}
          type="button"
          aria-pressed={value === opt.value}
          onClick={() => onChange(value === opt.value ? '' : opt.value)}
        >
          <Badge
            variant={value === opt.value ? 'default' : 'outline'}
            className="cursor-pointer text-xs"
          >
            {opt.label}
          </Badge>
        </button>
      ))}
    </div>
  )
}
