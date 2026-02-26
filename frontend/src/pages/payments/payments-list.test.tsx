import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TooltipProvider } from '@/components/ui/tooltip'
import { AuthProvider } from '@/contexts/auth-context'
import { TenantProvider } from '@/contexts/tenant-context'
import { PaymentsPage } from './index'

// Mock the payments query function
vi.mock('./payments-query', () => ({
  fetchPayments: vi.fn(),
}))

import { fetchPayments } from './payments-query'
const mockFetchPayments = vi.mocked(fetchPayments)

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  })
}

function Wrapper({ children }: { children: React.ReactNode }) {
  const qc = makeQueryClient()
  return (
    <QueryClientProvider client={qc}>
      <AuthProvider>
        <TenantProvider>
          <TooltipProvider>
            <MemoryRouter>{children}</MemoryRouter>
          </TooltipProvider>
        </TenantProvider>
      </AuthProvider>
    </QueryClientProvider>
  )
}

const samplePayments = [
  {
    paymentOrderId: 'po-001',
    debtorAccountId: 'acct-debtor-1',
    creditorReference: 'GB29 NWBK 6016 1331 9268 19',
    amount: '10050',
    currency: 'GBP',
    status: 'COMPLETED',
    createdAt: { seconds: BigInt(1700000000), nanos: 0 },
  },
  {
    paymentOrderId: 'po-002',
    debtorAccountId: 'acct-debtor-2',
    creditorReference: 'DE89 3704 0044 0532 0130 00',
    amount: '50000',
    currency: 'EUR',
    status: 'EXECUTING',
    createdAt: { seconds: BigInt(1700001000), nanos: 0 },
  },
]

describe('PaymentsPage - list rendering', () => {
  it('renders page heading', async () => {
    mockFetchPayments.mockResolvedValue({ items: [] })

    render(
      <Wrapper>
        <PaymentsPage />
      </Wrapper>,
    )

    expect(screen.getByRole('heading', { name: /payments/i })).toBeInTheDocument()
  })

  it('renders column headers', async () => {
    mockFetchPayments.mockResolvedValue({ items: [] })

    render(
      <Wrapper>
        <PaymentsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: /payment id/i })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: /debtor account/i })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: /creditor reference/i })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: /amount/i })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: /status/i })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: /created/i })).toBeInTheDocument()
    })
  })

  it('renders payment rows with data', async () => {
    mockFetchPayments.mockResolvedValue({ items: samplePayments })

    render(
      <Wrapper>
        <PaymentsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('po-001')).toBeInTheDocument()
      expect(screen.getByText('po-002')).toBeInTheDocument()
    })
  })

  it('renders amount using MoneyDisplay', async () => {
    mockFetchPayments.mockResolvedValue({ items: samplePayments })

    render(
      <Wrapper>
        <PaymentsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      // £100.50 (10050 pence / 100)
      expect(screen.getByText('£100.50')).toBeInTheDocument()
    })
  })

  it('renders status using StatusBadge', async () => {
    mockFetchPayments.mockResolvedValue({ items: samplePayments })

    render(
      <Wrapper>
        <PaymentsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('COMPLETED')).toBeInTheDocument()
      expect(screen.getByText('EXECUTING')).toBeInTheDocument()
    })
  })
})

describe('PaymentsPage - navigation', () => {
  it('navigates to detail page on row click', async () => {
    mockFetchPayments.mockResolvedValue({ items: samplePayments })

    const onNavigate = vi.fn()

    const qc = makeQueryClient()
    render(
      <QueryClientProvider client={qc}>
        <AuthProvider>
          <TenantProvider>
            <TooltipProvider>
              <MemoryRouter initialEntries={['/payments']}>
                <PaymentsPage onRowNavigate={onNavigate} />
              </MemoryRouter>
            </TooltipProvider>
          </TenantProvider>
        </AuthProvider>
      </QueryClientProvider>,
    )

    await waitFor(() => expect(screen.getByText('po-001')).toBeInTheDocument())
    await userEvent.click(screen.getByText('po-001'))

    expect(onNavigate).toHaveBeenCalledWith('po-001')
  })
})

describe('PaymentsPage - filters', () => {
  it('renders status filter select', async () => {
    mockFetchPayments.mockResolvedValue({ items: [] })

    render(
      <Wrapper>
        <PaymentsPage />
      </Wrapper>,
    )

    expect(screen.getByRole('combobox', { name: /status/i })).toBeInTheDocument()
  })
})
