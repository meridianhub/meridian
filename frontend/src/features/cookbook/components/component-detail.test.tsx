import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ComponentDetail } from './component-detail'
import type { CookbookItem, ComponentMeta } from '../hooks/use-cookbook'

vi.mock('@/api/transport', () => ({
  createTenantTransport: vi.fn(() => ({ __type: 'mock-transport' })),
}))

vi.mock('@/api/clients', () => ({
  createServiceClients: vi.fn(() => ({})),
}))

function renderDetail(item: CookbookItem) {
  return render(
    <MemoryRouter>
      <ComponentDetail item={item} />
    </MemoryRouter>,
  )
}

const fullItem: CookbookItem = {
  name: 'balance-card',
  type: 'registry:ui',
  title: 'Balance Card',
  description: 'Displays account balance',
  registryDependencies: ['currency-formatter', 'icon-set'],
  categories: ['accounts'],
  files: [
    { path: 'components/balance-card.tsx', type: 'registry:ui' },
    { path: 'components/balance-card.css' },
  ],
  meta: {
    feature_module: 'accounts',
    tenant_configurable: true,
    configurable_props: ['showCurrency', 'decimals'],
    used_by: ['dashboard', 'account-detail'],
  } satisfies ComponentMeta,
}

const bareItem: CookbookItem = {
  name: 'simple-widget',
  type: 'registry:ui',
  title: 'Simple Widget',
}

describe('ComponentDetail', () => {
  it('renders configurable props table', () => {
    renderDetail(fullItem)
    expect(screen.getByText('Configurable Props')).toBeInTheDocument()
    expect(screen.getByText('showCurrency')).toBeInTheDocument()
    expect(screen.getByText('decimals')).toBeInTheDocument()
  })

  it('renders usage context with feature module', () => {
    renderDetail(fullItem)
    expect(screen.getByText('Usage Context')).toBeInTheDocument()
    expect(screen.getByText('accounts')).toBeInTheDocument()
  })

  it('renders tenant configurable badge', () => {
    renderDetail(fullItem)
    expect(screen.getByText('Yes')).toBeInTheDocument()
  })

  it('renders used_by badges', () => {
    renderDetail(fullItem)
    expect(screen.getByText('dashboard')).toBeInTheDocument()
    expect(screen.getByText('account-detail')).toBeInTheDocument()
  })

  it('renders registry dependencies as links', () => {
    renderDetail(fullItem)
    expect(screen.getByText('Registry Dependencies')).toBeInTheDocument()
    expect(screen.getByText('currency-formatter')).toBeInTheDocument()
    expect(screen.getByText('icon-set')).toBeInTheDocument()

    const link = screen.getByText('currency-formatter').closest('a')
    expect(link).toHaveAttribute('href', '/cookbook/currency-formatter')
  })

  it('shows no dependencies message when none exist', () => {
    renderDetail(bareItem)
    expect(screen.getByText('No registry dependencies.')).toBeInTheDocument()
  })

  it('renders source files list', () => {
    renderDetail(fullItem)
    expect(screen.getByText('Source Files')).toBeInTheDocument()
    expect(screen.getByText('components/balance-card.tsx')).toBeInTheDocument()
    expect(screen.getByText('components/balance-card.css')).toBeInTheDocument()
  })

  it('renders live preview placeholder', () => {
    renderDetail(fullItem)
    expect(screen.getByText('Preview not available')).toBeInTheDocument()
  })

  it('hides props table when no configurable_props', () => {
    renderDetail(bareItem)
    expect(screen.queryByText('Configurable Props')).not.toBeInTheDocument()
  })

  it('hides usage context when no meta', () => {
    renderDetail(bareItem)
    expect(screen.queryByText('Usage Context')).not.toBeInTheDocument()
  })
})
