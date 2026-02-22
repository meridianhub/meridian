import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TooltipProvider } from '@/components/ui/tooltip'
import { AuthProvider } from '@/contexts/auth-context'
import { TenantProvider } from '@/contexts/tenant-context'
import { ReversePaymentDialog } from './reverse-payment-dialog'

vi.mock('./payment-mutations', () => ({
  useReversePayment: vi.fn(),
}))

import { useReversePayment } from './payment-mutations'
const mockUseReversePayment = vi.mocked(useReversePayment)

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
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

function makeMockMutation(overrides: Partial<ReturnType<typeof useReversePayment>> = {}) {
  return {
    mutateAsync: vi.fn(),
    isPending: false,
    isError: false,
    error: null,
    reset: vi.fn(),
    ...overrides,
  } as unknown as ReturnType<typeof useReversePayment>
}

describe('ReversePaymentDialog - rendering', () => {
  beforeEach(() => {
    mockUseReversePayment.mockReturnValue(makeMockMutation())
  })

  it('does not render dialog content when closed', () => {
    render(
      <Wrapper>
        <ReversePaymentDialog
          open={false}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
          paymentOrderId="po-001"
          currentStatus="COMPLETED"
        />
      </Wrapper>,
    )

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders dialog content when open for COMPLETED payment', () => {
    render(
      <Wrapper>
        <ReversePaymentDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
          paymentOrderId="po-001"
          currentStatus="COMPLETED"
        />
      </Wrapper>,
    )

    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /reverse payment/i })).toBeInTheDocument()
  })

  it('renders payment order ID in dialog', () => {
    render(
      <Wrapper>
        <ReversePaymentDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
          paymentOrderId="po-001"
          currentStatus="COMPLETED"
        />
      </Wrapper>,
    )

    expect(screen.getByText(/po-001/)).toBeInTheDocument()
  })

  it('renders reversal reason field', () => {
    render(
      <Wrapper>
        <ReversePaymentDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
          paymentOrderId="po-001"
          currentStatus="COMPLETED"
        />
      </Wrapper>,
    )

    expect(screen.getByLabelText(/reversal reason/i)).toBeInTheDocument()
  })
})

describe('ReversePaymentDialog - COMPLETED status requirement', () => {
  beforeEach(() => {
    mockUseReversePayment.mockReturnValue(makeMockMutation())
  })

  it('shows COMPLETED payment as reversible', () => {
    render(
      <Wrapper>
        <ReversePaymentDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
          paymentOrderId="po-001"
          currentStatus="COMPLETED"
        />
      </Wrapper>,
    )

    const confirmButton = screen.getByRole('button', { name: /confirm reversal/i })
    expect(confirmButton).not.toBeDisabled()
  })

  it('shows non-COMPLETED payment with disabled confirm and warning', () => {
    render(
      <Wrapper>
        <ReversePaymentDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
          paymentOrderId="po-001"
          currentStatus="EXECUTING"
        />
      </Wrapper>,
    )

    const confirmButton = screen.getByRole('button', { name: /confirm reversal/i })
    expect(confirmButton).toBeDisabled()
    expect(screen.getByText(/can only be reversed when completed/i)).toBeInTheDocument()
  })

  it('shows INITIATED payment with disabled confirm', () => {
    render(
      <Wrapper>
        <ReversePaymentDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
          paymentOrderId="po-001"
          currentStatus="INITIATED"
        />
      </Wrapper>,
    )

    const confirmButton = screen.getByRole('button', { name: /confirm reversal/i })
    expect(confirmButton).toBeDisabled()
  })
})

describe('ReversePaymentDialog - successful reversal', () => {
  it('calls mutateAsync with correct data on submit', async () => {
    const user = userEvent.setup()
    const mutateAsync = vi.fn().mockResolvedValue({})
    mockUseReversePayment.mockReturnValue(makeMockMutation({ mutateAsync }))
    const onSuccess = vi.fn()
    const onOpenChange = vi.fn()

    render(
      <Wrapper>
        <ReversePaymentDialog
          open={true}
          onOpenChange={onOpenChange}
          onSuccess={onSuccess}
          paymentOrderId="po-001"
          currentStatus="COMPLETED"
        />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/reversal reason/i), 'Customer dispute')
    await user.click(screen.getByRole('button', { name: /confirm reversal/i }))

    await waitFor(() => {
      expect(mutateAsync).toHaveBeenCalledWith(
        expect.objectContaining({
          paymentOrderId: 'po-001',
          reversalReason: 'Customer dispute',
        }),
      )
      expect(onSuccess).toHaveBeenCalledOnce()
      expect(onOpenChange).toHaveBeenCalledWith(false)
    })
  })

  it('requires a reversal reason', async () => {
    const user = userEvent.setup()
    mockUseReversePayment.mockReturnValue(makeMockMutation())

    render(
      <Wrapper>
        <ReversePaymentDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
          paymentOrderId="po-001"
          currentStatus="COMPLETED"
        />
      </Wrapper>,
    )

    await user.click(screen.getByRole('button', { name: /confirm reversal/i }))

    await waitFor(() => {
      expect(screen.getByText(/reason is required/i)).toBeInTheDocument()
    })
  })

  it('disables button while pending', () => {
    mockUseReversePayment.mockReturnValue(makeMockMutation({ isPending: true }))

    render(
      <Wrapper>
        <ReversePaymentDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
          paymentOrderId="po-001"
          currentStatus="COMPLETED"
        />
      </Wrapper>,
    )

    expect(screen.getByRole('button', { name: /reversing/i })).toBeDisabled()
  })
})

describe('ReversePaymentDialog - error handling', () => {
  it('shows error message when reversal fails', async () => {
    const user = userEvent.setup()
    const { Code, ConnectError } = await import('@connectrpc/connect')
    const err = new ConnectError('already reversed', Code.FailedPrecondition)
    const mutateAsync = vi.fn().mockRejectedValue(err)
    mockUseReversePayment.mockReturnValue(makeMockMutation({ mutateAsync }))

    render(
      <Wrapper>
        <ReversePaymentDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
          paymentOrderId="po-001"
          currentStatus="COMPLETED"
        />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/reversal reason/i), 'test reason')
    await user.click(screen.getByRole('button', { name: /confirm reversal/i }))

    await waitFor(() => {
      expect(
        screen.getByText(/the operation cannot be performed in the current state/i),
      ).toBeInTheDocument()
    })
  })
})
