import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TooltipProvider } from '@/components/ui/tooltip'
import { AuthProvider } from '@/contexts/auth-context'
import { TenantProvider } from '@/contexts/tenant-context'
import { PaymentDetailPage } from './payment-detail'

vi.mock('./payment-detail-query', () => ({
  fetchPaymentDetail: vi.fn(),
}))

// Mock dialog mutations to avoid requiring a live API transport
vi.mock('./dialogs/payment-mutations', () => ({
  useInitiatePayment: () => ({ mutateAsync: vi.fn(), isPending: false, reset: vi.fn() }),
  useCancelPayment: () => ({ mutateAsync: vi.fn(), isPending: false, reset: vi.fn() }),
  useReversePayment: () => ({ mutateAsync: vi.fn(), isPending: false, reset: vi.fn() }),
}))

import { fetchPaymentDetail } from './payment-detail-query'
const mockFetch = vi.mocked(fetchPaymentDetail)

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  })
}

function Wrapper({
  paymentOrderId = 'po-001',
  children,
}: {
  paymentOrderId?: string
  children: React.ReactNode
}) {
  const qc = makeQueryClient()
  return (
    <QueryClientProvider client={qc}>
      <AuthProvider>
        <TenantProvider>
          <TooltipProvider>
            <MemoryRouter initialEntries={[`/payments/${paymentOrderId}`]}>
              <Routes>
                <Route path="/payments/:paymentOrderId" element={children} />
              </Routes>
            </MemoryRouter>
          </TooltipProvider>
        </TenantProvider>
      </AuthProvider>
    </QueryClientProvider>
  )
}

const sampleDetail = {
  paymentOrderId: 'po-001',
  debtorAccountId: 'acct-debtor-1',
  creditorReference: 'GB29 NWBK 6016 1331 9268 19',
  amount: '10050',
  currency: 'GBP',
  status: 'COMPLETED',
  reference: 'REF-2024-001',
  createdAt: { seconds: BigInt(1700000000), nanos: 0 },
  sagaSteps: [
    { status: 'INITIATED', timestamp: { seconds: BigInt(1700000000), nanos: 0 } },
    { status: 'RESERVED', timestamp: { seconds: BigInt(1700000010), nanos: 0 } },
    { status: 'EXECUTING', timestamp: { seconds: BigInt(1700000020), nanos: 0 } },
    { status: 'COMPLETED', timestamp: { seconds: BigInt(1700000030), nanos: 0 } },
  ],
  compensationSteps: [],
}

describe('PaymentDetailPage - structure', () => {
  beforeEach(() => {
    mockFetch.mockResolvedValue(sampleDetail)
  })

  it('renders payment order ID as heading', async () => {
    render(
      <Wrapper>
        <PaymentDetailPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'po-001' })).toBeInTheDocument()
    })
  })

  it('renders back navigation link', async () => {
    render(
      <Wrapper>
        <PaymentDetailPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByRole('link', { name: /payments/i })).toBeInTheDocument()
    })
  })

  it('renders Overview, Saga Steps, and Audit Trail tabs', async () => {
    render(
      <Wrapper>
        <PaymentDetailPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /overview/i })).toBeInTheDocument()
      expect(screen.getByRole('tab', { name: /saga steps/i })).toBeInTheDocument()
      expect(screen.getByRole('tab', { name: /audit trail/i })).toBeInTheDocument()
    })
  })
})

describe('PaymentDetailPage - Overview tab', () => {
  beforeEach(() => {
    mockFetch.mockResolvedValue(sampleDetail)
  })

  it('shows payment details in Overview tab', async () => {
    render(
      <Wrapper>
        <PaymentDetailPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('Debtor Account')).toBeInTheDocument()
      expect(screen.getByText('acct-debtor-1')).toBeInTheDocument()
    })
  })

  it('shows amount using MoneyDisplay', async () => {
    render(
      <Wrapper>
        <PaymentDetailPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('£100.50')).toBeInTheDocument()
    })
  })

  it('shows status using StatusBadge', async () => {
    render(
      <Wrapper>
        <PaymentDetailPage />
      </Wrapper>,
    )

    await waitFor(() => {
      // StatusBadge renders "COMPLETED" - there may be multiple (header + detail row)
      const badges = screen.getAllByText('COMPLETED')
      expect(badges.length).toBeGreaterThanOrEqual(1)
    })
  })

  it('shows creditor reference', async () => {
    render(
      <Wrapper>
        <PaymentDetailPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('GB29 NWBK 6016 1331 9268 19')).toBeInTheDocument()
    })
  })
})

describe('PaymentDetailPage - Saga Steps tab', () => {
  beforeEach(() => {
    mockFetch.mockResolvedValue(sampleDetail)
  })

  it('shows SagaTimeline when Saga Steps tab is clicked', async () => {
    render(
      <Wrapper>
        <PaymentDetailPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /saga steps/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('tab', { name: /saga steps/i }))

    await waitFor(() => {
      // SagaTimeline renders step labels
      expect(screen.getByText('INITIATED')).toBeInTheDocument()
      expect(screen.getByText('RESERVED')).toBeInTheDocument()
      expect(screen.getByText('EXECUTING')).toBeInTheDocument()
    })
  })

  it('shows saga timeline with correct current step', async () => {
    render(
      <Wrapper>
        <PaymentDetailPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /saga steps/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('tab', { name: /saga steps/i }))

    // COMPLETED should be highlighted as current
    await waitFor(() => {
      const completedDot = screen.getByTestId('step-dot-COMPLETED')
      expect(completedDot).toBeInTheDocument()
    })
  })
})

describe('PaymentDetailPage - loading state', () => {
  it('shows skeleton while loading', () => {
    // Never resolves
    mockFetch.mockReturnValue(new Promise(() => {}))

    render(
      <Wrapper>
        <PaymentDetailPage />
      </Wrapper>,
    )

    expect(screen.getByTestId('payment-detail-skeleton')).toBeInTheDocument()
  })
})

describe('PaymentDetailPage - error state', () => {
  it('shows error message on fetch failure', async () => {
    mockFetch.mockRejectedValue(new Error('Not found'))

    render(
      <Wrapper>
        <PaymentDetailPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByTestId('payment-detail-error')).toBeInTheDocument()
    })
  })
})
