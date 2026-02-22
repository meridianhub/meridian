import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { TooltipProvider } from '@/components/ui/tooltip'
import { SagaTimeline } from './saga-timeline'

function renderWithTooltip(ui: React.ReactElement) {
  return render(<TooltipProvider>{ui}</TooltipProvider>)
}

const completedStep = (status: string, secondsOffset: number) => ({
  status,
  timestamp: { seconds: BigInt(1700000000 + secondsOffset), nanos: 0 },
})

const pendingStep = (status: string) => ({
  status,
  timestamp: null,
})

describe('SagaTimeline - loading state', () => {
  it('renders grey dots with pulse animation while loading', () => {
    renderWithTooltip(
      <SagaTimeline
        currentStatus="INITIATED"
        steps={[]}
        loading={true}
      />,
    )

    const skeleton = screen.getByTestId('saga-timeline-skeleton')
    expect(skeleton).toBeInTheDocument()
  })

  it('does not render step labels while loading', () => {
    renderWithTooltip(
      <SagaTimeline
        currentStatus="INITIATED"
        steps={[]}
        loading={true}
      />,
    )

    expect(screen.queryByText('INITIATED')).not.toBeInTheDocument()
    expect(screen.queryByText('COMPLETED')).not.toBeInTheDocument()
  })
})

describe('SagaTimeline - step progression', () => {
  it('renders all four saga steps', () => {
    renderWithTooltip(
      <SagaTimeline
        currentStatus="INITIATED"
        steps={[completedStep('INITIATED', 0)]}
      />,
    )

    expect(screen.getByText('INITIATED')).toBeInTheDocument()
    expect(screen.getByText('RESERVED')).toBeInTheDocument()
    expect(screen.getByText('EXECUTING')).toBeInTheDocument()
    expect(screen.getByText('COMPLETED')).toBeInTheDocument()
  })

  it('shows check mark for completed steps with timestamps', () => {
    renderWithTooltip(
      <SagaTimeline
        currentStatus="RESERVED"
        steps={[
          completedStep('INITIATED', 0),
          completedStep('RESERVED', 10),
        ]}
      />,
    )

    const checkMarks = screen.getAllByTestId('step-complete-icon')
    expect(checkMarks).toHaveLength(2)
  })

  it('shows step number for incomplete steps without timestamps', () => {
    renderWithTooltip(
      <SagaTimeline
        currentStatus="INITIATED"
        steps={[
          completedStep('INITIATED', 0),
          pendingStep('RESERVED'),
          pendingStep('EXECUTING'),
          pendingStep('COMPLETED'),
        ]}
      />,
    )

    // Steps 2, 3, 4 should show numbers (RESERVED=2, EXECUTING=3, COMPLETED=4)
    expect(screen.getByTestId('step-number-RESERVED')).toHaveTextContent('2')
    expect(screen.getByTestId('step-number-EXECUTING')).toHaveTextContent('3')
    expect(screen.getByTestId('step-number-COMPLETED')).toHaveTextContent('4')
  })

  it('highlights current step with pulse animation class', () => {
    renderWithTooltip(
      <SagaTimeline
        currentStatus="EXECUTING"
        steps={[
          completedStep('INITIATED', 0),
          completedStep('RESERVED', 10),
          pendingStep('EXECUTING'),
        ]}
      />,
    )

    const currentDot = screen.getByTestId('step-dot-EXECUTING')
    expect(currentDot).toHaveClass('animate-pulse')
  })

  it('shows timestamp for completed steps', () => {
    renderWithTooltip(
      <SagaTimeline
        currentStatus="COMPLETED"
        steps={[
          completedStep('INITIATED', 0),
          completedStep('RESERVED', 10),
          completedStep('EXECUTING', 20),
          completedStep('COMPLETED', 30),
        ]}
      />,
    )

    // All steps have timestamps, so TimeDisplay components should be rendered
    const timestamps = screen.getAllByTestId('step-timestamp')
    expect(timestamps).toHaveLength(4)
  })

  it('does not show timestamp for steps without timestamp', () => {
    renderWithTooltip(
      <SagaTimeline
        currentStatus="INITIATED"
        steps={[
          completedStep('INITIATED', 0),
          pendingStep('RESERVED'),
        ]}
      />,
    )

    const timestamps = screen.getAllByTestId('step-timestamp')
    expect(timestamps).toHaveLength(1) // Only INITIATED has timestamp
  })
})

describe('SagaTimeline - compensation branch', () => {
  it('renders compensation section when compensationSteps provided', () => {
    renderWithTooltip(
      <SagaTimeline
        currentStatus="FAILED"
        steps={[
          completedStep('INITIATED', 0),
          completedStep('RESERVED', 10),
        ]}
        compensationSteps={[
          completedStep('COMPENSATION_RESERVE_RELEASED', 30),
        ]}
      />,
    )

    expect(screen.getByTestId('compensation-section')).toBeInTheDocument()
    expect(screen.getByText('Compensation')).toBeInTheDocument()
  })

  it('renders compensation steps within the compensation section', () => {
    renderWithTooltip(
      <SagaTimeline
        currentStatus="FAILED"
        steps={[
          completedStep('INITIATED', 0),
          completedStep('RESERVED', 10),
        ]}
        compensationSteps={[
          completedStep('COMPENSATION_RESERVE_RELEASED', 30),
        ]}
      />,
    )

    expect(screen.getByText('COMPENSATION_RESERVE_RELEASED')).toBeInTheDocument()
  })

  it('does not render compensation section when compensationSteps is empty', () => {
    renderWithTooltip(
      <SagaTimeline
        currentStatus="COMPLETED"
        steps={[
          completedStep('INITIATED', 0),
          completedStep('COMPLETED', 30),
        ]}
        compensationSteps={[]}
      />,
    )

    expect(screen.queryByTestId('compensation-section')).not.toBeInTheDocument()
  })

  it('does not render compensation section when compensationSteps is undefined', () => {
    renderWithTooltip(
      <SagaTimeline
        currentStatus="COMPLETED"
        steps={[completedStep('INITIATED', 0)]}
      />,
    )

    expect(screen.queryByTestId('compensation-section')).not.toBeInTheDocument()
  })
})
