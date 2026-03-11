import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { EconomyEditPage } from './economy-edit-page'
import { ApplyStatus } from '@/api/gen/meridian/control_plane/v1/manifest_history_service_pb'

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
  ApiClientProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

// ManifestEditor uses CodeMirror which is hard to test in jsdom — stub it
vi.mock('../components/manifest-editor', () => ({
  ManifestEditor: ({ value, onChange }: { value: string; onChange: (v: string) => void }) => (
    <textarea
      data-testid="manifest-editor"
      value={value}
      onChange={(e) => onChange(e.target.value)}
    />
  ),
}))

// EditorGraphPanel is complex — stub it
vi.mock('../components/editor-graph-panel', () => ({
  EditorGraphPanel: ({ validationPassed }: { validationPassed: boolean }) => (
    <div data-testid="editor-graph-panel" data-validation-passed={String(validationPassed)} />
  ),
}))

// DeployWizard has complex state — stub it
vi.mock('../components/deploy-wizard', () => ({
  DeployWizard: ({ manifestChanged }: { manifestChanged: boolean }) => (
    <div data-testid="deploy-wizard" data-manifest-changed={String(manifestChanged)} />
  ),
}))

// ValidationPanel
vi.mock('../components/validation-panel', () => ({
  ValidationPanel: ({ errors, warnings }: { errors: unknown[]; warnings: unknown[] }) => (
    <div
      data-testid="validation-panel"
      data-errors={errors.length}
      data-warnings={warnings.length}
    />
  ),
}))

// useManifestValidate
vi.mock('../hooks/use-manifest-validate', () => ({
  useManifestValidate: vi.fn(),
}))

import { useApiClients } from '@/api/context'
import { useManifestValidate } from '../hooks/use-manifest-validate'

const mockManifestVersion = {
  id: 'mv-1',
  version: '1.0',
  manifest: {
    version: '1.0',
    metadata: {
      name: 'Test Economy',
      industry: 'energy',
      description: 'Test manifest',
    },
    instruments: [],
    accountTypes: [],
    valuationRules: [],
    sagas: [],
    seedData: undefined,
    paymentRails: [],
    partyTypes: [],
    mappings: [],
  },
  appliedAt: { seconds: BigInt(1700000000), nanos: 0 },
  appliedBy: 'admin@example.com',
  applyStatus: ApplyStatus.APPLIED,
  applyJobId: 'job-1',
  diffSummary: 'Initial manifest',
}

function mockApiClients(overrides: Record<string, unknown> = {}) {
  vi.mocked(useApiClients).mockReturnValue({
    manifestHistory: {
      getCurrentManifest: vi.fn().mockResolvedValue({ version: mockManifestVersion }),
      ...overrides,
    },
    manifestApplier: {
      applyManifest: vi.fn().mockResolvedValue({ status: 0, validationErrors: [], stepResults: [] }),
    },
  } as unknown as ReturnType<typeof useApiClients>)
}

function mockValidate() {
  vi.mocked(useManifestValidate).mockReturnValue({
    validate: vi.fn(),
    isValidating: false,
    result: null,
  })
}

function renderPage() {
  return renderWithProviders(
    <MemoryRouter>
      <EconomyEditPage />
    </MemoryRouter>,
    { initialToken: createTenantUserToken() },
  )
}

describe('EconomyEditPage', () => {
  beforeEach(() => {
    mockApiClients()
    mockValidate()
  })

  it('renders the manifest editor', async () => {
    renderPage()
    await waitFor(() => {
      expect(screen.getByTestId('manifest-editor')).toBeInTheDocument()
    })
  })

  it('renders the editor graph panel', async () => {
    renderPage()
    await waitFor(() => {
      expect(screen.getByTestId('editor-graph-panel')).toBeInTheDocument()
    })
  })

  it('renders the deploy wizard', async () => {
    renderPage()
    await waitFor(() => {
      expect(screen.getByTestId('deploy-wizard')).toBeInTheDocument()
    })
  })

  it('loads current manifest and converts it to YAML for the editor', async () => {
    renderPage()
    await waitFor(() => {
      const editor = screen.getByTestId('manifest-editor')
      // The YAML should contain the version field from the manifest
      expect((editor as HTMLTextAreaElement).value).toContain('version')
    })
  })

  it('shows loading state while fetching manifest', () => {
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: {
        getCurrentManifest: vi.fn().mockReturnValue(new Promise(() => {})),
      },
      manifestApplier: {
        applyManifest: vi.fn(),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderPage()
    expect(screen.getByTestId('edit-page-loading')).toBeInTheDocument()
  })

  it('uses skeleton manifest when no current manifest exists', async () => {
    vi.mocked(useApiClients).mockReturnValue({
      manifestHistory: {
        getCurrentManifest: vi.fn().mockResolvedValue({ version: undefined }),
      },
      manifestApplier: {
        applyManifest: vi.fn(),
      },
    } as unknown as ReturnType<typeof useApiClients>)

    renderPage()
    await waitFor(() => {
      const editor = screen.getByTestId('manifest-editor')
      expect((editor as HTMLTextAreaElement).value).toBeTruthy()
    })
  })

  it('passes validationPassed=false to EditorGraphPanel when there are errors', async () => {
    vi.mocked(useManifestValidate).mockReturnValue({
      validate: vi.fn(),
      isValidating: false,
      result: {
        errors: [{ severity: 'ERROR', path: 'instruments', code: 'REQUIRED', message: 'Required', suggestion: '' }],
        warnings: [],
        sequenceNumber: 1,
      },
    })

    renderPage()
    await waitFor(() => {
      const panel = screen.getByTestId('editor-graph-panel')
      expect(panel.getAttribute('data-validation-passed')).toBe('false')
    })
  })

  it('passes validationPassed=true to EditorGraphPanel when no errors', async () => {
    vi.mocked(useManifestValidate).mockReturnValue({
      validate: vi.fn(),
      isValidating: false,
      result: {
        errors: [],
        warnings: [],
        sequenceNumber: 1,
      },
    })

    renderPage()
    await waitFor(() => {
      const panel = screen.getByTestId('editor-graph-panel')
      expect(panel.getAttribute('data-validation-passed')).toBe('true')
    })
  })
})
