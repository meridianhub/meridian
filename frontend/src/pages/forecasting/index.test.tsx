import { describe, it, expect, vi } from 'vitest'
import { screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { ForecastingPage } from './index'

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
    marketInformation: {},
    forecasting: {
      computeForwardCurve: vi.fn(),
    },
  })),
}))

import { createServiceClients } from '@/api/clients'

const STRATEGY_UUID = '550e8400-e29b-41d4-a716-446655440000'

function makeForecastPoint(seconds: number, value: string) {
  return {
    timestamp: { seconds: BigInt(seconds), nanos: 0 },
    value,
    metadata: {},
  }
}

function setupMock({
  result = {
    strategyId: STRATEGY_UUID,
    strategyVersion: BigInt(1),
    outputDatasetCode: 'STRAT_001_FORECAST',
    pointCount: 3,
    executionTime: { seconds: BigInt(1700000000), nanos: 0 },
    forecastPoints: [
      makeForecastPoint(1700000000, '1.0850'),
      makeForecastPoint(1700086400, '1.0860'),
      makeForecastPoint(1700172800, '1.0870'),
    ],
  },
  error = null as Error | null,
} = {}) {
  const computeForwardCurve = error
    ? vi.fn().mockRejectedValue(error)
    : vi.fn().mockResolvedValue(result)

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
    marketInformation: {} as never,
    forecasting: { computeForwardCurve } as never,
  })
}

function renderPage() {
  return renderWithProviders(
    <MemoryRouter>
      <ForecastingPage />
    </MemoryRouter>,
    { initialToken: createTenantUserToken('tenant-001') },
  )
}

describe('ForecastingPage', () => {
  it('renders page heading', () => {
    setupMock()
    renderPage()
    expect(screen.getByRole('heading', { name: /forecasting/i })).toBeInTheDocument()
  })

  it('renders strategy ID input', () => {
    setupMock()
    renderPage()
    expect(screen.getByRole('textbox', { name: /strategy id/i })).toBeInTheDocument()
  })

  it('renders compute button', () => {
    setupMock()
    renderPage()
    expect(screen.getByRole('button', { name: /compute/i })).toBeInTheDocument()
  })

  it('compute button is disabled when strategy ID is empty', () => {
    setupMock()
    renderPage()
    expect(screen.getByRole('button', { name: /compute/i })).toBeDisabled()
  })

  it('compute button is enabled after entering strategy ID', () => {
    setupMock()
    renderPage()
    fireEvent.change(screen.getByRole('textbox', { name: /strategy id/i }), {
      target: { value: STRATEGY_UUID },
    })
    expect(screen.getByRole('button', { name: /compute/i })).not.toBeDisabled()
  })

  it('submits form and displays result chart', async () => {
    setupMock()
    renderPage()

    fireEvent.change(screen.getByRole('textbox', { name: /strategy id/i }), {
      target: { value: STRATEGY_UUID },
    })
    fireEvent.submit(screen.getByRole('form', { name: /forward curve form/i }))

    await waitFor(() => {
      expect(screen.getByTestId('curve-chart')).toBeInTheDocument()
    })
  })

  it('displays computation details after successful run', async () => {
    setupMock()
    renderPage()

    fireEvent.change(screen.getByRole('textbox', { name: /strategy id/i }), {
      target: { value: STRATEGY_UUID },
    })
    fireEvent.submit(screen.getByRole('form', { name: /forward curve form/i }))

    await waitFor(() => {
      expect(screen.getByText('STRAT_001_FORECAST')).toBeInTheDocument()
    })
    expect(screen.getByText('3')).toBeInTheDocument()
  })

  it('shows error message when computation fails', async () => {
    setupMock({ error: new Error('Strategy not found') })
    renderPage()

    fireEvent.change(screen.getByRole('textbox', { name: /strategy id/i }), {
      target: { value: STRATEGY_UUID },
    })
    fireEvent.submit(screen.getByRole('form', { name: /forward curve form/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })
    expect(screen.getByRole('alert')).toHaveTextContent(/failed to compute forward curve/i)
  })

  it('shows empty curve chart when no forecast points returned', async () => {
    setupMock({
      result: {
        strategyId: STRATEGY_UUID,
        strategyVersion: BigInt(1),
        outputDatasetCode: 'EMPTY_FORECAST',
        pointCount: 0,
        executionTime: { seconds: BigInt(1700000000), nanos: 0 },
        forecastPoints: [],
      },
    })
    renderPage()

    fireEvent.change(screen.getByRole('textbox', { name: /strategy id/i }), {
      target: { value: STRATEGY_UUID },
    })
    fireEvent.submit(screen.getByRole('form', { name: /forward curve form/i }))

    await waitFor(() => {
      expect(screen.getByTestId('curve-chart-empty')).toBeInTheDocument()
    })
  })

  it('renders no-tenant guard when tenant is missing', () => {
    setupMock()
    renderWithProviders(
      <MemoryRouter>
        <ForecastingPage />
      </MemoryRouter>,
    )
    expect(screen.getByText(/no tenant selected/i)).toBeInTheDocument()
  })
})
