import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { ManifestDiffPage } from './manifest-diff-page'

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
  ApiClientProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { useApiClients } from '@/api/context'

vi.mock('../components/manifest-diff-table', () => ({
  ManifestDiffTable: ({ summary }: { summary?: { totalActions: number } }) => (
    <div data-testid="manifest-diff-table">
      {summary && <span>total: {summary.totalActions}</span>}
    </div>
  ),
}))

const mockNavigate = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

function mockApi(overrides: Record<string, unknown> = {}) {
  vi.mocked(useApiClients).mockReturnValue({
    manifestHistory: {
      diffManifestVersions: vi.fn().mockResolvedValue({
        actions: [],
        summary: {
          totalActions: 3,
          creates: 1,
          updates: 1,
          deletes: 1,
          noChanges: 0,
          hasBreakingChanges: false,
        },
      }),
      ...overrides,
    },
  } as unknown as ReturnType<typeof useApiClients>)
}

function renderPage(path: string) {
  return renderWithProviders(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/economy/manifest/diff/:v1/:v2" element={<ManifestDiffPage />} />
        <Route path="*" element={<ManifestDiffPage />} />
      </Routes>
    </MemoryRouter>,
    { initialToken: createTenantUserToken() },
  )
}

describe('ManifestDiffPage', () => {
  beforeEach(() => {
    mockNavigate.mockClear()
    mockApi()
  })

  describe('invalid params', () => {
    it('shows invalid params message when no URL params', () => {
      renderWithProviders(
        <MemoryRouter>
          <ManifestDiffPage />
        </MemoryRouter>,
        { initialToken: createTenantUserToken() },
      )
      expect(screen.getByText(/Invalid version parameters/)).toBeInTheDocument()
    })

    it('renders Back to Economy button on invalid params', () => {
      renderWithProviders(
        <MemoryRouter>
          <ManifestDiffPage />
        </MemoryRouter>,
        { initialToken: createTenantUserToken() },
      )
      expect(screen.getByRole('button', { name: /Back to Economy/i })).toBeInTheDocument()
    })

    it('navigates to /economy when Back to Economy is clicked', async () => {
      renderWithProviders(
        <MemoryRouter>
          <ManifestDiffPage />
        </MemoryRouter>,
        { initialToken: createTenantUserToken() },
      )
      await userEvent.click(screen.getByRole('button', { name: /Back to Economy/i }))
      expect(mockNavigate).toHaveBeenCalledWith('/economy')
    })
  })

  describe('valid params', () => {
    it('shows correct title with version numbers', async () => {
      renderPage('/economy/manifest/diff/1/2')
      expect(await screen.findByText(/Manifest Diff: v1.*v2/)).toBeInTheDocument()
    })

    it('renders ManifestDiffTable when data loads', async () => {
      renderPage('/economy/manifest/diff/1/2')
      expect(await screen.findByTestId('manifest-diff-table')).toBeInTheDocument()
    })

    it('renders Back button', async () => {
      renderPage('/economy/manifest/diff/1/2')
      expect(await screen.findByRole('button', { name: /Back/i })).toBeInTheDocument()
    })

    it('navigates back when Back button clicked', async () => {
      renderPage('/economy/manifest/diff/1/2')
      const backBtn = await screen.findByRole('button', { name: /Back/i })
      await userEvent.click(backBtn)
      expect(mockNavigate).toHaveBeenCalledWith(-1)
    })

    it('shows error message when API fails', async () => {
      vi.mocked(useApiClients).mockReturnValue({
        manifestHistory: {
          diffManifestVersions: vi.fn().mockRejectedValue(new Error('Network error')),
        },
      } as unknown as ReturnType<typeof useApiClients>)

      renderPage('/economy/manifest/diff/1/2')
      expect(await screen.findByText(/Failed to load diff.*Network error/)).toBeInTheDocument()
    })
  })
})
