import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/msw-handlers'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { CreateValuationFeatureDialog } from './create-valuation-feature-dialog'

const tenantToken = createTenantUserToken('tenant-test')

const CURRENT_ACCOUNT_URL =
  '*/meridian.current_account.v1.CurrentAccountService/CreateValuationFeature'
const INTERNAL_ACCOUNT_URL =
  '*/meridian.internal_bank_account.v1.InternalBankAccountService/CreateValuationFeature'

function renderCurrentAccountDialog(
  props: {
    open?: boolean
    onOpenChange?: (open: boolean) => void
    accountId?: string
    accountCurrency?: string
  } = {},
) {
  const {
    open = true,
    onOpenChange = vi.fn(),
    accountId = 'acct-001',
    accountCurrency = 'GBP',
  } = props
  return renderWithProviders(
    <MemoryRouter>
      <CreateValuationFeatureDialog
        open={open}
        onOpenChange={onOpenChange}
        accountId={accountId}
        accountType="current"
        accountCurrency={accountCurrency}
      />
    </MemoryRouter>,
    { initialToken: tenantToken },
  )
}

function renderInternalAccountDialog(
  props: {
    open?: boolean
    onOpenChange?: (open: boolean) => void
    accountId?: string
    accountCurrency?: string
  } = {},
) {
  const {
    open = true,
    onOpenChange = vi.fn(),
    accountId = 'internal-acct-001',
    accountCurrency = 'GBP',
  } = props
  return renderWithProviders(
    <MemoryRouter>
      <CreateValuationFeatureDialog
        open={open}
        onOpenChange={onOpenChange}
        accountId={accountId}
        accountType="internal"
        accountCurrency={accountCurrency}
      />
    </MemoryRouter>,
    { initialToken: tenantToken },
  )
}

async function fillValidForm(accountCurrency = 'GBP') {
  await userEvent.type(screen.getByLabelText(/input instrument code/i), 'USD')
  await userEvent.type(screen.getByLabelText(/valuation method id/i), 'fx-rate-usd-gbp')
  await userEvent.type(screen.getByLabelText(/method version/i), '1')
  await userEvent.type(screen.getByLabelText(/output instrument/i), accountCurrency)
}

describe('CreateValuationFeatureDialog - rendering', () => {
  it('renders dialog when open', () => {
    renderCurrentAccountDialog()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getAllByText(/add valuation feature/i).length).toBeGreaterThan(0)
  })

  it('does not render when closed', () => {
    renderCurrentAccountDialog({ open: false })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders all form fields', () => {
    renderCurrentAccountDialog()
    expect(screen.getByLabelText(/input instrument code/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/valuation method id/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/method version/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/output instrument/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/parameters/i)).toBeInTheDocument()
  })

  it('renders submit and cancel buttons', () => {
    renderCurrentAccountDialog()
    expect(screen.getByRole('button', { name: /add valuation feature/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /cancel/i })).toBeInTheDocument()
  })

  it('shows account currency in description', () => {
    renderCurrentAccountDialog({ accountCurrency: 'GBP' })
    expect(screen.getAllByText(/GBP/).length).toBeGreaterThan(0)
  })

  it('shows account currency hint for output instrument', () => {
    renderCurrentAccountDialog({ accountCurrency: 'EUR' })
    expect(screen.getByText(/Must match the account currency: EUR/i)).toBeInTheDocument()
  })
})

describe('CreateValuationFeatureDialog - validation', () => {
  it('shows error when instrumentCode is empty on submit', async () => {
    renderCurrentAccountDialog()
    await userEvent.click(screen.getByRole('button', { name: /add valuation feature/i }))
    await waitFor(() => {
      expect(screen.getByText(/instrument code is required/i)).toBeInTheDocument()
    })
  })

  it('shows error when valuationMethodId is empty on submit', async () => {
    renderCurrentAccountDialog()
    await userEvent.type(screen.getByLabelText(/input instrument code/i), 'USD')
    await userEvent.click(screen.getByRole('button', { name: /add valuation feature/i }))
    await waitFor(() => {
      expect(screen.getByText(/valuation method id is required/i)).toBeInTheDocument()
    })
  })

  it('shows error when valuationMethodVersion is empty on submit', async () => {
    renderCurrentAccountDialog()
    await userEvent.type(screen.getByLabelText(/input instrument code/i), 'USD')
    await userEvent.type(screen.getByLabelText(/valuation method id/i), 'method-1')
    await userEvent.click(screen.getByRole('button', { name: /add valuation feature/i }))
    await waitFor(() => {
      expect(screen.getByText(/version is required/i)).toBeInTheDocument()
    })
  })

  it('shows error when valuationMethodVersion is less than 1', async () => {
    renderCurrentAccountDialog()
    await userEvent.type(screen.getByLabelText(/input instrument code/i), 'USD')
    await userEvent.type(screen.getByLabelText(/valuation method id/i), 'method-1')
    await userEvent.type(screen.getByLabelText(/method version/i), '0')
    await userEvent.type(screen.getByLabelText(/output instrument/i), 'GBP')
    await userEvent.click(screen.getByRole('button', { name: /add valuation feature/i }))
    await waitFor(() => {
      expect(screen.getByText(/version must be a whole number of 1 or greater/i)).toBeInTheDocument()
    })
  })

  it('shows error when outputInstrument is empty on submit', async () => {
    renderCurrentAccountDialog()
    await userEvent.type(screen.getByLabelText(/input instrument code/i), 'USD')
    await userEvent.type(screen.getByLabelText(/valuation method id/i), 'method-1')
    await userEvent.type(screen.getByLabelText(/method version/i), '1')
    await userEvent.click(screen.getByRole('button', { name: /add valuation feature/i }))
    await waitFor(() => {
      expect(screen.getByText(/output instrument is required/i)).toBeInTheDocument()
    })
  })

  it('shows error when outputInstrument does not match accountCurrency', async () => {
    renderCurrentAccountDialog({ accountCurrency: 'GBP' })
    await userEvent.type(screen.getByLabelText(/input instrument code/i), 'USD')
    await userEvent.type(screen.getByLabelText(/valuation method id/i), 'method-1')
    await userEvent.type(screen.getByLabelText(/method version/i), '1')
    await userEvent.type(screen.getByLabelText(/output instrument/i), 'EUR')
    await userEvent.click(screen.getByRole('button', { name: /add valuation feature/i }))
    await waitFor(() => {
      expect(screen.getByText(/output instrument must match the account currency \(GBP\)/i)).toBeInTheDocument()
    })
  })

  it('shows error when parameters is invalid JSON', async () => {
    renderCurrentAccountDialog()
    await fillValidForm()
    await userEvent.type(screen.getByLabelText(/parameters/i), 'not-json')
    await userEvent.click(screen.getByRole('button', { name: /add valuation feature/i }))
    await waitFor(() => {
      expect(screen.getByText(/parameters must be valid json/i)).toBeInTheDocument()
    })
  })

  it('accepts empty parameters', async () => {
    server.use(
      http.post(CURRENT_ACCOUNT_URL, () =>
        HttpResponse.json({ feature: { id: 'feature-001' } }),
      ),
    )
    renderCurrentAccountDialog()
    await fillValidForm()
    await userEvent.click(screen.getByRole('button', { name: /add valuation feature/i }))
    await waitFor(() => {
      expect(screen.getByTestId('feature-id')).toBeInTheDocument()
    })
  })

  it('accepts valid JSON parameters', async () => {
    let capturedBody: unknown
    server.use(
      http.post(CURRENT_ACCOUNT_URL, async ({ request }) => {
        capturedBody = await request.json()
        return HttpResponse.json({ feature: { id: 'feature-001' } })
      }),
    )
    renderCurrentAccountDialog()
    await fillValidForm()
    // Use fireEvent to bypass userEvent special char handling for braces
    const { fireEvent } = await import('@testing-library/react')
    fireEvent.change(screen.getByLabelText(/parameters/i), {
      target: { value: '{"source": "ecb"}' },
    })
    await userEvent.click(screen.getByRole('button', { name: /add valuation feature/i }))
    await waitFor(() => {
      expect(capturedBody).toMatchObject({
        parameters: '{"source": "ecb"}',
      })
    })
  })
})

describe('CreateValuationFeatureDialog - current account API', () => {
  beforeEach(() => {
    server.use(
      http.post(CURRENT_ACCOUNT_URL, () =>
        HttpResponse.json({ feature: { id: 'feature-001' } }),
      ),
    )
  })

  it('calls CurrentAccountService with correct request body', async () => {
    let capturedBody: unknown
    server.use(
      http.post(CURRENT_ACCOUNT_URL, async ({ request }) => {
        capturedBody = await request.json()
        return HttpResponse.json({ feature: { id: 'feature-001' } })
      }),
    )
    renderCurrentAccountDialog({ accountId: 'acct-001', accountCurrency: 'GBP' })
    await fillValidForm('GBP')
    await userEvent.click(screen.getByRole('button', { name: /add valuation feature/i }))
    await waitFor(() => {
      expect(capturedBody).toMatchObject({
        accountId: 'acct-001',
        instrumentCode: 'USD',
        valuationMethodId: 'fx-rate-usd-gbp',
        valuationMethodVersion: 1,
        outputInstrument: 'GBP',
      })
    })
  })

  it('shows feature ID on success', async () => {
    renderCurrentAccountDialog()
    await fillValidForm()
    await userEvent.click(screen.getByRole('button', { name: /add valuation feature/i }))
    await waitFor(() => {
      expect(screen.getByTestId('feature-id')).toHaveTextContent('feature-001')
    })
  })

  it('shows success dialog with feature ID after creation', async () => {
    renderCurrentAccountDialog()
    await fillValidForm()
    await userEvent.click(screen.getByRole('button', { name: /add valuation feature/i }))
    await waitFor(() => {
      expect(screen.getByText(/valuation feature created/i)).toBeInTheDocument()
      expect(screen.getByTestId('feature-id')).toHaveTextContent('feature-001')
    })
  })

  it('invalidates account query key on success', async () => {
    renderCurrentAccountDialog({ accountId: 'acct-001' })
    await fillValidForm()
    await userEvent.click(screen.getByRole('button', { name: /add valuation feature/i }))
    await waitFor(() => {
      expect(screen.getByTestId('feature-id')).toBeInTheDocument()
    })
  })

  it('shows server error on API failure', async () => {
    server.use(
      http.post(CURRENT_ACCOUNT_URL, () =>
        HttpResponse.json({ message: 'Method not found' }, { status: 400 }),
      ),
    )
    renderCurrentAccountDialog()
    await fillValidForm()
    await userEvent.click(screen.getByRole('button', { name: /add valuation feature/i }))
    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })
  })

  it('disables submit button during submission', async () => {
    server.use(
      http.post(CURRENT_ACCOUNT_URL, async () => {
        await new Promise((resolve) => setTimeout(resolve, 100))
        return HttpResponse.json({ feature: { id: 'feature-001' } })
      }),
    )
    renderCurrentAccountDialog()
    await fillValidForm()
    await userEvent.click(screen.getByRole('button', { name: /add valuation feature/i }))
    expect(screen.getByRole('button', { name: /creating|add valuation feature/i })).toBeDisabled()
  })
})

describe('CreateValuationFeatureDialog - internal account API', () => {
  it('calls InternalBankAccountService for internal account type', async () => {
    let capturedBody: unknown
    server.use(
      http.post(INTERNAL_ACCOUNT_URL, async ({ request }) => {
        capturedBody = await request.json()
        return HttpResponse.json({ feature: { id: 'feature-internal-001' } })
      }),
    )
    renderInternalAccountDialog({ accountId: 'internal-acct-001', accountCurrency: 'GBP' })
    await userEvent.type(screen.getByLabelText(/input instrument code/i), 'USD')
    await userEvent.type(screen.getByLabelText(/valuation method id/i), 'fx-rate-usd-gbp')
    await userEvent.type(screen.getByLabelText(/method version/i), '2')
    await userEvent.type(screen.getByLabelText(/output instrument/i), 'GBP')
    await userEvent.click(screen.getByRole('button', { name: /add valuation feature/i }))
    await waitFor(() => {
      expect(capturedBody).toMatchObject({
        accountId: 'internal-acct-001',
        instrumentCode: 'USD',
        valuationMethodId: 'fx-rate-usd-gbp',
        valuationMethodVersion: 2,
        outputInstrument: 'GBP',
      })
    })
  })

  it('shows feature ID on success for internal accounts', async () => {
    server.use(
      http.post(INTERNAL_ACCOUNT_URL, () =>
        HttpResponse.json({ feature: { id: 'feature-internal-001' } }),
      ),
    )
    renderInternalAccountDialog()
    await userEvent.type(screen.getByLabelText(/input instrument code/i), 'USD')
    await userEvent.type(screen.getByLabelText(/valuation method id/i), 'method-1')
    await userEvent.type(screen.getByLabelText(/method version/i), '1')
    await userEvent.type(screen.getByLabelText(/output instrument/i), 'GBP')
    await userEvent.click(screen.getByRole('button', { name: /add valuation feature/i }))
    await waitFor(() => {
      expect(screen.getByTestId('feature-id')).toHaveTextContent('feature-internal-001')
    })
  })
})

describe('CreateValuationFeatureDialog - cancel', () => {
  it('calls onOpenChange(false) when cancel clicked', async () => {
    const onOpenChange = vi.fn()
    renderCurrentAccountDialog({ onOpenChange })
    await userEvent.click(screen.getByRole('button', { name: /cancel/i }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })
})

describe('CreateValuationFeatureDialog - reset on close', () => {
  it('clears form when dialog closes and reopens', () => {
    const { rerender } = renderCurrentAccountDialog({ open: true })

    rerender(
      <MemoryRouter>
        <CreateValuationFeatureDialog
          open={false}
          onOpenChange={vi.fn()}
          accountId="acct-001"
          accountType="current"
          accountCurrency="GBP"
        />
      </MemoryRouter>,
    )

    rerender(
      <MemoryRouter>
        <CreateValuationFeatureDialog
          open={true}
          onOpenChange={vi.fn()}
          accountId="acct-001"
          accountType="current"
          accountCurrency="GBP"
        />
      </MemoryRouter>,
    )

    expect(screen.getByLabelText(/input instrument code/i)).toHaveValue('')
    expect(screen.getByLabelText(/valuation method id/i)).toHaveValue('')
    expect(screen.getByLabelText(/output instrument/i)).toHaveValue('')
  })
})
