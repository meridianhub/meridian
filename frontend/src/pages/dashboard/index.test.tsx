import { describe, it, expect, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/msw-handlers'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { DashboardPage } from './index'

// Mock the transport and clients modules to avoid importing proto generated files
vi.mock('@/api/transport', () => ({
  createTenantTransport: vi.fn(() => ({ __type: 'mock-transport' })),
}))

vi.mock('@/api/clients', () => ({
  createServiceClients: vi.fn(() => ({
    currentAccount: {},
    paymentOrder: {
      listPaymentOrders: vi.fn(),
    },
    financialAccounting: {
      listFinancialBookingLogs: vi.fn(),
      listLedgerPostings: vi.fn(),
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
    internalBankAccount: {},
    marketInformation: {},
  })),
}))

import { createServiceClients } from '@/api/clients'

function setupMockClients({
  paymentOrdersResult,
  bookingLogsResult,
  ledgerPostingsResult,
}: {
  paymentOrdersResult?: object | Error
  bookingLogsResult?: object | Error
  ledgerPostingsResult?: object | Error
} = {}) {
  const mockPaymentOrders = vi.fn()
  const mockBookingLogs = vi.fn()
  const mockLedgerPostings = vi.fn()

  if (paymentOrdersResult instanceof Error) {
    mockPaymentOrders.mockRejectedValue(paymentOrdersResult)
  } else if (paymentOrdersResult) {
    mockPaymentOrders.mockResolvedValue(paymentOrdersResult)
  } else {
    mockPaymentOrders.mockResolvedValue({
      paymentOrders: [],
      pagination: { totalCount: BigInt(0), nextPageToken: '' },
    })
  }

  if (bookingLogsResult instanceof Error) {
    mockBookingLogs.mockRejectedValue(bookingLogsResult)
  } else if (bookingLogsResult) {
    mockBookingLogs.mockResolvedValue(bookingLogsResult)
  } else {
    mockBookingLogs.mockResolvedValue({
      financialBookingLogs: [],
      pagination: { totalCount: BigInt(0), nextPageToken: '' },
    })
  }

  if (ledgerPostingsResult instanceof Error) {
    mockLedgerPostings.mockRejectedValue(ledgerPostingsResult)
  } else if (ledgerPostingsResult) {
    mockLedgerPostings.mockResolvedValue(ledgerPostingsResult)
  } else {
    mockLedgerPostings.mockResolvedValue({
      ledgerPostings: [],
      pagination: { totalCount: BigInt(0), nextPageToken: '' },
    })
  }

  vi.mocked(createServiceClients).mockReturnValue({
    currentAccount: {} as never,
    paymentOrder: { listPaymentOrders: mockPaymentOrders } as never,
    financialAccounting: {
      listFinancialBookingLogs: mockBookingLogs,
      listLedgerPostings: mockLedgerPostings,
    } as never,
    positionKeeping: {} as never,
    accountReconciliation: {} as never,
    party: {} as never,
    tenant: {} as never,
    sagaRegistry: {} as never,
    sagaAdmin: {} as never,
    referenceData: {} as never,
    accountTypeRegistry: {} as never,
    node: {} as never,
    internalBankAccount: {} as never,
    marketInformation: {} as never,
  })
}

describe('DashboardPage', () => {
  it('renders dashboard heading', () => {
    setupMockClients()
    renderWithProviders(<DashboardPage />, {
      initialToken: createTenantUserToken('tenant-001'),
    })

    expect(screen.getByRole('heading', { name: /dashboard/i })).toBeInTheDocument()
  })

  it('renders stat card titles', () => {
    setupMockClients()
    renderWithProviders(<DashboardPage />, {
      initialToken: createTenantUserToken('tenant-001'),
    })

    expect(screen.getByText('Payment Orders')).toBeInTheDocument()
    expect(screen.getByText('Booking Logs')).toBeInTheDocument()
    expect(screen.getByText('Ledger Postings')).toBeInTheDocument()
  })

  it('shows loading skeletons initially', () => {
    // Use a never-resolving mock to keep loading state
    vi.mocked(createServiceClients).mockReturnValue({
      currentAccount: {} as never,
      paymentOrder: { listPaymentOrders: vi.fn(() => new Promise(() => {})) } as never,
      financialAccounting: {
        listFinancialBookingLogs: vi.fn(() => new Promise(() => {})),
        listLedgerPostings: vi.fn(() => new Promise(() => {})),
      } as never,
      positionKeeping: {} as never,
      accountReconciliation: {} as never,
      party: {} as never,
      tenant: {} as never,
      sagaRegistry: {} as never,
      sagaAdmin: {} as never,
      referenceData: {} as never,
      accountTypeRegistry: {} as never,
      node: {} as never,
      internalBankAccount: {} as never,
      marketInformation: {} as never,
    })

    renderWithProviders(<DashboardPage />, {
      initialToken: createTenantUserToken('tenant-001'),
    })

    const skeletons = screen.getAllByTestId('stat-card-skeleton')
    expect(skeletons.length).toBeGreaterThan(0)
  })

  it('renders stat card values after data loads', async () => {
    setupMockClients({
      paymentOrdersResult: {
        paymentOrders: [],
        pagination: { totalCount: BigInt(5), nextPageToken: '' },
      },
      bookingLogsResult: {
        financialBookingLogs: [],
        pagination: { totalCount: BigInt(12), nextPageToken: '' },
      },
      ledgerPostingsResult: {
        ledgerPostings: [],
        pagination: { totalCount: BigInt(42), nextPageToken: '' },
      },
    })

    renderWithProviders(<DashboardPage />, {
      initialToken: createTenantUserToken('tenant-001'),
    })

    await waitFor(() => {
      expect(screen.getByText('5')).toBeInTheDocument()
    })
    expect(screen.getByText('12')).toBeInTheDocument()
    expect(screen.getByText('42')).toBeInTheDocument()
  })

  it('error in one stat card does not break others', async () => {
    setupMockClients({
      paymentOrdersResult: new Error('Network error'),
      bookingLogsResult: {
        financialBookingLogs: [],
        pagination: { totalCount: BigInt(7), nextPageToken: '' },
      },
      ledgerPostingsResult: {
        ledgerPostings: [],
        pagination: { totalCount: BigInt(3), nextPageToken: '' },
      },
    })

    renderWithProviders(<DashboardPage />, {
      initialToken: createTenantUserToken('tenant-001'),
    })

    await waitFor(() => {
      expect(screen.getByText('7')).toBeInTheDocument()
    })
    expect(screen.getByText('3')).toBeInTheDocument()
    // Error state shows dash
    expect(screen.getByText('—')).toBeInTheDocument()
  })

  it('renders recent activity section', () => {
    setupMockClients()
    renderWithProviders(<DashboardPage />, {
      initialToken: createTenantUserToken('tenant-001'),
    })

    expect(screen.getByText('Recent Activity')).toBeInTheDocument()
  })

  it('renders quick actions section', () => {
    setupMockClients()
    renderWithProviders(<DashboardPage />, {
      initialToken: createTenantUserToken('tenant-001'),
    })

    expect(screen.getByText('Quick Actions')).toBeInTheDocument()
  })

  it('shows "recent" qualifier when totalCount is -1 (unknown)', async () => {
    setupMockClients({
      paymentOrdersResult: {
        paymentOrders: [{ id: 'po1' }, { id: 'po2' }],
        pagination: { totalCount: BigInt(-1), nextPageToken: '' },
      },
      bookingLogsResult: {
        financialBookingLogs: [],
        pagination: { totalCount: BigInt(-1), nextPageToken: '' },
      },
      ledgerPostingsResult: {
        ledgerPostings: [],
        pagination: { totalCount: BigInt(-1), nextPageToken: '' },
      },
    })

    renderWithProviders(<DashboardPage />, {
      initialToken: createTenantUserToken('tenant-001'),
    })

    await waitFor(() => {
      const recentLabels = screen.getAllByText('recent')
      expect(recentLabels.length).toBeGreaterThan(0)
    })
  })

  it('renders without crashing when no tenant context', () => {
    setupMockClients()
    renderWithProviders(<DashboardPage />)

    expect(screen.getByRole('heading', { name: /dashboard/i })).toBeInTheDocument()
  })

  it('does not make queries when no tenantSlug', () => {
    const mockListPayments = vi.fn()
    vi.mocked(createServiceClients).mockReturnValue({
      currentAccount: {} as never,
      paymentOrder: { listPaymentOrders: mockListPayments } as never,
      financialAccounting: {
        listFinancialBookingLogs: vi.fn(),
        listLedgerPostings: vi.fn(),
      } as never,
      positionKeeping: {} as never,
      accountReconciliation: {} as never,
      party: {} as never,
      tenant: {} as never,
      sagaRegistry: {} as never,
      sagaAdmin: {} as never,
      referenceData: {} as never,
      accountTypeRegistry: {} as never,
      node: {} as never,
      internalBankAccount: {} as never,
      marketInformation: {} as never,
    })

    // Render without token (no tenant)
    renderWithProviders(<DashboardPage />)

    // Queries should not be called with no tenant
    expect(mockListPayments).not.toHaveBeenCalled()
  })
})
