import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createPlatformAdminToken } from '@/test/jwt-helpers'
import { IdentityStatus } from '@/api/gen/meridian/identity/v1/identity_pb'
import { UsersListPage } from './users-list-page'

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
  ApiClientProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { useApiClients } from '@/api/context'

const mockIdentities = [
  {
    id: 'user-1',
    email: 'alice@example.com',
    externalIdp: '',
    externalIdpSub: '',
    status: IdentityStatus.ACTIVE,
    lastLoginAt: { seconds: BigInt(1700000000), nanos: 0 },
    failedAttempts: 0,
    lockedUntil: undefined,
    mfaEnabled: true,
    createdAt: { seconds: BigInt(1699000000), nanos: 0 },
    updatedAt: { seconds: BigInt(1700000000), nanos: 0 },
    version: 1,
  },
  {
    id: 'user-2',
    email: 'bob@example.com',
    externalIdp: '',
    externalIdpSub: '',
    status: IdentityStatus.SUSPENDED,
    lastLoginAt: undefined,
    failedAttempts: 0,
    lockedUntil: undefined,
    mfaEnabled: false,
    createdAt: { seconds: BigInt(1699001000), nanos: 0 },
    updatedAt: { seconds: BigInt(1699001000), nanos: 0 },
    version: 1,
  },
  {
    id: 'user-3',
    email: 'carol@example.com',
    externalIdp: '',
    externalIdpSub: '',
    status: IdentityStatus.PENDING_INVITE,
    lastLoginAt: undefined,
    failedAttempts: 0,
    lockedUntil: undefined,
    mfaEnabled: false,
    createdAt: { seconds: BigInt(1699002000), nanos: 0 },
    updatedAt: { seconds: BigInt(1699002000), nanos: 0 },
    version: 1,
  },
]

function mockApiClients() {
  vi.mocked(useApiClients).mockReturnValue({
    identity: {
      listIdentities: vi.fn().mockResolvedValue({
        identities: mockIdentities,
        nextPageToken: '',
        totalCount: 3,
      }),
      inviteUser: vi.fn().mockResolvedValue({
        invitation: { id: 'inv-1' },
        identity: mockIdentities[0],
        invitationToken: 'token-123',
      }),
      retrieveIdentity: vi.fn(),
      listRoleAssignments: vi.fn(),
      grantRole: vi.fn(),
      revokeRole: vi.fn(),
      suspendIdentity: vi.fn(),
      reactivateIdentity: vi.fn(),
    },
  } as unknown as ReturnType<typeof useApiClients>)
}

function renderUsersListPage(token: string) {
  return renderWithProviders(
    <MemoryRouter initialEntries={['/users']}>
      <Routes>
        <Route path="/users" element={<UsersListPage />} />
        <Route path="/users/:userId" element={<div data-testid="user-detail-page">Detail</div>} />
      </Routes>
    </MemoryRouter>,
    { initialToken: token },
  )
}

describe('UsersListPage', () => {
  beforeEach(() => mockApiClients())

  it('renders page heading', async () => {
    renderUsersListPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: /users/i })).toBeInTheDocument()
    })
  })

  it('renders user list table', async () => {
    renderUsersListPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByText('alice@example.com')).toBeInTheDocument()
    })
    expect(screen.getByText('bob@example.com')).toBeInTheDocument()
    expect(screen.getByText('carol@example.com')).toBeInTheDocument()
  })

  it('shows status badges', async () => {
    renderUsersListPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByText('ACTIVE')).toBeInTheDocument()
    })
    expect(screen.getByText('SUSPENDED')).toBeInTheDocument()
    expect(screen.getByText('PENDING INVITE')).toBeInTheDocument()
  })

  it('shows MFA status', async () => {
    renderUsersListPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByText('Enabled')).toBeInTheDocument()
    })
    expect(screen.getAllByText('Disabled').length).toBeGreaterThan(0)
  })

  it('shows invite user button', async () => {
    renderUsersListPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /invite user/i })).toBeInTheDocument()
    })
  })

  it('opens invite dialog when button is clicked', async () => {
    renderUsersListPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /invite user/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /invite user/i }))

    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(screen.getByLabelText(/email/i)).toBeInTheDocument()
  })

  it('navigates to user detail on row click', async () => {
    renderUsersListPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByText('alice@example.com')).toBeInTheDocument()
    })

    await userEvent.click(screen.getByText('alice@example.com'))

    await waitFor(() => {
      expect(screen.getByTestId('user-detail-page')).toBeInTheDocument()
    })
  })

  it('shows status filter dropdown', async () => {
    renderUsersListPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('combobox', { name: /status/i })).toBeInTheDocument()
    })
  })
})

describe('UsersListPage - empty state', () => {
  it('shows empty state when no users', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      identity: {
        listIdentities: vi.fn().mockResolvedValue({
          identities: [],
          nextPageToken: '',
          totalCount: 0,
        }),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderUsersListPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByText('No users found')).toBeInTheDocument()
    })
  })
})

describe('UsersListPage - error state', () => {
  it('shows error state when API fails', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      identity: {
        listIdentities: vi.fn().mockRejectedValue(new Error('Internal Server Error')),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderUsersListPage(createPlatformAdminToken())

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    })
  })
})
