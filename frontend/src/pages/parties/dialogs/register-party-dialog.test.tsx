import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TooltipProvider } from '@/components/ui/tooltip'
import { RegisterPartyDialog } from './register-party-dialog'

const mockNavigate = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

const mockRegisterParty = vi.fn()
const mockListPartyTypes = vi.fn()
const mockInvalidateQueries = vi.fn()

vi.mock('@/api/context', async () => {
  const actual = await vi.importActual('@/api/context')
  return {
    ...actual,
    useApiClients: vi.fn(() => ({
      party: {
        registerParty: mockRegisterParty,
        listPartyTypes: mockListPartyTypes,
      },
    })),
    useClients: vi.fn(() => ({
      party: {
        registerParty: mockRegisterParty,
        listPartyTypes: mockListPartyTypes,
      },
    })),
  }
})

vi.mock('@tanstack/react-query', async () => {
  const actual = await vi.importActual('@tanstack/react-query')
  return {
    ...actual,
    useQueryClient: () => ({
      invalidateQueries: mockInvalidateQueries,
    }),
  }
})

vi.mock('@/hooks/use-tenant-context', () => ({
  useTenantSlug: () => 'test-tenant',
  useCurrentTenant: () => null,
  useIsPlatformAdmin: () => false,
  useSwitchTenant: () => vi.fn(),
  useClearTenant: () => vi.fn(),
}))

const mockPartyTypes = [
  { id: 'pt-1', partyType: 'PERSON', tenantId: 'test-tenant' },
  { id: 'pt-2', partyType: 'ORGANIZATION', tenantId: 'test-tenant' },
]

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

function Wrapper({ children }: { children: React.ReactNode }) {
  const qc = makeQueryClient()
  return (
    <QueryClientProvider client={qc}>
      <TooltipProvider>
        <MemoryRouter>{children}</MemoryRouter>
      </TooltipProvider>
    </QueryClientProvider>
  )
}

function renderDialog(props: { open?: boolean; onOpenChange?: (open: boolean) => void } = {}) {
  const { open = true, onOpenChange = vi.fn() } = props
  return render(
    <Wrapper>
      <RegisterPartyDialog open={open} onOpenChange={onOpenChange} />
    </Wrapper>,
  )
}

describe('RegisterPartyDialog - rendering', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListPartyTypes.mockResolvedValue({ partyTypeDefinitions: mockPartyTypes })
    mockRegisterParty.mockResolvedValue({ party: { partyId: 'party-123' } })
  })

  it('does not render dialog content when closed', () => {
    renderDialog({ open: false })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders dialog content when open', () => {
    renderDialog()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /register party/i })).toBeInTheDocument()
  })

  it('renders all form fields', () => {
    renderDialog()
    expect(screen.getByLabelText(/display name/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/party type/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/legal name/i)).toBeInTheDocument()
  })

  it('renders submit and cancel buttons', () => {
    renderDialog()
    expect(screen.getByRole('button', { name: /register party/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument()
  })
})

describe('RegisterPartyDialog - party types loading', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('populates party type select from listPartyTypes response', async () => {
    mockListPartyTypes.mockResolvedValue({ partyTypeDefinitions: mockPartyTypes })
    renderDialog()

    await waitFor(() => {
      const select = screen.getByLabelText(/party type/i) as HTMLSelectElement
      const options = Array.from(select.options).map((o) => o.value)
      expect(options).toContain('PERSON')
      expect(options).toContain('ORGANIZATION')
    })
  })

  it('shows helper message when no party types are configured', async () => {
    mockListPartyTypes.mockResolvedValue({ partyTypeDefinitions: [] })
    renderDialog()

    await waitFor(() => {
      expect(screen.getByText(/no party types have been configured/i)).toBeInTheDocument()
    })
  })

  it('disables submit button when no party types are configured', async () => {
    mockListPartyTypes.mockResolvedValue({ partyTypeDefinitions: [] })
    renderDialog()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /register party/i })).toBeDisabled()
    })
  })

  it('disables party type select when no party types are configured', async () => {
    mockListPartyTypes.mockResolvedValue({ partyTypeDefinitions: [] })
    renderDialog()

    await waitFor(() => {
      expect(screen.getByLabelText(/party type/i)).toBeDisabled()
    })
  })
})

describe('RegisterPartyDialog - validation', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListPartyTypes.mockResolvedValue({ partyTypeDefinitions: mockPartyTypes })
  })

  it('shows error when display name is empty on submit', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.click(screen.getByRole('button', { name: /register party/i }))

    await waitFor(() => {
      expect(screen.getByText(/display name is required/i)).toBeInTheDocument()
    })
  })

  it('shows error when party type is not selected on submit', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/display name/i), 'Test Party')
    await user.click(screen.getByRole('button', { name: /register party/i }))

    await waitFor(() => {
      expect(screen.getByText(/party type is required/i)).toBeInTheDocument()
    })
  })
})

describe('RegisterPartyDialog - successful submission', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListPartyTypes.mockResolvedValue({ partyTypeDefinitions: mockPartyTypes })
    mockRegisterParty.mockResolvedValue({ party: { partyId: 'party-new-123' } })
  })

  it('submits form with correct data and navigates to party detail page on success', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    renderDialog({ onOpenChange })

    await waitFor(() => {
      const select = screen.getByLabelText(/party type/i) as HTMLSelectElement
      expect(Array.from(select.options).some((o) => o.value === 'PERSON')).toBe(true)
    })

    await user.type(screen.getByLabelText(/display name/i), 'Acme Corp')
    await user.selectOptions(screen.getByLabelText(/party type/i), 'PERSON')
    await user.click(screen.getByRole('button', { name: /register party/i }))

    await waitFor(() => {
      expect(mockRegisterParty).toHaveBeenCalledWith(
        expect.objectContaining({
          displayName: 'Acme Corp',
          partyType: 1, // PERSON enum value
        }),
      )
    })

    await waitFor(() => {
      expect(mockInvalidateQueries).toHaveBeenCalledWith(
        expect.objectContaining({
          queryKey: expect.arrayContaining(['parties']),
        }),
      )
      expect(onOpenChange).toHaveBeenCalledWith(false)
      expect(mockNavigate).toHaveBeenCalledWith('/parties/party-new-123')
    })
  })

  it('maps ORGANIZATION party type to enum value 2', async () => {
    const user = userEvent.setup()
    renderDialog()

    await waitFor(() => {
      const select = screen.getByLabelText(/party type/i) as HTMLSelectElement
      expect(Array.from(select.options).some((o) => o.value === 'ORGANIZATION')).toBe(true)
    })

    await user.type(screen.getByLabelText(/display name/i), 'Corp Ltd')
    await user.selectOptions(screen.getByLabelText(/party type/i), 'ORGANIZATION')
    await user.click(screen.getByRole('button', { name: /register party/i }))

    await waitFor(() => {
      expect(mockRegisterParty).toHaveBeenCalledWith(
        expect.objectContaining({ partyType: 2 }),
      )
    })
  })

  it('disables submit button while mutation is pending', async () => {
    mockRegisterParty.mockImplementation(() => new Promise(() => {}))
    const user = userEvent.setup()
    renderDialog()

    await waitFor(() => {
      const select = screen.getByLabelText(/party type/i) as HTMLSelectElement
      expect(Array.from(select.options).some((o) => o.value === 'PERSON')).toBe(true)
    })

    await user.type(screen.getByLabelText(/display name/i), 'Acme Corp')
    await user.selectOptions(screen.getByLabelText(/party type/i), 'PERSON')
    await user.click(screen.getByRole('button', { name: /register party/i }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /registering/i })).toBeDisabled()
    })
  })
})

describe('RegisterPartyDialog - error handling', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListPartyTypes.mockResolvedValue({ partyTypeDefinitions: mockPartyTypes })
  })

  it('shows field-level error for INVALID_ARGUMENT with field violations', async () => {
    const user = userEvent.setup()
    const { Code, ConnectError } = await import('@connectrpc/connect')
    const err = new ConnectError('invalid', Code.InvalidArgument)
    err.details = [
      {
        type: 'google.rpc.BadRequest',
        value: new Uint8Array(),
        debug: {
          fieldViolations: [{ field: 'display_name', description: 'Display name already exists' }],
        },
      },
    ]
    mockRegisterParty.mockRejectedValue(err)

    renderDialog()

    await waitFor(() => {
      const select = screen.getByLabelText(/party type/i) as HTMLSelectElement
      expect(Array.from(select.options).some((o) => o.value === 'PERSON')).toBe(true)
    })

    await user.type(screen.getByLabelText(/display name/i), 'Existing Corp')
    await user.selectOptions(screen.getByLabelText(/party type/i), 'PERSON')
    await user.click(screen.getByRole('button', { name: /register party/i }))

    await waitFor(() => {
      expect(screen.getByText('Display name already exists')).toBeInTheDocument()
    })
  })

  it('shows banner error for general errors', async () => {
    const user = userEvent.setup()
    const { Code, ConnectError } = await import('@connectrpc/connect')
    const err = new ConnectError('server error', Code.Internal)
    mockRegisterParty.mockRejectedValue(err)

    renderDialog()

    await waitFor(() => {
      const select = screen.getByLabelText(/party type/i) as HTMLSelectElement
      expect(Array.from(select.options).some((o) => o.value === 'PERSON')).toBe(true)
    })

    await user.type(screen.getByLabelText(/display name/i), 'Test Corp')
    await user.selectOptions(screen.getByLabelText(/party type/i), 'PERSON')
    await user.click(screen.getByRole('button', { name: /register party/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })
  })
})

describe('RegisterPartyDialog - reset on close', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListPartyTypes.mockResolvedValue({ partyTypeDefinitions: mockPartyTypes })
  })

  it('clears form when dialog is closed and reopened', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    const { rerender } = renderDialog({ onOpenChange })

    await user.type(screen.getByLabelText(/display name/i), 'Filled In')

    rerender(
      <Wrapper>
        <RegisterPartyDialog open={false} onOpenChange={onOpenChange} />
      </Wrapper>,
    )

    rerender(
      <Wrapper>
        <RegisterPartyDialog open={true} onOpenChange={onOpenChange} />
      </Wrapper>,
    )

    expect(screen.getByLabelText(/display name/i)).toHaveValue('')
  })

  it('closes dialog when cancel is clicked', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    renderDialog({ onOpenChange })

    await user.click(screen.getByRole('button', { name: /cancel/i }))

    expect(onOpenChange).toHaveBeenCalledWith(false)
  })
})
