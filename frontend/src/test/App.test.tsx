import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import App from '@/App'

// Mock transport and clients to avoid dependency on generated proto files
vi.mock('@/api/transport', () => ({
  createTenantTransport: vi.fn(() => ({ __type: 'mock-transport' })),
}))

vi.mock('@/api/clients', () => ({
  createServiceClients: vi.fn(() => ({
    currentAccount: {},
    paymentOrder: {},
    financialAccounting: {
      listFinancialBookingLogs: vi.fn().mockResolvedValue({ financialBookingLogs: [], pagination: {} }),
      retrieveFinancialBookingLog: vi.fn().mockResolvedValue({ financialBookingLog: null }),
    },
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
    marketInformation: {},
    mapping: {},
    forecasting: {},
  })),
}))

describe('App', () => {
  it('renders the operations console heading', () => {
    render(<App />)
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(
      'Meridian Operations Console',
    )
  })

  it('renders without crashing with QueryClientProvider', () => {
    const { container } = render(<App />)
    expect(container).toBeTruthy()
  })
})
