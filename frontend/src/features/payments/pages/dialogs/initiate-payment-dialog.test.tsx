import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TooltipProvider } from '@/components/ui/tooltip'
import { AuthProvider } from '@/contexts/auth-context'
import { TenantProvider } from '@/contexts/tenant-context'
import { InitiatePaymentDialog } from './initiate-payment-dialog'

vi.mock('./payment-mutations', () => ({
  useInitiatePayment: vi.fn(),
}))

import { useInitiatePayment } from './payment-mutations'
const mockUseInitiatePayment = vi.mocked(useInitiatePayment)

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

function makeMockMutation(overrides: Partial<ReturnType<typeof useInitiatePayment>> = {}) {
  return {
    mutateAsync: vi.fn(),
    isPending: false,
    isError: false,
    error: null,
    reset: vi.fn(),
    ...overrides,
  } as unknown as ReturnType<typeof useInitiatePayment>
}

describe('InitiatePaymentDialog - rendering', () => {
  beforeEach(() => {
    mockUseInitiatePayment.mockReturnValue(makeMockMutation())
  })

  it('does not render dialog content when closed', () => {
    render(
      <Wrapper>
        <InitiatePaymentDialog open={false} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders dialog content when open', () => {
    render(
      <Wrapper>
        <InitiatePaymentDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /initiate payment/i })).toBeInTheDocument()
  })

  it('renders all required form fields', () => {
    render(
      <Wrapper>
        <InitiatePaymentDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    expect(screen.getByLabelText(/debtor account/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/creditor reference/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/amount/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/currency/i)).toBeInTheDocument()
  })

  it('renders submit and cancel buttons', () => {
    render(
      <Wrapper>
        <InitiatePaymentDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    expect(screen.getByRole('button', { name: /initiate payment/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument()
  })
})

describe('InitiatePaymentDialog - creditor reference validation', () => {
  beforeEach(() => {
    mockUseInitiatePayment.mockReturnValue(makeMockMutation())
  })

  it('shows validation error for empty creditor reference on submit', async () => {
    const user = userEvent.setup()

    render(
      <Wrapper>
        <InitiatePaymentDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    await user.click(screen.getByRole('button', { name: /initiate payment/i }))

    await waitFor(() => {
      expect(screen.getByText(/creditor reference is required/i)).toBeInTheDocument()
    })
  })

  it('accepts any non-empty creditor reference', async () => {
    const user = userEvent.setup()
    const mutateAsync = vi.fn().mockResolvedValue({ paymentOrderId: 'po-new' })
    mockUseInitiatePayment.mockReturnValue(makeMockMutation({ mutateAsync }))

    render(
      <Wrapper>
        <InitiatePaymentDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/debtor account/i), 'acct-001')
    await user.type(screen.getByLabelText(/creditor reference/i), '12-34-56-78901234')
    await user.type(screen.getByLabelText(/amount/i), '100.00')
    await user.click(screen.getByRole('button', { name: /initiate payment/i }))

    await waitFor(() => {
      expect(mutateAsync).toHaveBeenCalledOnce()
    })
  })

  it('shows validation error for empty amount', async () => {
    const user = userEvent.setup()

    render(
      <Wrapper>
        <InitiatePaymentDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/debtor account/i), 'acct-001')
    await user.type(screen.getByLabelText(/creditor reference/i), 'GB29NWBK60161331926819')
    await user.click(screen.getByRole('button', { name: /initiate payment/i }))

    await waitFor(() => {
      expect(screen.getByText(/amount is required/i)).toBeInTheDocument()
    })
  })

  it('shows validation error for non-positive amount', async () => {
    const user = userEvent.setup()

    render(
      <Wrapper>
        <InitiatePaymentDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/debtor account/i), 'acct-001')
    await user.type(screen.getByLabelText(/creditor reference/i), 'GB29NWBK60161331926819')
    await user.type(screen.getByLabelText(/amount/i), '0')
    await user.click(screen.getByRole('button', { name: /initiate payment/i }))

    await waitFor(() => {
      expect(screen.getByText(/amount must be positive/i)).toBeInTheDocument()
    })
  })
})

describe('InitiatePaymentDialog - successful submission', () => {
  it('calls mutateAsync with correct data and navigates on success', async () => {
    const user = userEvent.setup()
    const mutateAsync = vi.fn().mockResolvedValue({ paymentOrderId: 'po-new-001' })
    mockUseInitiatePayment.mockReturnValue(makeMockMutation({ mutateAsync }))
    const onSuccess = vi.fn()
    const onOpenChange = vi.fn()

    render(
      <Wrapper>
        <InitiatePaymentDialog open={true} onOpenChange={onOpenChange} onSuccess={onSuccess} />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/debtor account/i), 'acct-001')
    await user.type(screen.getByLabelText(/creditor reference/i), 'GB29NWBK60161331926819')
    await user.type(screen.getByLabelText(/amount/i), '100.00')
    await user.click(screen.getByRole('button', { name: /initiate payment/i }))

    await waitFor(() => {
      expect(mutateAsync).toHaveBeenCalledOnce()
      expect(mutateAsync).toHaveBeenCalledWith(
        expect.objectContaining({
          debtorAccountId: 'acct-001',
          creditorReference: 'GB29NWBK60161331926819',
        }),
      )
    })

    await waitFor(() => {
      expect(onSuccess).toHaveBeenCalledWith('po-new-001')
      expect(onOpenChange).toHaveBeenCalledWith(false)
    })
  })

  it('disables submit button while pending', () => {
    mockUseInitiatePayment.mockReturnValue(makeMockMutation({ isPending: true }))

    render(
      <Wrapper>
        <InitiatePaymentDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    expect(screen.getByRole('button', { name: /initiating/i })).toBeDisabled()
  })
})

describe('InitiatePaymentDialog - error handling', () => {
  it('shows field-level error for creditor reference validation failure', async () => {
    const user = userEvent.setup()
    const { Code, ConnectError } = await import('@connectrpc/connect')
    const err = new ConnectError('invalid', Code.InvalidArgument)
    err.details = [
      {
        type: 'google.rpc.BadRequest',
        value: new Uint8Array(),
        debug: {
          fieldViolations: [
            { field: 'creditor_reference', description: 'Invalid creditor reference' },
          ],
        },
      },
    ]
    const mutateAsync = vi.fn().mockRejectedValue(err)
    mockUseInitiatePayment.mockReturnValue(makeMockMutation({ mutateAsync }))

    render(
      <Wrapper>
        <InitiatePaymentDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/debtor account/i), 'acct-001')
    await user.type(screen.getByLabelText(/creditor reference/i), 'GB29NWBK60161331926819')
    await user.type(screen.getByLabelText(/amount/i), '100.00')
    await user.click(screen.getByRole('button', { name: /initiate payment/i }))

    await waitFor(() => {
      expect(screen.getByText('Invalid creditor reference')).toBeInTheDocument()
    })
  })

  it('shows banner error for FAILED_PRECONDITION', async () => {
    const user = userEvent.setup()
    const { Code, ConnectError } = await import('@connectrpc/connect')
    const err = new ConnectError('insufficient funds', Code.FailedPrecondition)
    const mutateAsync = vi.fn().mockRejectedValue(err)
    mockUseInitiatePayment.mockReturnValue(makeMockMutation({ mutateAsync }))

    render(
      <Wrapper>
        <InitiatePaymentDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/debtor account/i), 'acct-001')
    await user.type(screen.getByLabelText(/creditor reference/i), 'GB29NWBK60161331926819')
    await user.type(screen.getByLabelText(/amount/i), '100.00')
    await user.click(screen.getByRole('button', { name: /initiate payment/i }))

    await waitFor(() => {
      expect(
        screen.getByText(/the operation cannot be performed in the current state/i),
      ).toBeInTheDocument()
    })
  })
})

describe('InitiatePaymentDialog - reset on close', () => {
  it('clears form when dialog is closed', async () => {
    const user = userEvent.setup()
    mockUseInitiatePayment.mockReturnValue(makeMockMutation())

    const { rerender } = render(
      <Wrapper>
        <InitiatePaymentDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/debtor account/i), 'acct-filled')

    rerender(
      <Wrapper>
        <InitiatePaymentDialog open={false} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    rerender(
      <Wrapper>
        <InitiatePaymentDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    expect(screen.getByLabelText(/debtor account/i)).toHaveValue('')
  })
})
