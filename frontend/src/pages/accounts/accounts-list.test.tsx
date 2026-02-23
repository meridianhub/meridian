import { describe, it, expect } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/msw-handlers'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { AccountsPage } from './index'

const tenantToken = createTenantUserToken('tenant-test')

function renderAccountsPage(initialPath = '/accounts') {
  return renderWithProviders(
    <MemoryRouter initialEntries={[initialPath]}>
      <Routes>
        <Route path="/accounts" element={<AccountsPage />} />
        <Route path="/accounts/:accountId" element={<div>Account Detail Page</div>} />
      </Routes>
    </MemoryRouter>,
    { initialToken: tenantToken },
  )
}

// Mock proto response shape (CurrentAccountFacility fields)
const mockAccounts = [
  {
    accountId: 'acct-001',
    accountIdentification: 'GB29NWBK60161331926819',
    accountStatus: 'ACCOUNT_STATUS_ACTIVE',
    baseCurrency: 'CURRENCY_GBP',
    createdAt: { seconds: 1700000000, nanos: 0 },
  },
  {
    accountId: 'acct-002',
    accountIdentification: 'DE89370400440532013000',
    accountStatus: 'ACCOUNT_STATUS_FROZEN',
    baseCurrency: 'CURRENCY_EUR',
    createdAt: { seconds: 1700100000, nanos: 0 },
  },
]

describe('AccountsPage - list rendering', () => {
  it('renders the page heading', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/ListCurrentAccounts', () =>
        HttpResponse.json({ accounts: mockAccounts, nextPageToken: '' }),
      ),
    )

    renderAccountsPage()

    expect(screen.getByRole('heading', { name: /accounts/i })).toBeInTheDocument()
  })

  it('renders accounts with correct columns after data loads', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/ListCurrentAccounts', () =>
        HttpResponse.json({ accounts: mockAccounts, nextPageToken: '' }),
      ),
    )

    renderAccountsPage()

    await waitFor(() => {
      expect(screen.getByText('acct-001')).toBeInTheDocument()
    })

    expect(screen.getByText('GB29NWBK60161331926819')).toBeInTheDocument()
    expect(screen.getByText('acct-002')).toBeInTheDocument()
    expect(screen.getByText('DE89370400440532013000')).toBeInTheDocument()
  })

  it('renders status badges for accounts', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/ListCurrentAccounts', () =>
        HttpResponse.json({ accounts: mockAccounts, nextPageToken: '' }),
      ),
    )

    renderAccountsPage()

    await waitFor(() => {
      expect(screen.getByText('ACTIVE')).toBeInTheDocument()
    })

    expect(screen.getByText('FROZEN')).toBeInTheDocument()
  })

  it('renders column headers', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/ListCurrentAccounts', () =>
        HttpResponse.json({ accounts: [], nextPageToken: '' }),
      ),
    )

    renderAccountsPage()

    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: /account id/i })).toBeInTheDocument()
    })

    expect(screen.getByRole('columnheader', { name: /iban/i })).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: /status/i })).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: /currency/i })).toBeInTheDocument()
  })

  it('shows skeleton rows while loading', () => {
    // Never resolves - stays in loading state
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/ListCurrentAccounts', () =>
        new Promise(() => {}),
      ),
    )

    renderAccountsPage()

    expect(screen.getAllByTestId('skeleton-row').length).toBeGreaterThan(0)
  })

  it('shows empty state when no accounts exist', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/ListCurrentAccounts', () =>
        HttpResponse.json({ accounts: [], nextPageToken: '' }),
      ),
    )

    renderAccountsPage()

    await waitFor(() => {
      expect(screen.getByTestId('empty-state')).toBeInTheDocument()
    })
  })
})

describe('AccountsPage - filtering', () => {
  it('renders status filter select', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/ListCurrentAccounts', () =>
        HttpResponse.json({ accounts: [], nextPageToken: '' }),
      ),
    )

    renderAccountsPage()

    expect(screen.getByRole('combobox', { name: /status/i })).toBeInTheDocument()
  })

  it('renders IBAN text filter', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/ListCurrentAccounts', () =>
        HttpResponse.json({ accounts: [], nextPageToken: '' }),
      ),
    )

    renderAccountsPage()

    expect(screen.getByPlaceholderText(/filter by iban/i)).toBeInTheDocument()
  })

  it('passes status filter to query when selected', async () => {
    const capturedRequests: unknown[] = []

    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/ListCurrentAccounts', async ({ request }) => {
        const body = await request.json()
        capturedRequests.push(body)
        return HttpResponse.json({ accounts: [], nextPageToken: '' })
      }),
    )

    renderAccountsPage()

    await waitFor(() => expect(capturedRequests.length).toBeGreaterThan(0))

    const statusSelect = screen.getByRole('combobox', { name: /status/i })
    await userEvent.selectOptions(statusSelect, 'ACCOUNT_STATUS_ACTIVE')

    await waitFor(() => {
      // DataTable passes filters as a record - the page should call the API with the filter
      expect(capturedRequests.length).toBeGreaterThan(1)
    })
  })
})

describe('AccountsPage - navigation', () => {
  it('navigates to account detail on row click', async () => {
    server.use(
      http.post('*/meridian.current_account.v1.CurrentAccountService/ListCurrentAccounts', () =>
        HttpResponse.json({ accounts: mockAccounts, nextPageToken: '' }),
      ),
    )

    renderAccountsPage()

    await waitFor(() => {
      expect(screen.getByText('acct-001')).toBeInTheDocument()
    })

    await userEvent.click(screen.getByText('acct-001'))

    expect(screen.getByText('Account Detail Page')).toBeInTheDocument()
  })
})
