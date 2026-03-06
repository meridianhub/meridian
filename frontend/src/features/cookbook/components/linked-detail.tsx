import { useState, useMemo, useCallback, useRef } from 'react'
import { type EditorView } from '@codemirror/view'
import { StarlarkEditor } from '@/features/sagas/components/starlark-editor'
import { HandlerReference } from '@/shared/handler-reference'
import { SagaFlowDiagram } from './saga-flow'
import type { SagaFlow } from '../lib/star-parser'

interface LinkedPatternDetailProps {
  flow: SagaFlow
  starlarkContent: string
}

export function LinkedPatternDetail({ flow, starlarkContent }: LinkedPatternDetailProps) {
  const [highlightedHandler, setHighlightedHandler] = useState<string | null>(null)
  const editorViewRef = useRef<EditorView | null>(null)

  const serviceNames = useMemo(() => {
    const names = new Set<string>()
    for (const step of flow.steps) {
      for (const call of step.serviceCalls) {
        names.add(call.service)
      }
    }
    return Array.from(names)
  }, [flow])

  const handleStepClick = useCallback((stepName: string, lineNumber: number) => {
    const view = editorViewRef.current
    if (view && lineNumber >= 1 && lineNumber <= view.state.doc.lines) {
      const line = view.state.doc.line(lineNumber)
      view.dispatch({
        selection: { anchor: line.from },
        scrollIntoView: true,
      })
    }

    const step = flow.steps.find((s) => s.name === stepName)
    if (step && step.serviceCalls.length > 0) {
      const firstCall = step.serviceCalls[0]
      setHighlightedHandler(`${firstCall.service}.${firstCall.method}`)
    } else {
      setHighlightedHandler(null)
    }
  }, [flow])

  return (
    <div data-testid="linked-detail" className="flex flex-col gap-4 lg:flex-row lg:gap-6">
      {/* Left panel: Starlark editor */}
      <div className="flex-1 min-w-0">
        <StarlarkEditor
          value={starlarkContent}
          onChange={() => {}}
          readOnly
          editorViewRef={editorViewRef}
        />
      </div>

      {/* Right panel: Diagram + Handler reference */}
      <div className="flex flex-col gap-4 lg:w-[480px] lg:shrink-0">
        <div className="h-[400px] rounded-lg border">
          <SagaFlowDiagram
            flow={flow}
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
    </div>
  )
}
