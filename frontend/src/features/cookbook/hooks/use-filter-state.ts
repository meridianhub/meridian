import { useSearchParams } from 'react-router-dom'
import type { CookbookItem, PatternMeta } from './use-cookbook'

export interface FilterState {
  search: string
  type: string
  category: string
  industry: string
}

export function useFilterState(): [FilterState, (patch: Partial<FilterState>) => void] {
  const [searchParams, setSearchParams] = useSearchParams()

  const state: FilterState = {
    search: searchParams.get('search') ?? '',
    type: searchParams.get('type') ?? '',
    category: searchParams.get('category') ?? '',
    industry: searchParams.get('industry') ?? '',
  }

  function update(patch: Partial<FilterState>) {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      for (const [key, value] of Object.entries(patch)) {
        if (value) {
          next.set(key, value)
        } else {
          next.delete(key)
        }
      }
      return next
    })
  }

  return [state, update]
}

export function applyFilters(items: CookbookItem[], filters: FilterState): CookbookItem[] {
  return items.filter((item) => {
    if (filters.type) {
      const typeMap: Record<string, string> = { pattern: 'registry:pattern', ui: 'registry:ui' }
      const typeLabel = typeMap[filters.type]
      if (!typeLabel || item.type !== typeLabel) return false
    }

    if (filters.category) {
      if (!item.categories?.includes(filters.category)) return false
    }

    if (filters.industry) {
      const meta = item.meta as PatternMeta | undefined
      if (!meta?.industries?.includes(filters.industry)) return false
    }

    if (filters.search) {
      const q = filters.search.toLowerCase()
      const haystack = [item.name, item.title, item.description ?? ''].join(' ').toLowerCase()
      if (!haystack.includes(q)) return false
    }

    return true
  })
}
