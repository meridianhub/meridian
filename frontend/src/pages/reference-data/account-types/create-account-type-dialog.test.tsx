import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import { TooltipProvider } from '@/components/ui/tooltip'

const mockCreateDraft = vi.hoisted(() =>
  vi.fn().mockResolvedValue({
    definition: {
      id: 'aaaaaaaa-0000-0000-0000-000000000001',
      code: 'CUSTOMER_CURRENT',
      version: 1,
      status: 1, // DRAFT
    },
  }),
)

const mockListInstruments = vi.hoisted(() =>
  vi.fn().mockResolvedValue({
    instruments: [
      { code: 'GBP', displayName: 'British Pound', status: 2 },
      { code: 'USD', displayName: 'US Dollar', status: 2 },
      { code: 'KWH', displayName: 'Kilowatt Hour', status: 2 },
    ],
    nextPageToken: '',
  }),
)

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(() => ({
    accountTypeRegistry: {
      createDraft: mockCreateDraft,
    },
    referenceData: {
      listInstruments: mockListInstruments,
    },
  })),
}))

import { CreateAccountTypeDialog } from './create-account-type-dialog'

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity },
      mutations: { retry: false },
    },
  })
}

function Wrapper({ children, queryClient }: { children: React.ReactNode; queryClient?: QueryClient }) {
  const qc = queryClient ?? makeQueryClient()
  return (
    <QueryClientProvider client={qc}>
      <TooltipProvider>
        <BrowserRouter>{children}</BrowserRouter>
      </TooltipProvider>
    </QueryClientProvider>
  )
}

function renderDialog(props: { open?: boolean; onOpenChange?: (open: boolean) => void } = {}) {
  const onOpenChange = props.onOpenChange ?? vi.fn()
  render(
    <Wrapper>
      <CreateAccountTypeDialog open={props.open ?? true} onOpenChange={onOpenChange} />
    </Wrapper>,
  )
  return { onOpenChange }
}

async function fillRequiredFields(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText(/code/i), 'CUSTOMER')
  await user.type(screen.getByLabelText(/display name/i), 'Customer Account')
  await user.selectOptions(screen.getByLabelText(/normal balance/i), '2') // Credit
  await user.selectOptions(screen.getByLabelText(/behavior class/i), '1') // Customer
  await waitFor(() => {
    expect(screen.getByLabelText(/instrument/i)).not.toBeDisabled()
  })
  await user.selectOptions(screen.getByLabelText(/instrument/i), 'GBP')
}

describe('CreateAccountTypeDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListInstruments.mockResolvedValue({
      instruments: [
        { code: 'GBP', displayName: 'British Pound', status: 2 },
        { code: 'USD', displayName: 'US Dollar', status: 2 },
        { code: 'KWH', displayName: 'Kilowatt Hour', status: 2 },
      ],
      nextPageToken: '',
    })
  })

  it('renders dialog title when open', () => {
    renderDialog()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /create account type/i })).toBeInTheDocument()
  })

  it('does not render when closed', () => {
    renderDialog({ open: false })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders all required form fields', () => {
    renderDialog()
    expect(screen.getByLabelText(/code/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/display name/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/normal balance/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/behavior class/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/instrument/i)).toBeInTheDocument()
  })

  it('renders optional fields', () => {
    renderDialog()
    expect(screen.getByLabelText(/default saga prefix/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/description/i)).toBeInTheDocument()
  })

  it('renders Cancel and Create buttons', () => {
    renderDialog()
    expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /create account type/i })).toBeInTheDocument()
  })

  it('auto-uppercases code field input', async () => {
    const user = userEvent.setup()
    renderDialog()
    const codeInput = screen.getByLabelText(/code/i)
    await user.type(codeInput, 'customer')
    expect(codeInput).toHaveValue('CUSTOMER')
  })

  it('validates code is required on submit', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.click(screen.getByRole('button', { name: /create account type/i }))
    expect(await screen.findByText(/code is required/i)).toBeInTheDocument()
  })

  it('validates code pattern — must start with uppercase letter', async () => {
    const user = userEvent.setup()
    renderDialog()
    const codeInput = screen.getByLabelText(/code/i)
    await user.type(codeInput, '1INVALID')
    await user.click(screen.getByRole('button', { name: /create account type/i }))
    expect(await screen.findByText(/invalid code format/i)).toBeInTheDocument()
  })

  it('validates code with trailing underscore as invalid', async () => {
    const user = userEvent.setup()
    renderDialog()
    const codeInput = screen.getByLabelText(/code/i)
    // Trailing underscore is rejected by proto pattern
    await user.type(codeInput, 'CUSTOMER_')
    await user.click(screen.getByRole('button', { name: /create account type/i }))
    expect(await screen.findByText(/invalid code format/i)).toBeInTheDocument()
  })

  it('validates display name is required on submit', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.type(screen.getByLabelText(/code/i), 'CUSTOMER')
    await user.click(screen.getByRole('button', { name: /create account type/i }))
    expect(await screen.findByText(/display name is required/i)).toBeInTheDocument()
  })

  it('validates normal balance is required on submit', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.type(screen.getByLabelText(/code/i), 'CUSTOMER')
    await user.type(screen.getByLabelText(/display name/i), 'Customer Account')
    await user.click(screen.getByRole('button', { name: /create account type/i }))
    expect(await screen.findByText(/normal balance is required/i)).toBeInTheDocument()
  })

  it('validates behavior class is required on submit', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.type(screen.getByLabelText(/code/i), 'CUSTOMER')
    await user.type(screen.getByLabelText(/display name/i), 'Customer Account')
    await user.selectOptions(screen.getByLabelText(/normal balance/i), '2')
    await user.click(screen.getByRole('button', { name: /create account type/i }))
    expect(await screen.findByText(/behavior class is required/i)).toBeInTheDocument()
  })

  it('validates instrument is required on submit', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.type(screen.getByLabelText(/code/i), 'CUSTOMER')
    await user.type(screen.getByLabelText(/display name/i), 'Customer Account')
    await user.selectOptions(screen.getByLabelText(/normal balance/i), '2')
    await user.selectOptions(screen.getByLabelText(/behavior class/i), '1')
    await user.click(screen.getByRole('button', { name: /create account type/i }))
    expect(await screen.findByText(/instrument is required/i)).toBeInTheDocument()
  })

  it('validates saga prefix pattern when provided', async () => {
    const user = userEvent.setup()
    renderDialog()
    await fillRequiredFields(user)
    await user.type(screen.getByLabelText(/default saga prefix/i), 'InvalidPrefix')
    await user.click(screen.getByRole('button', { name: /create account type/i }))
    expect(await screen.findByText(/saga prefix must start with a lowercase letter/i)).toBeInTheDocument()
  })

  it('accepts valid lowercase saga prefix', async () => {
    const user = userEvent.setup()
    renderDialog()
    await fillRequiredFields(user)
    await user.type(screen.getByLabelText(/default saga prefix/i), 'savings')
    await user.click(screen.getByRole('button', { name: /create account type/i }))
    expect(screen.queryByText(/saga prefix must start/i)).not.toBeInTheDocument()
  })

  it('renders DEBIT and CREDIT normal balance options', () => {
    renderDialog()
    const balanceSelect = screen.getByLabelText(/normal balance/i)
    const options = Array.from(balanceSelect.querySelectorAll('option')).map((o) => o.textContent)
    expect(options).toContain('Debit')
    expect(options).toContain('Credit')
  })

  it('renders all behavior class options', () => {
    renderDialog()
    const behaviorSelect = screen.getByLabelText(/behavior class/i)
    const options = Array.from(behaviorSelect.querySelectorAll('option')).map((o) => o.textContent)
    expect(options).toContain('Customer')
    expect(options).toContain('Clearing')
    expect(options).toContain('Nostro')
    expect(options).toContain('Vostro')
    expect(options).toContain('Holding')
    expect(options).toContain('Suspense')
    expect(options).toContain('Revenue')
    expect(options).toContain('Expense')
    expect(options).toContain('Inventory')
  })

  it('loads and shows only ACTIVE instruments in dropdown', async () => {
    renderDialog()
    await waitFor(() => {
      expect(mockListInstruments).toHaveBeenCalledWith(expect.objectContaining({ statusFilter: 2 }))
    })
    await waitFor(() => {
      const instrumentSelect = screen.getByLabelText(/instrument/i)
      expect(instrumentSelect).not.toBeDisabled()
      const options = Array.from(instrumentSelect.querySelectorAll('option')).map((o) => o.textContent)
      expect(options.some((o) => o?.includes('GBP'))).toBe(true)
      expect(options.some((o) => o?.includes('USD'))).toBe(true)
    })
  })

  it('shows loading state for instruments while fetching', () => {
    mockListInstruments.mockImplementation(() => new Promise(() => {})) // never resolves
    renderDialog()
    const instrumentSelect = screen.getByLabelText(/instrument/i)
    expect(instrumentSelect).toBeDisabled()
    expect(screen.getByText(/loading instruments/i)).toBeInTheDocument()
  })

  it('renders CEL editor components', () => {
    renderDialog()
    expect(screen.getByText(/validation cel/i)).toBeInTheDocument()
    expect(screen.getByText(/bucketing cel/i)).toBeInTheDocument()
    expect(screen.getByText(/eligibility cel/i)).toBeInTheDocument()
  })

  it('calls createDraft with correct data on valid submit', async () => {
    const user = userEvent.setup()
    renderDialog()
    await fillRequiredFields(user)
    await user.click(screen.getByRole('button', { name: /create account type/i }))

    await waitFor(() => {
      expect(mockCreateDraft).toHaveBeenCalledWith(
        expect.objectContaining({
          code: 'CUSTOMER',
          displayName: 'Customer Account',
          normalBalance: 2,
          behaviorClass: 1,
          instrumentCode: 'GBP',
        }),
      )
    })
  })

  it('shows DRAFT status note after successful creation', async () => {
    const user = userEvent.setup()
    renderDialog()
    await fillRequiredFields(user)
    await user.click(screen.getByRole('button', { name: /create account type/i }))

    await waitFor(() => {
      expect(screen.getByRole('status')).toBeInTheDocument()
      expect(screen.getByText(/activation required before it can be used/i)).toBeInTheDocument()
    })
  })

  it('clears form fields after successful creation', async () => {
    const user = userEvent.setup()
    renderDialog()
    await fillRequiredFields(user)
    await user.click(screen.getByRole('button', { name: /create account type/i }))

    await waitFor(() => {
      expect(screen.getByRole('status')).toBeInTheDocument()
    })

    expect(screen.getByLabelText(/code/i)).toHaveValue('')
    expect(screen.getByLabelText(/display name/i)).toHaveValue('')
  })

  it('invalidates account-types query after successful creation', async () => {
    const user = userEvent.setup()
    const qc = makeQueryClient()
    vi.spyOn(qc, 'invalidateQueries')

    render(
      <QueryClientProvider client={qc}>
        <TooltipProvider>
          <BrowserRouter>
            <CreateAccountTypeDialog open={true} onOpenChange={vi.fn()} />
          </BrowserRouter>
        </TooltipProvider>
      </QueryClientProvider>,
    )

    await fillRequiredFields(user)
    await user.click(screen.getByRole('button', { name: /create account type/i }))

    await waitFor(() => {
      expect(qc.invalidateQueries).toHaveBeenCalledWith(
        expect.objectContaining({
          queryKey: expect.arrayContaining(['reference', 'account-types']),
        }),
      )
    })
  })

  it('closes dialog on Cancel button click', async () => {
    const user = userEvent.setup()
    const { onOpenChange } = renderDialog()
    await user.click(screen.getByRole('button', { name: /cancel/i }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('shows general error on API failure', async () => {
    mockCreateDraft.mockRejectedValueOnce(new Error('Server error'))
    const user = userEvent.setup()
    renderDialog()
    await fillRequiredFields(user)
    await user.click(screen.getByRole('button', { name: /create account type/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })
  })

  it('resets form when dialog is closed and reopened', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    const qc = makeQueryClient()
    const { rerender } = render(
      <Wrapper queryClient={qc}>
        <CreateAccountTypeDialog open={true} onOpenChange={onOpenChange} />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/code/i), 'CUSTOMER')
    expect(screen.getByLabelText(/code/i)).toHaveValue('CUSTOMER')

    rerender(
      <Wrapper queryClient={qc}>
        <CreateAccountTypeDialog open={false} onOpenChange={onOpenChange} />
      </Wrapper>,
    )
    rerender(
      <Wrapper queryClient={qc}>
        <CreateAccountTypeDialog open={true} onOpenChange={onOpenChange} />
      </Wrapper>,
    )

    expect(screen.getByLabelText(/code/i)).toHaveValue('')
  })

  it('shows saga prefix helper text', () => {
    renderDialog()
    expect(screen.getByText(/sagas for this account type will be resolved as/i)).toBeInTheDocument()
    expect(screen.getByText(/savings.withdraw/i)).toBeInTheDocument()
  })
})
