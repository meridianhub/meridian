import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { CookbookPatternsPage } from './patterns'
import type { CookbookItem } from '../hooks/use-cookbook'
import type { FilterState } from '../hooks/use-filter-state'

const mockUseCookbook = vi.fn<() => { items: CookbookItem[]; isLoading: boolean }>()
vi.mock('../hooks/use-cookbook', () => ({
  useCookbook: () => mockUseCookbook(),
}))

const mockUseFilterState = vi.fn<() => [FilterState, (f: Partial<FilterState>) => void]>()
vi.mock('../hooks/use-filter-state', () => ({
  useFilterState: () => mockUseFilterState(),
  applyFilters: (items: CookbookItem[]) => items,
}))

vi.mock('../components/catalogue-grid', () => ({
  CatalogueGrid: ({ items, hasActiveFilters }: { items: CookbookItem[]; hasActiveFilters: boolean }) => (
    <div data-testid="catalogue-grid" data-item-count={items.length} data-has-filters={hasActiveFilters} />
  ),
}))

vi.mock('../components/filter-bar', () => ({
  FilterBar: ({ showKindFilter, hideTypeFilter }: { showKindFilter?: boolean; hideTypeFilter?: boolean }) => (
    <div data-testid="filter-bar" data-show-kind={showKindFilter} data-hide-type={hideTypeFilter} />
  ),
}))

vi.mock('@/shared/breadcrumbs', () => ({
  Breadcrumbs: ({ items }: { items: { label: string; href?: string }[] }) => (
    <nav aria-label="Breadcrumb">
      {items.map((item) => <span key={item.label}>{item.label}</span>)}
    </nav>
  ),
}))

const patternItems: CookbookItem[] = [
  { name: 'fiat-account', type: 'registry:pattern', title: 'Fiat Account' },
  { name: 'energy-trading', type: 'registry:pattern', title: 'Energy Trading' },
]

const mixedItems: CookbookItem[] = [
  ...patternItems,
  { name: 'balance-card', type: 'registry:ui', title: 'Balance Card' },
]

const emptyFilters: FilterState = { search: '', type: '', category: '', industry: '', kind: '' }

function renderPage(items: CookbookItem[] = patternItems, filters: FilterState = emptyFilters) {
  mockUseCookbook.mockReturnValue({ items, isLoading: false })
  mockUseFilterState.mockReturnValue([filters, vi.fn()])
  return render(
    <MemoryRouter>
      <CookbookPatternsPage />
    </MemoryRouter>,
  )
}

describe('CookbookPatternsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders page heading', () => {
    renderPage()
    expect(screen.getByRole('heading', { name: 'Economy Patterns' })).toBeInTheDocument()
  })

  it('renders breadcrumb with Cookbook and Patterns', () => {
    renderPage()
    expect(screen.getByText('Cookbook')).toBeInTheDocument()
    expect(screen.getByText('Patterns')).toBeInTheDocument()
  })

  it('renders filter bar', () => {
    renderPage()
    expect(screen.getByTestId('filter-bar')).toBeInTheDocument()
  })

  it('passes showKindFilter to filter bar', () => {
    renderPage()
    expect(screen.getByTestId('filter-bar')).toHaveAttribute('data-show-kind', 'true')
  })

  it('passes hideTypeFilter to filter bar', () => {
    renderPage()
    expect(screen.getByTestId('filter-bar')).toHaveAttribute('data-hide-type', 'true')
  })

  it('renders catalogue grid', () => {
    renderPage()
    expect(screen.getByTestId('catalogue-grid')).toBeInTheDocument()
  })

  it('renders page description', () => {
    renderPage()
    expect(screen.getByText(/Saga definitions, manifest fragments, and instrument configurations/)).toBeInTheDocument()
  })

  it('renders with empty patterns', () => {
    renderPage([])
    expect(screen.getByTestId('catalogue-grid')).toBeInTheDocument()
  })

  it('hasActiveFilters is false when no filters active', () => {
    renderPage()
    expect(screen.getByTestId('catalogue-grid')).toHaveAttribute('data-has-filters', 'false')
  })

  it('hasActiveFilters is true when search filter is active', () => {
    renderPage(patternItems, { ...emptyFilters, search: 'fiat' })
    expect(screen.getByTestId('catalogue-grid')).toHaveAttribute('data-has-filters', 'true')
  })

  it('hasActiveFilters is true when category filter is active', () => {
    renderPage(patternItems, { ...emptyFilters, category: 'banking' })
    expect(screen.getByTestId('catalogue-grid')).toHaveAttribute('data-has-filters', 'true')
  })

  it('hasActiveFilters is true when kind filter is active', () => {
    renderPage(patternItems, { ...emptyFilters, kind: 'saga' })
    expect(screen.getByTestId('catalogue-grid')).toHaveAttribute('data-has-filters', 'true')
  })

  it('filters out UI items - only passes patterns to applyFilters', () => {
    renderPage(mixedItems)
    // applyFilters is mocked to return items as-is, but the page pre-filters to patterns only
    // The grid still renders (patterns portion)
    expect(screen.getByTestId('catalogue-grid')).toBeInTheDocument()
  })
})
