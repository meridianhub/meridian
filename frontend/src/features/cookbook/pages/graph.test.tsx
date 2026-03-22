import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { CookbookGraphPage } from './graph'
import type { CookbookItem } from '../hooks/use-cookbook'

const mockUseCookbook = vi.fn<() => { items: CookbookItem[]; isLoading: boolean }>()
vi.mock('../hooks/use-cookbook', () => ({
  useCookbook: () => mockUseCookbook(),
}))

vi.mock('../components/composition-graph', () => ({
  CompositionGraph: ({ patterns, className }: { patterns: CookbookItem[]; className?: string }) => (
    <div data-testid="composition-graph" data-pattern-count={patterns.length} className={className} />
  ),
}))

const patterns: CookbookItem[] = [
  { name: 'fiat-account', type: 'registry:pattern', title: 'Fiat Account' },
  { name: 'energy-trading', type: 'registry:pattern', title: 'Energy Trading' },
]

function renderPage(items: CookbookItem[], isLoading = false) {
  mockUseCookbook.mockReturnValue({ items, isLoading })
  return render(
    <MemoryRouter>
      <CookbookGraphPage />
    </MemoryRouter>,
  )
}

describe('CookbookGraphPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders composition graph when patterns are available', () => {
    renderPage(patterns)
    expect(screen.getByTestId('composition-graph')).toBeInTheDocument()
  })

  it('passes all items to composition graph', () => {
    renderPage(patterns)
    expect(screen.getByTestId('composition-graph')).toHaveAttribute('data-pattern-count', '2')
  })

  it('shows loading message while data is loading', () => {
    renderPage([], true)
    expect(screen.getByText(/Loading patterns/)).toBeInTheDocument()
  })

  it('does not render graph while loading', () => {
    renderPage([], true)
    expect(screen.queryByTestId('composition-graph')).not.toBeInTheDocument()
  })

  it('shows empty state message when no patterns available', () => {
    renderPage([])
    expect(screen.getByText(/No patterns available/)).toBeInTheDocument()
  })

  it('does not render graph when items is empty', () => {
    renderPage([])
    expect(screen.queryByTestId('composition-graph')).not.toBeInTheDocument()
  })

  it('passes className with h-full w-full to graph', () => {
    renderPage(patterns)
    const graph = screen.getByTestId('composition-graph')
    expect(graph.className).toContain('h-full')
    expect(graph.className).toContain('w-full')
  })
})
