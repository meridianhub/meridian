import { CheckCircle2, XCircle, MinusCircle, Loader2 } from 'lucide-react'
import { StepResultStatus } from '@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb'
import type { StepResult } from '@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb'

// ── Props ────────────────────────────────────────────────────────────────────

export interface ApplyPhasesStepperProps {
  /** Step results returned from the apply response. */
  steps: StepResult[]
  /** When true, the apply is still in progress (shows spinner on last step). */
  isApplying?: boolean
}

// ── Helpers ──────────────────────────────────────────────────────────────────

function stepIcon(status: StepResultStatus, isActive: boolean) {
  if (isActive) {
    return <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
  }
  switch (status) {
    case StepResultStatus.SUCCESS:
      return <CheckCircle2 className="h-4 w-4 text-success" />
    case StepResultStatus.FAILED:
      return <XCircle className="h-4 w-4 text-destructive" />
    case StepResultStatus.SKIPPED:
      return <MinusCircle className="h-4 w-4 text-muted-foreground" />
    default:
      return <MinusCircle className="h-4 w-4 text-muted-foreground" />
  }
}

function stepLabelClass(status: StepResultStatus): string {
  switch (status) {
    case StepResultStatus.SUCCESS:
      return 'text-foreground'
    case StepResultStatus.FAILED:
      return 'text-destructive font-medium'
    case StepResultStatus.SKIPPED:
      return 'text-muted-foreground'
    default:
      return 'text-muted-foreground'
  }
}

function formatStepName(name: string): string {
  return name.charAt(0).toUpperCase() + name.slice(1).toLowerCase().replace(/_/g, ' ')
}

// ── Component ────────────────────────────────────────────────────────────────

export function ApplyPhasesStepper({ steps, isApplying = false }: ApplyPhasesStepperProps) {
  if (steps.length === 0 && !isApplying) {
    return null
  }

  return (
    <ol
      className="space-y-1 text-sm"
      aria-label="Apply phases"
      data-testid="apply-phases-stepper"
    >
      {steps.map((step, index) => {
        const isLastStep = index === steps.length - 1
        const isActive = isApplying && isLastStep

        return (
          <li key={step.stepName} className="flex items-start gap-2.5">
            <span className="mt-0.5 shrink-0">{stepIcon(step.status, isActive)}</span>
            <div className="min-w-0 flex-1">
              <span className={stepLabelClass(step.status)} data-testid={`phase-step-${step.stepName}`}>
                {formatStepName(step.stepName)}
              </span>
              {step.message && step.status === StepResultStatus.FAILED && (
                <p className="mt-0.5 text-xs text-destructive/80">{step.message}</p>
              )}
            </div>
          </li>
        )
      })}
      {isApplying && steps.length === 0 && (
        <li className="flex items-center gap-2.5">
          <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
          <span className="text-muted-foreground">Applying…</span>
        </li>
      )}
    </ol>
  )
}
