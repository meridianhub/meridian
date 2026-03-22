import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { CookbookPage } from './index'
import type { CookbookItem } from '../hooks/use-cookbook'

const mockUseCookbook = vi.fn<() => { items: CookbookItem[]; isLoading: boolean }>()
vi.mock('../hooks/use-cookbook', () => ({
  useCookbook: () => mockUseCookbook(),
}))

vi.mock('@/hooks/use-page-title', () => ({
  usePageTitle: vi.fn(),
}))

const patterns: CookbookItem[] = [
  { name: 'fiat-account', type: 'registry:pattern', title: 'Fiat Account' },
  { name: 'energy-trading', type: 'registry:pattern', title: 'Energy Trading' },
]

const components: CookbookItem[] = [
  { name: 'balance-card', type: 'registry:ui', title: 'Balance Card' },
]

function renderPage(items: CookbookItem[] = [...patterns, ...components]) {
  mockUseCookbook.mockReturnValue({ items, isLoading: false })
  return render(
    <MemoryRouter>
      <CookbookPage />
    </MemoryRouter>,
  )
}

describe('CookbookPage', () => {
  it('renders page heading', () => {
    renderPage()
    expect(screen.getByRole('heading', { name: 'Cookbook' })).toBeInTheDocument()
  })

  it('renders Economy Patterns card', () => {
    renderPage()
    expect(screen.getByText('Economy Patterns')).toBeInTheDocument()
  })

  it('renders UI Components card', () => {
    renderPage()
    expect(screen.getByText('UI Components')).toBeInTheDocument()
  })

  it('renders Composition Graph card', () => {
    renderPage()
    expect(screen.getByText('Composition Graph')).toBeInTheDocument()
  })

  it('shows pattern count', () => {
    renderPage()
    // 2 patterns - count appears in Economy Patterns card
    const patternCard = screen.getByText('Economy Patterns').closest('a')
    expect(patternCard).toBeInTheDocument()
    expect(patternCard?.textContent).toContain('2')
  })

  it('shows component count', () => {
    renderPage()
    const componentCard = screen.getByText('UI Components').closest('a')
    expect(componentCard).toBeInTheDocument()
    expect(componentCard?.textContent).toContain('1')
  })

  it('shows total item count in composition graph card', () => {
    renderPage()
    const graphCard = screen.getByText('Composition Graph').closest('a')
    expect(graphCard?.textContent).toContain('3')
  })

  it('links Economy Patterns card to /cookbook/patterns', () => {
    renderPage()
    const link = screen.getByText('Economy Patterns').closest('a')
    expect(link).toHaveAttribute('href', '/cookbook/patterns')
  })

  it('links UI Components card to /cookbook/components', () => {
    renderPage()
    const link = screen.getByText('UI Components').closest('a')
    expect(link).toHaveAttribute('href', '/cookbook/components')
  })

  it('links Composition Graph card to /cookbook/graph', () => {
    renderPage()
    const link = screen.getByText('Composition Graph').closest('a')
    expect(link).toHaveAttribute('href', '/cookbook/graph')
  })

  it('renders View all links on each card', () => {
    renderPage()
    const viewAllLinks = screen.getAllByText('View all')
    expect(viewAllLinks).toHaveLength(3)
  })

  it('shows zero counts when no items', () => {
    renderPage([])
    const patternCard = screen.getByText('Economy Patterns').closest('a')
    const componentCard = screen.getByText('UI Components').closest('a')
    const graphCard = screen.getByText('Composition Graph').closest('a')
    expect(patternCard?.textContent).toContain('0')
    expect(componentCard?.textContent).toContain('0')
    expect(graphCard?.textContent).toContain('0')
  })

  it('renders card descriptions', () => {
    renderPage()
    expect(screen.getByText(/Saga definitions, manifest fragments/)).toBeInTheDocument()
    expect(screen.getByText(/Reusable interface components/)).toBeInTheDocument()
    expect(screen.getByText(/Visual dependency graph/)).toBeInTheDocument()
  })
})
