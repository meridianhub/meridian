import { CheckIcon } from 'lucide-react'
import { cn } from '@/lib/utils'
import { TimeDisplay } from '@/shared/time-display'

const SAGA_STEPS = ['INITIATED', 'RESERVED', 'EXECUTING', 'COMPLETED'] as const

export interface SagaStep {
  status: string
  timestamp: { seconds: bigint | number; nanos?: number } | null
}

export interface SagaTimelineProps {
  currentStatus: string
  steps: SagaStep[]
  compensationSteps?: SagaStep[]
  loading?: boolean
}

function SkeletonDot() {
  return (
    <div className="flex flex-col items-center gap-2">
      <div className="h-8 w-8 animate-pulse rounded-full bg-muted" />
      <div className="h-3 w-16 animate-pulse rounded bg-muted" />
    </div>
  )
}

export function SagaTimeline({
  currentStatus,
  steps,
  compensationSteps,
  loading,
}: SagaTimelineProps) {
  if (loading) {
    return (
      <div data-testid="saga-timeline-skeleton" className="flex items-start gap-0">
        {SAGA_STEPS.map((step, i) => (
          <div key={step} className="flex flex-1 items-center">
            <SkeletonDot />
            {i < SAGA_STEPS.length - 1 && (
              <div className="h-0.5 flex-1 bg-muted" />
            )}
          </div>
        ))}
      </div>
    )
  }

  const hasCompensation = compensationSteps && compensationSteps.length > 0

  return (
    <div className="space-y-6">
      {/* Main timeline */}
      <div className="flex items-start">
        {SAGA_STEPS.map((step, i) => {
          const stepData = steps.find((s) => s.status === step)
          const isCompleted = stepData?.timestamp != null
          const isCurrent = step === currentStatus

          return (
            <div key={step} className="flex flex-1 flex-col items-center">
              <div className="flex w-full items-center">
                {/* Left connector */}
                {i > 0 && (
                  <div
                    className={cn(
                      'h-0.5 flex-1',
                      isCompleted ? 'bg-primary' : 'bg-muted',
                    )}
                  />
                )}

                {/* Step dot */}
                <div
                  data-testid={`step-dot-${step}`}
                  className={cn(
                    'flex h-8 w-8 shrink-0 items-center justify-center rounded-full border-2 text-xs font-semibold',
                    isCompleted
                      ? 'border-primary bg-primary text-primary-foreground'
                      : isCurrent
                        ? 'animate-pulse border-primary bg-primary/10 text-primary'
                        : 'border-muted bg-background text-muted-foreground',
                  )}
                >
                  {isCompleted ? (
                    <CheckIcon
                      data-testid="step-complete-icon"
                      className="h-4 w-4"
                    />
                  ) : (
                    <span data-testid={`step-number-${step}`}>{i + 1}</span>
                  )}
                </div>

                {/* Right connector */}
                {i < SAGA_STEPS.length - 1 && (
                  <div
                    className={cn(
                      'h-0.5 flex-1',
                      isCompleted ? 'bg-primary' : 'bg-muted',
                    )}
                  />
                )}
              </div>

              {/* Label and timestamp */}
              <div className="mt-2 flex flex-col items-center gap-0.5 text-center">
                <span
                  className={cn(
                    'text-xs font-medium',
                    isCompleted || isCurrent ? 'text-foreground' : 'text-muted-foreground',
                  )}
                >
                  {step}
                </span>
                {stepData?.timestamp && (
                  <span
                    data-testid="step-timestamp"
                    className="text-xs text-muted-foreground"
                  >
                    <TimeDisplay timestamp={stepData.timestamp} format="relative" />
                  </span>
                )}
              </div>
            </div>
          )
        })}
      </div>

      {/* Compensation branch */}
      {hasCompensation && (
        <div data-testid="compensation-section" className="rounded-md border border-destructive/30 bg-destructive/5 p-4">
          <p className="mb-3 text-sm font-medium text-destructive">Compensation</p>
          <div className="space-y-2">
            {compensationSteps.map((step, i) => (
              <div key={`${step.status}-${i}`} className="flex items-center gap-3">
                <div className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-destructive/20 text-xs font-semibold text-destructive">
                  {i + 1}
                </div>
                <span className="text-sm">{step.status}</span>
                {step.timestamp && (
                  <span className="ml-auto text-xs text-muted-foreground">
                    <TimeDisplay timestamp={step.timestamp} format="relative" />
                  </span>
                )}
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
