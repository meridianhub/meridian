import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/msw-handlers'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { CreateLienDialog } from './create-lien-dialog'

const tenantToken = createTenantUserToken('tenant-test')

function renderCurrentAccountDialog(props: {
  open?: boolean
  onOpenChange?: (open: boolean) => void
  accountId?: string
  instrumentCode?: string
  decimalPlaces?: number
}) {
  const {
    open = true,
    onOpenChange = vi.fn(),
    accountId = 'acct-001',
    instrumentCode = 'GBP',
    decimalPlaces = 2,
  } = props
  return renderWithProviders(
    <MemoryRouter>
      <CreateLienDialog
        open={open}
        onOpenChange={onOpenChange}
        accountId={accountId}
        instrumentCode={instrumentCode}
        accountType="current"
        decimalPlaces={decimalPlaces}
      />
    </MemoryRouter>,
    { initialToken: tenantToken },
  )
}

function renderInternalAccountDialog(props: {
  open?: boolean
  onOpenChange?: (open: boolean) => void
  accountId?: string
  instrumentCode?: string
  decimalPlaces?: number
}) {
  const {
    open = true,
    onOpenChange = vi.fn(),
    accountId = 'internal-acct-001',
    instrumentCode = 'GBP',
    decimalPlaces = 2,
  } = props
  return renderWithProviders(
    <MemoryRouter>
      <CreateLienDialog
        open={open}
        onOpenChange={onOpenChange}
        accountId={accountId}
        instrumentCode={instrumentCode}
        accountType="internal"
        decimalPlaces={decimalPlaces}
      />
    </MemoryRouter>,
    { initialToken: tenantToken },
  )
}

describe('CreateLienDialog - rendering', () => {
  it('renders dialog when open', () => {
    renderCurrentAccountDialog({})
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getAllByText(/create lien/i).length).toBeGreaterThan(0)
  })

  it('does not render when closed', () => {
    renderCurrentAccountDialog({ open: false })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders amount input with instrument code label', () => {
    renderCurrentAccountDialog({ instrumentCode: 'GBP' })
    expect(screen.getByLabelText(/amount/i)).toBeInTheDocument()
    expect(screen.getByText(/Amount \(GBP\)/i)).toBeInTheDocument()
  })

  it('renders reason input', () => {
    renderCurrentAccountDialog({})
    expect(screen.getByLabelText(/reason/i)).toBeInTheDocument()
  })

  it('renders expiry input', () => {
    renderCurrentAccountDialog({})
    expect(screen.getByLabelText(/expiry/i)).toBeInTheDocument()
  })

  it('renders create lien and cancel buttons', () => {
    renderCurrentAccountDialog({})
    expect(screen.getByRole('button', { name: /create lien/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument()
  })

  it('shows instrument code in description', () => {
    renderCurrentAccountDialog({ instrumentCode: 'kWh' })
    expect(screen.getAllByText(/kWh/).length).toBeGreaterThan(0)
  })
})

describe('CreateLienDialog - validation', () => {
  it('shows error when amount is empty on submit', async () => {
    renderCurrentAccountDialog({})
    await userEvent.type(screen.getByLabelText(/reason/i), 'pay-ref-001')
    await userEvent.click(screen.getByRole('button', { name: /create lien/i }))
    await waitFor(() => {
      expect(screen.getByText(/amount is required/i)).toBeInTheDocument()
    })
  })

  it('shows error when amount is zero', async () => {
    renderCurrentAccountDialog({})
    await userEvent.type(screen.getByLabelText(/amount/i), '0')
    await userEvent.type(screen.getByLabelText(/reason/i), 'pay-ref-001')
    await userEvent.click(screen.getByRole('button', { name: /create lien/i }))
    await waitFor(() => {
      expect(screen.getByText(/amount must be greater than zero/i)).toBeInTheDocument()
    })
  })

  it('shows error when amount is negative', async () => {
    renderCurrentAccountDialog({})
    await userEvent.type(screen.getByLabelText(/amount/i), '-10')
    await userEvent.type(screen.getByLabelText(/reason/i), 'pay-ref-001')
    await userEvent.click(screen.getByRole('button', { name: /create lien/i }))
    await waitFor(() => {
      expect(screen.getByText(/amount must be greater than zero/i)).toBeInTheDocument()
    })
  })

  it('shows error when amount is invalid', async () => {
    renderCurrentAccountDialog({})
    await userEvent.type(screen.getByLabelText(/amount/i), 'abc')
    await userEvent.type(screen.getByLabelText(/reason/i), 'pay-ref-001')
    await userEvent.click(screen.getByRole('button', { name: /create lien/i }))
    await waitFor(() => {
      expect(screen.getByText(/invalid amount/i)).toBeInTheDocument()
    })
  })

  it('shows error when reason is empty', async () => {
    renderCurrentAccountDialog({})
    await userEvent.type(screen.getByLabelText(/amount/i), '100.00')
    await userEvent.click(screen.getByRole('button', { name: /create lien/i }))
    await waitFor(() => {
      expect(screen.getByText(/reason is required/i)).toBeInTheDocument()
    })
  })

  it('shows error when expiry is in the past', async () => {
    renderCurrentAccountDialog({})
    await userEvent.type(screen.getByLabelText(/amount/i), '100.00')
    await userEvent.type(screen.getByLabelText(/reason/i), 'pay-ref-001')
    // Set expiry to past date
    const expiryInput = screen.getByLabelText(/expiry/i)
    await userEvent.type(expiryInput, '2020-01-01T00:00')
    await userEvent.click(screen.getByRole('button', { name: /create lien/i }))
    await waitFor(() => {
      expect(screen.getByText(/expiry must be in the future/i)).toBeInTheDocument()
    })
  })

  it('accepts valid future expiry date', async () => {
    let capturedBody: unknown
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/InitiateLien', async ({ request }) => {
        capturedBody = await request.json()
        return HttpResponse.json({ lien: { lienId: 'lien-abc' } })
      }),
    )

    renderCurrentAccountDialog({})
    await userEvent.type(screen.getByLabelText(/amount/i), '100.00')
    await userEvent.type(screen.getByLabelText(/reason/i), 'pay-ref-001')
    const expiryInput = screen.getByLabelText(/expiry/i)
    await userEvent.type(expiryInput, '2099-12-31T23:59')
    await userEvent.click(screen.getByRole('button', { name: /create lien/i }))

    await waitFor(() => {
      expect(capturedBody).toMatchObject({
        expiresAt: expect.objectContaining({ seconds: expect.any(String) }),
      })
    })
  })
})

describe('CreateLienDialog - current account API', () => {
  beforeEach(() => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/InitiateLien', () =>
        HttpResponse.json({ lien: { lienId: 'lien-001' } }),
      ),
    )
  })

  it('calls CurrentAccountService with amount and paymentOrderReference', async () => {
    let capturedBody: unknown
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/InitiateLien', async ({ request }) => {
        capturedBody = await request.json()
        return HttpResponse.json({ lien: { lienId: 'lien-001' } })
      }),
    )

    renderCurrentAccountDialog({})
    await userEvent.type(screen.getByLabelText(/amount/i), '100.50')
    await userEvent.type(screen.getByLabelText(/reason/i), 'pay-ref-001')
    await userEvent.click(screen.getByRole('button', { name: /create lien/i }))

    await waitFor(() => {
      expect(capturedBody).toMatchObject({
        accountId: 'acct-001',
        amount: { amount: '10050' },
        paymentOrderReference: 'pay-ref-001',
      })
    })
  })

  it('shows lien ID on success', async () => {
    renderCurrentAccountDialog({})
    await userEvent.type(screen.getByLabelText(/amount/i), '100.00')
    await userEvent.type(screen.getByLabelText(/reason/i), 'pay-ref-001')
    await userEvent.click(screen.getByRole('button', { name: /create lien/i }))

    await waitFor(() => {
      expect(screen.getByTestId('lien-id')).toHaveTextContent('lien-001')
    })
  })

  it('shows server error on failure', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/InitiateLien', () =>
        HttpResponse.json({ message: 'Insufficient funds' }, { status: 400 }),
      ),
    )

    renderCurrentAccountDialog({})
    await userEvent.type(screen.getByLabelText(/amount/i), '100.00')
    await userEvent.type(screen.getByLabelText(/reason/i), 'pay-ref-001')
    await userEvent.click(screen.getByRole('button', { name: /create lien/i }))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })
  })

  it('disables submit button during submission', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/InitiateLien', async () => {
        await new Promise((resolve) => setTimeout(resolve, 100))
        return HttpResponse.json({ lien: { lienId: 'lien-001' } })
      }),
    )

    renderCurrentAccountDialog({})
    await userEvent.type(screen.getByLabelText(/amount/i), '100.00')
    await userEvent.type(screen.getByLabelText(/reason/i), 'pay-ref-001')
    await userEvent.click(screen.getByRole('button', { name: /create lien/i }))

    expect(screen.getByRole('button', { name: /creating|create lien/i })).toBeDisabled()
  })
})

describe('CreateLienDialog - internal account API', () => {
  it('calls InternalAccountService with input field instead of amount', async () => {
    let capturedBody: unknown
    server.use(
      http.post(
        '*/meridian.internal_account.v1.InternalAccountService/InitiateLien',
        async ({ request }) => {
          capturedBody = await request.json()
          return HttpResponse.json({ lien: { lienId: 'lien-internal-001' } })
        },
      ),
    )

    renderInternalAccountDialog({})
    await userEvent.type(screen.getByLabelText(/amount/i), '250.00')
    await userEvent.type(screen.getByLabelText(/reason/i), 'internal-ref-001')
    await userEvent.click(screen.getByRole('button', { name: /create lien/i }))

    await waitFor(() => {
      expect(capturedBody).toMatchObject({
        accountId: 'internal-acct-001',
        input: { amount: '25000' },
        paymentOrderReference: 'internal-ref-001',
      })
    })
  })

  it('does not include amount field for internal accounts', async () => {
    let capturedBody: unknown
    server.use(
      http.post(
        '*/meridian.internal_account.v1.InternalAccountService/InitiateLien',
        async ({ request }) => {
          capturedBody = await request.json()
          return HttpResponse.json({ lien: { lienId: 'lien-internal-001' } })
        },
      ),
    )

    renderInternalAccountDialog({})
    await userEvent.type(screen.getByLabelText(/amount/i), '100.00')
    await userEvent.type(screen.getByLabelText(/reason/i), 'internal-ref-001')
    await userEvent.click(screen.getByRole('button', { name: /create lien/i }))

    await waitFor(() => {
      const body = capturedBody as Record<string, unknown>
      expect(body.amount).toBeUndefined()
      expect(body.input).toBeDefined()
    })
  })

  it('shows lien ID on success for internal accounts', async () => {
    server.use(
      http.post(
        '*/meridian.internal_account.v1.InternalAccountService/InitiateLien',
        () => HttpResponse.json({ lien: { lienId: 'lien-internal-001' } }),
      ),
    )

    renderInternalAccountDialog({})
    await userEvent.type(screen.getByLabelText(/amount/i), '100.00')
    await userEvent.type(screen.getByLabelText(/reason/i), 'internal-ref-001')
    await userEvent.click(screen.getByRole('button', { name: /create lien/i }))

    await waitFor(() => {
      expect(screen.getByTestId('lien-id')).toHaveTextContent('lien-internal-001')
    })
  })
})

describe('CreateLienDialog - decimal places', () => {
  it('converts amount using custom decimal places', async () => {
    let capturedBody: unknown
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/InitiateLien', async ({ request }) => {
        capturedBody = await request.json()
        return HttpResponse.json({ lien: { lienId: 'lien-001' } })
      }),
    )

    renderCurrentAccountDialog({ instrumentCode: 'kWh', decimalPlaces: 3 })
    await userEvent.type(screen.getByLabelText(/amount/i), '1.500')
    await userEvent.type(screen.getByLabelText(/reason/i), 'energy-ref-001')
    await userEvent.click(screen.getByRole('button', { name: /create lien/i }))

    await waitFor(() => {
      expect(capturedBody).toMatchObject({
        amount: { amount: '1500' },
      })
    })
  })
})

describe('CreateLienDialog - cancel', () => {
  it('calls onOpenChange(false) when cancel clicked', async () => {
    const onOpenChange = vi.fn()
    renderCurrentAccountDialog({ onOpenChange })
    await userEvent.click(screen.getByRole('button', { name: /cancel/i }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })
})

describe('CreateLienDialog - reset on close', () => {
  it('clears form when dialog closes and reopens', () => {
    const { rerender } = renderCurrentAccountDialog({ open: true })

    // Close dialog
    rerender(
      <MemoryRouter>
        <CreateLienDialog
          open={false}
          onOpenChange={vi.fn()}
          accountId="acct-001"
          instrumentCode="GBP"
          accountType="current"
        />
      </MemoryRouter>,
    )

    // Reopen dialog
    rerender(
      <MemoryRouter>
        <CreateLienDialog
          open={true}
          onOpenChange={vi.fn()}
          accountId="acct-001"
          instrumentCode="GBP"
          accountType="current"
        />
      </MemoryRouter>,
    )

    expect(screen.getByLabelText(/amount/i)).toHaveValue('')
    expect(screen.getByLabelText(/reason/i)).toHaveValue('')
  })
})
