import * as React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '@/test/test-utils'
import { RegisterPartyTypeDialog } from './register-party-type-dialog'

const mockRegisterPartyType = vi.fn()
const mockInvalidateQueries = vi.fn()

vi.mock('@/api/context', () => ({
  ApiClientProvider: ({ children }: { children: React.ReactNode }) => children,
  useApiClients: vi.fn(() => ({
    party: {
      registerPartyType: mockRegisterPartyType,
    },
  })),
  useClients: vi.fn(() => ({
    party: {
      registerPartyType: mockRegisterPartyType,
    },
  })),
}))

vi.mock('@tanstack/react-query', async () => {
  const actual = await vi.importActual('@tanstack/react-query')
  return {
    ...actual,
    useQueryClient: () => ({
      invalidateQueries: mockInvalidateQueries,
    }),
  }
})


function renderDialog(props: { open?: boolean; onOpenChange?: (open: boolean) => void } = {}) {
  const { open = true, onOpenChange = vi.fn() } = props
  return renderWithProviders(
    <RegisterPartyTypeDialog open={open} onOpenChange={onOpenChange} />,
  )
}

describe('RegisterPartyTypeDialog - rendering', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockRegisterPartyType.mockResolvedValue({ partyTypeDefinition: { id: 'pt-1', partyType: 'INDIVIDUAL' } })
  })

  it('does not render dialog content when closed', () => {
    renderDialog({ open: false })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders dialog content when open', () => {
    renderDialog()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /add party type/i })).toBeInTheDocument()
  })

  it('renders code and description fields', () => {
    renderDialog()
    expect(screen.getByLabelText(/code/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/description/i)).toBeInTheDocument()
  })

  it('renders submit and cancel buttons', () => {
    renderDialog()
    expect(screen.getByRole('button', { name: /add party type/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument()
  })

  it('shows code pattern hint text', () => {
    renderDialog()
    expect(screen.getByText(/uppercase letters, numbers, underscores/i)).toBeInTheDocument()
  })
})

describe('RegisterPartyTypeDialog - code auto-uppercase', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockRegisterPartyType.mockResolvedValue({ partyTypeDefinition: { id: 'pt-1', partyType: 'INDIVIDUAL' } })
  })

  it('transforms input to uppercase', async () => {
    const user = userEvent.setup()
    renderDialog()

    const codeInput = screen.getByLabelText(/code/i)
    await user.type(codeInput, 'individual')

    expect(codeInput).toHaveValue('INDIVIDUAL')
  })

  it('transforms mixed case to uppercase', async () => {
    const user = userEvent.setup()
    renderDialog()

    const codeInput = screen.getByLabelText(/code/i)
    await user.type(codeInput, 'Corp_Entity')

    expect(codeInput).toHaveValue('CORP_ENTITY')
  })
})

describe('RegisterPartyTypeDialog - code pattern validation', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockRegisterPartyType.mockResolvedValue({ partyTypeDefinition: { id: 'pt-1', partyType: 'INDIVIDUAL' } })
  })

  it('shows error when code is empty on submit', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.click(screen.getByRole('button', { name: /add party type/i }))

    await waitFor(() => {
      expect(screen.getByText(/code is required/i)).toBeInTheDocument()
    })
  })

  it('shows error when code is less than 2 characters', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/code/i), 'A')
    await user.click(screen.getByRole('button', { name: /add party type/i }))

    await waitFor(() => {
      expect(screen.getByText(/at least 2 characters/i)).toBeInTheDocument()
    })
  })

  it('shows error for code starting with digit (e.g. 1ABC)', async () => {
    const user = userEvent.setup()
    renderDialog()

    // Type lowercase so we can test the digit prefix after uppercase
    await user.type(screen.getByLabelText(/code/i), '1ABC')
    await user.click(screen.getByRole('button', { name: /add party type/i }))

    await waitFor(() => {
      expect(screen.getByText(/must start with.*uppercase letter/i)).toBeInTheDocument()
    })
  })

  it('accepts valid code INDIVIDUAL without validation error', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/code/i), 'INDIVIDUAL')
    await user.click(screen.getByRole('button', { name: /add party type/i }))

    // The mutation should be called (no validation error)
    await waitFor(() => {
      expect(mockRegisterPartyType).toHaveBeenCalled()
    })
    expect(screen.queryByText(/code is required/i)).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('accepts valid code CORP_ENTITY without validation error', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/code/i), 'corp_entity')
    await user.click(screen.getByRole('button', { name: /add party type/i }))

    // The mutation should be called (no validation error)
    await waitFor(() => {
      expect(mockRegisterPartyType).toHaveBeenCalled()
    })
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})

describe('RegisterPartyTypeDialog - description character limit', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockRegisterPartyType.mockResolvedValue({ partyTypeDefinition: { id: 'pt-1', partyType: 'INDIVIDUAL' } })
  })

  it('shows character count for description', () => {
    renderDialog()
    expect(screen.getByText('0/1000')).toBeInTheDocument()
  })

  it('updates character count as user types', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/description/i), 'Hello')

    expect(screen.getByText('5/1000')).toBeInTheDocument()
  })
})

describe('RegisterPartyTypeDialog - successful submission', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockRegisterPartyType.mockResolvedValue({ partyTypeDefinition: { id: 'pt-1', partyType: 'INDIVIDUAL' } })
  })

  it('submits with correct partyType value', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    renderDialog({ onOpenChange })

    await user.type(screen.getByLabelText(/code/i), 'individual')
    await user.click(screen.getByRole('button', { name: /add party type/i }))

    await waitFor(() => {
      expect(mockRegisterPartyType).toHaveBeenCalledWith(
        expect.objectContaining({
          partyType: 'INDIVIDUAL',
        }),
      )
    })
  })

  it('invalidates party-types query on success', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/code/i), 'INDIVIDUAL')
    await user.click(screen.getByRole('button', { name: /add party type/i }))

    await waitFor(() => {
      expect(mockInvalidateQueries).toHaveBeenCalledWith(
        expect.objectContaining({
          queryKey: expect.arrayContaining(['reference']),
        }),
      )
    })
  })

  it('closes dialog on success', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    renderDialog({ onOpenChange })

    await user.type(screen.getByLabelText(/code/i), 'INDIVIDUAL')
    await user.click(screen.getByRole('button', { name: /add party type/i }))

    await waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false)
    })
  })

  it('disables submit button while pending', async () => {
    mockRegisterPartyType.mockImplementation(() => new Promise(() => {}))
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/code/i), 'INDIVIDUAL')
    await user.click(screen.getByRole('button', { name: /add party type/i }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /adding/i })).toBeDisabled()
    })
  })
})

describe('RegisterPartyTypeDialog - error handling', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows field-level error for INVALID_ARGUMENT on party_type field', async () => {
    const user = userEvent.setup()
    const { Code, ConnectError } = await import('@connectrpc/connect')
    const err = new ConnectError('invalid', Code.InvalidArgument)
    err.details = [
      {
        type: 'google.rpc.BadRequest',
        value: new Uint8Array(),
        debug: {
          fieldViolations: [{ field: 'party_type', description: 'Party type code is reserved' }],
        },
      },
    ]
    mockRegisterPartyType.mockRejectedValue(err)

    renderDialog()

    await user.type(screen.getByLabelText(/code/i), 'RESERVED')
    await user.click(screen.getByRole('button', { name: /add party type/i }))

    await waitFor(() => {
      expect(screen.getByText('Party type code is reserved')).toBeInTheDocument()
    })
  })

  it('shows duplicate code error for ALREADY_EXISTS', async () => {
    const user = userEvent.setup()
    const { Code, ConnectError } = await import('@connectrpc/connect')
    const err = new ConnectError('already exists', Code.AlreadyExists)
    mockRegisterPartyType.mockRejectedValue(err)

    renderDialog()

    await user.type(screen.getByLabelText(/code/i), 'INDIVIDUAL')
    await user.click(screen.getByRole('button', { name: /add party type/i }))

    await waitFor(() => {
      expect(screen.getByText(/already exists/i)).toBeInTheDocument()
    })
  })

  it('shows general error banner for server errors', async () => {
    const user = userEvent.setup()
    const { Code, ConnectError } = await import('@connectrpc/connect')
    const err = new ConnectError('server error', Code.Internal)
    mockRegisterPartyType.mockRejectedValue(err)

    renderDialog()

    await user.type(screen.getByLabelText(/code/i), 'INDIVIDUAL')
    await user.click(screen.getByRole('button', { name: /add party type/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })
  })
})

describe('RegisterPartyTypeDialog - reset on close', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockRegisterPartyType.mockResolvedValue({ partyTypeDefinition: { id: 'pt-1', partyType: 'INDIVIDUAL' } })
  })

  it('clears form when dialog is closed', async () => {
    const user = userEvent.setup()
    const { rerender } = renderDialog()

    await user.type(screen.getByLabelText(/code/i), 'INDIVIDUAL')

    rerender(<RegisterPartyTypeDialog open={false} onOpenChange={vi.fn()} />)
    rerender(<RegisterPartyTypeDialog open={true} onOpenChange={vi.fn()} />)

    expect(screen.getByLabelText(/code/i)).toHaveValue('')
  })

  it('closes dialog when cancel is clicked', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    renderDialog({ onOpenChange })

    await user.click(screen.getByRole('button', { name: /cancel/i }))

    expect(onOpenChange).toHaveBeenCalledWith(false)
  })
})
