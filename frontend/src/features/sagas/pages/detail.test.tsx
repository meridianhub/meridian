import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import type { ServiceClients } from '@/api/clients'
import { StarlarkDetailPage } from './detail'

// Mock useApiClients
vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
}))

// Mock CodeMirror (same as starlark-editor tests)
vi.mock('codemirror', () => ({
  basicSetup: [],
}))

const mockDispatch = vi.fn()

vi.mock('@codemirror/view', () => ({
  EditorView: class MockEditorView {
    static editable = { of: vi.fn(() => ({})) }
    static updateListener = { of: vi.fn(() => ({})) }
    dom: HTMLElement
    state: { doc: { toString: () => string } }
    dispatch = mockDispatch

    constructor(config: {
      doc?: string
      extensions?: unknown[]
      parent?: HTMLElement
    }) {
      this.dom = document.createElement('div')
      this.dom.className = 'cm-editor'
      this.dom.setAttribute('data-testid', 'codemirror-editor')
      this.state = { doc: { toString: () => config.doc ?? '' } }
      if (config.parent) {
        config.parent.appendChild(this.dom)
      }
    }

    destroy() {
      this.dom.remove()
    }
  },
  keymap: { of: vi.fn(() => ({})) },
}))

vi.mock('@codemirror/state', () => ({
  Compartment: class {
    of(value: unknown) { return value }
    reconfigure(value: unknown) { return value }
  },
  EditorState: {
    create: vi.fn(() => ({})),
    readOnly: { of: vi.fn(() => ({})) },
  },
  Transaction: {
    userEvent: 'userEvent',
  },
}))

vi.mock('@codemirror/lang-python', () => ({
  python: vi.fn(() => ({})),
}))

vi.mock('@codemirror/lint', () => ({
  linter: vi.fn((fn: () => unknown) => fn),
  lintGutter: vi.fn(() => ({})),
}))

import { useApiClients } from '@/api/context'

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity },
    },
  })
}

function renderWithRoute(definitionId: string, clients: ReturnType<typeof makeMockClients>) {
  vi.mocked(useApiClients).mockReturnValue(clients as unknown as ServiceClients)
  const qc = makeQueryClient()
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[`/starlark-config/${definitionId}`]}>
        <Routes>
          <Route path="/starlark-config/:definitionId" element={<StarlarkDetailPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

const activeSaga = {
  id: 'saga-1',
  name: 'current_account_withdrawal',
  version: 1,
  script: 'def saga():\n  # Main saga logic\n  pass',
  status: 2, // ACTIVE
  isSystem: true,
  displayName: 'Current Account Withdrawal',
  description: 'Handles withdrawals from current accounts',
  createdAt: undefined,
  updatedAt: { seconds: BigInt(1707000000), nanos: 0 },
  activatedAt: { seconds: BigInt(1707000010), nanos: 0 },
  deprecatedAt: undefined,
  successorId: '',
  preconditionsExpression: '',
}

const draftSaga = {
  id: 'saga-2',
  name: 'payment_initiation',
  version: 2,
  script: 'def saga():\n  pass',
  status: 1, // DRAFT
  isSystem: false,
  displayName: 'Payment Initiation',
  description: 'Initiates a payment',
  createdAt: undefined,
  updatedAt: { seconds: BigInt(1707000001), nanos: 0 },
  activatedAt: undefined,
  deprecatedAt: undefined,
  successorId: '',
  preconditionsExpression: '',
}

const tenantOverrideSaga = {
  id: 'saga-3',
  name: 'current_account_withdrawal',
  version: 1,
  script: 'def saga():\n  # Override logic\n  pass',
  status: 2, // ACTIVE
  isSystem: false,
  displayName: 'Current Account Withdrawal (Override)',
  description: 'Tenant-specific withdrawal logic',
  createdAt: undefined,
  updatedAt: { seconds: BigInt(1707000002), nanos: 0 },
  activatedAt: undefined,
  deprecatedAt: undefined,
  successorId: '',
  preconditionsExpression: '',
}

function makeMockClients(options: {
  saga?: typeof activeSaga
  activeSaga?: typeof activeSaga
  validateSuccess?: boolean
  validateErrors?: Array<{ line: number; column: number; message: string; category: number }>
  validateMetrics?: { handlerCallCount: number; operationCount: number; estimatedDurationMs: number; complexityScore: number }
} = {}) {
  const saga = options.saga ?? activeSaga
  return {
    sagaRegistry: {
      getSaga: vi.fn().mockResolvedValue({ saga }),
      getActiveSaga: options.activeSaga
        ? vi.fn().mockResolvedValue({ saga: options.activeSaga, isTenantOverride: true })
        : vi.fn().mockResolvedValue({ saga, isTenantOverride: false }),
      validateSaga: vi.fn().mockResolvedValue({
        success: options.validateSuccess ?? true,
        errors: options.validateErrors ?? [],
        metrics: options.validateMetrics ?? {
          handlerCallCount: 3,
          operationCount: 5,
          estimatedDurationMs: 120,
          complexityScore: 2,
        },
        formattedReport: '',
      }),
      activateSaga: vi.fn().mockResolvedValue({ saga: { ...saga, status: 2 }, validation: {} }),
      deprecateSaga: vi.fn().mockResolvedValue({ saga: { ...saga, status: 3 } }),
      updateSagaDefinition: vi.fn().mockResolvedValue({ saga }),
    },
  }
}

describe('StarlarkDetailPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  describe('rendering', () => {
    it('renders page with saga name as heading', async () => {
      renderWithRoute('saga-1', makeMockClients())

      await waitFor(() => {
        expect(screen.getByRole('heading', { name: /current_account_withdrawal/i })).toBeInTheDocument()
      })
    })

    it('renders StarlarkEditor with saga script', async () => {
      renderWithRoute('saga-1', makeMockClients())

      await waitFor(() => {
        expect(screen.getByTestId('starlark-editor')).toBeInTheDocument()
      })
    })

    it('shows status badge for the saga', async () => {
      renderWithRoute('saga-1', makeMockClients())

      await waitFor(() => {
        expect(screen.getByText('ACTIVE')).toBeInTheDocument()
      })
    })

    it('shows description when available', async () => {
      renderWithRoute('saga-1', makeMockClients())

      await waitFor(() => {
        expect(screen.getByText('Handles withdrawals from current accounts')).toBeInTheDocument()
      })
    })

    it('renders loading skeleton while fetching', () => {
      vi.mocked(useApiClients).mockReturnValue({
        sagaRegistry: {
          getSaga: vi.fn().mockReturnValue(new Promise(() => {})),
          getActiveSaga: vi.fn().mockReturnValue(new Promise(() => {})),
        },
      } as unknown as ServiceClients)

      const qc = makeQueryClient()
      render(
        <QueryClientProvider client={qc}>
          <MemoryRouter initialEntries={['/starlark-config/saga-1']}>
            <Routes>
              <Route path="/starlark-config/:definitionId" element={<StarlarkDetailPage />} />
            </Routes>
          </MemoryRouter>
        </QueryClientProvider>,
      )

      expect(screen.getByTestId('detail-skeleton')).toBeInTheDocument()
    })
  })

  describe('tenant override view - split pane', () => {
    it('shows split pane when tenant has an override for the saga name', async () => {
      // draftSaga is a tenant override (isSystem = false), and we also provide activeSaga
      renderWithRoute(
        'saga-3',
        makeMockClients({
          saga: tenantOverrideSaga,
          activeSaga: activeSaga,
        }),
      )

      await waitFor(() => {
        expect(screen.getByTestId('split-pane')).toBeInTheDocument()
      })
    })

    it('shows platform default label in split pane', async () => {
      renderWithRoute(
        'saga-3',
        makeMockClients({
          saga: tenantOverrideSaga,
          activeSaga: activeSaga,
        }),
      )

      await waitFor(() => {
        expect(screen.getByText(/Platform Default/i)).toBeInTheDocument()
      })
    })

    it('shows tenant override label in split pane', async () => {
      renderWithRoute(
        'saga-3',
        makeMockClients({
          saga: tenantOverrideSaga,
          activeSaga: activeSaga,
        }),
      )

      await waitFor(() => {
        expect(screen.getByText(/Tenant Override/i)).toBeInTheDocument()
      })
    })

    it('does not show split pane for system (platform default) saga', async () => {
      // activeSaga is a system saga, no tenant override
      renderWithRoute('saga-1', makeMockClients({ saga: activeSaga }))

      await waitFor(() => {
        expect(screen.queryByTestId('split-pane')).not.toBeInTheDocument()
      })
    })
  })

  describe('validation - ValidateSaga RPC', () => {
    it('calls ValidateSaga RPC when validate button is clicked', async () => {
      const user = userEvent.setup()
      const clients = makeMockClients({ saga: draftSaga })
      renderWithRoute('saga-2', clients)

      const validateButton = await screen.findByRole('button', { name: /Validate/i })
      await user.click(validateButton)

      expect(clients.sagaRegistry.validateSaga).toHaveBeenCalledWith(
        expect.objectContaining({
          sagaName: 'payment_initiation',
          script: expect.any(String),
        }),
      )
    })

    it('displays complexity metrics after successful validation', async () => {
      const user = userEvent.setup()
      const clients = makeMockClients({
        saga: draftSaga,
        validateSuccess: true,
        validateMetrics: {
          handlerCallCount: 3,
          operationCount: 7,
          estimatedDurationMs: 150,
          complexityScore: 4,
        },
      })
      renderWithRoute('saga-2', clients)

      const validateButton = await screen.findByRole('button', { name: /Validate/i })
      await user.click(validateButton)

      await waitFor(() => {
        expect(screen.getByTestId('complexity-metrics-panel')).toBeInTheDocument()
      })
    })

    it('shows validation errors in StarlarkEditor when validation fails', async () => {
      const user = userEvent.setup()
      const clients = makeMockClients({
        saga: draftSaga,
        validateSuccess: false,
        validateErrors: [
          { line: 2, column: 1, message: 'Undefined handler: foo_bar', category: 2 },
        ],
      })
      renderWithRoute('saga-2', clients)

      const validateButton = await screen.findByRole('button', { name: /Validate/i })
      await user.click(validateButton)

      await waitFor(() => {
        expect(screen.getByTestId('error-panel')).toBeInTheDocument()
        expect(screen.getByText('Undefined handler: foo_bar')).toBeInTheDocument()
      })
    })
  })

  describe('activate/deprecate state transitions', () => {
    it('shows Activate button for DRAFT saga', async () => {
      renderWithRoute('saga-2', makeMockClients({ saga: draftSaga }))

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /Activate/i })).toBeInTheDocument()
      })
    })

    it('shows Deprecate button for ACTIVE saga', async () => {
      renderWithRoute('saga-1', makeMockClients({ saga: activeSaga }))

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /Deprecate/i })).toBeInTheDocument()
      })
    })

    it('does not show Activate or Deprecate for DEPRECATED saga', async () => {
      const deprecatedSaga = { ...activeSaga, status: 3 }
      renderWithRoute('saga-1', makeMockClients({ saga: deprecatedSaga }))

      await waitFor(() => {
        expect(screen.queryByRole('button', { name: /Activate/i })).not.toBeInTheDocument()
        expect(screen.queryByRole('button', { name: /Deprecate/i })).not.toBeInTheDocument()
      })
    })

    it('calls ActivateSaga RPC when Activate is clicked', async () => {
      const user = userEvent.setup()
      const clients = makeMockClients({ saga: draftSaga })
      renderWithRoute('saga-2', clients)

      const activateButton = await screen.findByRole('button', { name: /Activate/i })
      await user.click(activateButton)

      expect(clients.sagaRegistry.activateSaga).toHaveBeenCalledWith({ id: 'saga-2' })
    })

    it('calls DeprecateSaga RPC when Deprecate is clicked', async () => {
      const user = userEvent.setup()
      const clients = makeMockClients({ saga: activeSaga })
      renderWithRoute('saga-1', clients)

      const deprecateButton = await screen.findByRole('button', { name: /Deprecate/i })
      await user.click(deprecateButton)

      expect(clients.sagaRegistry.deprecateSaga).toHaveBeenCalledWith({ id: 'saga-1', successorId: '' })
    })

    it('editor is read-only for ACTIVE system saga', async () => {
      renderWithRoute('saga-1', makeMockClients({ saga: activeSaga }))

      await waitFor(() => {
        expect(screen.getByTestId('readonly-badge')).toBeInTheDocument()
      })
    })

    it('editor is editable for DRAFT non-system saga', async () => {
      renderWithRoute('saga-2', makeMockClients({ saga: draftSaga }))

      await waitFor(() => {
        expect(screen.queryByTestId('readonly-badge')).not.toBeInTheDocument()
      })
    })
  })

  describe('complexity metrics display', () => {
    it('shows complexity metrics panel after validation', async () => {
      const user = userEvent.setup()
      const clients = makeMockClients({
        saga: draftSaga,
        validateSuccess: true,
        validateMetrics: {
          handlerCallCount: 5,
          operationCount: 10,
          estimatedDurationMs: 200,
          complexityScore: 3,
        },
      })
      renderWithRoute('saga-2', clients)

      const validateButton = await screen.findByRole('button', { name: /Validate/i })
      await user.click(validateButton)

      await waitFor(() => {
        const panel = screen.getByTestId('complexity-metrics-panel')
        expect(panel).toBeInTheDocument()
      })
    })
  })
})
