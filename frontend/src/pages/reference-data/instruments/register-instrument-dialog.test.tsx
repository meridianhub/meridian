import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import { TooltipProvider } from '@/components/ui/tooltip'

const mockRegisterInstrument = vi.fn().mockResolvedValue({
  instrument: {
    id: 'aaaaaaaa-0000-0000-0000-000000000001',
    code: 'KWH',
    version: 1,
    dimension: 2,
    precision: 6,
    status: 1,
    displayName: 'Kilowatt Hour',
    description: '',
  },
})

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(() => ({
    referenceData: {
      registerInstrument: mockRegisterInstrument,
    },
  })),
}))

import { RegisterInstrumentDialog } from './register-instrument-dialog'

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity },
      mutations: { retry: false },
    },
  })
}

function Wrapper({ children }: { children: React.ReactNode }) {
  const qc = makeQueryClient()
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
      <RegisterInstrumentDialog open={props.open ?? true} onOpenChange={onOpenChange} />
    </Wrapper>,
  )
  return { onOpenChange }
}

describe('RegisterInstrumentDialog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders dialog title when open', () => {
    renderDialog()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /register instrument/i })).toBeInTheDocument()
  })

  it('does not render when closed', () => {
    renderDialog({ open: false })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders all required form fields', () => {
    renderDialog()
    expect(screen.getByLabelText(/code/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/display name/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/dimension/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/decimal places/i)).toBeInTheDocument()
  })

  it('renders optional description field', () => {
    renderDialog()
    expect(screen.getByLabelText(/description/i)).toBeInTheDocument()
  })

  it('renders Cancel and Register buttons', () => {
    renderDialog()
    expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /register instrument/i })).toBeInTheDocument()
  })

  it('auto-uppercases code field input', async () => {
    const user = userEvent.setup()
    renderDialog()
    const codeInput = screen.getByLabelText(/code/i)
    await user.type(codeInput, 'kwh')
    expect(codeInput).toHaveValue('KWH')
  })

  it('validates code is required on submit', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.click(screen.getByRole('button', { name: /register instrument/i }))
    expect(await screen.findByText(/code is required/i)).toBeInTheDocument()
  })

  it('validates code pattern - must match ^[A-Z][A-Z0-9_]*$', async () => {
    const user = userEvent.setup()
    renderDialog()
    const codeInput = screen.getByLabelText(/code/i)
    // Type lowercase then blur (auto-uppercase won't help if we set directly)
    await user.type(codeInput, '1INVALID')
    await user.click(screen.getByRole('button', { name: /register instrument/i }))
    expect(await screen.findByText(/invalid code format/i)).toBeInTheDocument()
  })

  it('validates display name is required on submit', async () => {
    const user = userEvent.setup()
    renderDialog()
    const codeInput = screen.getByLabelText(/code/i)
    await user.type(codeInput, 'KWH')
    await user.click(screen.getByRole('button', { name: /register instrument/i }))
    expect(await screen.findByText(/display name is required/i)).toBeInTheDocument()
  })

  it('validates dimension is required on submit', async () => {
    const user = userEvent.setup()
    renderDialog()
    const codeInput = screen.getByLabelText(/code/i)
    await user.type(codeInput, 'KWH')
    const displayNameInput = screen.getByLabelText(/display name/i)
    await user.type(displayNameInput, 'Kilowatt Hour')
    await user.click(screen.getByRole('button', { name: /register instrument/i }))
    expect(await screen.findByText(/dimension is required/i)).toBeInTheDocument()
  })

  it('validates decimal places range 0-18', async () => {
    const user = userEvent.setup()
    renderDialog()
    const decimalInput = screen.getByLabelText(/decimal places/i)
    await user.clear(decimalInput)
    await user.type(decimalInput, '19')
    await user.click(screen.getByRole('button', { name: /register instrument/i }))
    expect(await screen.findByText(/decimal places must be between 0 and 18/i)).toBeInTheDocument()
  })

  it('renders all dimension options in the select', () => {
    renderDialog()
    const dimensionSelect = screen.getByLabelText(/dimension/i)
    const options = Array.from(dimensionSelect.querySelectorAll('option')).map((o) => o.textContent)
    expect(options).toContain('Currency')
    expect(options).toContain('Energy')
    expect(options).toContain('Mass')
    expect(options).toContain('Volume')
    expect(options).toContain('Time')
    expect(options).toContain('Compute')
    expect(options).toContain('Carbon')
    expect(options).toContain('Data')
    expect(options).toContain('Count')
  })

  it('calls registerInstrument with correct data on valid submit', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/code/i), 'KWH')
    await user.type(screen.getByLabelText(/display name/i), 'Kilowatt Hour')
    await user.selectOptions(screen.getByLabelText(/dimension/i), '2') // Energy
    const decimalInput = screen.getByLabelText(/decimal places/i)
    await user.clear(decimalInput)
    await user.type(decimalInput, '6')
    await user.click(screen.getByRole('button', { name: /register instrument/i }))

    await waitFor(() => {
      expect(mockRegisterInstrument).toHaveBeenCalledWith(
        expect.objectContaining({
          code: 'KWH',
          displayName: 'Kilowatt Hour',
          dimension: 2,
          precision: 6,
        }),
      )
    })
  })

  it('shows DRAFT status notification after successful registration', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/code/i), 'KWH')
    await user.type(screen.getByLabelText(/display name/i), 'Kilowatt Hour')
    await user.selectOptions(screen.getByLabelText(/dimension/i), '2')
    await user.click(screen.getByRole('button', { name: /register instrument/i }))

    await waitFor(() => {
      expect(
        screen.getByText(/instrument created in draft status/i),
      ).toBeInTheDocument()
    })
  })

  it('closes dialog on Cancel button click', async () => {
    const user = userEvent.setup()
    const { onOpenChange } = renderDialog()
    await user.click(screen.getByRole('button', { name: /cancel/i }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('shows general error on API failure', async () => {
    mockRegisterInstrument.mockRejectedValueOnce(new Error('Server error'))
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/code/i), 'KWH')
    await user.type(screen.getByLabelText(/display name/i), 'Kilowatt Hour')
    await user.selectOptions(screen.getByLabelText(/dimension/i), '2')
    await user.click(screen.getByRole('button', { name: /register instrument/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })
  })

  it('resets form when dialog is closed and reopened', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()
    const { rerender } = render(
      <Wrapper>
        <RegisterInstrumentDialog open={true} onOpenChange={onOpenChange} />
      </Wrapper>,
    )

    await user.type(screen.getByLabelText(/code/i), 'KWH')
    expect(screen.getByLabelText(/code/i)).toHaveValue('KWH')

    rerender(
      <Wrapper>
        <RegisterInstrumentDialog open={false} onOpenChange={onOpenChange} />
      </Wrapper>,
    )
    rerender(
      <Wrapper>
        <RegisterInstrumentDialog open={true} onOpenChange={onOpenChange} />
      </Wrapper>,
    )

    expect(screen.getByLabelText(/code/i)).toHaveValue('')
  })
})
