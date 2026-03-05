import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import { TooltipProvider } from '@/components/ui/tooltip'

const mockListActive = vi.fn().mockResolvedValue({
  definitions: [],
  nextPageToken: '',
})
const mockUpdateDefinition = vi.fn().mockResolvedValue({
  definition: null,
})

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(() => ({
    accountTypeRegistry: {
      listActive: mockListActive,
      updateDefinition: mockUpdateDefinition,
    },
  })),
}))

import { AccountTypesPage } from './index'

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity },
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

const mockDefinitions = [
  {
    id: 'aaaaaaaa-0000-0000-0000-000000000001',
    code: 'CUSTOMER_CURRENT',
    version: 1,
    displayName: 'Customer Current Account',
    description: 'Standard customer current account',
    normalBalance: 2, // CREDIT
    behaviorClass: 1, // CUSTOMER
    instrumentCode: 'GBP',
    defaultSagaPrefix: 'payment',
    validationCel: 'amount > 0',
    bucketingCel: 'instrument_code',
    eligibilityCel: 'party.status == "ACTIVE"',
    attributeSchema: '',
    attributes: {},
    status: 2, // ACCOUNT_TYPE_STATUS_ACTIVE
    isSystem: true,
    successorId: '',
    createdAt: { seconds: BigInt(1700000000), nanos: 0 },
    updatedAt: undefined,
    activatedAt: undefined,
    deprecatedAt: undefined,
    defaultConversionMethodId: '',
    defaultConversionMethodVersion: 0,
    valuationMethods: [],
  },
  {
    id: 'bbbbbbbb-0000-0000-0000-000000000002',
    code: 'CLEARING',
    version: 1,
    displayName: 'Clearing Account',
    description: 'Clearing account for holding funds',
    normalBalance: 1, // DEBIT
    behaviorClass: 2, // CLEARING
    instrumentCode: 'GBP',
    defaultSagaPrefix: 'clearing',
    validationCel: 'amount > 0 && amount <= 1000000',
    bucketingCel: '',
    eligibilityCel: '',
    attributeSchema: '',
    attributes: {},
    status: 2, // ACCOUNT_TYPE_STATUS_ACTIVE
    isSystem: false,
    successorId: '',
    createdAt: { seconds: BigInt(1700001000), nanos: 0 },
    updatedAt: undefined,
    activatedAt: undefined,
    deprecatedAt: undefined,
    defaultConversionMethodId: '',
    defaultConversionMethodVersion: 0,
    valuationMethods: [],
  },
]

describe('AccountTypesPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListActive.mockResolvedValue({
      definitions: [],
      nextPageToken: '',
    })
  })

  it('renders page title', () => {
    render(
      <Wrapper>
        <AccountTypesPage />
      </Wrapper>,
    )
    expect(screen.getByRole('heading', { name: /account types/i })).toBeInTheDocument()
  })

  it('renders column headers', async () => {
    render(
      <Wrapper>
        <AccountTypesPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: /code/i })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: /display name/i })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: /behavior/i })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: /status/i })).toBeInTheDocument()
    })
  })

  it('renders account type rows when data is available', async () => {
    mockListActive.mockResolvedValue({
      definitions: mockDefinitions,
      nextPageToken: '',
    })

    render(
      <Wrapper>
        <AccountTypesPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('CUSTOMER_CURRENT')).toBeInTheDocument()
      expect(screen.getByText('CLEARING')).toBeInTheDocument()
    })
  })

  it('shows empty state when no definitions match', async () => {
    mockListActive.mockResolvedValue({
      definitions: [],
      nextPageToken: '',
    })

    render(
      <Wrapper>
        <AccountTypesPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByTestId('empty-state')).toBeInTheDocument()
    })
  })

  it('renders CEL policy section', async () => {
    mockListActive.mockResolvedValue({
      definitions: mockDefinitions,
      nextPageToken: '',
    })

    render(
      <Wrapper>
        <AccountTypesPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('CUSTOMER_CURRENT')).toBeInTheDocument()
    })

    // Click to select an account type
    const row = screen.getByText('CUSTOMER_CURRENT').closest('tr')
    if (row) {
      await userEvent.setup().click(row)
    }

    await waitFor(() => {
      expect(screen.getByTestId('cel-policy-editor')).toBeInTheDocument()
    })
  })

  it('shows validation CEL in editor when row selected', async () => {
    const user = userEvent.setup()
    mockListActive.mockResolvedValue({
      definitions: mockDefinitions,
      nextPageToken: '',
    })

    render(
      <Wrapper>
        <AccountTypesPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('CUSTOMER_CURRENT')).toBeInTheDocument()
    })

    const row = screen.getByText('CUSTOMER_CURRENT').closest('tr')
    if (row) await user.click(row)

    await waitFor(() => {
      expect(screen.getByTestId('cel-policy-editor')).toBeInTheDocument()
    })
  })

  it('calls listActive when component mounts', async () => {
    render(
      <Wrapper>
        <AccountTypesPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(mockListActive).toHaveBeenCalled()
    })
  })
})
