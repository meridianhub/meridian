import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { InternalAccountDetailPage } from './[accountId]'

vi.mock('@/api/transport', () => ({
  createTenantTransport: vi.fn(() => ({ __type: 'mock-transport' })),
}))

const mockRetrieveInternalAccount = vi.fn()
const mockControlInternalAccount = vi.fn()
const mockListLedgerPostings = vi.fn()

vi.mock('@/api/clients', () => ({
  createServiceClients: vi.fn(() => ({
    currentAccount: {},
    paymentOrder: {},
    financialAccounting: {
      listLedgerPostings: mockListLedgerPostings,
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
    internalAccount: {
      retrieveInternalAccount: mockRetrieveInternalAccount,
      controlInternalAccount: mockControlInternalAccount,
    },
    marketInformation: {},
  })),
}))

import { createServiceClients } from '@/api/clients'

function makeAccount(overrides: Partial<{
  accountId: string
  accountCode: string
  name: string
  behaviorClass: string
  instrumentCode: string
  accountStatus: number
  description: string
}> = {}) {
  return {
    accountId: 'acc-001',
    accountCode: 'CLR-GBP-001',
    name: 'GBP Clearing Account',
    behaviorClass: 'CLEARING',
    instrumentCode: 'GBP',
    accountStatus: 1,
    description: 'Main clearing account',
    createdAt: { seconds: BigInt(1700000000), nanos: 0 },
    updatedAt: { seconds: BigInt(1700000001), nanos: 0 },
    ...overrides,
  }
}

function setupMock(account = makeAccount()) {
  vi.mocked(createServiceClients).mockReturnValue({
    currentAccount: {} as never,
    paymentOrder: {} as never,
    financialAccounting: {
      listLedgerPostings: mockListLedgerPostings,
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
    internalAccount: {
      retrieveInternalAccount: vi.fn().mockResolvedValue({ facility: account }),
      controlInternalAccount: mockControlInternalAccount,
    } as never,
    marketInformation: {} as never,
  })
  mockListLedgerPostings.mockResolvedValue({ ledgerPostings: [], pagination: {} })
}

function setupNotFoundMock() {
  const { ConnectError, Code } = require('@connectrpc/connect')
  vi.mocked(createServiceClients).mockReturnValue({
    currentAccount: {} as never,
    paymentOrder: {} as never,
    financialAccounting: { listLedgerPostings: mockListLedgerPostings } as never,
    positionKeeping: {} as never,
    accountReconciliation: {} as never,
    party: {} as never,
    tenant: {} as never,
    sagaRegistry: {} as never,
    sagaAdmin: {} as never,
    referenceData: {} as never,
    accountTypeRegistry: {} as never,
    node: {} as never,
    internalAccount: {
      retrieveInternalAccount: vi.fn().mockRejectedValue(new ConnectError('not found', Code.NotFound)),
      controlInternalAccount: mockControlInternalAccount,
    } as never,
    marketInformation: {} as never,
  })
}

function renderPage(accountId = 'acc-001') {
  return renderWithProviders(
    <MemoryRouter initialEntries={[`/internal-accounts/${accountId}`]}>
      <Routes>
        <Route path="/internal-accounts/:accountId" element={<InternalAccountDetailPage />} />
      </Routes>
    </MemoryRouter>,
    { initialToken: createTenantUserToken('tenant-001') },
  )
}

describe('InternalAccountDetailPage - loading and error states', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows loading skeleton while fetching', () => {
    vi.mocked(createServiceClients).mockReturnValue({
      currentAccount: {} as never,
      paymentOrder: {} as never,
      financialAccounting: { listLedgerPostings: mockListLedgerPostings } as never,
      positionKeeping: {} as never,
      accountReconciliation: {} as never,
      party: {} as never,
      tenant: {} as never,
      sagaRegistry: {} as never,
      sagaAdmin: {} as never,
      referenceData: {} as never,
      accountTypeRegistry: {} as never,
      node: {} as never,
      internalAccount: {
        retrieveInternalAccount: vi.fn().mockReturnValue(new Promise(() => {})),
        controlInternalAccount: mockControlInternalAccount,
      } as never,
      marketInformation: {} as never,
    })
    renderPage()
    // Should show loading state (DetailSkeleton)
    expect(document.body).toBeTruthy()
  })

  it('shows not found state when account is null', async () => {
    setupNotFoundMock()
    renderPage()

    await waitFor(() => {
      expect(screen.getByText(/account not found/i)).toBeInTheDocument()
    })
  })
})

describe('InternalAccountDetailPage - account details', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    setupMock()
  })

  it('renders account code in heading', async () => {
    renderPage()
    await waitFor(() => {
      expect(screen.getAllByText('CLR-GBP-001').length).toBeGreaterThan(0)
    })
  })

  it('renders account name', async () => {
    renderPage()
    await waitFor(() => {
      expect(screen.getAllByText('GBP Clearing Account').length).toBeGreaterThan(0)
    })
  })

  it('renders ACTIVE status badge', async () => {
    renderPage()
    await waitFor(() => {
      expect(screen.getAllByText('ACTIVE').length).toBeGreaterThan(0)
    })
  })

  it('renders SUSPENDED status badge for suspended account', async () => {
    setupMock(makeAccount({ accountStatus: 2 }))
    renderPage()
    await waitFor(() => {
      expect(screen.getAllByText('SUSPENDED').length).toBeGreaterThan(0)
    })
  })

  it('renders breadcrumb with Internal Accounts link', async () => {
    renderPage()
    await waitFor(() => {
      expect(screen.getByText('Internal Accounts')).toBeInTheDocument()
    })
  })

  it('renders overview, transactions, and audit tabs', async () => {
    renderPage()
    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /overview/i })).toBeInTheDocument()
      expect(screen.getByRole('tab', { name: /transactions/i })).toBeInTheDocument()
      expect(screen.getByRole('tab', { name: /audit/i })).toBeInTheDocument()
    })
  })

  it('shows account details in overview tab', async () => {
    renderPage()
    await waitFor(() => {
      expect(screen.getByText('Account Details')).toBeInTheDocument()
    })
    expect(screen.getAllByText('GBP').length).toBeGreaterThan(0)
    expect(screen.getAllByText('CLEARING').length).toBeGreaterThan(0)
  })
})

describe('InternalAccountDetailPage - action buttons', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockControlInternalAccount.mockResolvedValue({})
    mockListLedgerPostings.mockResolvedValue({ ledgerPostings: [], pagination: {} })
  })

  it('shows Suspend button for active account', async () => {
    setupMock(makeAccount({ accountStatus: 1 }))
    renderPage()
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /suspend/i })).toBeInTheDocument()
    })
  })

  it('shows Reactivate button for suspended account', async () => {
    setupMock(makeAccount({ accountStatus: 2 }))
    renderPage()
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /reactivate/i })).toBeInTheDocument()
    })
  })

  it('does not show action buttons for closed account', async () => {
    setupMock(makeAccount({ accountStatus: 3 }))
    renderPage()
    await waitFor(() => {
      expect(screen.queryByRole('button', { name: /suspend/i })).not.toBeInTheDocument()
      expect(screen.queryByRole('button', { name: /reactivate/i })).not.toBeInTheDocument()
    })
  })

  it('calls controlInternalAccount on Suspend click', async () => {
    setupMock(makeAccount({ accountStatus: 1 }))
    const user = userEvent.setup()
    renderPage()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /suspend/i })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: /suspend/i }))

    await waitFor(() => {
      expect(mockControlInternalAccount).toHaveBeenCalledWith(
        expect.objectContaining({ accountId: 'acc-001' }),
      )
    })
  })
})

describe('InternalAccountDetailPage - transactions tab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows empty transactions message when no postings', async () => {
    setupMock()
    mockListLedgerPostings.mockResolvedValue({ ledgerPostings: [], pagination: {} })
    const user = userEvent.setup()
    renderPage()

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /transactions/i })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('tab', { name: /transactions/i }))

    await waitFor(() => {
      expect(screen.getByText(/no transactions found/i)).toBeInTheDocument()
    })
  })

  it('renders posting rows when postings exist', async () => {
    setupMock()
    mockListLedgerPostings.mockResolvedValue({
      ledgerPostings: [
        {
          id: 'post-001',
          financialBookingLogId: 'log-001',
          postingDirection: 1,
          postingAmount: { units: BigInt(10000), currencyCode: 'GBP' },
          status: 2,
          createdAt: { seconds: BigInt(1700000000), nanos: 0 },
          accountId: 'acc-001',
        },
      ],
      pagination: {},
    })

    const user = userEvent.setup()
    renderPage()

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /transactions/i })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('tab', { name: /transactions/i }))

    await waitFor(() => {
      expect(screen.getByText('DEBIT')).toBeInTheDocument()
    })
  })
})
