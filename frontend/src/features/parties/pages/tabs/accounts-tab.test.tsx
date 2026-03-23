import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { render } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AccountsTab } from './accounts-tab'
import { PartyType } from '@/api/gen/meridian/party/v1/party_pb'

const mockListCurrentAccounts = vi.fn()

vi.mock('@/api/context', () => ({
  useClients: () => ({
    currentAccount: {
      listCurrentAccounts: mockListCurrentAccounts,
    },
  }),
  useApiClients: () => ({
    currentAccount: {
      listCurrentAccounts: mockListCurrentAccounts,
    },
  }),
}))

vi.mock('@/hooks/use-tenant-context', () => ({
  useTenantSlug: () => 'test-tenant',
}))

function renderTab(partyId: string, partyType?: number | string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <AccountsTab partyId={partyId} partyType={partyType} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('AccountsTab - server-side filtering', () => {
  beforeEach(() => {
    mockListCurrentAccounts.mockResolvedValue({ accounts: [], nextPageToken: '' })
  })

  it('passes partyId filter for a person party', async () => {
    renderTab('party-001')

    await waitFor(() => {
      expect(mockListCurrentAccounts).toHaveBeenCalledWith(
        expect.objectContaining({ partyId: 'party-001' }),
      )
    })
    expect(mockListCurrentAccounts).not.toHaveBeenCalledWith(
      expect.objectContaining({ orgPartyId: expect.any(String) }),
    )
  })

  it('passes orgPartyId filter for an organization party (PartyType enum)', async () => {
    renderTab('org-001', PartyType.ORGANIZATION)

    await waitFor(() => {
      expect(mockListCurrentAccounts).toHaveBeenCalledWith(
        expect.objectContaining({ orgPartyId: 'org-001' }),
      )
    })
    expect(mockListCurrentAccounts).not.toHaveBeenCalledWith(
      expect.objectContaining({ partyId: 'org-001' }),
    )
  })

  it('passes orgPartyId filter for an organization party (string value)', async () => {
    renderTab('org-002', 'ORGANIZATION')

    await waitFor(() => {
      expect(mockListCurrentAccounts).toHaveBeenCalledWith(
        expect.objectContaining({ orgPartyId: 'org-002' }),
      )
    })
  })

  it('renders returned accounts in the table', async () => {
    mockListCurrentAccounts.mockResolvedValue({
      accounts: [
        {
          accountId: 'acct-001',
          externalIdentifier: 'EXT-001',
          accountStatus: 1, // ACTIVE
          instrumentCode: 'GBP',
          createdAt: undefined,
          partyId: 'party-001',
          orgPartyId: '',
        },
      ],
      nextPageToken: '',
    })

    renderTab('party-001')

    await waitFor(() => {
      expect(screen.getByText('EXT-001')).toBeInTheDocument()
    })
  })

  it('renders empty state when no accounts are returned', async () => {
    mockListCurrentAccounts.mockResolvedValue({ accounts: [], nextPageToken: '' })

    renderTab('party-no-accounts')

    await waitFor(() => {
      expect(screen.getByTestId('empty-state')).toBeInTheDocument()
    })
  })
})
