import { describe, it, expect, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { CatalogueGrid } from './catalogue-grid'
import type { CookbookItem } from '../hooks/use-cookbook'

vi.mock('@/api/transport', () => ({
  createTenantTransport: vi.fn(() => ({ __type: 'mock-transport' })),
}))

vi.mock('@/api/clients', () => ({
  createServiceClients: vi.fn(() => ({})),
}))

const mockItems: CookbookItem[] = [
  {
    name: 'energy-trading',
    type: 'registry:pattern',
    title: 'Energy Trading',
    description: 'Buy and sell electricity',
    categories: ['energy', 'trading'],
    meta: { complexity: 7, design_pattern: 'saga', industries: ['energy'] },
  },
  {
    name: 'balance-card',
    type: 'registry:ui',
    title: 'Balance Card',
    description: 'Displays account balance',
    categories: ['accounts'],
    meta: { feature_module: 'accounts', configurable: true },
  },
]

const mockNavigate = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return { ...actual, useNavigate: () => mockNavigate }
})

function renderGrid(items: CookbookItem[], hasActiveFilters = false) {
  return renderWithProviders(
    <MemoryRouter>
      <CatalogueGrid items={items} hasActiveFilters={hasActiveFilters} />
    </MemoryRouter>,
  )
}

describe('CatalogueGrid', () => {
  it('renders cards for each item', () => {
    renderGrid(mockItems)
    expect(screen.getByText('Energy Trading')).toBeInTheDocument()
    expect(screen.getByText('Balance Card')).toBeInTheDocument()
  })

  it('shows type badges', () => {
    renderGrid(mockItems)
    expect(screen.getByText('Pattern')).toBeInTheDocument()
    expect(screen.getByText('UI')).toBeInTheDocument()
  })

  it('shows category badges', () => {
    renderGrid(mockItems)
    expect(screen.getByText('energy')).toBeInTheDocument()
    expect(screen.getByText('trading')).toBeInTheDocument()
    expect(screen.getByText('accounts')).toBeInTheDocument()
  })

  it('shows empty state when no items', () => {
    renderGrid([])
    expect(screen.getByText('No cookbook entries yet')).toBeInTheDocument()
  })

  it('shows filter-specific empty state', () => {
    renderGrid([], true)
    expect(screen.getByText('No matching items')).toBeInTheDocument()
  })

  it('navigates on card click', async () => {
    const user = userEvent.setup()
    renderGrid(mockItems)
    await user.click(screen.getByText('Energy Trading'))
    expect(mockNavigate).toHaveBeenCalledWith('/cookbook/energy-trading')
  })

  it('shows complexity indicator for patterns', () => {
    renderGrid(mockItems)
    const indicators = screen.getAllByTestId('complexity-indicator')
    expect(indicators.length).toBe(1)
  })

  it('shows design pattern label', () => {
    renderGrid(mockItems)
    expect(screen.getByText('saga')).toBeInTheDocument()
  })
})
