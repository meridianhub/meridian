import { describe, it, expect, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { MarketDataPage } from './index'

vi.mock('@/api/transport', () => ({
  createTenantTransport: vi.fn(() => ({ __type: 'mock-transport' })),
}))

vi.mock('@/api/clients', () => ({
  createServiceClients: vi.fn(() => ({
    currentAccount: {},
    paymentOrder: {},
    financialAccounting: {},
    positionKeeping: {},
    accountReconciliation: {},
    party: {},
    tenant: {},
    sagaRegistry: {},
    sagaAdmin: {},
    referenceData: {},
    accountTypeRegistry: {},
    node: {},
    internalAccount: {},
    marketInformation: {
      listDataSets: vi.fn(),
      registerDataSet: vi.fn(),
    },
    mapping: {},
    forecasting: {},
    manifestHistory: {},
    manifestApplier: {},
  })),
}))

import { createServiceClients } from '@/api/clients'

function makeDataset(overrides: Partial<{
  id: string
  code: string
  displayName: string
  category: number
  unit: string
  status: number
}> = {}) {
  return {
    id: 'ds-001',
    code: 'USD_EUR_FX',
    displayName: 'USD/EUR FX Rate',
    category: 1,
    unit: 'USD/EUR',
    status: 2,
    createdAt: { seconds: BigInt(1700000000), nanos: 0 },
    version: 1,
    ...overrides,
  }
}

function setupMock(datasets = [makeDataset()], nextPageToken = '') {
  vi.mocked(createServiceClients).mockReturnValue({
    currentAccount: {} as never,
    paymentOrder: {} as never,
    financialAccounting: {} as never,
    positionKeeping: {} as never,
    accountReconciliation: {} as never,
    party: {} as never,
    tenant: {} as never,
    sagaRegistry: {} as never,
    sagaAdmin: {} as never,
    referenceData: {} as never,
    accountTypeRegistry: {} as never,
    node: {} as never,
    internalAccount: {} as never,
    marketInformation: {
      listDataSets: vi.fn().mockResolvedValue({
        datasets,
        nextPageToken,
      }),
      registerDataSet: vi.fn(),
    } as never,
    mapping: {} as never,
    forecasting: {} as never,
    manifestHistory: {} as never,
    manifestApplier: {} as never,
  })
}

function renderPage() {
  return renderWithProviders(
    <MemoryRouter>
      <MarketDataPage />
    </MemoryRouter>,
    { initialToken: createTenantUserToken('tenant-001') },
  )
}

describe('MarketDataPage', () => {
  it('renders page heading', () => {
    setupMock()
    renderPage()
    expect(screen.getByRole('heading', { name: /market data/i })).toBeInTheDocument()
  })

  it('renders dataset rows after loading', async () => {
    setupMock()
    renderPage()
    await waitFor(() => {
      expect(screen.getByText('USD_EUR_FX')).toBeInTheDocument()
    })
    expect(screen.getByText('USD/EUR FX Rate')).toBeInTheDocument()
    // "FX Rate" appears in both the filter dropdown and the data row
    expect(screen.getAllByText('FX Rate').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('USD/EUR')).toBeInTheDocument()
  })

  it('renders ACTIVE status badge', async () => {
    setupMock()
    renderPage()
    await waitFor(() => {
      expect(screen.getByText('ACTIVE')).toBeInTheDocument()
    })
  })

  it('renders DRAFT status badge', async () => {
    setupMock([makeDataset({ status: 1 })])
    renderPage()
    await waitFor(() => {
      expect(screen.getByText('DRAFT')).toBeInTheDocument()
    })
  })

  it('renders DEPRECATED status badge', async () => {
    setupMock([makeDataset({ status: 3 })])
    renderPage()
    await waitFor(() => {
      expect(screen.getByText('DEPRECATED')).toBeInTheDocument()
    })
  })

  it('shows empty state when no datasets', async () => {
    setupMock([])
    renderPage()
    await waitFor(() => {
      expect(screen.getByTestId('empty-state')).toBeInTheDocument()
    })
  })

  it('shows loading skeletons while fetching', () => {
    vi.mocked(createServiceClients).mockReturnValue({
      currentAccount: {} as never,
      paymentOrder: {} as never,
      financialAccounting: {} as never,
      positionKeeping: {} as never,
      accountReconciliation: {} as never,
      party: {} as never,
      tenant: {} as never,
      sagaRegistry: {} as never,
      sagaAdmin: {} as never,
      referenceData: {} as never,
      accountTypeRegistry: {} as never,
      node: {} as never,
      internalAccount: {} as never,
      marketInformation: {
        listDataSets: vi.fn().mockReturnValue(new Promise(() => {})),
        registerDataSet: vi.fn(),
      } as never,
      mapping: {} as never,
      forecasting: {} as never,
      manifestHistory: {} as never,
      manifestApplier: {} as never,
    })
    renderPage()
    expect(screen.getAllByTestId('skeleton-row').length).toBeGreaterThan(0)
  })

  it('renders no-tenant guard when tenant is missing', () => {
    setupMock()
    renderWithProviders(
      <MemoryRouter>
        <MarketDataPage />
      </MemoryRouter>,
    )
    expect(screen.getByText(/no tenant selected/i)).toBeInTheDocument()
  })

  it('renders category filter', () => {
    setupMock()
    renderPage()
    expect(screen.getByRole('combobox', { name: /category/i })).toBeInTheDocument()
  })

  it('renders status filter', () => {
    setupMock()
    renderPage()
    expect(screen.getByRole('combobox', { name: /status/i })).toBeInTheDocument()
  })

  it('renders Register Dataset button', () => {
    setupMock()
    renderPage()
    expect(screen.getByRole('button', { name: /register dataset/i })).toBeInTheDocument()
  })

  it('opens registration dialog when Register Dataset button is clicked', async () => {
    const { default: userEvent } = await import('@testing-library/user-event')
    const user = userEvent.setup()
    setupMock()
    renderPage()
    await user.click(screen.getByRole('button', { name: /register dataset/i }))
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /register data set/i })).toBeInTheDocument()
  })
})
