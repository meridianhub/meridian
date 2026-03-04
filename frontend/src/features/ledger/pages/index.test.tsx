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
import { LedgerPage } from './index'

vi.mock('@/api/transport', () => ({
  createTenantTransport: vi.fn(() => ({ __type: 'mock-transport' })),
}))

vi.mock('@/api/clients', () => ({
  createServiceClients: vi.fn(() => ({
    currentAccount: {},
    paymentOrder: {},
    financialAccounting: {
      listFinancialBookingLogs: vi.fn(),
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
  })),
}))

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

const mockBookingLogs = [
  {
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
        accountId: 'acct-1',
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
        accountId: 'acct-2',
        valueDate: { seconds: 1700000000n, nanos: 0 },
        postingResult: '',
        createdAt: { seconds: 1700000000n, nanos: 0 },
        status: 'PENDING',
      },
    ],
  },
  {
    id: 'log-002',
    financialAccountType: 'DEBIT',
    productServiceReference: 'PROD-B',
    businessUnitReference: 'BU-OPS',
    chartOfAccountsRules: 'STANDARD',
    baseCurrency: 'USD',
    status: 'POSTED',
    createdAt: { seconds: 1700002000n, nanos: 0 },
    updatedAt: { seconds: 1700003000n, nanos: 0 },
    postings: [],
  },
]

function setupMockClients({
  result,
}: {
  result?: object | Error
} = {}) {
  const mockListBookingLogs = vi.fn()

  if (result instanceof Error) {
    mockListBookingLogs.mockRejectedValue(result)
  } else if (result) {
    mockListBookingLogs.mockResolvedValue(result)
  } else {
    mockListBookingLogs.mockResolvedValue({
      financialBookingLogs: mockBookingLogs,
      pagination: { totalCount: 2n, nextPageToken: '' },
    })
  }

  vi.mocked(createServiceClients).mockReturnValue({
    currentAccount: {} as never,
    paymentOrder: {} as never,
    financialAccounting: {
      listFinancialBookingLogs: mockListBookingLogs,
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
    internalAccount: {} as never,
    marketInformation: {} as never,
  })

  return { mockListBookingLogs }
}

describe('LedgerPage - list view', () => {
  it('renders the page heading', () => {
    setupMockClients()
    renderWithApiClients(<LedgerPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })
    expect(screen.getByRole('heading', { name: /ledger/i })).toBeInTheDocument()
  })

  it('renders booking log IDs after loading', async () => {
    setupMockClients()
    renderWithApiClients(<LedgerPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    await waitFor(() => {
      expect(screen.getByText('log-001')).toBeInTheDocument()
    })
    expect(screen.getByText('log-002')).toBeInTheDocument()
  })

  it('renders PENDING status badge', async () => {
    setupMockClients()
    renderWithApiClients(<LedgerPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    await waitFor(() => {
      expect(screen.getByText('PENDING')).toBeInTheDocument()
    })
  })

  it('renders POSTED status badge', async () => {
    setupMockClients()
    renderWithApiClients(<LedgerPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    await waitFor(() => {
      expect(screen.getByText('POSTED')).toBeInTheDocument()
    })
  })

  it('renders posting count in list', async () => {
    setupMockClients()
    renderWithApiClients(<LedgerPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    await waitFor(() => {
      // log-001 has 2 postings
      const rows = screen.getAllByRole('row')
      const log001Row = rows.find((row) => row.textContent?.includes('log-001'))
      expect(log001Row).toHaveTextContent('2')
    })
  })

  it('renders status filter select', () => {
    setupMockClients()
    renderWithApiClients(<LedgerPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })
    expect(screen.getByRole('combobox', { name: /status/i })).toBeInTheDocument()
  })

  it('shows skeleton rows while loading', () => {
    vi.mocked(createServiceClients).mockReturnValue({
      currentAccount: {} as never,
      paymentOrder: {} as never,
      financialAccounting: {
        listFinancialBookingLogs: vi.fn(() => new Promise(() => {})),
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
      internalAccount: {} as never,
      marketInformation: {} as never,
    })

    renderWithApiClients(<LedgerPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    expect(screen.getAllByTestId('skeleton-row').length).toBeGreaterThan(0)
  })

  it('shows empty state when no booking logs exist', async () => {
    setupMockClients({
      result: {
        financialBookingLogs: [],
        pagination: { totalCount: 0n, nextPageToken: '' },
      },
    })

    renderWithApiClients(<LedgerPage />, {
      initialToken: createTenantUserToken('tenant-001'),
      queryClient: createTestQueryClient(),
    })

    await waitFor(() => {
      expect(screen.getByTestId('empty-state')).toBeInTheDocument()
    })
  })

  it('does not make queries when no tenant', () => {
    const { mockListBookingLogs } = setupMockClients()
    renderWithApiClients(<LedgerPage />)
    expect(mockListBookingLogs).not.toHaveBeenCalled()
  })
})
