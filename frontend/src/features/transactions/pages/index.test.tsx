import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { TransactionsPage } from './index'

vi.mock('@/api/transport', () => ({
  createTenantTransport: vi.fn(() => ({ __type: 'mock-transport' })),
}))

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
    internalAccount: {},
    marketInformation: {},
  })),
}))

import { createServiceClients } from '@/api/clients'

function makePosting(overrides: Partial<{
  id: string
  financialBookingLogId: string
  postingDirection: number
  postingAmount: { units: bigint; currencyCode: string }
  status: number
  accountId: string
  accountServiceDomain: number
}> = {}) {
  return {
    id: 'post-001',
    financialBookingLogId: 'log-00000001',
    postingDirection: 2,
    postingAmount: { units: BigInt(5000), currencyCode: 'GBP' },
    status: 2,
    accountId: 'acc-001',
    accountServiceDomain: 3,
    valueDate: { seconds: BigInt(1700000000), nanos: 0 },
    createdAt: { seconds: BigInt(1700000000), nanos: 0 },
    ...overrides,
  }
}

function setupMock(postings = [makePosting()], nextPageToken = '') {
  vi.mocked(createServiceClients).mockReturnValue({
    currentAccount: {} as never,
    paymentOrder: {} as never,
    financialAccounting: {
      listLedgerPostings: vi.fn().mockResolvedValue({
        ledgerPostings: postings,
        pagination: { nextPageToken, totalCount: BigInt(postings.length) },
      }),
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
}

function renderPage() {
  return renderWithProviders(
    <MemoryRouter>
      <TransactionsPage />
    </MemoryRouter>,
    { initialToken: createTenantUserToken('tenant-001') },
  )
}

describe('TransactionsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders page heading', () => {
    setupMock()
    renderPage()
    expect(screen.getByRole('heading', { name: /transactions/i })).toBeInTheDocument()
  })

  it('renders page description', () => {
    setupMock()
    renderPage()
    expect(screen.getByText(/ledger postings across all accounts/i)).toBeInTheDocument()
  })

  it('renders posting data after loading', async () => {
    setupMock()
    renderPage()
    await waitFor(() => {
      expect(screen.getByTestId('direction-badge')).toBeInTheDocument()
    })
    expect(screen.getByText('GBP')).toBeInTheDocument()
  })

  it('renders DEBIT direction badge', async () => {
    setupMock([makePosting({ postingDirection: 1 })])
    renderPage()
    await waitFor(() => {
      const badge = screen.getByTestId('direction-badge')
      expect(badge).toHaveAttribute('data-direction', 'DEBIT')
    })
  })

  it('renders POSTED status', async () => {
    setupMock()
    renderPage()
    await waitFor(() => {
      expect(screen.getByText('POSTED')).toBeInTheDocument()
    })
  })

  it('shows empty state when no postings', async () => {
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
      financialAccounting: {
        listLedgerPostings: vi.fn().mockReturnValue(new Promise(() => {})),
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
    renderPage()
    expect(screen.getAllByTestId('skeleton-row').length).toBeGreaterThan(0)
  })

  it('shows empty state when no tenant is selected', async () => {
    setupMock()
    renderWithProviders(
      <MemoryRouter>
        <TransactionsPage />
      </MemoryRouter>,
    )
    // No tenant means fetchPostings returns empty items → empty state
    await waitFor(() => {
      expect(screen.getByTestId('empty-state')).toBeInTheDocument()
    })
  })

  it('renders account ID filter', () => {
    setupMock()
    renderPage()
    expect(screen.getByRole('textbox', { name: /account id/i })).toBeInTheDocument()
  })

  it('renders direction filter', () => {
    setupMock()
    renderPage()
    expect(screen.getByRole('combobox', { name: /direction/i })).toBeInTheDocument()
  })
})
