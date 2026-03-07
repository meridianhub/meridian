import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { ManifestsPage } from './index'

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
  ApiClientProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

vi.mock('../components/manifest-graph', () => ({
  ManifestGraph: ({ manifest }: { manifest: unknown }) => (
    <div data-testid="manifest-graph">Graph rendered</div>
  ),
}))

import { useApiClients } from '@/api/context'

function mockApiClients(overrides?: {
  getCurrentManifest?: ReturnType<typeof vi.fn>
}) {
  vi.mocked(useApiClients).mockReturnValue({
    manifestHistory: {
      getCurrentManifest: overrides?.getCurrentManifest ?? vi.fn().mockResolvedValue({ version: null }),
      listManifestVersions: vi.fn().mockResolvedValue({ versions: [], totalCount: 0 }),
      getManifestVersion: vi.fn(),
    },
    manifestApplier: {
      applyManifest: vi.fn(),
    },
  } as unknown as ReturnType<typeof useApiClients>)
}

function renderManifestsPage() {
  return renderWithProviders(
    <MemoryRouter initialEntries={['/manifests']}>
      <Routes>
        <Route path="/manifests" element={<ManifestsPage />} />
      </Routes>
    </MemoryRouter>,
    { initialToken: createTenantUserToken() },
  )
}

describe('ManifestsPage', () => {
  beforeEach(() => mockApiClients())

  it('renders page heading', async () => {
    renderManifestsPage()

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: /manifest configuration/i })).toBeInTheDocument()
    })
  })

  it('renders Apply Manifest button', async () => {
    renderManifestsPage()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /apply manifest/i })).toBeInTheDocument()
    })
  })

  it('opens ManifestApplyDialog when Apply Manifest is clicked', async () => {
    renderManifestsPage()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /apply manifest/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('button', { name: /apply manifest/i }))

    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('renders Current Manifest, Version History, and Graph tabs', async () => {
    renderManifestsPage()

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /current manifest/i })).toBeInTheDocument()
      expect(screen.getByRole('tab', { name: /version history/i })).toBeInTheDocument()
      expect(screen.getByRole('tab', { name: /graph/i })).toBeInTheDocument()
    })
  })

  it('defaults to Current Manifest tab', async () => {
    renderManifestsPage()

    await waitFor(() => {
      const currentTab = screen.getByRole('tab', { name: /current manifest/i })
      expect(currentTab).toHaveAttribute('data-state', 'active')
    })
  })

  it('switches to Version History tab', async () => {
    renderManifestsPage()

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /version history/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('tab', { name: /version history/i }))

    const historyTab = screen.getByRole('tab', { name: /version history/i })
    expect(historyTab).toHaveAttribute('data-state', 'active')
  })

  it('switches to Graph tab', async () => {
    renderManifestsPage()

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /graph/i })).toBeInTheDocument()
    })

    await userEvent.click(screen.getByRole('tab', { name: /graph/i }))

    const graphTab = screen.getByRole('tab', { name: /graph/i })
    expect(graphTab).toHaveAttribute('data-state', 'active')
  })

  it('shows empty state in Graph tab when no manifest is applied', async () => {
    renderManifestsPage()

    await userEvent.click(screen.getByRole('tab', { name: /graph/i }))

    await waitFor(() => {
      expect(screen.getByTestId('graph-empty')).toBeInTheDocument()
    })
  })

  it('shows error state in Graph tab on fetch failure', async () => {
    mockApiClients({
      getCurrentManifest: vi.fn().mockRejectedValue(new Error('Network error')),
    })

    renderManifestsPage()

    await userEvent.click(screen.getByRole('tab', { name: /graph/i }))

    await waitFor(() => {
      expect(screen.getByTestId('graph-error')).toBeInTheDocument()
      expect(screen.getByText(/network error/i)).toBeInTheDocument()
    })
  })

  it('renders ManifestGraph when manifest data is available', async () => {
    mockApiClients({
      getCurrentManifest: vi.fn().mockResolvedValue({
        version: {
          version: 1,
          manifest: {
            metadata: { name: 'Test', industry: 'energy' },
            instruments: [],
            accountTypes: [],
            valuationRules: [],
            sagas: [],
          },
          appliedAt: { seconds: BigInt(1700000000), nanos: 0 },
          appliedBy: 'admin',
          applyStatus: 1,
        },
      }),
    })

    renderManifestsPage()

    await userEvent.click(screen.getByRole('tab', { name: /graph/i }))

    await waitFor(() => {
      expect(screen.getByTestId('manifest-graph')).toBeInTheDocument()
    })
  })
})
