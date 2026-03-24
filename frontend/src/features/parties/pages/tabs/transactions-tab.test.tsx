import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'

vi.mock('@/api/context', () => ({
  useClients: vi.fn(),
}))

vi.mock('@/hooks/use-tenant-context', () => ({
  useTenantSlug: () => 'test-tenant',
}))

import { useClients } from '@/api/context'
import { TransactionsTab } from './transactions-tab'

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: 0, staleTime: 0 },
    },
  })
}

function renderTab(partyId = 'party-001', partyType?: string) {
  return render(
    <BrowserRouter>
      <QueryClientProvider client={makeQueryClient()}>
        <TransactionsTab partyId={partyId} partyType={partyType} />
      </QueryClientProvider>
    </BrowserRouter>,
  )
}

const mockPostings = [
  {
    id: 'posting-1',
    postingDirection: 2, // CREDIT
    postingAmount: { units: 10000n, nanos: 0, currencyCode: 'GBP' },
    accountId: 'acct-001',
    status: 2, // POSTED
    createdAt: { seconds: 1700000000, nanos: 0 },
  },
  {
    id: 'posting-2',
    postingDirection: 1, // DEBIT
    postingAmount: { units: 5000n, nanos: 0, currencyCode: 'GBP' },
    accountId: 'acct-002',
    status: 1, // PENDING
    createdAt: { seconds: 1700001000, nanos: 0 },
  },
]

describe('TransactionsTab', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  describe('loading state', () => {
    it('renders loading skeleton while fetching accounts', () => {
      vi.mocked(useClients).mockReturnValue({
        currentAccount: {
          listCurrentAccounts: vi.fn(() => new Promise(() => {})),
        },
        financialAccounting: {
          listLedgerPostings: vi.fn(),
        },
      } as ReturnType<typeof useClients>)

      const { container } = renderTab()
      expect(container.querySelector('.animate-pulse')).toBeInTheDocument()
    })
  })

  describe('empty state', () => {
    it('renders empty message when party has no accounts', async () => {
      vi.mocked(useClients).mockReturnValue({
        currentAccount: {
          listCurrentAccounts: vi.fn().mockResolvedValue({
            accounts: [],
            nextPageToken: '',
          }),
        },
        financialAccounting: {
          listLedgerPostings: vi.fn().mockResolvedValue({ ledgerPostings: [] }),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByText(/no transactions found/i)).toBeInTheDocument()
      })
    })

    it('renders empty message when accounts have no postings', async () => {
      vi.mocked(useClients).mockReturnValue({
        currentAccount: {
          listCurrentAccounts: vi.fn().mockResolvedValue({
            accounts: [{ accountId: 'acct-001' }],
            nextPageToken: '',
          }),
        },
        financialAccounting: {
          listLedgerPostings: vi.fn().mockResolvedValue({ ledgerPostings: [] }),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByText(/no transactions found/i)).toBeInTheDocument()
      })
    })
  })

  describe('error state', () => {
    it('renders error message when account fetch fails', async () => {
      vi.mocked(useClients).mockReturnValue({
        currentAccount: {
          listCurrentAccounts: vi.fn().mockRejectedValue(new Error('network error')),
        },
        financialAccounting: {
          listLedgerPostings: vi.fn(),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByText(/failed to load transactions/i)).toBeInTheDocument()
      })
    })

    it('renders error message when postings fetch fails', async () => {
      vi.mocked(useClients).mockReturnValue({
        currentAccount: {
          listCurrentAccounts: vi.fn().mockResolvedValue({
            accounts: [{ accountId: 'acct-001' }],
            nextPageToken: '',
          }),
        },
        financialAccounting: {
          listLedgerPostings: vi.fn().mockRejectedValue(new Error('postings error')),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByText(/failed to load transactions/i)).toBeInTheDocument()
      })
    })
  })

  describe('data display', () => {
    it('renders postings with direction, account, status, and date columns', async () => {
      vi.mocked(useClients).mockReturnValue({
        currentAccount: {
          listCurrentAccounts: vi.fn().mockResolvedValue({
            accounts: [{ accountId: 'acct-001' }, { accountId: 'acct-002' }],
            nextPageToken: '',
          }),
        },
        financialAccounting: {
          listLedgerPostings: vi.fn().mockResolvedValue({ ledgerPostings: mockPostings }),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByText('CREDIT')).toBeInTheDocument()
        expect(screen.getByText('DEBIT')).toBeInTheDocument()
        expect(screen.getByText('POSTED')).toBeInTheDocument()
        expect(screen.getByText('PENDING')).toBeInTheDocument()
      })
    })

    it('renders account links for each posting', async () => {
      vi.mocked(useClients).mockReturnValue({
        currentAccount: {
          listCurrentAccounts: vi.fn().mockResolvedValue({
            accounts: [{ accountId: 'acct-001' }, { accountId: 'acct-002' }],
            nextPageToken: '',
          }),
        },
        financialAccounting: {
          listLedgerPostings: vi.fn().mockResolvedValue({ ledgerPostings: mockPostings }),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        const links = screen.getAllByRole('link')
        const hrefs = links.map((l) => l.getAttribute('href'))
        expect(hrefs).toContain('/accounts/acct-001')
        expect(hrefs).toContain('/accounts/acct-002')
      })
    })

    it('renders table headers: Direction, Amount, Account, Status, Created', async () => {
      vi.mocked(useClients).mockReturnValue({
        currentAccount: {
          listCurrentAccounts: vi.fn().mockResolvedValue({
            accounts: [{ accountId: 'acct-001' }],
            nextPageToken: '',
          }),
        },
        financialAccounting: {
          listLedgerPostings: vi.fn().mockResolvedValue({ ledgerPostings: [mockPostings[0]] }),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByText('Direction')).toBeInTheDocument()
        expect(screen.getByText('Amount')).toBeInTheDocument()
        expect(screen.getByText('Account')).toBeInTheDocument()
        expect(screen.getByText('Status')).toBeInTheDocument()
        expect(screen.getByText('Created')).toBeInTheDocument()
      })
    })
  })

  describe('account filter', () => {
    it('does not show account filter when only one account', async () => {
      vi.mocked(useClients).mockReturnValue({
        currentAccount: {
          listCurrentAccounts: vi.fn().mockResolvedValue({
            accounts: [{ accountId: 'acct-001' }],
            nextPageToken: '',
          }),
        },
        financialAccounting: {
          listLedgerPostings: vi.fn().mockResolvedValue({ ledgerPostings: [mockPostings[0]] }),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.queryByLabelText(/filter by account/i)).not.toBeInTheDocument()
      })
    })

    it('shows account filter when multiple accounts exist', async () => {
      vi.mocked(useClients).mockReturnValue({
        currentAccount: {
          listCurrentAccounts: vi.fn().mockResolvedValue({
            accounts: [{ accountId: 'acct-001' }, { accountId: 'acct-002' }],
            nextPageToken: '',
          }),
        },
        financialAccounting: {
          listLedgerPostings: vi.fn().mockResolvedValue({ ledgerPostings: mockPostings }),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByLabelText(/filter by account/i)).toBeInTheDocument()
      })
    })

    it('filters postings when a specific account is selected', async () => {
      const user = userEvent.setup()
      vi.mocked(useClients).mockReturnValue({
        currentAccount: {
          listCurrentAccounts: vi.fn().mockResolvedValue({
            accounts: [{ accountId: 'acct-001' }, { accountId: 'acct-002' }],
            nextPageToken: '',
          }),
        },
        financialAccounting: {
          listLedgerPostings: vi.fn().mockResolvedValue({ ledgerPostings: mockPostings }),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(screen.getByLabelText(/filter by account/i)).toBeInTheDocument()
      })

      // Select acct-001 - should show only CREDIT posting
      await user.selectOptions(screen.getByLabelText(/filter by account/i), 'acct-001')

      await waitFor(() => {
        expect(screen.getByText('CREDIT')).toBeInTheDocument()
        expect(screen.queryByText('DEBIT')).not.toBeInTheDocument()
      })
    })
  })

  describe('pagination', () => {
    it('paginates through all pages of accounts', async () => {
      const listCurrentAccounts = vi.fn()
        .mockResolvedValueOnce({
          accounts: [{ accountId: 'acct-001' }],
          nextPageToken: 'page2-token',
        })
        .mockResolvedValueOnce({
          accounts: [{ accountId: 'acct-002' }],
          nextPageToken: '',
        })

      vi.mocked(useClients).mockReturnValue({
        currentAccount: { listCurrentAccounts },
        financialAccounting: {
          listLedgerPostings: vi.fn().mockResolvedValue({ ledgerPostings: [] }),
        },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(listCurrentAccounts).toHaveBeenCalledTimes(2)
        expect(listCurrentAccounts).toHaveBeenNthCalledWith(1, expect.objectContaining({ pageToken: '' }))
        expect(listCurrentAccounts).toHaveBeenNthCalledWith(2, expect.objectContaining({ pageToken: 'page2-token' }))
      })
    })
  })

  describe('organization party', () => {
    it('uses orgPartyId filter for organization parties', async () => {
      const listCurrentAccounts = vi.fn().mockResolvedValue({
        accounts: [],
        nextPageToken: '',
      })

      vi.mocked(useClients).mockReturnValue({
        currentAccount: { listCurrentAccounts },
        financialAccounting: {
          listLedgerPostings: vi.fn().mockResolvedValue({ ledgerPostings: [] }),
        },
      } as ReturnType<typeof useClients>)

      renderTab('org-party-001', 'PARTY_TYPE_ORGANIZATION')

      await waitFor(() => {
        expect(listCurrentAccounts).toHaveBeenCalledWith(
          expect.objectContaining({ orgPartyId: 'org-party-001' }),
        )
      })
    })

    it('uses partyId filter for non-organization parties', async () => {
      const listCurrentAccounts = vi.fn().mockResolvedValue({
        accounts: [],
        nextPageToken: '',
      })

      vi.mocked(useClients).mockReturnValue({
        currentAccount: { listCurrentAccounts },
        financialAccounting: {
          listLedgerPostings: vi.fn().mockResolvedValue({ ledgerPostings: [] }),
        },
      } as ReturnType<typeof useClients>)

      renderTab('person-party-001', 'PARTY_TYPE_PERSON')

      await waitFor(() => {
        expect(listCurrentAccounts).toHaveBeenCalledWith(
          expect.objectContaining({ partyId: 'person-party-001' }),
        )
      })
    })
  })

  describe('batch account IDs', () => {
    it('calls listLedgerPostings with accountIds batch filter', async () => {
      const listLedgerPostings = vi.fn().mockResolvedValue({ ledgerPostings: [], pagination: {} })

      vi.mocked(useClients).mockReturnValue({
        currentAccount: {
          listCurrentAccounts: vi.fn().mockResolvedValue({
            accounts: [{ accountId: 'acct-001' }, { accountId: 'acct-002' }],
            nextPageToken: '',
          }),
        },
        financialAccounting: { listLedgerPostings },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(listLedgerPostings).toHaveBeenCalledWith(
          expect.objectContaining({ accountIds: ['acct-001', 'acct-002'] }),
        )
      })
    })

    it('paginates through all pages of postings', async () => {
      const listLedgerPostings = vi.fn()
        .mockResolvedValueOnce({
          ledgerPostings: [mockPostings[0]],
          pagination: { nextPageToken: 'postings-page2' },
        })
        .mockResolvedValueOnce({
          ledgerPostings: [mockPostings[1]],
          pagination: {},
        })

      vi.mocked(useClients).mockReturnValue({
        currentAccount: {
          listCurrentAccounts: vi.fn().mockResolvedValue({
            accounts: [{ accountId: 'acct-001' }],
            nextPageToken: '',
          }),
        },
        financialAccounting: { listLedgerPostings },
      } as ReturnType<typeof useClients>)

      renderTab()

      await waitFor(() => {
        expect(listLedgerPostings).toHaveBeenCalledTimes(2)
        expect(listLedgerPostings).toHaveBeenNthCalledWith(1,
          expect.objectContaining({ pagination: { pageSize: 100, pageToken: '' } }),
        )
        expect(listLedgerPostings).toHaveBeenNthCalledWith(2,
          expect.objectContaining({ pagination: { pageSize: 100, pageToken: 'postings-page2' } }),
        )
      })

      // Both pages of postings should be displayed
      await waitFor(() => {
        expect(screen.getByText('CREDIT')).toBeInTheDocument()
        expect(screen.getByText('DEBIT')).toBeInTheDocument()
      })
    })
  })
})
