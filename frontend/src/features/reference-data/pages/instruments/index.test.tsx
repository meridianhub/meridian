import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import { TooltipProvider } from '@/components/ui/tooltip'

const mockListInstruments = vi.fn().mockResolvedValue({
  instruments: [],
  nextPageToken: '',
})
const mockEvaluateInstrument = vi.fn().mockResolvedValue({
  compileErrors: [],
  validationResult: true,
  fungibilityKey: 'USD:1',
  errorMessage: '',
})
const mockRegisterInstrument = vi.fn().mockResolvedValue({
  instrument: { id: 'test-id', code: 'KWH', version: 1, dimension: 2, precision: 6, status: 1 },
})

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(() => ({
    referenceData: {
      listInstruments: mockListInstruments,
      evaluateInstrument: mockEvaluateInstrument,
      registerInstrument: mockRegisterInstrument,
    },
    manifestHistory: {
      getCurrentManifest: vi.fn().mockResolvedValue({ version: null }),
    },
  })),
}))

import { InstrumentsPage } from './index'

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

const mockInstruments = [
  {
    id: 'aaaaaaaa-0000-0000-0000-000000000001',
    code: 'GBP',
    version: 1,
    dimension: 1, // CURRENCY
    precision: 2,
    status: 2, // ACTIVE
    displayName: 'British Pound',
    description: 'UK currency',
    validationExpression: 'amount > 0',
    fungibilityKeyExpression: 'instrument_code',
    errorMessageExpression: '',
    attributeSchema: '',
    isSystem: true,
    createdAt: { seconds: BigInt(1700000000), nanos: 0 },
    activatedAt: undefined,
    deprecatedAt: undefined,
    successorId: '',
  },
  {
    id: 'bbbbbbbb-0000-0000-0000-000000000002',
    code: 'KWH',
    version: 1,
    dimension: 2, // ENERGY
    precision: 6,
    status: 2, // ACTIVE
    displayName: 'Kilowatt Hour',
    description: 'Energy unit',
    validationExpression: 'amount > 0',
    fungibilityKeyExpression: 'instrument_code',
    errorMessageExpression: '',
    attributeSchema: '',
    isSystem: false,
    createdAt: { seconds: BigInt(1700001000), nanos: 0 },
    activatedAt: undefined,
    deprecatedAt: undefined,
    successorId: '',
  },
  {
    id: 'cccccccc-0000-0000-0000-000000000003',
    code: 'GPU_H',
    version: 1,
    dimension: 6, // COMPUTE
    precision: 13, // precision > 12 triggers overflow warning
    status: 1, // DRAFT
    displayName: 'GPU Hour',
    description: 'Compute unit',
    validationExpression: '',
    fungibilityKeyExpression: '',
    errorMessageExpression: '',
    attributeSchema: '',
    isSystem: false,
    createdAt: { seconds: BigInt(1700002000), nanos: 0 },
    activatedAt: undefined,
    deprecatedAt: undefined,
    successorId: '',
  },
]

describe('InstrumentsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockListInstruments.mockResolvedValue({
      instruments: [],
      nextPageToken: '',
    })
  })

  it('renders page title', () => {
    render(
      <Wrapper>
        <InstrumentsPage />
      </Wrapper>,
    )
    expect(screen.getByRole('heading', { name: /instruments/i })).toBeInTheDocument()
  })

  it('renders column headers', async () => {
    render(
      <Wrapper>
        <InstrumentsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByRole('columnheader', { name: /code/i })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: /dimension/i })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: /precision/i })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: /status/i })).toBeInTheDocument()
      expect(screen.getByRole('columnheader', { name: /display name/i })).toBeInTheDocument()
    })
  })

  it('renders instrument rows when data is available', async () => {
    mockListInstruments.mockResolvedValue({
      instruments: mockInstruments,
      nextPageToken: '',
    })

    render(
      <Wrapper>
        <InstrumentsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByText('GBP')).toBeInTheDocument()
      expect(screen.getByText('KWH')).toBeInTheDocument()
    })
  })

  it('renders status filter dropdown', () => {
    render(
      <Wrapper>
        <InstrumentsPage />
      </Wrapper>,
    )

    expect(screen.getByLabelText(/status/i)).toBeInTheDocument()
  })

  it('renders dimension filter dropdown', () => {
    render(
      <Wrapper>
        <InstrumentsPage />
      </Wrapper>,
    )

    expect(screen.getByLabelText(/dimension/i)).toBeInTheDocument()
  })

  it('shows empty state when no instruments match', async () => {
    mockListInstruments.mockResolvedValue({
      instruments: [],
      nextPageToken: '',
    })

    render(
      <Wrapper>
        <InstrumentsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByTestId('empty-state')).toBeInTheDocument()
    })
  })

  it('shows precision overflow warning for precision > 12', async () => {
    mockListInstruments.mockResolvedValue({
      instruments: [mockInstruments[2]], // GPU_H with precision 13
      nextPageToken: '',
    })

    render(
      <Wrapper>
        <InstrumentsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.getByTestId('precision-overflow-warning')).toBeInTheDocument()
    })
  })

  it('does not show precision overflow warning for precision <= 12', async () => {
    mockListInstruments.mockResolvedValue({
      instruments: [mockInstruments[0]], // GBP with precision 2
      nextPageToken: '',
    })

    render(
      <Wrapper>
        <InstrumentsPage />
      </Wrapper>,
    )

    await waitFor(() => {
      expect(screen.queryByTestId('precision-overflow-warning')).not.toBeInTheDocument()
    })
  })

  it('calls listInstruments with status filter', async () => {
    const user = userEvent.setup()

    render(
      <Wrapper>
        <InstrumentsPage />
      </Wrapper>,
    )

    const statusFilter = screen.getByLabelText(/status/i)
    await user.selectOptions(statusFilter, '2') // ACTIVE

    await waitFor(() => {
      expect(mockListInstruments).toHaveBeenCalledWith(
        expect.objectContaining({
          statusFilter: 2,
        }),
      )
    })
  })

  it('calls listInstruments with dimension filter', async () => {
    const user = userEvent.setup()

    render(
      <Wrapper>
        <InstrumentsPage />
      </Wrapper>,
    )

    const dimFilter = screen.getByLabelText(/dimension/i)
    await user.selectOptions(dimFilter, '1') // CURRENCY

    await waitFor(() => {
      expect(mockListInstruments).toHaveBeenCalledWith(
        expect.objectContaining({
          dimensionFilter: 1,
        }),
      )
    })
  })

  it('renders CEL playground section', () => {
    render(
      <Wrapper>
        <InstrumentsPage />
      </Wrapper>,
    )

    expect(screen.getByTestId('cel-playground')).toBeInTheDocument()
  })

  it('calls evaluateInstrument when CEL playground run button clicked', async () => {
    const user = userEvent.setup()

    render(
      <Wrapper>
        <InstrumentsPage />
      </Wrapper>,
    )

    const runButton = screen.getByRole('button', { name: /evaluate/i })
    await user.click(runButton)

    await waitFor(() => {
      expect(mockEvaluateInstrument).toHaveBeenCalled()
    })
  })

  it('shows CEL evaluation result after running', async () => {
    const user = userEvent.setup()
    mockEvaluateInstrument.mockResolvedValue({
      compileErrors: [],
      validationResult: true,
      fungibilityKey: 'GBP:1',
      errorMessage: '',
    })

    render(
      <Wrapper>
        <InstrumentsPage />
      </Wrapper>,
    )

    const runButton = screen.getByRole('button', { name: /evaluate/i })
    await user.click(runButton)

    await waitFor(() => {
      expect(screen.getByTestId('cel-result')).toBeInTheDocument()
    })
  })

  it('renders Register Instrument button in the header', () => {
    render(
      <Wrapper>
        <InstrumentsPage />
      </Wrapper>,
    )
    expect(screen.getByRole('button', { name: /register instrument/i })).toBeInTheDocument()
  })

  it('opens RegisterInstrumentDialog when Register Instrument button is clicked', async () => {
    const user = userEvent.setup()

    render(
      <Wrapper>
        <InstrumentsPage />
      </Wrapper>,
    )

    await user.click(screen.getByRole('button', { name: /register instrument/i }))

    await waitFor(() => {
      expect(screen.getByRole('dialog')).toBeInTheDocument()
    })
  })

  describe('Platform badge', () => {
    it('renders Platform badge for isSystem instruments', async () => {
      mockListInstruments.mockResolvedValue({
        instruments: mockInstruments,
        nextPageToken: '',
      })

      render(
        <Wrapper>
          <InstrumentsPage />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getByText('GBP')).toBeInTheDocument()
      })

      const badges = screen.getAllByText('Platform')
      expect(badges).toHaveLength(1)
    })

    it('does not render Platform badge for non-system instruments', async () => {
      mockListInstruments.mockResolvedValue({
        instruments: [mockInstruments[1]], // KWH with isSystem: false
        nextPageToken: '',
      })

      render(
        <Wrapper>
          <InstrumentsPage />
        </Wrapper>,
      )

      await waitFor(() => {
        expect(screen.getByText('KWH')).toBeInTheDocument()
      })

      expect(screen.queryByText('Platform')).not.toBeInTheDocument()
    })
  })
})
