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

import { useApiClients } from '@/api/context'

function mockApiClients() {
  vi.mocked(useApiClients).mockReturnValue({
    manifestHistory: {
      getCurrentManifest: vi.fn().mockResolvedValue({ version: null }),
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

  it('renders Current Manifest and Version History tabs', async () => {
    renderManifestsPage()

    await waitFor(() => {
      expect(screen.getByRole('tab', { name: /current manifest/i })).toBeInTheDocument()
      expect(screen.getByRole('tab', { name: /version history/i })).toBeInTheDocument()
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
})
