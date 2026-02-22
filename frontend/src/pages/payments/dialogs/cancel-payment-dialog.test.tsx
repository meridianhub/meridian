import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TooltipProvider } from '@/components/ui/tooltip'
import { AuthProvider } from '@/contexts/auth-context'
import { TenantProvider } from '@/contexts/tenant-context'
import { CancelPaymentDialog } from './cancel-payment-dialog'

vi.mock('./payment-mutations', () => ({
  useCancelPayment: vi.fn(),
}))

import { useCancelPayment } from './payment-mutations'
const mockUseCancelPayment = vi.mocked(useCancelPayment)

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

function makeMockMutation(overrides: Partial<ReturnType<typeof useCancelPayment>> = {}) {
  return {
    mutateAsync: vi.fn(),
    isPending: false,
    isError: false,
    error: null,
    reset: vi.fn(),
    ...overrides,
  } as unknown as ReturnType<typeof useCancelPayment>
}

describe('CancelPaymentDialog - rendering', () => {
  beforeEach(() => {
    mockUseCancelPayment.mockReturnValue(makeMockMutation())
  })

  it('does not render dialog content when closed', () => {
    render(
      <Wrapper>
        <CancelPaymentDialog
          open={false}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
          paymentOrderId="po-001"
          currentStatus="INITIATED"
        />
      </Wrapper>,
    )

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders dialog content when open', () => {
    render(
      <Wrapper>
        <CancelPaymentDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
          paymentOrderId="po-001"
          currentStatus="INITIATED"
        />
      </Wrapper>,
    )

    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /cancel payment/i })).toBeInTheDocument()
  })

  it('renders payment order ID in dialog', () => {
    render(
      <Wrapper>
        <CancelPaymentDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
          paymentOrderId="po-001"
          currentStatus="INITIATED"
        />
      </Wrapper>,
    )

    expect(screen.getByText(/po-001/)).toBeInTheDocument()
  })

  it('renders cancellation reason field', () => {
    render(
      <Wrapper>
        <CancelPaymentDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
          paymentOrderId="po-001"
          currentStatus="INITIATED"
        />
      </Wrapper>,
    )

    expect(screen.getByLabelText(/cancellation reason/i)).toBeInTheDocument()
  })
})

describe('CancelPaymentDialog - status-aware behaviour', () => {
  beforeEach(() => {
    mockUseCancelPayment.mockReturnValue(makeMockMutation())
  })

  it('shows INITIATED payment as cancellable', () => {
    render(
      <Wrapper>
        <CancelPaymentDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
          paymentOrderId="po-001"
          currentStatus="INITIATED"
        />
      </Wrapper>,
    )

    const confirmButton = screen.getByRole('button', { name: /confirm cancellation/i })
    expect(confirmButton).not.toBeDisabled()
  })

  it('shows RESERVED payment as cancellable', () => {
    render(
      <Wrapper>
        <CancelPaymentDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
          paymentOrderId="po-001"
          currentStatus="RESERVED"
        />
      </Wrapper>,
    )

    const confirmButton = screen.getByRole('button', { name: /confirm cancellation/i })
    expect(confirmButton).not.toBeDisabled()
  })

  it('shows EXECUTING payment with disabled confirm and warning', () => {
    render(
      <Wrapper>
        <CancelPaymentDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
          paymentOrderId="po-001"
          currentStatus="EXECUTING"
        />
      </Wrapper>,
    )

    const confirmButton = screen.getByRole('button', { name: /confirm cancellation/i })
    expect(confirmButton).toBeDisabled()
    expect(
      screen.getByText(/cannot be cancelled while executing/i),
    ).toBeInTheDocument()
  })
})

describe('CancelPaymentDialog - successful cancellation', () => {
  it('calls mutateAsync with correct data on submit', async () => {
    const user = userEvent.setup()
    const mutateAsync = vi.fn().mockResolvedValue({})
    mockUseCancelPayment.mockReturnValue(makeMockMutation({ mutateAsync }))
    const onSuccess = vi.fn()
    const onOpenChange = vi.fn()

    render(
      <Wrapper>
        <CancelPaymentDialog
          open={true}
          onOpenChange={onOpenChange}
          onSuccess={onSuccess}
          paymentOrderId="po-001"
          currentStatus="INITIATED"
        />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/cancellation reason/i), 'Customer requested')
    await user.click(screen.getByRole('button', { name: /confirm cancellation/i }))

    await waitFor(() => {
      expect(mutateAsync).toHaveBeenCalledWith(
        expect.objectContaining({
          paymentOrderId: 'po-001',
          cancellationReason: 'Customer requested',
        }),
      )
      expect(onSuccess).toHaveBeenCalledOnce()
      expect(onOpenChange).toHaveBeenCalledWith(false)
    })
  })

  it('requires a cancellation reason', async () => {
    const user = userEvent.setup()
    mockUseCancelPayment.mockReturnValue(makeMockMutation())

    render(
      <Wrapper>
        <CancelPaymentDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
          paymentOrderId="po-001"
          currentStatus="INITIATED"
        />
      </Wrapper>,
    )

    await user.click(screen.getByRole('button', { name: /confirm cancellation/i }))

    await waitFor(() => {
      expect(screen.getByText(/reason is required/i)).toBeInTheDocument()
    })
  })

  it('disables button while pending', () => {
    mockUseCancelPayment.mockReturnValue(makeMockMutation({ isPending: true }))

    render(
      <Wrapper>
        <CancelPaymentDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
          paymentOrderId="po-001"
          currentStatus="INITIATED"
        />
      </Wrapper>,
    )

    expect(screen.getByRole('button', { name: /cancelling/i })).toBeDisabled()
  })
})

describe('CancelPaymentDialog - error handling', () => {
  it('shows error message when cancellation fails', async () => {
    const user = userEvent.setup()
    const { Code, ConnectError } = await import('@connectrpc/connect')
    const err = new ConnectError('precondition failed', Code.FailedPrecondition)
    const mutateAsync = vi.fn().mockRejectedValue(err)
    mockUseCancelPayment.mockReturnValue(makeMockMutation({ mutateAsync }))

    render(
      <Wrapper>
        <CancelPaymentDialog
          open={true}
          onOpenChange={vi.fn()}
          onSuccess={vi.fn()}
          paymentOrderId="po-001"
          currentStatus="INITIATED"
        />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/cancellation reason/i), 'test reason')
    await user.click(screen.getByRole('button', { name: /confirm cancellation/i }))

    await waitFor(() => {
      expect(
        screen.getByText(/the operation cannot be performed in the current state/i),
      ).toBeInTheDocument()
    })
  })
})
