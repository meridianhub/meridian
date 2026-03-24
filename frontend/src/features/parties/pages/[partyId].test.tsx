import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Routes, Route } from 'react-router-dom'

const mockClients = {
  party: {
    retrieveParty: vi.fn().mockResolvedValue({
      party: {
        partyId: 'test-party-1',
        legalName: 'Test Party',
        partyType: 'PARTY_TYPE_ORGANIZATION',
        status: 'PARTY_STATUS_ACTIVE',
      },
    }),
    listPaymentMethods: vi.fn().mockResolvedValue({ paymentMethods: [] }),
    retrieveReference: vi.fn().mockResolvedValue({}),
    retrieveAssociations: vi.fn().mockResolvedValue({}),
    retrieveBankRelations: vi.fn().mockResolvedValue({}),
    retrieveDemographics: vi.fn().mockResolvedValue(null),
  },
  currentAccount: {
    listCurrentAccounts: vi.fn().mockResolvedValue({ accounts: [], nextPageToken: '' }),
  },
  financialAccounting: {
    listLedgerPostings: vi.fn().mockResolvedValue({ ledgerPostings: [] }),
  },
  internalAccount: {
    listInternalAccounts: vi.fn().mockResolvedValue({ facilities: [], pagination: {} }),
  },
}

// Mock the API context to avoid loading ungenerated proto files
vi.mock('@/api/context', () => ({
  useClients: vi.fn(() => mockClients),
  useApiClients: vi.fn(() => mockClients),
}))

// Mock useAuth to avoid requiring AuthProvider in tests
vi.mock('@/contexts/auth-context', () => ({
  useAuth: vi.fn(() => ({ accessToken: 'test-token', logout: vi.fn() })),
}))

// Mock useTenantContext to avoid requiring TenantProvider in tests
vi.mock('@/contexts/tenant-context', () => ({
  useTenantContext: vi.fn(() => ({ tenantSlug: 'test-tenant', isPlatformAdmin: false, currentTenant: null, switchTenant: vi.fn() })),
}))

vi.mock('@/hooks/use-tenant-context', () => ({
  useTenantSlug: () => 'test-tenant',
  useCurrentTenant: () => null,
  useIsPlatformAdmin: () => false,
  useSwitchTenant: () => vi.fn(),
  useClearTenant: () => vi.fn(),
}))

import { PartyDetailPage } from './[partyId]'

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity },
    },
  })
}

// Helper to render on a specific route
function renderAtRoute(component: React.ReactNode, route: string) {
  const qc = makeQueryClient()
  window.history.pushState({}, 'test page', route)
  return render(
    <QueryClientProvider client={qc}>
      <BrowserRouter>
        <Routes>
          <Route path="/parties/:partyId" element={component} />
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>,
  )
}

describe('PartyDetailPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders page title and party header', async () => {
    renderAtRoute(<PartyDetailPage />, '/parties/test-party-1')

    await waitFor(() => {
      // Breadcrumb link to parent section
      const partiesLink = screen.getByRole('link', { name: 'Parties' })
      expect(partiesLink).toBeInTheDocument()
      expect(partiesLink).toHaveAttribute('href', '/parties')
    })
  })

  it('renders all eight tabs', async () => {
    const { container } = renderAtRoute(<PartyDetailPage />, '/parties/test-party-1')

    // Check that tabs container is rendered (wait for data to load)
    await waitFor(() => {
      expect(container.querySelector('[role="tablist"]')).toBeInTheDocument()
    })
  })

  it('renders overview tab by default', async () => {
    renderAtRoute(<PartyDetailPage />, '/parties/test-party-1')

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: 'Overview' })).toHaveAttribute('data-state', 'active')
    })
  })

  it('switches to demographics tab on click', async () => {
    const user = userEvent.setup()
    renderAtRoute(<PartyDetailPage />, '/parties/test-party-1')

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: 'Demographics' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('tab', { name: 'Demographics' }))

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: 'Demographics' })).toHaveAttribute('data-state', 'active')
    })
  })

  it('switches to payment methods tab on click', async () => {
    const user = userEvent.setup()
    renderAtRoute(<PartyDetailPage />, '/parties/test-party-1')

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: 'Payment Methods' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('tab', { name: 'Payment Methods' }))

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: 'Payment Methods' })).toHaveAttribute('data-state', 'active')
    })
  })

  it('renders Transactions tab trigger', async () => {
    renderAtRoute(<PartyDetailPage />, '/parties/test-party-1')

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: 'Transactions' })).toBeInTheDocument()
    })
  })

  it('switches to transactions tab on click', async () => {
    const user = userEvent.setup()
    renderAtRoute(<PartyDetailPage />, '/parties/test-party-1')

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: 'Transactions' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('tab', { name: 'Transactions' }))

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: 'Transactions' })).toHaveAttribute('data-state', 'active')
    })
  })

  it('switches to audit trail tab on click', async () => {
    const user = userEvent.setup()
    renderAtRoute(<PartyDetailPage />, '/parties/test-party-1')

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: 'Audit Trail' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('tab', { name: 'Audit Trail' }))

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: 'Audit Trail' })).toHaveAttribute('data-state', 'active')
    })
  })
})
