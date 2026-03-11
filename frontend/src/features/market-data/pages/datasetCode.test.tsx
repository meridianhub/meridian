import { describe, it, expect, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { DatasetDetailPage } from './[datasetCode]'

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
      retrieveDataSet: vi.fn(),
      listObservations: vi.fn(),
    },
  })),
}))

import { createServiceClients } from '@/api/clients'

function makeObservation(overrides: Partial<{
  id: string
  value: string
  quality: number
  observedAt: { seconds: bigint | number; nanos: number }
  resolutionKeyValue: string
}> = {}) {
  return {
    id: 'obs-001',
    datasetCode: 'USD_EUR_FX',
    datasetVersion: 1,
    value: '1.0850',
    quality: 3,
    observedAt: { seconds: BigInt(1700000000), nanos: 0 },
    validFrom: { seconds: BigInt(1700000000), nanos: 0 },
    resolutionKeyValue: 'spot',
    createdAt: { seconds: BigInt(1700000000), nanos: 0 },
    sourceId: 'src-001',
    ...overrides,
  }
}

function setupMock({
  dataset = {
    id: 'ds-001',
    code: 'USD_EUR_FX',
    displayName: 'USD/EUR FX Rate',
    category: 1,
    unit: 'USD/EUR',
    status: 2,
    version: 1,
    description: 'Euro exchange rate',
    createdAt: { seconds: BigInt(1700000000), nanos: 0 },
  },
  observations = [makeObservation()],
} = {}) {
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
      retrieveDataSet: vi.fn().mockResolvedValue({ dataset }),
      listObservations: vi.fn().mockResolvedValue({
        observations,
        nextPageToken: '',
        totalCount: observations.length,
      }),
    } as never,
  })
}

function renderPage(datasetCode = 'USD_EUR_FX') {
  return renderWithProviders(
    <MemoryRouter initialEntries={[`/market-data/${datasetCode}`]}>
      <Routes>
        <Route path="/market-data/:datasetCode" element={<DatasetDetailPage />} />
      </Routes>
    </MemoryRouter>,
    { initialToken: createTenantUserToken('tenant-001') },
  )
}

describe('DatasetDetailPage', () => {
  it('renders dataset display name as heading', async () => {
    setupMock()
    renderPage()
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: /USD\/EUR FX Rate/i })).toBeInTheDocument()
    })
  })

  it('renders dataset code in metadata', async () => {
    setupMock()
    renderPage()
    await waitFor(() => {
      expect(screen.getByText('USD_EUR_FX')).toBeInTheDocument()
    })
  })

  it('renders dataset status badge', async () => {
    setupMock()
    renderPage()
    await waitFor(() => {
      expect(screen.getByText('ACTIVE')).toBeInTheDocument()
    })
  })

  it('renders observation chart when data is loaded', async () => {
    setupMock()
    renderPage()
    await waitFor(() => {
      expect(screen.getByTestId('observation-chart')).toBeInTheDocument()
    })
  })

  it('renders empty chart state when no observations', async () => {
    setupMock({ observations: [] })
    renderPage()
    await waitFor(() => {
      expect(screen.getByTestId('observation-chart-empty')).toBeInTheDocument()
    })
  })

  it('renders observation values in table', async () => {
    setupMock()
    renderPage()
    await waitFor(() => {
      expect(screen.getByText('1.0850')).toBeInTheDocument()
    })
  })

  it('renders observation quality badge', async () => {
    setupMock()
    renderPage()
    await waitFor(() => {
      expect(screen.getByText('ACTUAL')).toBeInTheDocument()
    })
  })

  it('shows detail skeleton while loading', () => {
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
        retrieveDataSet: vi.fn().mockReturnValue(new Promise(() => {})),
        listObservations: vi.fn().mockReturnValue(new Promise(() => {})),
      } as never,
    })
    renderPage()
    expect(screen.getByTestId('detail-skeleton')).toBeInTheDocument()
  })

  it('renders no-tenant guard when tenant is missing', () => {
    setupMock()
    renderWithProviders(
      <MemoryRouter initialEntries={['/market-data/USD_EUR_FX']}>
        <Routes>
          <Route path="/market-data/:datasetCode" element={<DatasetDetailPage />} />
        </Routes>
      </MemoryRouter>,
    )
    expect(screen.getByText(/no tenant selected/i)).toBeInTheDocument()
  })
})
