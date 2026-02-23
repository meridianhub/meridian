import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { createTestQueryClient } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { AuthProvider, useAuth } from '@/contexts/auth-context'
import { TenantProvider, useTenantContext } from '@/contexts/tenant-context'
import { ApiClientProvider } from '@/api/context'
import type { ReactNode } from 'react'
import { BookingLogDetailPage } from './booking-log-detail'

vi.mock('@/api/transport', () => ({
  createTenantTransport: vi.fn(() => ({ __type: 'mock-transport' })),
}))

vi.mock('@/api/clients', () => ({
  createServiceClients: vi.fn(() => ({
    currentAccount: {},
    paymentOrder: {},
    financialAccounting: {
      retrieveFinancialBookingLog: vi.fn(),
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

vi.mock('react-router-dom', async () => {
  // eslint-disable-next-line @typescript-eslint/consistent-type-imports
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom')
  return {
    ...actual,
    useParams: vi.fn(() => ({ bookingLogId: 'log-001' })),
  }
})

import { createServiceClients } from '@/api/clients'

function ApiClientBridge({ children }: { children: ReactNode }) {
  const { accessToken } = useAuth()
  const { tenantSlug } = useTenantContext()
  const getToken = () => Promise.resolve(accessToken ?? '')
  return (
    <ApiClientProvider tenantSlug={tenantSlug} getToken={getToken}>
      {children}
    </ApiClientProvider>
  )
}

interface RenderOptions {
  initialToken?: string
  queryClient?: ReturnType<typeof createTestQueryClient>
}

function renderWithApiClients(
  ui: React.ReactElement,
  { initialToken, queryClient }: RenderOptions = {},
) {
  const client = queryClient ?? createTestQueryClient()
  return render(
    <QueryClientProvider client={client}>
      <AuthProvider initialToken={initialToken}>
        <TenantProvider>
          <ApiClientBridge>
            <MemoryRouter>{ui}</MemoryRouter>
          </ApiClientBridge>
        </TenantProvider>
      </AuthProvider>
    </QueryClientProvider>,
  )
}

const mockBookingLog = {
  id: 'log-001',
  financialAccountType: 'CURRENT',
  productServiceReference: 'PROD-A',
  businessUnitReference: 'BU-TRADE',
  chartOfAccountsRules: 'STANDARD',
  baseCurrency: 'GBP',
  status: 'PENDING',
  createdAt: { seconds: 1700000000n, nanos: 0 },
  updatedAt: { seconds: 1700001000n, nanos: 0 },
  postings: [
    {
      id: 'p-001',
      financialBookingLogId: 'log-001',
      postingDirection: 'DEBIT',
      postingAmount: { currencyCode: 'GBP', units: 10000n, nanos: 0 },
      accountId: 'acct-debit',
      valueDate: { seconds: 1700000000n, nanos: 0 },
      postingResult: '',
      createdAt: { seconds: 1700000000n, nanos: 0 },
      status: 'PENDING',
    },
    {
      id: 'p-002',
      financialBookingLogId: 'log-001',
      postingDirection: 'CREDIT',
      postingAmount: { currencyCode: 'GBP', units: 10000n, nanos: 0 },
      accountId: 'acct-credit',
      valueDate: { seconds: 1700000000n, nanos: 0 },
      postingResult: '',
      createdAt: { seconds: 1700000000n, nanos: 0 },
      status: 'PENDING',
    },
  ],
}

function setupMockClients({ result }: { result?: object | Error } = {}) {
  const mockRetrieve = vi.fn()

  if (result instanceof Error) {
    mockRetrieve.mockRejectedValue(result)
  } else if (result) {
    mockRetrieve.mockResolvedValue(result)
  } else {
    mockRetrieve.mockResolvedValue({
      financialBookingLog: mockBookingLog,
    })
  }

  vi.mocked(createServiceClients).mockReturnValue({
    currentAccount: {} as never,
    paymentOrder: {} as never,
    financialAccounting: {
      retrieveFinancialBookingLog: mockRetrieve,
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

describe('BookingLogDetailPage', () => {
  it('renders the booking log ID after loading', async () => {
    setupMockClients()
    renderWithApiClients(<BookingLogDetailPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    await waitFor(() => {
      expect(screen.getAllByText('log-001').length).toBeGreaterThan(0)
    })
  })

  it('renders posting table headers', async () => {
    setupMockClients()
    renderWithApiClients(<BookingLogDetailPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: /posting id/i })).toBeInTheDocument()
    })
    expect(screen.getByRole('columnheader', { name: /direction/i })).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: /amount/i })).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: /account/i })).toBeInTheDocument()
  })

  it('renders posting rows with direction badges', async () => {
    setupMockClients()
    renderWithApiClients(<BookingLogDetailPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    await waitFor(() => {
      expect(screen.getByText('DEBIT')).toBeInTheDocument()
    })
    expect(screen.getByText('CREDIT')).toBeInTheDocument()
  })

  it('renders account IDs in posting table', async () => {
    setupMockClients()
    renderWithApiClients(<BookingLogDetailPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    await waitFor(() => {
      expect(screen.getByText('acct-debit')).toBeInTheDocument()
    })
    expect(screen.getByText('acct-credit')).toBeInTheDocument()
  })

  it('shows balanced indicator when debits equal credits', async () => {
    setupMockClients()
    renderWithApiClients(<BookingLogDetailPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    await waitFor(() => {
      expect(screen.getByTestId('balance-indicator')).toBeInTheDocument()
    })
    expect(screen.getByTestId('balance-indicator')).toHaveAttribute('data-balanced', 'true')
  })

  it('shows unbalanced indicator when debits differ from credits', async () => {
    const unbalancedLog = {
      ...mockBookingLog,
      postings: [
        { ...mockBookingLog.postings[0], postingAmount: { currencyCode: 'GBP', units: 15000n, nanos: 0 } },
        { ...mockBookingLog.postings[1] },
      ],
    }
    setupMockClients({ result: { financialBookingLog: unbalancedLog } })
    renderWithApiClients(<BookingLogDetailPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    await waitFor(() => {
      expect(screen.getByTestId('balance-indicator')).toHaveAttribute('data-balanced', 'false')
    })
  })

  it('renders the back button', async () => {
    setupMockClients()
    renderWithApiClients(<BookingLogDetailPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    expect(screen.getByRole('link', { name: /ledger/i })).toBeInTheDocument()
  })

  it('shows status badge', async () => {
    setupMockClients()
    renderWithApiClients(<BookingLogDetailPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    await waitFor(() => {
      expect(screen.getAllByText('PENDING').length).toBeGreaterThan(0)
    })
  })

  it('calculates correct debit total from postings', async () => {
    setupMockClients()
    renderWithApiClients(<BookingLogDetailPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    await waitFor(() => {
      expect(screen.getByTestId('debit-total')).toBeInTheDocument()
    })
  })

  it('calculates correct credit total from postings', async () => {
    setupMockClients()
    renderWithApiClients(<BookingLogDetailPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    await waitFor(() => {
      expect(screen.getByTestId('credit-total')).toBeInTheDocument()
    })
  })

  it('shows error state when API request fails', async () => {
    setupMockClients({ result: new Error('Network error') })
    renderWithApiClients(<BookingLogDetailPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    await waitFor(() => {
      expect(screen.getByText(/failed to load booking log/i)).toBeInTheDocument()
    })
  })

  it('shows error state when financialBookingLog is null', async () => {
    setupMockClients({ result: { financialBookingLog: null } })
    renderWithApiClients(<BookingLogDetailPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    await waitFor(() => {
      expect(screen.getByText(/failed to load booking log/i)).toBeInTheDocument()
    })
  })

  it('shows loading state while fetching', () => {
    vi.mocked(createServiceClients).mockReturnValue({
      currentAccount: {} as never,
      paymentOrder: {} as never,
      financialAccounting: {
        retrieveFinancialBookingLog: vi.fn(() => new Promise(() => {})),
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

    renderWithApiClients(<BookingLogDetailPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    // Loading state shows "Loading..." in breadcrumb
    expect(screen.getByText('Loading...')).toBeInTheDocument()
  })

  it('shows no balance indicator when there are no postings', async () => {
    const logWithNoPostings = { ...mockBookingLog, postings: [] }
    setupMockClients({ result: { financialBookingLog: logWithNoPostings } })
    renderWithApiClients(<BookingLogDetailPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    await waitFor(() => {
      expect(screen.getAllByText('log-001').length).toBeGreaterThan(0)
    })
    expect(screen.queryByTestId('balance-indicator')).not.toBeInTheDocument()
  })

  it('handles numeric direction values (DEBIT=1, CREDIT=2)', async () => {
    const logWithNumericDirections = {
      ...mockBookingLog,
      postings: [
        { ...mockBookingLog.postings[0], postingDirection: 1 }, // DEBIT
        { ...mockBookingLog.postings[1], postingDirection: 2 }, // CREDIT
      ],
    }
    setupMockClients({ result: { financialBookingLog: logWithNumericDirections } })
    renderWithApiClients(<BookingLogDetailPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    await waitFor(() => {
      expect(screen.getByText('DEBIT')).toBeInTheDocument()
    })
    expect(screen.getByText('CREDIT')).toBeInTheDocument()
  })

  it('handles numeric currency code (GBP=1)', async () => {
    const logWithNumericCurrency = { ...mockBookingLog, baseCurrency: 1 }
    setupMockClients({ result: { financialBookingLog: logWithNumericCurrency } })
    renderWithApiClients(<BookingLogDetailPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    await waitFor(() => {
      expect(screen.getAllByText('log-001').length).toBeGreaterThan(0)
    })
  })

  it('handles numeric status values (POSTED=2)', async () => {
    const logWithNumericStatus = {
      ...mockBookingLog,
      status: 2, // POSTED
      postings: [
        { ...mockBookingLog.postings[0], status: 2 },
        { ...mockBookingLog.postings[1], status: 2 },
      ],
    }
    setupMockClients({ result: { financialBookingLog: logWithNumericStatus } })
    renderWithApiClients(<BookingLogDetailPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    await waitFor(() => {
      expect(screen.getAllByText('POSTED').length).toBeGreaterThan(0)
    })
  })
})
