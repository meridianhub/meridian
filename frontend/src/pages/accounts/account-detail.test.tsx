import { describe, it, expect } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/msw-handlers'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { AccountDetailPage } from './[accountId]'

const tenantToken = createTenantUserToken('tenant-test')

function renderDetailPage(accountId = 'acct-001') {
  return renderWithProviders(
    <MemoryRouter initialEntries={[`/accounts/${accountId}`]}>
      <Routes>
        <Route path="/accounts/:accountId" element={<AccountDetailPage />} />
        <Route path="/accounts" element={<div>Accounts List</div>} />
      </Routes>
    </MemoryRouter>,
    { initialToken: tenantToken },
  )
}

// Proto shape: RetrieveCurrentAccountResponse.facility (CurrentAccountFacility)
const mockFacility = {
  accountId: 'acct-001',
  externalIdentifier: 'GB29NWBK60161331926819',
  accountStatus: 1, // ACCOUNT_STATUS_ACTIVE
  instrumentCode: 'GBP',
  orgPartyId: 'party-123',
  createdAt: '2023-11-14T22:13:20Z',
  updatedAt: '2023-11-14T22:13:21Z',
}

const mockFrozenFacility = {
  ...mockFacility,
  accountId: 'acct-frozen',
  accountStatus: 2, // ACCOUNT_STATUS_FROZEN
}

const mockClosedFacility = {
  ...mockFacility,
  accountId: 'acct-closed',
  accountStatus: 3, // ACCOUNT_STATUS_CLOSED
}

describe('AccountDetailPage - loading and error states', () => {
  it('renders skeleton while loading', () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () =>
        new Promise(() => {}),
      ),
    )

    renderDetailPage()

    expect(screen.getByTestId('account-detail-skeleton')).toBeInTheDocument()
  })

  it('shows 404 state for non-existent account', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () =>
        HttpResponse.json({ message: 'not found' }, { status: 404 }),
      ),
    )

    renderDetailPage('nonexistent-id')

    await waitFor(() => {
      expect(screen.getByTestId('account-not-found')).toBeInTheDocument()
    })
  })
})

describe('AccountDetailPage - account overview', () => {
  it('renders account ID in header', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () =>
        HttpResponse.json({ facility: mockFacility }),
      ),
    )

    renderDetailPage()

    await waitFor(() => {
      expect(screen.getAllByText('acct-001').length).toBeGreaterThan(0)
    })
  })

  it('renders external reference', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () =>
        HttpResponse.json({ facility: mockFacility }),
      ),
    )

    renderDetailPage()

    await waitFor(() => {
      expect(screen.getAllByText('GB29NWBK60161331926819').length).toBeGreaterThan(0)
    })
  })

  it('renders status badge', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () =>
        HttpResponse.json({ facility: mockFacility }),
      ),
    )

    renderDetailPage()

    await waitFor(() => {
      expect(screen.getAllByText('ACTIVE').length).toBeGreaterThan(0)
    })
  })

  it('renders currency', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () =>
        HttpResponse.json({ facility: mockFacility }),
      ),
    )

    renderDetailPage()

    await waitFor(() => {
      expect(screen.getAllByText('GBP').length).toBeGreaterThan(0)
    })
  })
})

describe('AccountDetailPage - tabs', () => {
  it('renders Overview tab by default', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () =>
        HttpResponse.json({ facility: mockFacility }),
      ),
    )

    renderDetailPage()

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /overview/i })).toBeInTheDocument()
    })
  })

  it('renders all required tabs', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () =>
        HttpResponse.json({ facility: mockFacility }),
      ),
    )

    renderDetailPage()

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /overview/i })).toBeInTheDocument()
    })

    expect(screen.getByRole('tab', { name: /transactions/i })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: /liens/i })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: /audit/i })).toBeInTheDocument()
  })

  it('switches to Transactions tab when clicked', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () =>
        HttpResponse.json({ facility: mockFacility }),
      ),
    )

    renderDetailPage()

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /transactions/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('tab', { name: /transactions/i }))

    expect(screen.getByRole('tab', { name: /transactions/i })).toHaveAttribute(
      'data-state',
      'active',
    )
  })

  it('switches to Audit Trail tab when clicked', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () =>
        HttpResponse.json({ facility: mockFacility }),
      ),
    )

    renderDetailPage()

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /audit/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('tab', { name: /audit/i }))

    expect(screen.getByRole('tab', { name: /audit/i })).toHaveAttribute('data-state', 'active')
  })
})

describe('AccountDetailPage - action buttons', () => {
  it('renders Freeze button for ACTIVE account', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () =>
        HttpResponse.json({ facility: mockFacility }),
      ),
    )

    renderDetailPage()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /freeze/i })).toBeInTheDocument()
    })
  })

  it('renders Unfreeze button for FROZEN account', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () =>
        HttpResponse.json({ facility: mockFrozenFacility }),
      ),
    )

    renderDetailPage('acct-frozen')

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /unfreeze/i })).toBeInTheDocument()
    })
  })

  it('does not render action buttons for CLOSED account', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () =>
        HttpResponse.json({ facility: mockClosedFacility }),
      ),
    )

    renderDetailPage('acct-closed')

    await waitFor(() => {
      // Closed account should show no action buttons
      expect(screen.queryByRole('button', { name: /freeze/i })).not.toBeInTheDocument()
      expect(screen.queryByRole('button', { name: /unfreeze/i })).not.toBeInTheDocument()
      expect(screen.queryByRole('button', { name: /close/i })).not.toBeInTheDocument()
    })
  })

  it('renders Close button for ACTIVE account', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () =>
        HttpResponse.json({ facility: mockFacility }),
      ),
    )

    renderDetailPage()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /close account/i })).toBeInTheDocument()
    })
  })

  it('hides Close button for FROZEN account (must unfreeze first)', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () =>
        HttpResponse.json({ facility: mockFrozenFacility }),
      ),
    )

    renderDetailPage('acct-frozen')

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /unfreeze/i })).toBeInTheDocument()
    })

    expect(screen.queryByRole('button', { name: /close account/i })).not.toBeInTheDocument()
  })
})

describe('AccountDetailPage - dialog integration', () => {
  it('opens DepositDialog when Deposit button is clicked', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () =>
        HttpResponse.json({ facility: mockFacility }),
      ),
    )

    renderDetailPage()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /deposit/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /deposit/i }))

    await waitFor(() => {
      expect(screen.getAllByText(/deposit funds/i).length).toBeGreaterThan(0)
    })
  })

  it('opens WithdrawDialog when Withdraw button is clicked', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () =>
        HttpResponse.json({ facility: mockFacility }),
      ),
    )

    renderDetailPage()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /withdraw/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /withdraw/i }))

    await waitFor(() => {
      expect(screen.getAllByText(/withdraw/i).length).toBeGreaterThan(1)
    })
  })

  it('opens ControlDialog when Freeze button is clicked', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () =>
        HttpResponse.json({ facility: mockFacility }),
      ),
    )

    renderDetailPage()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /^freeze$/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /^freeze$/i }))

    await waitFor(() => {
      expect(screen.getByRole('dialog')).toBeInTheDocument()
    })
  })

  it('opens ControlDialog when Unfreeze button is clicked', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () =>
        HttpResponse.json({ facility: mockFrozenFacility }),
      ),
    )

    renderDetailPage('acct-frozen')

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /^unfreeze$/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /^unfreeze$/i }))

    await waitFor(() => {
      expect(screen.getByRole('dialog')).toBeInTheDocument()
    })
  })

  it('opens ControlDialog when Close Account button is clicked', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () =>
        HttpResponse.json({ facility: mockFacility }),
      ),
    )

    renderDetailPage()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /close account/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /close account/i }))

    await waitFor(() => {
      expect(screen.getByRole('dialog')).toBeInTheDocument()
    })
  })
})

describe('AccountDetailPage - back navigation', () => {
  it('renders a back link to accounts list', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount', () =>
        HttpResponse.json({ facility: mockFacility }),
      ),
    )

    renderDetailPage()

    await waitFor(() => {
      expect(screen.getByRole('link', { name: /accounts/i })).toBeInTheDocument()
    })
  })
})
