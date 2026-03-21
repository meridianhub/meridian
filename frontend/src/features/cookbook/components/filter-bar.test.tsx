import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { FilterBar } from './filter-bar'
import type { CookbookItem, PatternMeta } from '../hooks/use-cookbook'
import type { FilterState } from '../hooks/use-filter-state'

function makeItem(overrides: Partial<CookbookItem> = {}): CookbookItem {
  return {
    name: 'test-item',
    type: 'registry:pattern',
    title: 'Test Item',
    ...overrides,
  }
}

const emptyFilters: FilterState = { search: '', type: '', category: '', industry: '', kind: '' }

const items: CookbookItem[] = [
  makeItem({
    name: 'banking',
    title: 'Banking Pattern',
    categories: ['finance', 'banking'],
    meta: { industries: ['banking', 'fintech'] } as PatternMeta,
  }),
  makeItem({
    name: 'energy',
    title: 'Energy',
    type: 'registry:ui',
    categories: ['energy'],
    meta: { industries: ['utilities'] } as PatternMeta,
  }),
]

describe('FilterBar', () => {
  it('renders search input', () => {
    render(
      <FilterBar items={items} filters={emptyFilters} onFilterChange={vi.fn()} />,
    )

    expect(screen.getByPlaceholderText('Search patterns and components...')).toBeDefined()
  })

  it('renders type filter chips', () => {
    render(
      <FilterBar items={items} filters={emptyFilters} onFilterChange={vi.fn()} />,
    )

    expect(screen.getByText('Patterns')).toBeDefined()
    expect(screen.getByText('UI Components')).toBeDefined()
  })

  it('hides type filter when hideTypeFilter is true', () => {
    render(
      <FilterBar items={items} filters={emptyFilters} onFilterChange={vi.fn()} hideTypeFilter />,
    )

    expect(screen.queryByText('Patterns')).toBeNull()
    expect(screen.queryByText('UI Components')).toBeNull()
  })

  it('renders category chips from items', () => {
    render(
      <FilterBar items={items} filters={emptyFilters} onFilterChange={vi.fn()} />,
    )

    // Categories include: banking, energy, finance (sorted)
    const categoryGroup = screen.getByRole('group', { name: 'Category filter' })
    expect(categoryGroup).toBeDefined()
    expect(screen.getByText('energy')).toBeDefined()
    expect(screen.getByText('finance')).toBeDefined()
  })

  it('renders industry chips from items', () => {
    render(
      <FilterBar items={items} filters={emptyFilters} onFilterChange={vi.fn()} />,
    )

    // Industries include: banking, fintech, utilities
    const industryGroup = screen.getByRole('group', { name: 'Industry filter' })
    expect(industryGroup).toBeDefined()
    expect(screen.getByText('fintech')).toBeDefined()
    expect(screen.getByText('utilities')).toBeDefined()
  })

  it('calls onFilterChange when search input changes', async () => {
    const user = userEvent.setup()
    const onFilterChange = vi.fn()
    render(
      <FilterBar items={items} filters={emptyFilters} onFilterChange={onFilterChange} />,
    )

    const input = screen.getByPlaceholderText('Search patterns and components...')
    await user.type(input, 'a')

    expect(onFilterChange).toHaveBeenCalledWith({ search: 'a' })
  })

  it('calls onFilterChange when type chip is clicked', async () => {
    const user = userEvent.setup()
    const onFilterChange = vi.fn()
    render(
      <FilterBar items={items} filters={emptyFilters} onFilterChange={onFilterChange} />,
    )

    await user.click(screen.getByText('Patterns'))

    expect(onFilterChange).toHaveBeenCalledWith({ type: 'pattern' })
  })

  it('deselects type chip when already selected', async () => {
    const user = userEvent.setup()
    const onFilterChange = vi.fn()
    render(
      <FilterBar
        items={items}
        filters={{ ...emptyFilters, type: 'pattern' }}
        onFilterChange={onFilterChange}
      />,
    )

    await user.click(screen.getByText('Patterns'))

    expect(onFilterChange).toHaveBeenCalledWith({ type: '' })
  })

  it('shows Clear button when filters are active', () => {
    render(
      <FilterBar
        items={items}
        filters={{ ...emptyFilters, search: 'test' }}
        onFilterChange={vi.fn()}
      />,
    )

    expect(screen.getByText('Clear')).toBeDefined()
  })

  it('does not show Clear button when no filters are active', () => {
    render(
      <FilterBar items={items} filters={emptyFilters} onFilterChange={vi.fn()} />,
    )

    expect(screen.queryByText('Clear')).toBeNull()
  })

  it('clears all filters when Clear button is clicked', async () => {
    const user = userEvent.setup()
    const onFilterChange = vi.fn()
    render(
      <FilterBar
        items={items}
        filters={{ ...emptyFilters, search: 'test', type: 'pattern' }}
        onFilterChange={onFilterChange}
      />,
    )

    await user.click(screen.getByText('Clear'))

    expect(onFilterChange).toHaveBeenCalledWith({
      search: '',
      type: '',
      category: '',
      industry: '',
      kind: '',
    })
  })

  it('renders kind filter chips when showKindFilter is true and items have kinds', () => {
    const patternItems: CookbookItem[] = [
      makeItem({
        name: 'p1',
        type: 'registry:pattern',
        meta: { provides: { sagas: ['s1'] } } as PatternMeta,
      }),
      makeItem({
        name: 'p2',
        type: 'registry:pattern',
        categories: ['gateway'],
      }),
    ]

    render(
      <FilterBar
        items={patternItems}
        filters={emptyFilters}
        onFilterChange={vi.fn()}
        showKindFilter
      />,
    )

    expect(screen.getByText('Economy')).toBeDefined()
    expect(screen.getByText('Integration')).toBeDefined()
  })

  it('does not show kind filter when showKindFilter is false', () => {
    const patternItems: CookbookItem[] = [
      makeItem({
        name: 'p1',
        type: 'registry:pattern',
        meta: { provides: { sagas: ['s1'] } } as PatternMeta,
      }),
    ]

    render(
      <FilterBar
        items={patternItems}
        filters={emptyFilters}
        onFilterChange={vi.fn()}
      />,
    )

    expect(screen.queryByText('Economy')).toBeNull()
  })

  it('sets aria-pressed on selected filter chip', () => {
    render(
      <FilterBar
        items={items}
        filters={{ ...emptyFilters, type: 'pattern' }}
        onFilterChange={vi.fn()}
      />,
    )

    const button = screen.getByText('Patterns').closest('button')
    expect(button?.getAttribute('aria-pressed')).toBe('true')

    const uiButton = screen.getByText('UI Components').closest('button')
    expect(uiButton?.getAttribute('aria-pressed')).toBe('false')
  })
})
