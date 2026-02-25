import * as React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { RegisterDataSetDialog } from './register-dataset-dialog'
import { CATEGORY_OPTIONS } from './constants'

vi.mock('@/api/transport', () => ({
  createTenantTransport: vi.fn(() => ({ __type: 'mock-transport' })),
}))

vi.mock('@/api/clients', () => ({
  createServiceClients: vi.fn(() => ({
    currentAccount: {},
    paymentOrder: {},
    financialAccounting: {},
    positionKeeping: {},
    accountReconciliation: {},
    party: {},
    tenant: {},
    sagaRegistry: {},
    sagaAdmin: {},
    referenceData: {},
    accountTypeRegistry: {},
    node: {},
    internalAccount: {},
    marketInformation: {
      registerDataSet: vi.fn(),
    },
    mapping: {},
    forecasting: {},
    manifestHistory: {},
    manifestApplier: {},
  })),
}))

import { createServiceClients } from '@/api/clients'

function setupMock(registerDataSet: ReturnType<typeof vi.fn> = vi.fn()) {
  vi.mocked(createServiceClients).mockReturnValue({
    currentAccount: {} as never,
    paymentOrder: {} as never,
    financialAccounting: {} as never,
    positionKeeping: {} as never,
    accountReconciliation: {} as never,
    party: {} as never,
    tenant: {} as never,
    sagaRegistry: {} as never,
    sagaAdmin: {} as never,
    referenceData: {} as never,
    accountTypeRegistry: {} as never,
    node: {} as never,
    internalAccount: {} as never,
    marketInformation: {
      registerDataSet,
    } as never,
    mapping: {} as never,
    forecasting: {} as never,
    manifestHistory: {} as never,
    manifestApplier: {} as never,
  })
}

function renderDialog(open = true, onOpenChange = vi.fn()) {
  return renderWithProviders(
    <MemoryRouter>
      <RegisterDataSetDialog open={open} onOpenChange={onOpenChange} />
    </MemoryRouter>,
    { initialToken: createTenantUserToken('tenant-001') },
  )
}

describe('RegisterDataSetDialog - rendering', () => {
  beforeEach(() => {
    setupMock()
  })

  it('does not render dialog content when closed', () => {
    renderDialog(false)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders dialog when open', () => {
    renderDialog()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: /register data set/i })).toBeInTheDocument()
  })

  it('renders all form fields', () => {
    renderDialog()
    expect(screen.getByLabelText(/^code$/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/display name/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/^category$/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/^unit$/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/description/i)).toBeInTheDocument()
  })

  it('renders submit and cancel buttons', () => {
    renderDialog()
    expect(screen.getByRole('button', { name: /register data set/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument()
  })
})

describe('RegisterDataSetDialog - category options', () => {
  beforeEach(() => {
    setupMock()
  })

  it('renders all CATEGORY_OPTIONS in the select', () => {
    renderDialog()
    const select = screen.getByLabelText(/^category$/i)
    for (const opt of CATEGORY_OPTIONS) {
      expect(select).toContainElement(
        screen.getByRole('option', { name: opt.label }),
      )
    }
  })

  it('renders 10 category options plus the placeholder', () => {
    renderDialog()
    const select = screen.getByLabelText(/^category$/i) as HTMLSelectElement
    // 10 categories + 1 placeholder
    expect(select.options).toHaveLength(11)
  })
})

describe('RegisterDataSetDialog - code validation', () => {
  beforeEach(() => {
    setupMock()
  })

  it('shows error when code is empty', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.click(screen.getByRole('button', { name: /register data set/i }))
    await waitFor(() => {
      expect(screen.getByText(/code is required/i)).toBeInTheDocument()
    })
  })

  it('shows error for code shorter than 2 characters', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.type(screen.getByLabelText(/^code$/i), 'A')
    await user.click(screen.getByRole('button', { name: /register data set/i }))
    await waitFor(() => {
      expect(screen.getByText(/2.50 characters/i)).toBeInTheDocument()
    })
  })

  it('shows error for code that does not match pattern (lowercase)', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.type(screen.getByLabelText(/^code$/i), 'invalid_code')
    await user.click(screen.getByRole('button', { name: /register data set/i }))
    await waitFor(() => {
      expect(screen.getByText(/must start with a letter/i)).toBeInTheDocument()
    })
  })

  it('accepts valid code matching pattern', async () => {
    const user = userEvent.setup()
    setupMock(vi.fn().mockResolvedValue({ dataset: { code: 'USD_EUR_FX' } }))
    renderDialog()
    await user.type(screen.getByLabelText(/^code$/i), 'USD_EUR_FX')
    await user.type(screen.getByLabelText(/display name/i), 'USD/EUR FX Rate')
    await user.selectOptions(screen.getByLabelText(/^category$/i), '1')
    await user.type(screen.getByLabelText(/^unit$/i), 'USD/EUR')
    await user.click(screen.getByRole('button', { name: /register data set/i }))
    await waitFor(() => {
      expect(screen.queryByText(/must start with a letter/i)).not.toBeInTheDocument()
      expect(screen.queryByText(/code is required/i)).not.toBeInTheDocument()
    })
  })
})

describe('RegisterDataSetDialog - required field validation', () => {
  beforeEach(() => {
    setupMock()
  })

  it('shows error when display name is empty', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.type(screen.getByLabelText(/^code$/i), 'USD_EUR_FX')
    await user.click(screen.getByRole('button', { name: /register data set/i }))
    await waitFor(() => {
      expect(screen.getByText(/display name is required/i)).toBeInTheDocument()
    })
  })

  it('shows error when category is not selected', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.type(screen.getByLabelText(/^code$/i), 'USD_EUR_FX')
    await user.type(screen.getByLabelText(/display name/i), 'USD/EUR FX Rate')
    await user.click(screen.getByRole('button', { name: /register data set/i }))
    await waitFor(() => {
      expect(screen.getByText(/category is required/i)).toBeInTheDocument()
    })
  })

  it('shows error when unit is empty', async () => {
    const user = userEvent.setup()
    renderDialog()
    await user.type(screen.getByLabelText(/^code$/i), 'USD_EUR_FX')
    await user.type(screen.getByLabelText(/display name/i), 'USD/EUR FX Rate')
    await user.selectOptions(screen.getByLabelText(/^category$/i), '1')
    await user.click(screen.getByRole('button', { name: /register data set/i }))
    await waitFor(() => {
      expect(screen.getByText(/unit is required/i)).toBeInTheDocument()
    })
  })
})

describe('RegisterDataSetDialog - successful submission', () => {
  it('calls registerDataSet with correct data', async () => {
    const user = userEvent.setup()
    const registerDataSet = vi.fn().mockResolvedValue({
      dataset: { code: 'USD_EUR_FX', id: 'ds-001' },
    })
    setupMock(registerDataSet)

    renderDialog()

    await user.type(screen.getByLabelText(/^code$/i), 'USD_EUR_FX')
    await user.type(screen.getByLabelText(/display name/i), 'USD/EUR FX Rate')
    await user.selectOptions(screen.getByLabelText(/^category$/i), '1')
    await user.type(screen.getByLabelText(/^unit$/i), 'USD/EUR')
    await user.type(screen.getByLabelText(/description/i), 'Exchange rate dataset')
    await user.click(screen.getByRole('button', { name: /register data set/i }))

    await waitFor(() => {
      expect(registerDataSet).toHaveBeenCalledOnce()
      expect(registerDataSet).toHaveBeenCalledWith(
        expect.objectContaining({
          code: 'USD_EUR_FX',
          displayName: 'USD/EUR FX Rate',
          category: 1,
          unit: 'USD/EUR',
          description: 'Exchange rate dataset',
        }),
      )
    })
  })

  it('disables submit button while pending', async () => {
    const user = userEvent.setup()
    setupMock(vi.fn().mockReturnValue(new Promise(() => {})))
    renderDialog()

    await user.type(screen.getByLabelText(/^code$/i), 'USD_EUR_FX')
    await user.type(screen.getByLabelText(/display name/i), 'USD/EUR FX Rate')
    await user.selectOptions(screen.getByLabelText(/^category$/i), '1')
    await user.type(screen.getByLabelText(/^unit$/i), 'USD/EUR')
    await user.click(screen.getByRole('button', { name: /register data set/i }))

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /registering/i })).toBeDisabled()
    })
  })
})

describe('RegisterDataSetDialog - error handling', () => {
  it('shows general error banner on server error', async () => {
    const user = userEvent.setup()
    const { Code, ConnectError } = await import('@connectrpc/connect')
    const err = new ConnectError('Service unavailable', Code.Unavailable)
    setupMock(vi.fn().mockRejectedValue(err))

    renderDialog()

    await user.type(screen.getByLabelText(/^code$/i), 'USD_EUR_FX')
    await user.type(screen.getByLabelText(/display name/i), 'USD/EUR FX Rate')
    await user.selectOptions(screen.getByLabelText(/^category$/i), '1')
    await user.type(screen.getByLabelText(/^unit$/i), 'USD/EUR')
    await user.click(screen.getByRole('button', { name: /register data set/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })
  })
})

describe('RegisterDataSetDialog - reset on close', () => {
  it('clears form when dialog closes via cancel and reopens', async () => {
    const user = userEvent.setup()
    setupMock()

    function ControlledDialog() {
      const [open, setOpen] = React.useState(false)
      return (
        <>
          <button onClick={() => setOpen(true)}>Open Dialog</button>
          <RegisterDataSetDialog open={open} onOpenChange={setOpen} />
        </>
      )
    }

    renderWithProviders(
      <MemoryRouter>
        <ControlledDialog />
      </MemoryRouter>,
      { initialToken: createTenantUserToken('tenant-001') },
    )

    // Open dialog and type a value
    await user.click(screen.getByRole('button', { name: 'Open Dialog' }))
    await user.type(screen.getByLabelText(/^code$/i), 'USD_EUR_FX')
    expect(screen.getByLabelText(/^code$/i)).toHaveValue('USD_EUR_FX')

    // Close via Cancel button
    await user.click(screen.getByRole('button', { name: /cancel/i }))

    // Re-open
    await user.click(screen.getByRole('button', { name: 'Open Dialog' }))

    // Form should be cleared
    expect(screen.getByLabelText(/^code$/i)).toHaveValue('')
  })
})
