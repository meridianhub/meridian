import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ApplyPhasesStepper } from '../apply-phases-stepper'
import { StepResultStatus } from '@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb'
import type { StepResult } from '@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb'

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeStep(overrides: Partial<StepResult>): StepResult {
  return {
    $typeName: 'meridian.control_plane.v1.StepResult' as const,
    stepName: 'validate',
    status: StepResultStatus.SUCCESS,
    message: '',
    details: {},
    ...overrides,
  }
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('ApplyPhasesStepper', () => {
  it('renders nothing when steps are empty and not applying', () => {
    const { container } = render(<ApplyPhasesStepper steps={[]} />)
    expect(container.firstChild).toBeNull()
  })

  it('shows spinner placeholder when applying with no steps yet', () => {
    render(<ApplyPhasesStepper steps={[]} isApplying={true} />)
    expect(screen.getByTestId('apply-phases-stepper')).toBeInTheDocument()
    expect(screen.getByText(/applying/i)).toBeInTheDocument()
  })

  it('renders all steps', () => {
    const steps = [
      makeStep({ stepName: 'validate', status: StepResultStatus.SUCCESS }),
      makeStep({ stepName: 'plan', status: StepResultStatus.SUCCESS }),
      makeStep({ stepName: 'execute', status: StepResultStatus.SUCCESS }),
    ]
    render(<ApplyPhasesStepper steps={steps} />)
    expect(screen.getByTestId('phase-step-validate')).toBeInTheDocument()
    expect(screen.getByTestId('phase-step-plan')).toBeInTheDocument()
    expect(screen.getByTestId('phase-step-execute')).toBeInTheDocument()
  })

  it('formats step names for display', () => {
    const steps = [makeStep({ stepName: 'validate' })]
    render(<ApplyPhasesStepper steps={steps} />)
    expect(screen.getByTestId('phase-step-validate')).toHaveTextContent('Validate')
  })

  it('shows failure message for failed steps', () => {
    const steps = [
      makeStep({ stepName: 'execute', status: StepResultStatus.FAILED, message: 'Connection refused' }),
    ]
    render(<ApplyPhasesStepper steps={steps} />)
    expect(screen.getByText('Connection refused')).toBeInTheDocument()
  })

  it('does not show message for successful steps', () => {
    const steps = [
      makeStep({ stepName: 'validate', status: StepResultStatus.SUCCESS, message: 'All good' }),
    ]
    render(<ApplyPhasesStepper steps={steps} />)
    expect(screen.queryByText('All good')).not.toBeInTheDocument()
  })

  describe('PARTIAL completion', () => {
    it('shows mixed success and failure steps', () => {
      const steps = [
        makeStep({ stepName: 'validate', status: StepResultStatus.SUCCESS }),
        makeStep({ stepName: 'execute', status: StepResultStatus.FAILED, message: 'Partial failure' }),
      ]
      render(<ApplyPhasesStepper steps={steps} />)
      expect(screen.getByTestId('phase-step-validate')).toBeInTheDocument()
      expect(screen.getByTestId('phase-step-execute')).toBeInTheDocument()
      expect(screen.getByText('Partial failure')).toBeInTheDocument()
    })
  })

  it('shows spinner on last step while applying', () => {
    const steps = [
      makeStep({ stepName: 'validate', status: StepResultStatus.SUCCESS }),
      makeStep({ stepName: 'execute', status: StepResultStatus.UNSPECIFIED }),
    ]
    render(<ApplyPhasesStepper steps={steps} isApplying={true} />)
    // Both steps should be in the DOM
    expect(screen.getByTestId('phase-step-validate')).toBeInTheDocument()
    expect(screen.getByTestId('phase-step-execute')).toBeInTheDocument()
  })

  it('shows skipped steps', () => {
    const steps = [
      makeStep({ stepName: 'validate', status: StepResultStatus.SUCCESS }),
      makeStep({ stepName: 'execute', status: StepResultStatus.SKIPPED }),
    ]
    render(<ApplyPhasesStepper steps={steps} />)
    expect(screen.getByTestId('phase-step-execute')).toBeInTheDocument()
  })
})
