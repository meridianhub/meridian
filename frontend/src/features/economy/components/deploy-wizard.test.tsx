import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { DeployWizard } from './deploy-wizard'
import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'

// ── Mocks ──────────────────────────────────────────────────────────────────

const mockApplyManifest = vi.fn()
const mockPlanManifestAsync = vi.fn()
let mockPlan: unknown = null

vi.mock('@/api/context', () => ({
  useApiClients: () => ({
    manifestApplier: { applyManifest: mockApplyManifest },
  }),
}))

vi.mock('@/contexts/auth-context', () => ({
  useAuth: () => ({ claims: { userId: 'test-user' }, lens: 'platform' }),
}))

vi.mock('../hooks/use-manifest-plan', () => ({
  useManifestPlan: () => ({
    plan: mockPlan,
    planManifestAsync: mockPlanManifestAsync,
    isPlanning: false,
  }),
}))

function renderWizard(props?: Partial<React.ComponentProps<typeof DeployWizard>>) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const manifest = {} as Manifest
  return render(
    <QueryClientProvider client={queryClient}>
      <DeployWizard
        manifest={manifest}
        manifestChanged={false}
        {...props}
      />
    </QueryClientProvider>,
  )
}

// ── Tests ──────────────────────────────────────────────────────────────────

describe('DeployWizard', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockPlan = null
  })

  describe('Apply button disabled state', () => {
    it('shows explanation when Apply is disabled due to blocking errors', async () => {
      // Plan with validation errors
      mockPlan = {
        diffSummary: '1 modify',
        counts: { add: 0, modify: 1, remove: 0 },
        validationErrors: [
          { severity: 'ERROR', path: 'instruments[0].code', message: 'Immutable field cannot be changed' },
        ],
      }

      mockPlanManifestAsync.mockResolvedValue(undefined)

      renderWizard()

      // Click Plan to move to review state
      const planButton = screen.getByRole('button', { name: /plan/i })
      await userEvent.click(planButton)

      // The Apply button should be disabled
      const applyButton = screen.getByTestId('deploy-apply-button')
      expect(applyButton).toBeDisabled()

      // There should be an explanatory message about why Apply is disabled
      expect(screen.getByText(/cannot apply/i)).toBeInTheDocument()
    })

    it('shows explanation when Apply is disabled due to stale plan', async () => {
      mockPlan = {
        diffSummary: '1 add',
        counts: { add: 1, modify: 0, remove: 0 },
        validationErrors: [],
      }

      mockPlanManifestAsync.mockResolvedValue(undefined)

      renderWizard({ manifestChanged: true })

      const planButton = screen.getByRole('button', { name: /plan/i })
      await userEvent.click(planButton)

      // Stale plan warning should be visible
      expect(screen.getByText(/manifest has changed/i)).toBeInTheDocument()
    })
  })
})
