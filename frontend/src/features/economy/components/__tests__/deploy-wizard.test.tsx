import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ApplyManifestStatus, StepResultStatus } from '@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { DeployWizard } from '../deploy-wizard'
import type { Manifest } from '@/api/gen/meridian/control_plane/v1/manifest_pb'

// ── Mocks ────────────────────────────────────────────────────────────────────

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
  ApiClientProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

vi.mock('../../hooks/use-manifest-plan', () => ({
  useManifestPlan: vi.fn(),
}))

import { useApiClients } from '@/api/context'
import { useManifestPlan } from '../../hooks/use-manifest-plan'

// ── Helpers ──────────────────────────────────────────────────────────────────

const fakeManifest = { $typeName: 'meridian.control_plane.v1.Manifest' } as unknown as Manifest

function makeValidationError(overrides: {
  path?: string
  message?: string
  code?: string
  severity?: string
  suggestion?: string
} = {}) {
  return {
    $typeName: 'meridian.control_plane.v1.ValidationError' as const,
    severity: 'ERROR',
    path: '',
    code: '',
    message: 'Validation error',
    suggestion: '',
    ...overrides,
  }
}

function makePlan(overrides: Record<string, unknown> = {}) {
  return {
    status: 1, // DRY_RUN
    diffSummary: '1 add, 0 modify, 0 remove',
    stepResults: [],
    validationErrors: [],
    counts: { add: 1, modify: 0, remove: 0 },
    ...overrides,
  }
}

function mockPlanHook(overrides: Record<string, unknown> = {}) {
  const planManifestAsync = vi.fn().mockResolvedValue(makePlan())
  vi.mocked(useManifestPlan).mockReturnValue({
    plan: null,
    planManifest: vi.fn(),
    planManifestAsync,
    isPlanning: false,
    error: null,
    ...overrides,
  } as unknown as ReturnType<typeof useManifestPlan>)
  return { planManifestAsync }
}

function mockApiClients(applyManifest = vi.fn().mockResolvedValue({})) {
  vi.mocked(useApiClients).mockReturnValue({
    manifestApplier: { applyManifest },
  } as unknown as ReturnType<typeof useApiClients>)
  return { applyManifest }
}

function renderWizard(props: Partial<React.ComponentProps<typeof DeployWizard>> = {}) {
  return renderWithProviders(
    <DeployWizard
      manifest={fakeManifest}
      manifestChanged={false}
      onLineClick={vi.fn()}
      onSuggestionApply={vi.fn()}
      onReloadManifest={vi.fn()}
      {...props}
    />,
    { initialToken: createTenantUserToken() },
  )
}

// ── Tests ────────────────────────────────────────────────────────────────────

describe('DeployWizard', () => {
  beforeEach(() => {
    mockPlanHook()
    mockApiClients()
  })

  // Subtask 1: step state machine
  describe('step state machine', () => {
    it('shows idle step label on mount', () => {
      renderWizard()
      expect(screen.getByTestId('deploy-step-label')).toHaveTextContent('Ready to plan')
    })

    it('shows Plan button in idle state', () => {
      renderWizard()
      expect(screen.getByRole('button', { name: /^plan$/i })).toBeInTheDocument()
    })

    it('transitions to planning then review after successful plan', async () => {
      const user = userEvent.setup()
      // After planManifestAsync resolves, useManifestPlan should reflect the plan.
      // We re-render by updating the mock mid-interaction using a resolved promise.
      const plan = makePlan()
      let resolvePlan!: (value: typeof plan) => void
      const planManifestAsync = vi.fn().mockReturnValue(
        new Promise<typeof plan>((resolve) => { resolvePlan = resolve }),
      )
      vi.mocked(useManifestPlan).mockReturnValue({
        plan: null,
        planManifest: vi.fn(),
        planManifestAsync,
        isPlanning: false,
        error: null,
      } as unknown as ReturnType<typeof useManifestPlan>)

      renderWizard()
      await user.click(screen.getByRole('button', { name: /^plan$/i }))

      resolvePlan(plan)
      await waitFor(() => {
        expect(screen.getByTestId('deploy-step-label')).toHaveTextContent('Review plan')
      })
    })

    it('shows error step label when plan throws', async () => {
      const user = userEvent.setup()
      const planManifestAsync = vi.fn().mockRejectedValue(new Error('Network error'))
      vi.mocked(useManifestPlan).mockReturnValue({
        plan: null,
        planManifest: vi.fn(),
        planManifestAsync,
        isPlanning: false,
        error: null,
      } as unknown as ReturnType<typeof useManifestPlan>)

      renderWizard()
      await user.click(screen.getByRole('button', { name: /^plan$/i }))

      await waitFor(() => {
        expect(screen.getByTestId('deploy-step-label')).toHaveTextContent('Failed')
      })
      expect(screen.getByText(/Planning failed/)).toBeInTheDocument()
    })
  })

  // Subtask 2: useManifestPlan integration and plan hash tracking
  describe('plan hook integration', () => {
    it('calls planManifestAsync with the manifest when Plan is clicked', async () => {
      const user = userEvent.setup()
      const { planManifestAsync } = mockPlanHook()
      mockApiClients()
      renderWizard()
      await user.click(screen.getByRole('button', { name: /^plan$/i }))
      expect(planManifestAsync).toHaveBeenCalledWith(fakeManifest)
    })

    it('shows planning spinner while isPlanning is true', () => {
      vi.mocked(useManifestPlan).mockReturnValue({
        plan: null,
        planManifest: vi.fn(),
        planManifestAsync: vi.fn(),
        isPlanning: true,
        error: null,
      } as unknown as ReturnType<typeof useManifestPlan>)
      renderWizard()
      expect(screen.getByRole('button', { name: /planning/i })).toBeDisabled()
    })

    it('shows diff summary when plan is available in review step', async () => {
      const user = userEvent.setup()
      const plan = makePlan({ diffSummary: '3 add, 1 modify, 0 remove' })
      const planManifestAsync = vi.fn().mockResolvedValue(plan)
      vi.mocked(useManifestPlan)
        .mockReturnValueOnce({
          plan: null,
          planManifest: vi.fn(),
          planManifestAsync,
          isPlanning: false,
          error: null,
        } as unknown as ReturnType<typeof useManifestPlan>)
        .mockReturnValue({
          plan,
          planManifest: vi.fn(),
          planManifestAsync,
          isPlanning: false,
          error: null,
        } as unknown as ReturnType<typeof useManifestPlan>)

      renderWizard()
      await user.click(screen.getByRole('button', { name: /^plan$/i }))

      await waitFor(() => {
        expect(screen.getByText('3 add, 1 modify, 0 remove')).toBeInTheDocument()
      })
    })
  })

  // Subtask 3: confirmation modal and apply mutation
  describe('confirmation modal and apply', () => {
    async function planAndReview() {
      const user = userEvent.setup()
      const plan = makePlan()
      const planManifestAsync = vi.fn().mockResolvedValue(plan)
      vi.mocked(useManifestPlan)
        .mockReturnValueOnce({
          plan: null,
          planManifest: vi.fn(),
          planManifestAsync,
          isPlanning: false,
          error: null,
        } as unknown as ReturnType<typeof useManifestPlan>)
        .mockReturnValue({
          plan,
          planManifest: vi.fn(),
          planManifestAsync,
          isPlanning: false,
          error: null,
        } as unknown as ReturnType<typeof useManifestPlan>)

      const applyManifest = vi.fn().mockResolvedValue({})
      mockApiClients(applyManifest)

      renderWizard()
      await user.click(screen.getByRole('button', { name: /^plan$/i }))
      await waitFor(() => screen.getByTestId('deploy-apply-button'))

      return { user, applyManifest }
    }

    it('shows Apply button after planning with no errors', async () => {
      await planAndReview()
      expect(screen.getByTestId('deploy-apply-button')).toBeInTheDocument()
    })

    it('opens confirmation dialog when Apply is clicked', async () => {
      const { user } = await planAndReview()
      await user.click(screen.getByTestId('deploy-apply-button'))
      expect(screen.getByRole('dialog')).toBeInTheDocument()
      expect(screen.getByText(/Confirm Apply/i)).toBeInTheDocument()
    })

    it('calls applyManifest when confirm button is clicked', async () => {
      const { user, applyManifest } = await planAndReview()
      await user.click(screen.getByTestId('deploy-apply-button'))
      await user.click(screen.getByTestId('confirm-apply-button'))
      await waitFor(() => {
        expect(applyManifest).toHaveBeenCalledWith(
          expect.objectContaining({ dryRun: false, force: false }),
        )
      })
    })

    it('shows success step after apply completes', async () => {
      const { user } = await planAndReview()
      await user.click(screen.getByTestId('deploy-apply-button'))
      await user.click(screen.getByTestId('confirm-apply-button'))
      await waitFor(() => {
        expect(screen.getByTestId('deploy-step-label')).toHaveTextContent('Applied')
      })
    })

    it('shows error when apply fails', async () => {
      const user = userEvent.setup()
      const plan = makePlan()
      const planManifestAsync = vi.fn().mockResolvedValue(plan)
      vi.mocked(useManifestPlan)
        .mockReturnValueOnce({
          plan: null,
          planManifest: vi.fn(),
          planManifestAsync,
          isPlanning: false,
          error: null,
        } as unknown as ReturnType<typeof useManifestPlan>)
        .mockReturnValue({
          plan,
          planManifest: vi.fn(),
          planManifestAsync,
          isPlanning: false,
          error: null,
        } as unknown as ReturnType<typeof useManifestPlan>)

      const applyManifest = vi.fn().mockRejectedValue(new Error('Apply failed'))
      mockApiClients(applyManifest)

      renderWizard()
      await user.click(screen.getByRole('button', { name: /^plan$/i }))
      await waitFor(() => screen.getByTestId('deploy-apply-button'))
      await user.click(screen.getByTestId('deploy-apply-button'))
      await user.click(screen.getByTestId('confirm-apply-button'))

      await waitFor(() => {
        expect(screen.getByTestId('deploy-step-label')).toHaveTextContent('Failed')
      })
      expect(screen.getByText('Apply failed')).toBeInTheDocument()
    })

    it('disables Apply button when validation errors exist', async () => {
      const user = userEvent.setup()
      const plan = makePlan({
        validationErrors: [makeValidationError({ severity: 'ERROR', message: 'Bad field' })],
      })
      const planManifestAsync = vi.fn().mockResolvedValue(plan)
      vi.mocked(useManifestPlan)
        .mockReturnValueOnce({
          plan: null,
          planManifest: vi.fn(),
          planManifestAsync,
          isPlanning: false,
          error: null,
        } as unknown as ReturnType<typeof useManifestPlan>)
        .mockReturnValue({
          plan,
          planManifest: vi.fn(),
          planManifestAsync,
          isPlanning: false,
          error: null,
        } as unknown as ReturnType<typeof useManifestPlan>)

      renderWizard()
      await user.click(screen.getByRole('button', { name: /^plan$/i }))
      await waitFor(() => screen.getByTestId('deploy-apply-button'))
      expect(screen.getByTestId('deploy-apply-button')).toBeDisabled()
    })
  })

  // Subtask 4: plan hash invalidation on manifest edit
  describe('plan hash invalidation', () => {
    it('disables Apply and shows stale warning when manifestChanged is true', async () => {
      const user = userEvent.setup()
      const plan = makePlan()
      const planManifestAsync = vi.fn().mockResolvedValue(plan)
      vi.mocked(useManifestPlan)
        .mockReturnValueOnce({
          plan: null,
          planManifest: vi.fn(),
          planManifestAsync,
          isPlanning: false,
          error: null,
        } as unknown as ReturnType<typeof useManifestPlan>)
        .mockReturnValue({
          plan,
          planManifest: vi.fn(),
          planManifestAsync,
          isPlanning: false,
          error: null,
        } as unknown as ReturnType<typeof useManifestPlan>)

      const { rerender } = renderWithProviders(
        <DeployWizard
          manifest={fakeManifest}
          manifestChanged={false}
          onLineClick={vi.fn()}
          onSuggestionApply={vi.fn()}
          onReloadManifest={vi.fn()}
        />,
        { initialToken: createTenantUserToken() },
      )

      await user.click(screen.getByRole('button', { name: /^plan$/i }))
      await waitFor(() => screen.getByTestId('deploy-apply-button'))

      // Simulate manifest edit after planning
      rerender(
        <DeployWizard
          manifest={fakeManifest}
          manifestChanged={true}
          onLineClick={vi.fn()}
          onSuggestionApply={vi.fn()}
          onReloadManifest={vi.fn()}
        />,
      )

      expect(screen.getByTestId('deploy-apply-button')).toBeDisabled()
      expect(screen.getByText(/Manifest has changed since last plan/)).toBeInTheDocument()
    })

    it('re-enables Apply after re-planning with manifestChanged=false', async () => {
      const user = userEvent.setup()
      const plan = makePlan()
      const planManifestAsync = vi.fn().mockResolvedValue(plan)
      vi.mocked(useManifestPlan).mockReturnValue({
        plan,
        planManifest: vi.fn(),
        planManifestAsync,
        isPlanning: false,
        error: null,
      } as unknown as ReturnType<typeof useManifestPlan>)

      const { rerender } = renderWithProviders(
        <DeployWizard
          manifest={fakeManifest}
          manifestChanged={false}
          onLineClick={vi.fn()}
          onSuggestionApply={vi.fn()}
          onReloadManifest={vi.fn()}
        />,
        { initialToken: createTenantUserToken() },
      )

      // Plan once, get to review
      await user.click(screen.getByRole('button', { name: /^plan$/i }))
      await waitFor(() => screen.getByTestId('deploy-apply-button'))

      // Mark stale
      rerender(
        <DeployWizard
          manifest={fakeManifest}
          manifestChanged={true}
          onLineClick={vi.fn()}
          onSuggestionApply={vi.fn()}
          onReloadManifest={vi.fn()}
        />,
      )

      expect(screen.getByTestId('deploy-apply-button')).toBeDisabled()

      // Re-plan
      rerender(
        <DeployWizard
          manifest={fakeManifest}
          manifestChanged={false}
          onLineClick={vi.fn()}
          onSuggestionApply={vi.fn()}
          onReloadManifest={vi.fn()}
        />,
      )
      await user.click(screen.getByRole('button', { name: /re-plan/i }))
      await waitFor(() => {
        expect(screen.getByTestId('deploy-apply-button')).not.toBeDisabled()
      })
    })
  })

  // Subtask 5 (Task 21): Phase stepper integration
  describe('phase stepper', () => {
    async function planAndApplyWith(applyResponse: Record<string, unknown>) {
      const user = userEvent.setup()
      const plan = makePlan()
      const planManifestAsync = vi.fn().mockResolvedValue(plan)
      vi.mocked(useManifestPlan)
        .mockReturnValueOnce({
          plan: null,
          planManifest: vi.fn(),
          planManifestAsync,
          isPlanning: false,
          error: null,
        } as unknown as ReturnType<typeof useManifestPlan>)
        .mockReturnValue({
          plan,
          planManifest: vi.fn(),
          planManifestAsync,
          isPlanning: false,
          error: null,
        } as unknown as ReturnType<typeof useManifestPlan>)

      const applyManifest = vi.fn().mockResolvedValue(applyResponse)
      mockApiClients(applyManifest)

      renderWizard()
      await user.click(screen.getByRole('button', { name: /^plan$/i }))
      await waitFor(() => screen.getByTestId('deploy-apply-button'))
      await user.click(screen.getByTestId('deploy-apply-button'))
      await user.click(screen.getByTestId('confirm-apply-button'))

      return { user }
    }

    it('shows phase stepper after successful apply with step results', async () => {
      await planAndApplyWith({
        status: ApplyManifestStatus.APPLIED,
        stepResults: [
          { $typeName: 'meridian.control_plane.v1.StepResult', stepName: 'validate', status: StepResultStatus.SUCCESS, message: '', details: {} },
          { $typeName: 'meridian.control_plane.v1.StepResult', stepName: 'execute', status: StepResultStatus.SUCCESS, message: '', details: {} },
        ],
        validationErrors: [],
        diffSummary: '',
      })

      await waitFor(() => {
        expect(screen.getByTestId('deploy-step-label')).toHaveTextContent('Applied')
      })
      expect(screen.getByTestId('apply-phases-stepper')).toBeInTheDocument()
      expect(screen.getByTestId('phase-step-validate')).toBeInTheDocument()
      expect(screen.getByTestId('phase-step-execute')).toBeInTheDocument()
    })

    it('shows phase stepper with error for PARTIAL completion', async () => {
      await planAndApplyWith({
        status: ApplyManifestStatus.FAILED,
        stepResults: [
          { $typeName: 'meridian.control_plane.v1.StepResult', stepName: 'validate', status: StepResultStatus.SUCCESS, message: '', details: {} },
          { $typeName: 'meridian.control_plane.v1.StepResult', stepName: 'execute', status: StepResultStatus.FAILED, message: 'Resource conflict', details: {} },
        ],
        validationErrors: [],
        diffSummary: '',
      })

      await waitFor(() => {
        expect(screen.getByTestId('deploy-step-label')).toHaveTextContent('Failed')
      })
      expect(screen.getByTestId('apply-phases-stepper')).toBeInTheDocument()
      expect(screen.getByText('Resource conflict')).toBeInTheDocument()
      expect(screen.getByText(/partial failures/i)).toBeInTheDocument()
    })
  })
})
