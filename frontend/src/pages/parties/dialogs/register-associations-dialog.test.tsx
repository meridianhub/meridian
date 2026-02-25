import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { TooltipProvider } from '@/components/ui/tooltip'
import { RegisterAssociationsDialog } from './register-associations-dialog'

const mockRegisterAssociations = vi.fn()
const mockListParties = vi.fn()
const mockInvalidateQueries = vi.fn()

vi.mock('@/api/context', async () => {
  const actual = await vi.importActual('@/api/context')
  return {
    ...actual,
    useApiClients: vi.fn(() => ({
      party: {
        registerAssociations: mockRegisterAssociations,
        listParties: mockListParties,
      },
    })),
    useClients: vi.fn(() => ({
      party: {
        registerAssociations: mockRegisterAssociations,
        listParties: mockListParties,
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

const mockParties = [
  { partyId: 'party-001', displayName: 'Acme Corp', legalName: 'Acme Corporation Ltd' },
  { partyId: 'party-002', displayName: 'Beta Inc', legalName: 'Beta Incorporated' },
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
      <TooltipProvider>{children}</TooltipProvider>
    </QueryClientProvider>
  )
}

function renderDialog(
  props: {
    open?: boolean
    onOpenChange?: (open: boolean) => void
    partyId?: string
  } = {},
) {
  const { open = true, onOpenChange = vi.fn(), partyId = 'current-party-001' } = props
  return render(
    <Wrapper>
      <RegisterAssociationsDialog
        open={open}
        onOpenChange={onOpenChange}
        partyId={partyId}
      />
    </Wrapper>,
  )
}

describe('RegisterAssociationsDialog - rendering', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListParties.mockResolvedValue({ parties: [] })
    mockRegisterAssociations.mockResolvedValue({ associationId: 'assoc-123' })
  })

  it('does not render dialog content when closed', () => {
    renderDialog({ open: false })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders dialog content when open', () => {
    renderDialog()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /add association/i })).toBeInTheDocument()
  })

  it('renders relationship type select', () => {
    renderDialog()
    expect(screen.getByLabelText(/relationship type/i)).toBeInTheDocument()
  })

  it('renders effective from and effective to date inputs', () => {
    renderDialog()
    expect(screen.getByLabelText(/effective from/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/effective to/i)).toBeInTheDocument()
  })

  it('renders submit and cancel buttons', () => {
    renderDialog()
    expect(screen.getByRole('button', { name: /add association/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument()
  })

  it('renders all relationship type options', () => {
    renderDialog()
    const select = screen.getByLabelText(/relationship type/i) as HTMLSelectElement
    const optionValues = Array.from(select.options).map((o) => o.value)
    expect(optionValues).toContain('RELATIONSHIP_TYPE_SPOUSE')
    expect(optionValues).toContain('RELATIONSHIP_TYPE_DEPENDENT')
    expect(optionValues).toContain('RELATIONSHIP_TYPE_BUSINESS_PARTNER')
    expect(optionValues).toContain('RELATIONSHIP_TYPE_GUARANTOR')
    expect(optionValues).toContain('RELATIONSHIP_TYPE_BENEFICIAL_OWNER')
    expect(optionValues).toContain('RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT')
    expect(optionValues).toContain('RELATIONSHIP_TYPE_SYNDICATE_HOST')
  })
})

describe('RegisterAssociationsDialog - party search', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListParties.mockResolvedValue({ parties: mockParties })
    mockRegisterAssociations.mockResolvedValue({ associationId: 'assoc-123' })
  })

  it('renders the party search input', () => {
    renderDialog()
    expect(screen.getByLabelText(/related party/i)).toBeInTheDocument()
  })

  it('does not call listParties when search is less than 2 characters', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/related party/i), 'A')

    await new Promise((r) => setTimeout(r, 400))
    expect(mockListParties).not.toHaveBeenCalled()
  })

  it('calls listParties with searchQuery when input has 2+ characters', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/related party/i), 'Ac')

    await waitFor(() => {
      expect(mockListParties).toHaveBeenCalledWith(
        expect.objectContaining({ searchQuery: 'Ac', pageSize: 20 }),
      )
    })
  })

  it('shows party search results in dropdown', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/related party/i), 'Ac')

    await waitFor(() => {
      expect(screen.getByText('Acme Corp')).toBeInTheDocument()
    })
  })

  it('shows party ID alongside party name in results', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/related party/i), 'Ac')

    await waitFor(() => {
      expect(screen.getByText(/party-001/i)).toBeInTheDocument()
    })
  })

  it('allows selecting a party from results', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/related party/i), 'Ac')

    await waitFor(() => {
      expect(screen.getByText('Acme Corp')).toBeInTheDocument()
    })

    await user.click(screen.getByText('Acme Corp'))

    // After selection, the input should show the selected party name
    await waitFor(() => {
      const input = screen.getByLabelText(/related party/i) as HTMLInputElement
      expect(input.value).toBe('Acme Corp')
    })
  })

  it('clears selected party ID when input is cleared after selection', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/related party/i), 'Ac')
    await waitFor(() => {
      expect(screen.getByText('Acme Corp')).toBeInTheDocument()
    })
    await user.click(screen.getByText('Acme Corp'))

    // Verify party was selected (input shows party name)
    const input = screen.getByLabelText(/related party/i) as HTMLInputElement
    expect(input.value).toBe('Acme Corp')

    // Clear the input - submission should fail without a re-selection
    await user.clear(input)

    // After clearing and submitting, should show error (party was deselected)
    await user.click(screen.getByRole('button', { name: /add association/i }))
    await waitFor(() => {
      expect(screen.getByText(/related party is required/i)).toBeInTheDocument()
    })
  })
})

describe('RegisterAssociationsDialog - validation', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListParties.mockResolvedValue({ parties: mockParties })
    mockRegisterAssociations.mockResolvedValue({ associationId: 'assoc-123' })
  })

  it('shows error when related party is not selected on submit', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.click(screen.getByRole('button', { name: /add association/i }))

    await waitFor(() => {
      expect(screen.getByText(/related party is required/i)).toBeInTheDocument()
    })
  })

  it('shows error when relationship type is not selected on submit', async () => {
    const user = userEvent.setup()
    renderDialog()

    // Select a party
    await user.type(screen.getByLabelText(/related party/i), 'Ac')
    await waitFor(() => expect(screen.getByText('Acme Corp')).toBeInTheDocument())
    await user.click(screen.getByText('Acme Corp'))

    await user.click(screen.getByRole('button', { name: /add association/i }))

    await waitFor(() => {
      expect(screen.getByText(/relationship type is required/i)).toBeInTheDocument()
    })
  })

  it('shows error when effectiveTo is before effectiveFrom', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/effective from/i), '2025-06-01')
    await user.type(screen.getByLabelText(/effective to/i), '2025-01-01')
    await user.click(screen.getByRole('button', { name: /add association/i }))

    await waitFor(() => {
      expect(screen.getByText(/effective to must be after effective from/i)).toBeInTheDocument()
    })
  })

  it('does not show date error when effectiveTo equals effectiveFrom', async () => {
    const user = userEvent.setup()
    renderDialog()

    // Select a party and relationship type first
    await user.type(screen.getByLabelText(/related party/i), 'Ac')
    await waitFor(() => expect(screen.getByText('Acme Corp')).toBeInTheDocument())
    await user.click(screen.getByText('Acme Corp'))
    await user.selectOptions(screen.getByLabelText(/relationship type/i), 'RELATIONSHIP_TYPE_GUARANTOR')

    await user.type(screen.getByLabelText(/effective from/i), '2025-06-01')
    await user.type(screen.getByLabelText(/effective to/i), '2025-06-01')
    await user.click(screen.getByRole('button', { name: /add association/i }))

    await waitFor(() => {
      expect(screen.queryByText(/effective to must be after/i)).not.toBeInTheDocument()
    })
  })
})

describe('RegisterAssociationsDialog - successful submission', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListParties.mockResolvedValue({ parties: mockParties })
    mockRegisterAssociations.mockResolvedValue({ associationId: 'assoc-new-123' })
  })

  it('submits form with partyId, relatedPartyId, and relationshipType', async () => {
    const user = userEvent.setup()
    renderDialog({ partyId: 'current-party-001' })

    await user.type(screen.getByLabelText(/related party/i), 'Ac')
    await waitFor(() => expect(screen.getByText('Acme Corp')).toBeInTheDocument())
    await user.click(screen.getByText('Acme Corp'))

    await user.selectOptions(screen.getByLabelText(/relationship type/i), 'RELATIONSHIP_TYPE_GUARANTOR')

    await user.click(screen.getByRole('button', { name: /add association/i }))

    await waitFor(() => {
      expect(mockRegisterAssociations).toHaveBeenCalledWith(
        expect.objectContaining({
          partyId: 'current-party-001',
          relatedPartyId: 'party-001',
          relationshipType: 4, // RELATIONSHIP_TYPE_GUARANTOR enum value
        }),
      )
    })
  })

  it('invalidates party associations query on success', async () => {
    const user = userEvent.setup()
    renderDialog({ partyId: 'current-party-001' })

    await user.type(screen.getByLabelText(/related party/i), 'Ac')
    await waitFor(() => expect(screen.getByText('Acme Corp')).toBeInTheDocument())
    await user.click(screen.getByText('Acme Corp'))
    await user.selectOptions(screen.getByLabelText(/relationship type/i), 'RELATIONSHIP_TYPE_GUARANTOR')
    await user.click(screen.getByRole('button', { name: /add association/i }))

    await waitFor(() => {
      expect(mockInvalidateQueries).toHaveBeenCalledWith(
        expect.objectContaining({
          queryKey: expect.arrayContaining(['associations']),
        }),
      )
    })
  })

  it('closes dialog on success', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    renderDialog({ onOpenChange, partyId: 'current-party-001' })

    await user.type(screen.getByLabelText(/related party/i), 'Ac')
    await waitFor(() => expect(screen.getByText('Acme Corp')).toBeInTheDocument())
    await user.click(screen.getByText('Acme Corp'))
    await user.selectOptions(screen.getByLabelText(/relationship type/i), 'RELATIONSHIP_TYPE_GUARANTOR')
    await user.click(screen.getByRole('button', { name: /add association/i }))

    await waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false)
    })
  })

  it('includes effectiveFrom timestamp when provided', async () => {
    const user = userEvent.setup()
    renderDialog({ partyId: 'current-party-001' })

    await user.type(screen.getByLabelText(/related party/i), 'Ac')
    await waitFor(() => expect(screen.getByText('Acme Corp')).toBeInTheDocument())
    await user.click(screen.getByText('Acme Corp'))
    await user.selectOptions(screen.getByLabelText(/relationship type/i), 'RELATIONSHIP_TYPE_GUARANTOR')
    await user.type(screen.getByLabelText(/effective from/i), '2025-01-15')

    await user.click(screen.getByRole('button', { name: /add association/i }))

    await waitFor(() => {
      expect(mockRegisterAssociations).toHaveBeenCalledWith(
        expect.objectContaining({
          effectiveFrom: expect.objectContaining({ seconds: expect.any(BigInt) }),
        }),
      )
    })
  })

  it('disables submit button while mutation is pending', async () => {
    mockRegisterAssociations.mockImplementation(() => new Promise(() => {}))
    const user = userEvent.setup()
    renderDialog({ partyId: 'current-party-001' })

    await user.type(screen.getByLabelText(/related party/i), 'Ac')
    await waitFor(() => expect(screen.getByText('Acme Corp')).toBeInTheDocument())
    await user.click(screen.getByText('Acme Corp'))
    await user.selectOptions(screen.getByLabelText(/relationship type/i), 'RELATIONSHIP_TYPE_GUARANTOR')
    await user.click(screen.getByRole('button', { name: /add association/i }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /adding/i })).toBeDisabled()
    })
  })
})

describe('RegisterAssociationsDialog - error handling', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListParties.mockResolvedValue({ parties: mockParties })
  })

  it('shows general error banner for server errors', async () => {
    const user = userEvent.setup()
    const { Code, ConnectError } = await import('@connectrpc/connect')
    mockRegisterAssociations.mockRejectedValue(new ConnectError('server error', Code.Internal))

    renderDialog({ partyId: 'current-party-001' })

    await user.type(screen.getByLabelText(/related party/i), 'Ac')
    await waitFor(() => expect(screen.getByText('Acme Corp')).toBeInTheDocument())
    await user.click(screen.getByText('Acme Corp'))
    await user.selectOptions(screen.getByLabelText(/relationship type/i), 'RELATIONSHIP_TYPE_GUARANTOR')
    await user.click(screen.getByRole('button', { name: /add association/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })
  })
})

describe('RegisterAssociationsDialog - reset on close', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListParties.mockResolvedValue({ parties: mockParties })
    mockRegisterAssociations.mockResolvedValue({ associationId: 'assoc-123' })
  })

  it('clears form when dialog is closed and reopened', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    const { rerender } = renderDialog({ onOpenChange })

    await user.selectOptions(screen.getByLabelText(/relationship type/i), 'RELATIONSHIP_TYPE_GUARANTOR')

    rerender(
      <Wrapper>
        <RegisterAssociationsDialog open={false} onOpenChange={onOpenChange} partyId="current-party-001" />
      </Wrapper>,
    )

    rerender(
      <Wrapper>
        <RegisterAssociationsDialog open={true} onOpenChange={onOpenChange} partyId="current-party-001" />
      </Wrapper>,
    )

    const select = screen.getByLabelText(/relationship type/i) as HTMLSelectElement
    expect(select.value).toBe('')
  })

  it('closes dialog when cancel is clicked', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    renderDialog({ onOpenChange })

    await user.click(screen.getByRole('button', { name: /cancel/i }))

    expect(onOpenChange).toHaveBeenCalledWith(false)
  })
})
