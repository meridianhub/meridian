import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { CreateInternalAccountDialog } from './create-internal-account-dialog'

const mockNavigate = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

const mockInitiateInternalBankAccount = vi.fn()
const mockListActive = vi.fn()
const mockListInstruments = vi.fn()
const mockInvalidateQueries = vi.fn()

vi.mock('@/api/context', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/context')>()
  return {
    ...actual,
    useApiClients: vi.fn(() => ({
      internalBankAccount: {
        initiateInternalBankAccount: mockInitiateInternalBankAccount,
      },
      accountTypeRegistry: {
        listActive: mockListActive,
      },
      referenceData: {
        listInstruments: mockListInstruments,
      },
    })),
    useClients: vi.fn(() => ({
      internalBankAccount: {
        initiateInternalBankAccount: mockInitiateInternalBankAccount,
      },
      accountTypeRegistry: {
        listActive: mockListActive,
      },
      referenceData: {
        listInstruments: mockListInstruments,
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
}))

// Internal account types: behaviorClass 1 = Customer (excluded), 2+ = internal
const mockAccountTypes = [
  { id: 'at-1', code: 'CLEARING_GBP', displayName: 'GBP Clearing', behaviorClass: 2, status: 2 },
  { id: 'at-2', code: 'NOSTRO_USD', displayName: 'USD Nostro', behaviorClass: 3, status: 2 },
  { id: 'at-3', code: 'CUSTOMER_CURRENT', displayName: 'Customer Current', behaviorClass: 1, status: 2 },
  { id: 'at-4', code: 'SUSPENSE_GBP', displayName: 'GBP Suspense', behaviorClass: 6, status: 2 },
]

const mockInstruments = [
  { code: 'GBP', displayName: 'British Pound' },
  { code: 'USD', displayName: 'US Dollar' },
  { code: 'EUR', displayName: 'Euro' },
]

function renderDialog(props: { open?: boolean; onOpenChange?: (open: boolean) => void } = {}) {
  const { open = true, onOpenChange = vi.fn() } = props
  return renderWithProviders(
    <MemoryRouter>
      <CreateInternalAccountDialog open={open} onOpenChange={onOpenChange} />
    </MemoryRouter>,
  )
}

describe('CreateInternalAccountDialog - rendering', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListActive.mockResolvedValue({ definitions: mockAccountTypes })
    mockListInstruments.mockResolvedValue({ instruments: mockInstruments })
    mockInitiateInternalBankAccount.mockResolvedValue({ accountId: 'acc-new-123' })
  })

  it('does not render dialog content when closed', () => {
    renderDialog({ open: false })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders dialog content when open', () => {
    renderDialog()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /new internal account/i })).toBeInTheDocument()
  })

  it('renders all required form fields', () => {
    renderDialog()
    expect(screen.getByLabelText(/account name/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/account code/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/account type/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/instrument/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/description/i)).toBeInTheDocument()
  })

  it('renders submit and cancel buttons', () => {
    renderDialog()
    expect(screen.getByRole('button', { name: /create account/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument()
  })
})

describe('CreateInternalAccountDialog - account type filtering', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListInstruments.mockResolvedValue({ instruments: mockInstruments })
  })

  it('filters out customer behavior class (1), shows only internal classes', async () => {
    mockListActive.mockResolvedValue({ definitions: mockAccountTypes })
    renderDialog()

    await waitFor(() => {
      const select = screen.getByLabelText(/account type/i) as HTMLSelectElement
      const options = Array.from(select.options).map((o) => o.value)
      expect(options).toContain('CLEARING_GBP')
      expect(options).toContain('NOSTRO_USD')
      expect(options).toContain('SUSPENSE_GBP')
      expect(options).not.toContain('CUSTOMER_CURRENT')
    })
  })

  it('shows helper message when no internal account types are configured', async () => {
    mockListActive.mockResolvedValue({ definitions: [] })
    renderDialog()

    await waitFor(() => {
      expect(screen.getByText(/no internal account types have been configured/i)).toBeInTheDocument()
    })
  })

  it('shows loading state while fetching account types', () => {
    mockListActive.mockReturnValue(new Promise(() => {}))
    renderDialog()

    const select = screen.getByLabelText(/account type/i) as HTMLSelectElement
    expect(select).toBeDisabled()
    expect(screen.getByText(/loading account types/i)).toBeInTheDocument()
  })
})

describe('CreateInternalAccountDialog - instruments query', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListActive.mockResolvedValue({ definitions: mockAccountTypes })
  })

  it('populates instrument select with active instruments', async () => {
    mockListInstruments.mockResolvedValue({ instruments: mockInstruments })
    renderDialog()

    await waitFor(() => {
      const select = screen.getByLabelText(/instrument/i) as HTMLSelectElement
      const options = Array.from(select.options).map((o) => o.value)
      expect(options).toContain('GBP')
      expect(options).toContain('USD')
      expect(options).toContain('EUR')
    })
  })

  it('calls listInstruments with status filter 2 (ACTIVE)', async () => {
    mockListInstruments.mockResolvedValue({ instruments: mockInstruments })
    renderDialog()

    await waitFor(() => {
      expect(mockListInstruments).toHaveBeenCalledWith({ statusFilter: 2 })
    })
  })

  it('shows loading state while fetching instruments', () => {
    mockListInstruments.mockReturnValue(new Promise(() => {}))
    renderDialog()

    const select = screen.getByLabelText(/instrument/i) as HTMLSelectElement
    expect(select).toBeDisabled()
    expect(screen.getByText(/loading instruments/i)).toBeInTheDocument()
  })
})

describe('CreateInternalAccountDialog - validation', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListActive.mockResolvedValue({ definitions: mockAccountTypes })
    mockListInstruments.mockResolvedValue({ instruments: mockInstruments })
  })

  it('shows error when account name is empty on submit', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByText(/account name is required/i)).toBeInTheDocument()
    })
  })

  it('shows error when account code is empty on submit', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/account name/i), 'Test Account')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByText(/account code is required/i)).toBeInTheDocument()
    })
  })

  it('shows error when account type is not selected on submit', async () => {
    const user = userEvent.setup()
    renderDialog()

    await waitFor(() => {
      expect(screen.getByRole('combobox', { name: /account type/i })).toBeInTheDocument()
    })

    await user.type(screen.getByLabelText(/account name/i), 'Test Account')
    await user.type(screen.getByLabelText(/account code/i), 'CLR-TEST-001')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByText(/account type is required/i)).toBeInTheDocument()
    })
  })

  it('shows error when instrument is not selected on submit', async () => {
    const user = userEvent.setup()
    renderDialog()

    await waitFor(() => {
      expect(screen.getAllByRole('combobox').length).toBeGreaterThanOrEqual(2)
    })

    await user.type(screen.getByLabelText(/account name/i), 'Test Account')
    await user.type(screen.getByLabelText(/account code/i), 'CLR-TEST-001')
    await user.selectOptions(screen.getByLabelText(/account type/i), 'CLEARING_GBP')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByText(/instrument is required/i)).toBeInTheDocument()
    })
  })

  it('shows error for invalid account code format (lowercase)', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/account name/i), 'Test Account')
    await user.type(screen.getByLabelText(/account code/i), 'clr-test-001')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByText(/uppercase letters, digits, underscores, or hyphens/i)).toBeInTheDocument()
    })
  })

  it('shows error when description exceeds 1000 characters', async () => {
    const user = userEvent.setup()
    renderDialog()

    await waitFor(() => {
      expect(screen.getAllByRole('combobox').length).toBeGreaterThanOrEqual(2)
    })

    await user.type(screen.getByLabelText(/account name/i), 'Test Account')
    await user.type(screen.getByLabelText(/account code/i), 'CLR-TEST-001')
    await user.selectOptions(screen.getByLabelText(/account type/i), 'CLEARING_GBP')
    await user.selectOptions(screen.getByLabelText(/instrument/i), 'GBP')

    // Use fireEvent to avoid slow typing of 1001 chars
    fireEvent.change(screen.getByLabelText(/description/i), {
      target: { value: 'x'.repeat(1001) },
    })

    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByText(/1000 characters or fewer/i)).toBeInTheDocument()
    })
  })
})

describe('CreateInternalAccountDialog - successful submission', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListActive.mockResolvedValue({ definitions: mockAccountTypes })
    mockListInstruments.mockResolvedValue({ instruments: mockInstruments })
    mockInitiateInternalBankAccount.mockResolvedValue({ accountId: 'acc-new-123' })
  })

  it('submits form and navigates to account detail page on success', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    renderDialog({ onOpenChange })

    await waitFor(() => {
      expect(screen.getAllByRole('combobox').length).toBeGreaterThanOrEqual(2)
    })

    await user.type(screen.getByLabelText(/account name/i), 'GBP Clearing')
    await user.type(screen.getByLabelText(/account code/i), 'CLR-GBP-001')
    await user.selectOptions(screen.getByLabelText(/account type/i), 'CLEARING_GBP')
    await user.selectOptions(screen.getByLabelText(/instrument/i), 'GBP')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(mockInitiateInternalBankAccount).toHaveBeenCalledWith(
        expect.objectContaining({
          name: 'GBP Clearing',
          accountCode: 'CLR-GBP-001',
          productTypeCode: 'CLEARING_GBP',
          instrumentCode: 'GBP',
        }),
      )
    })

    await waitFor(() => {
      expect(mockInvalidateQueries).toHaveBeenCalledWith(
        expect.objectContaining({
          queryKey: expect.arrayContaining(['internal-accounts']),
        }),
      )
      expect(onOpenChange).toHaveBeenCalledWith(false)
      expect(mockNavigate).toHaveBeenCalledWith('/internal-accounts/acc-new-123')
    })
  })

  it('disables submit button while pending', async () => {
    mockInitiateInternalBankAccount.mockImplementation(() => new Promise(() => {}))
    const user = userEvent.setup()
    renderDialog()

    await waitFor(() => {
      expect(screen.getAllByRole('combobox').length).toBeGreaterThanOrEqual(2)
    })

    await user.type(screen.getByLabelText(/account name/i), 'GBP Clearing')
    await user.type(screen.getByLabelText(/account code/i), 'CLR-GBP-001')
    await user.selectOptions(screen.getByLabelText(/account type/i), 'CLEARING_GBP')
    await user.selectOptions(screen.getByLabelText(/instrument/i), 'GBP')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /creating/i })).toBeDisabled()
    })
  })
})

describe('CreateInternalAccountDialog - error handling', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListActive.mockResolvedValue({ definitions: mockAccountTypes })
    mockListInstruments.mockResolvedValue({ instruments: mockInstruments })
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
          fieldViolations: [{ field: 'account_code', description: 'Account code already exists' }],
        },
      },
    ]
    mockInitiateInternalBankAccount.mockRejectedValue(err)

    renderDialog()

    await waitFor(() => {
      expect(screen.getAllByRole('combobox').length).toBeGreaterThanOrEqual(2)
    })

    await user.type(screen.getByLabelText(/account name/i), 'GBP Clearing')
    await user.type(screen.getByLabelText(/account code/i), 'CLR-GBP-001')
    await user.selectOptions(screen.getByLabelText(/account type/i), 'CLEARING_GBP')
    await user.selectOptions(screen.getByLabelText(/instrument/i), 'GBP')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByText('Account code already exists')).toBeInTheDocument()
    })
  })

  it('shows banner error for general server errors', async () => {
    const user = userEvent.setup()
    const { Code, ConnectError } = await import('@connectrpc/connect')
    const err = new ConnectError('server error', Code.Internal)
    mockInitiateInternalBankAccount.mockRejectedValue(err)

    renderDialog()

    await waitFor(() => {
      expect(screen.getAllByRole('combobox').length).toBeGreaterThanOrEqual(2)
    })

    await user.type(screen.getByLabelText(/account name/i), 'GBP Clearing')
    await user.type(screen.getByLabelText(/account code/i), 'CLR-GBP-001')
    await user.selectOptions(screen.getByLabelText(/account type/i), 'CLEARING_GBP')
    await user.selectOptions(screen.getByLabelText(/instrument/i), 'GBP')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })
  })
})

describe('CreateInternalAccountDialog - cache invalidation', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListActive.mockResolvedValue({ definitions: mockAccountTypes })
    mockListInstruments.mockResolvedValue({ instruments: mockInstruments })
    mockInitiateInternalBankAccount.mockResolvedValue({ accountId: 'acc-new-456' })
  })

  it('invalidates internal-accounts query key on success', async () => {
    const user = userEvent.setup()
    renderDialog()

    await waitFor(() => {
      expect(screen.getAllByRole('combobox').length).toBeGreaterThanOrEqual(2)
    })

    await user.type(screen.getByLabelText(/account name/i), 'GBP Clearing')
    await user.type(screen.getByLabelText(/account code/i), 'CLR-GBP-001')
    await user.selectOptions(screen.getByLabelText(/account type/i), 'CLEARING_GBP')
    await user.selectOptions(screen.getByLabelText(/instrument/i), 'GBP')
    await user.click(screen.getByRole('button', { name: /create account/i }))

    await waitFor(() => {
      expect(mockInvalidateQueries).toHaveBeenCalledWith(
        expect.objectContaining({
          queryKey: expect.arrayContaining(['internal-accounts']),
        }),
      )
    })
  })
})

describe('CreateInternalAccountDialog - reset on close', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListActive.mockResolvedValue({ definitions: mockAccountTypes })
    mockListInstruments.mockResolvedValue({ instruments: mockInstruments })
  })

  it('clears form when dialog is closed', async () => {
    const user = userEvent.setup()
    const { rerender } = renderDialog()

    await user.type(screen.getByLabelText(/account name/i), 'Filled In')

    rerender(
      <MemoryRouter>
        <CreateInternalAccountDialog open={false} onOpenChange={vi.fn()} />
      </MemoryRouter>,
    )

    rerender(
      <MemoryRouter>
        <CreateInternalAccountDialog open={true} onOpenChange={vi.fn()} />
      </MemoryRouter>,
    )

    expect(screen.getByLabelText(/account name/i)).toHaveValue('')
  })

  it('closes dialog when cancel is clicked', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    renderDialog({ onOpenChange })

    await user.click(screen.getByRole('button', { name: /cancel/i }))

    expect(onOpenChange).toHaveBeenCalledWith(false)
  })
})
