import { useState, useMemo, useCallback, useEffect } from 'react'
import { Maximize2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { HandlerReference } from '@/shared/handler-reference'
import { SagaFlowDiagram } from './saga-flow'
import type { FlowDirection } from './saga-flow'
import type { SagaFlow } from '../lib/star-parser'

const MOBILE_BREAKPOINT = 640

function useIsMobile() {
  const [isMobile, setIsMobile] = useState(
    typeof window !== 'undefined' ? window.innerWidth < MOBILE_BREAKPOINT : false,
  )

  useEffect(() => {
    const mql = window.matchMedia(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`)
    const onChange = (e: MediaQueryListEvent) => setIsMobile(e.matches)
    mql.addEventListener('change', onChange)
    setIsMobile(mql.matches)
    return () => mql.removeEventListener('change', onChange)
  }, [])

  return isMobile
}

interface LinkedPatternDetailProps {
  flows: SagaFlow[]
}

export function LinkedPatternDetail({ flows }: LinkedPatternDetailProps) {
  const [highlightedHandler, setHighlightedHandler] = useState<string | null>(null)
  const [fullscreen, setFullscreen] = useState(false)
  const isMobile = useIsMobile()

  const direction: FlowDirection = isMobile ? 'TB' : 'LR'

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
      <div className="relative h-[300px] sm:h-[400px] rounded-lg border">
        <SagaFlowDiagram
          flows={flows}
          onStepClick={handleStepClick}
          direction={direction}
        />
        <Button
          variant="outline"
          size="icon"
          className="absolute top-2 right-2 z-10 size-8 bg-background/80 backdrop-blur-sm"
          onClick={() => setFullscreen(true)}
          aria-label="View fullscreen"
        >
          <Maximize2 className="size-4" />
        </Button>
      </div>

      <Dialog open={fullscreen} onOpenChange={setFullscreen}>
        <DialogContent className="max-w-[calc(100vw-2rem)] h-[calc(100vh-2rem)] sm:max-w-[calc(100vw-4rem)] sm:h-[calc(100vh-4rem)] flex flex-col p-4">
          <DialogHeader className="shrink-0">
            <DialogTitle>Saga Flow</DialogTitle>
          </DialogHeader>
          <div className="flex-1 min-h-0 rounded-lg border">
            <SagaFlowDiagram
              flows={flows}
              onStepClick={handleStepClick}
              direction={direction}
            />
          </div>
        </DialogContent>
      </Dialog>

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
