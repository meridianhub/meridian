import { describe, it, expect, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { InternalAccountsPage } from './index'

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
    internalAccount: {
      listInternalAccounts: vi.fn(),
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
  accountStatus: number
  instrumentCode: string
}> = {}) {
  return {
    accountId: 'acc-001',
    accountCode: 'CLR-GBP-001',
    name: 'GBP Clearing Account',
    behaviorClass: 'CLEARING',
    accountStatus: 1,
    instrumentCode: 'GBP',
    createdAt: { seconds: BigInt(1700000000), nanos: 0 },
    ...overrides,
  }
}

function setupMock(accounts = [makeAccount()], nextPageToken = '') {
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
    internalAccount: {
      listInternalAccounts: vi.fn().mockResolvedValue({
        facilities: accounts,
        pagination: { nextPageToken, totalCount: BigInt(accounts.length) },
      }),
    } as never,
    marketInformation: {} as never,
  })
}

function renderPage() {
  return renderWithProviders(
    <MemoryRouter>
      <InternalAccountsPage />
    </MemoryRouter>,
    { initialToken: createTenantUserToken('tenant-001') },
  )
}

describe('InternalAccountsPage', () => {
  it('renders page heading', () => {
    setupMock()
    renderPage()
    expect(screen.getByRole('heading', { name: /internal accounts/i })).toBeInTheDocument()
  })

  it('renders account data after loading', async () => {
    setupMock()
    renderPage()
    await waitFor(() => {
      expect(screen.getByText('CLR-GBP-001')).toBeInTheDocument()
    })
    expect(screen.getByText('GBP Clearing Account')).toBeInTheDocument()
    expect(screen.getByText('CLEARING')).toBeInTheDocument()
    expect(screen.getByText('GBP')).toBeInTheDocument()
  })

  it('renders status badge for active account', async () => {
    setupMock()
    renderPage()
    await waitFor(() => {
      expect(screen.getByText('ACTIVE')).toBeInTheDocument()
    })
  })

  it('renders status badge for suspended account', async () => {
    setupMock([makeAccount({ accountStatus: 2 })])
    renderPage()
    await waitFor(() => {
      expect(screen.getByText('SUSPENDED')).toBeInTheDocument()
    })
  })

  it('renders status badge for closed account', async () => {
    setupMock([makeAccount({ accountStatus: 3 })])
    renderPage()
    await waitFor(() => {
      expect(screen.getByText('CLOSED')).toBeInTheDocument()
    })
  })

  it('shows empty state when no accounts', async () => {
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
      internalAccount: {
        listInternalAccounts: vi.fn().mockReturnValue(new Promise(() => {})),
      } as never,
      marketInformation: {} as never,
    })
    renderPage()
    expect(screen.getAllByTestId('skeleton-row').length).toBeGreaterThan(0)
  })

  it('renders no-tenant guard when tenant is missing', () => {
    setupMock()
    renderWithProviders(
      <MemoryRouter>
        <InternalAccountsPage />
      </MemoryRouter>,
    )
    expect(screen.getByText(/no tenant selected/i)).toBeInTheDocument()
  })

  it('renders behavior class filter', () => {
    setupMock()
    renderPage()
    expect(screen.getByRole('combobox', { name: /type/i })).toBeInTheDocument()
  })

  it('renders status filter', () => {
    setupMock()
    renderPage()
    expect(screen.getByRole('combobox', { name: /status/i })).toBeInTheDocument()
  })
})
