import { useState, useMemo, useCallback } from 'react'
import { HandlerReference } from '@/shared/handler-reference'
import { SagaFlowDiagram } from './saga-flow'
import type { SagaFlow } from '../lib/star-parser'

interface LinkedPatternDetailProps {
  flows: SagaFlow[]
}

export function LinkedPatternDetail({ flows }: LinkedPatternDetailProps) {
  const [highlightedHandler, setHighlightedHandler] = useState<string | null>(null)

  const serviceNames = useMemo(() => {
    const names = new Set<string>()
    for (const flow of flows) {
      for (const step of flow.steps) {
        for (const call of step.serviceCalls) {
          names.add(call.service)
        }
      }
    }
    return Array.from(names)
  }, [flows])

  const handleStepClick = useCallback((_stepName: string, _lineNumber: number) => {
    // Find the step across all flows
    for (const flow of flows) {
      const step = flow.steps.find((s) => s.name === _stepName)
      if (step && step.serviceCalls.length > 0) {
        const firstCall = step.serviceCalls[0]
        setHighlightedHandler(`${firstCall.service}.${firstCall.method}`)
        return
      }
    }
    setHighlightedHandler(null)
  }, [flows])

  return (
    <div data-testid="linked-detail" className="flex flex-col gap-4">
      <div className="h-[400px] rounded-lg border">
        <SagaFlowDiagram
          flows={flows}
          onStepClick={handleStepClick}
        />
      </div>

      <div className="rounded-lg border p-3">
        <h3 className="mb-2 text-sm font-medium text-muted-foreground">Handler Reference</h3>
        <HandlerReference
          serviceNames={serviceNames.length > 0 ? serviceNames : undefined}
          highlightedHandler={highlightedHandler ?? undefined}
        />
      </div>
    </div>
  )
}
