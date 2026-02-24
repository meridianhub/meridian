import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TooltipProvider } from '@/components/ui/tooltip'
import { AuthProvider } from '@/contexts/auth-context'
import { TenantProvider } from '@/contexts/tenant-context'
import { CreateMappingDialog } from './create-mapping-dialog'

vi.mock('./mapping-mutations', () => ({
  useCreateMapping: vi.fn(),
}))

import { useCreateMapping } from './mapping-mutations'
const mockUseCreateMapping = vi.mocked(useCreateMapping)

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

function makeMockMutation(overrides: Partial<ReturnType<typeof useCreateMapping>> = {}) {
  return {
    mutateAsync: vi.fn(),
    isPending: false,
    isError: false,
    error: null,
    reset: vi.fn(),
    ...overrides,
  } as unknown as ReturnType<typeof useCreateMapping>
}

describe('CreateMappingDialog - rendering', () => {
  beforeEach(() => {
    mockUseCreateMapping.mockReturnValue(makeMockMutation())
  })

  it('does not render dialog content when closed', () => {
    render(
      <Wrapper>
        <CreateMappingDialog open={false} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders dialog content when open', () => {
    render(
      <Wrapper>
        <CreateMappingDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /create mapping/i })).toBeInTheDocument()
  })

  it('renders all required form fields', () => {
    render(
      <Wrapper>
        <CreateMappingDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    expect(screen.getByLabelText(/name/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/source format/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/target service/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/description/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/mapping rules/i)).toBeInTheDocument()
  })

  it('renders submit and cancel buttons', () => {
    render(
      <Wrapper>
        <CreateMappingDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    expect(screen.getByRole('button', { name: /create mapping/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument()
  })
})

describe('CreateMappingDialog - source format options', () => {
  beforeEach(() => {
    mockUseCreateMapping.mockReturnValue(makeMockMutation())
  })

  it('renders all source format options', () => {
    render(
      <Wrapper>
        <CreateMappingDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    const select = screen.getByLabelText(/source format/i)
    expect(select).toBeInTheDocument()

    const options = Array.from((select as HTMLSelectElement).options).map((o) => o.text)
    expect(options).toContain('JSON')
    expect(options).toContain('XML')
    expect(options).toContain('CSV')
    expect(options).toContain('ISO 20022')
  })
})

describe('CreateMappingDialog - target service options', () => {
  beforeEach(() => {
    mockUseCreateMapping.mockReturnValue(makeMockMutation())
  })

  it('renders target service options', () => {
    render(
      <Wrapper>
        <CreateMappingDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    const select = screen.getByLabelText(/target service/i)
    expect(select).toBeInTheDocument()

    const options = Array.from((select as HTMLSelectElement).options).map((o) => o.text)
    expect(options).toContain('Current Account')
    expect(options).toContain('Payment Order')
    expect(options).toContain('Party')
  })
})

describe('CreateMappingDialog - validation', () => {
  beforeEach(() => {
    mockUseCreateMapping.mockReturnValue(makeMockMutation())
  })

  it('shows validation error for empty name', async () => {
    const user = userEvent.setup()

    render(
      <Wrapper>
        <CreateMappingDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    await user.click(screen.getByRole('button', { name: /create mapping/i }))

    await waitFor(() => {
      expect(screen.getByText(/name is required/i)).toBeInTheDocument()
    })
  })

  it('shows validation error when source format not selected', async () => {
    const user = userEvent.setup()

    render(
      <Wrapper>
        <CreateMappingDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/^name/i), 'My Mapping')
    await user.click(screen.getByRole('button', { name: /create mapping/i }))

    await waitFor(() => {
      expect(screen.getByText(/source format is required/i)).toBeInTheDocument()
    })
  })

  it('shows validation error when target service not selected', async () => {
    const user = userEvent.setup()

    render(
      <Wrapper>
        <CreateMappingDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/^name/i), 'My Mapping')
    await user.selectOptions(screen.getByLabelText(/source format/i), 'SOURCE_FORMAT_JSON')
    await user.click(screen.getByRole('button', { name: /create mapping/i }))

    await waitFor(() => {
      expect(screen.getByText(/target service is required/i)).toBeInTheDocument()
    })
  })

  it('shows error for invalid JSON in mapping rules', async () => {
    const user = userEvent.setup()

    render(
      <Wrapper>
        <CreateMappingDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/^name/i), 'My Mapping')
    await user.selectOptions(screen.getByLabelText(/source format/i), 'SOURCE_FORMAT_JSON')
    await user.selectOptions(
      screen.getByLabelText(/target service/i),
      'meridian.current_account.v1.CurrentAccountService',
    )

    // Clear and type invalid JSON (avoid { } which are special chars in userEvent)
    const rulesField = screen.getByLabelText(/mapping rules/i)
    await user.clear(rulesField)
    await user.type(rulesField, 'not-valid-json')

    await user.click(screen.getByRole('button', { name: /create mapping/i }))

    await waitFor(() => {
      expect(screen.getByText(/invalid json/i)).toBeInTheDocument()
    })
  })

  it('accepts valid JSON in mapping rules', async () => {
    const user = userEvent.setup()
    const mutateAsync = vi.fn().mockResolvedValue({ id: 'mapping-new-1' })
    mockUseCreateMapping.mockReturnValue(makeMockMutation({ mutateAsync }))

    render(
      <Wrapper>
        <CreateMappingDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/^name/i), 'My Mapping')
    await user.selectOptions(screen.getByLabelText(/source format/i), 'SOURCE_FORMAT_JSON')
    await user.selectOptions(
      screen.getByLabelText(/target service/i),
      'meridian.current_account.v1.CurrentAccountService',
    )

    await user.click(screen.getByRole('button', { name: /create mapping/i }))

    await waitFor(() => {
      expect(screen.queryByText(/invalid json/i)).not.toBeInTheDocument()
    })
  })
})

describe('CreateMappingDialog - JSON editor', () => {
  beforeEach(() => {
    mockUseCreateMapping.mockReturnValue(makeMockMutation())
  })

  it('pre-populates mapping rules with a template', () => {
    render(
      <Wrapper>
        <CreateMappingDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    const rulesField = screen.getByLabelText(/mapping rules/i) as HTMLTextAreaElement
    expect(rulesField.value).toContain('fieldMappings')
  })

  it('shows inline syntax error for invalid JSON', async () => {
    const user = userEvent.setup()

    render(
      <Wrapper>
        <CreateMappingDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/^name/i), 'My Mapping')
    await user.selectOptions(screen.getByLabelText(/source format/i), 'SOURCE_FORMAT_JSON')
    await user.selectOptions(
      screen.getByLabelText(/target service/i),
      'meridian.current_account.v1.CurrentAccountService',
    )

    // Use fireEvent to set invalid JSON directly (avoid userEvent special char issues with { })
    const rulesField = screen.getByLabelText(/mapping rules/i)
    await user.clear(rulesField)
    // Type something that is not valid JSON without using { } special chars
    await user.type(rulesField, 'not-valid-json')

    await user.click(screen.getByRole('button', { name: /create mapping/i }))

    await waitFor(() => {
      const errorEl = screen.getByRole('alert')
      expect(errorEl).toHaveTextContent(/invalid json/i)
    })
  })
})

describe('CreateMappingDialog - successful submission', () => {
  it('calls mutateAsync with correct data and navigates on success', async () => {
    const user = userEvent.setup()
    const mutateAsync = vi.fn().mockResolvedValue({ id: 'mapping-new-001' })
    mockUseCreateMapping.mockReturnValue(makeMockMutation({ mutateAsync }))
    const onSuccess = vi.fn()
    const onOpenChange = vi.fn()

    render(
      <Wrapper>
        <CreateMappingDialog open={true} onOpenChange={onOpenChange} onSuccess={onSuccess} />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/^name/i), 'Stripe Webhook')
    await user.selectOptions(screen.getByLabelText(/source format/i), 'SOURCE_FORMAT_JSON')
    await user.selectOptions(
      screen.getByLabelText(/target service/i),
      'meridian.payment_order.v1.PaymentOrderService',
    )
    await user.click(screen.getByRole('button', { name: /create mapping/i }))

    await waitFor(() => {
      expect(mutateAsync).toHaveBeenCalledOnce()
      expect(mutateAsync).toHaveBeenCalledWith(
        expect.objectContaining({
          name: 'Stripe Webhook',
          sourceFormat: 'SOURCE_FORMAT_JSON',
          targetService: 'meridian.payment_order.v1.PaymentOrderService',
        }),
      )
    })

    await waitFor(() => {
      expect(onSuccess).toHaveBeenCalledWith('mapping-new-001')
      expect(onOpenChange).toHaveBeenCalledWith(false)
    })
  })

  it('disables submit button while pending', () => {
    mockUseCreateMapping.mockReturnValue(makeMockMutation({ isPending: true }))

    render(
      <Wrapper>
        <CreateMappingDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    expect(screen.getByRole('button', { name: /creating/i })).toBeDisabled()
  })
})

describe('CreateMappingDialog - error handling', () => {
  it('shows field-level error for INVALID_ARGUMENT with field violations', async () => {
    const user = userEvent.setup()
    const { Code, ConnectError } = await import('@connectrpc/connect')
    const err = new ConnectError('invalid', Code.InvalidArgument)
    err.details = [
      {
        type: 'google.rpc.BadRequest',
        value: new Uint8Array(),
        debug: {
          fieldViolations: [{ field: 'name', description: 'Name already exists' }],
        },
      },
    ]
    const mutateAsync = vi.fn().mockRejectedValue(err)
    mockUseCreateMapping.mockReturnValue(makeMockMutation({ mutateAsync }))

    render(
      <Wrapper>
        <CreateMappingDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/^name/i), 'My Mapping')
    await user.selectOptions(screen.getByLabelText(/source format/i), 'SOURCE_FORMAT_JSON')
    await user.selectOptions(
      screen.getByLabelText(/target service/i),
      'meridian.current_account.v1.CurrentAccountService',
    )
    await user.click(screen.getByRole('button', { name: /create mapping/i }))

    await waitFor(() => {
      expect(screen.getByText('Name already exists')).toBeInTheDocument()
    })
  })

  it('shows general error banner for server errors', async () => {
    const user = userEvent.setup()
    const { Code, ConnectError } = await import('@connectrpc/connect')
    const err = new ConnectError('server error', Code.Internal)
    const mutateAsync = vi.fn().mockRejectedValue(err)
    mockUseCreateMapping.mockReturnValue(makeMockMutation({ mutateAsync }))

    render(
      <Wrapper>
        <CreateMappingDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/^name/i), 'My Mapping')
    await user.selectOptions(screen.getByLabelText(/source format/i), 'SOURCE_FORMAT_JSON')
    await user.selectOptions(
      screen.getByLabelText(/target service/i),
      'meridian.current_account.v1.CurrentAccountService',
    )
    await user.click(screen.getByRole('button', { name: /create mapping/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(/internal server error/i)
    })
  })
})

describe('CreateMappingDialog - reset on close', () => {
  it('clears form when dialog is closed', async () => {
    const user = userEvent.setup()
    mockUseCreateMapping.mockReturnValue(makeMockMutation())

    const { rerender } = render(
      <Wrapper>
        <CreateMappingDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/^name/i), 'My Test Mapping')

    rerender(
      <Wrapper>
        <CreateMappingDialog open={false} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    rerender(
      <Wrapper>
        <CreateMappingDialog open={true} onOpenChange={vi.fn()} onSuccess={vi.fn()} />
      </Wrapper>,
    )

    expect(screen.getByLabelText(/^name/i)).toHaveValue('')
  })
})

describe('CreateMappingDialog - DRAFT status', () => {
  it('newly created mapping is returned with an id (DRAFT status implied)', async () => {
    const user = userEvent.setup()
    const mutateAsync = vi.fn().mockResolvedValue({ id: 'mapping-draft-1' })
    mockUseCreateMapping.mockReturnValue(makeMockMutation({ mutateAsync }))
    const onSuccess = vi.fn()

    render(
      <Wrapper>
        <CreateMappingDialog open={true} onOpenChange={vi.fn()} onSuccess={onSuccess} />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/^name/i), 'Draft Mapping')
    await user.selectOptions(screen.getByLabelText(/source format/i), 'SOURCE_FORMAT_JSON')
    await user.selectOptions(
      screen.getByLabelText(/target service/i),
      'meridian.current_account.v1.CurrentAccountService',
    )
    await user.click(screen.getByRole('button', { name: /create mapping/i }))

    await waitFor(() => {
      expect(onSuccess).toHaveBeenCalledWith('mapping-draft-1')
    })
  })
})
