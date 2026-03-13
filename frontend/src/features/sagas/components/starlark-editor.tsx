import { useEffect, useRef, type MutableRefObject } from 'react'
import { EditorView } from '@codemirror/view'
import { Compartment, Transaction } from '@codemirror/state'
import { python } from '@codemirror/lang-python'
import { linter, lintGutter, type Diagnostic } from '@codemirror/lint'
import { basicSetup } from 'codemirror'
import { cn } from '@/lib/utils'

export type ValidationErrorCategory = 'SYNTAX' | 'ERROR' | 'WARNING'

export interface ValidationError {
  line: number
  column: number
  message: string
  category: ValidationErrorCategory
}

export interface ComplexityMetrics {
  handlerCalls: number
  operations: number
  estimatedDurationMs: number
  complexityScore: number
}

export interface StarlarkEditorProps {
  value: string
  onChange: (value: string) => void
  errors?: ValidationError[]
  readOnly?: boolean
  complexityMetrics?: ComplexityMetrics
  onErrorClick?: (error: ValidationError) => void
  /** Ref to capture the CodeMirror EditorView instance for external control (e.g., scroll-to-line). */
  editorViewRef?: MutableRefObject<EditorView | null>
  className?: string
}

function categoryToSeverity(
  category: ValidationErrorCategory,
): 'error' | 'warning' | 'info' {
  switch (category) {
    case 'SYNTAX':
    case 'ERROR':
      return 'error'
    case 'WARNING':
      return 'warning'
    default:
      return 'info'
  }
}

function getLineOffset(doc: string, line: number, column: number): number {
  const lines = doc.split('\n')
  let offset = 0
  for (let i = 0; i < line - 1 && i < lines.length; i++) {
    offset += lines[i].length + 1 // +1 for newline
  }
  // Proto ValidationError uses 1-indexed columns; convert to 0-indexed for CodeMirror
  const zeroIndexedColumn = Math.max(0, column - 1)
  // Clamp to doc length to prevent Diagnostic range errors when doc shrinks
  return Math.min(offset + zeroIndexedColumn, doc.length)
}

export function StarlarkEditor({
  value,
  onChange,
  errors = [],
  readOnly = false,
  complexityMetrics,
  onErrorClick,
  editorViewRef,
  className,
}: StarlarkEditorProps) {
  const editorRef = useRef<HTMLDivElement>(null)
  const viewRef = useRef<EditorView | null>(null)
  const readOnlyCompartment = useRef(new Compartment())
  const linterCompartment = useRef(new Compartment())

  // Keep refs to latest errors and onChange to avoid stale closures
  const errorsRef = useRef(errors)
  const onChangeRef = useRef(onChange)
  errorsRef.current = errors
  onChangeRef.current = onChange

  // Mount editor once
  useEffect(() => {
    if (!editorRef.current) return

    const errorLinter = linter((): Diagnostic[] => {
      const currentErrors = errorsRef.current
      const currentDoc = viewRef.current?.state.doc.toString() ?? ''
      return currentErrors.map((e) => {
        const from = getLineOffset(currentDoc, e.line, e.column)
        // Ensure to >= from and both within doc bounds
        const to = Math.max(from, Math.min(from + 10, currentDoc.length))
        return {
          from,
          to,
          severity: categoryToSeverity(e.category),
          message: e.message,
        }
      })
    })

    const view = new EditorView({
      doc: value,
      extensions: [
        basicSetup,
        python(),
        lintGutter(),
        linterCompartment.current.of(errorLinter),
        readOnlyCompartment.current.of(EditorView.editable.of(!readOnly)),
        EditorView.updateListener.of((update) => {
          // Only invoke onChange for genuine user edits, not programmatic dispatches
          if (
            update.docChanged &&
            update.transactions.some(
              (tr) => tr.annotation(Transaction.userEvent) != null,
            )
          ) {
            onChangeRef.current(update.state.doc.toString())
          }
        }),
      ],
      parent: editorRef.current,
    })

    viewRef.current = view
    if (editorViewRef) editorViewRef.current = view

    return () => {
      view.destroy()
      viewRef.current = null
      if (editorViewRef) editorViewRef.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Sync external value changes into the editor without rebuilding
  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    const current = view.state.doc.toString()
    if (current !== value) {
      view.dispatch({
        changes: { from: 0, to: current.length, insert: value },
      })
    }
  }, [value])

  // Reconfigure read-only state
  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    view.dispatch({
      effects: readOnlyCompartment.current.reconfigure(EditorView.editable.of(!readOnly)),
    })
  }, [readOnly])

  // Force linter to re-run when errors change by replacing the linter extension
  useEffect(() => {
    const view = viewRef.current
    if (!view) return

    const errorLinter = linter((): Diagnostic[] => {
      const currentErrors = errorsRef.current
      const currentDoc = view.state.doc.toString()
      return currentErrors.map((e) => {
        const from = getLineOffset(currentDoc, e.line, e.column)
        // Ensure to >= from and both within doc bounds
        const to = Math.max(from, Math.min(from + 10, currentDoc.length))
        return {
          from,
          to,
          severity: categoryToSeverity(e.category),
          message: e.message,
        }
      })
    })

    view.dispatch({
      effects: linterCompartment.current.reconfigure(errorLinter),
    })
  }, [errors])

  return (
    <div
      data-testid="starlark-editor"
      className={cn('flex flex-col gap-2', className)}
    >
      <div className="relative">
        {readOnly && (
          <span
            data-testid="readonly-badge"
            className="absolute right-2 top-2 z-10 rounded bg-muted px-2 py-0.5 text-xs text-muted-foreground"
          >
            Read only
          </span>
        )}
        <div
          ref={editorRef}
          className="min-h-[200px] rounded border border-input bg-background text-sm"
        />
      </div>

      {errors.length > 0 && (
        <div data-testid="error-panel" className="rounded border border-destructive/30 bg-destructive/5">
          <div className="border-b border-destructive/20 px-3 py-1.5 text-xs font-medium text-destructive">
            {errors.length} {errors.length === 1 ? 'issue' : 'issues'}
          </div>
          <ul className="divide-y divide-destructive/10 text-xs">
            {errors.map((error, index) => (
              <li key={index}>
                <button
                  type="button"
                  data-testid={`error-item-${index}`}
                  className={cn(
                    'flex w-full cursor-pointer items-start gap-2 px-3 py-1.5 text-left hover:bg-destructive/10',
                    error.category === 'WARNING' && 'text-yellow-700 dark:text-yellow-400',
                    error.category !== 'WARNING' && 'text-destructive',
                  )}
                  onClick={() => onErrorClick?.(error)}
                >
                  <span className="shrink-0 font-mono text-muted-foreground">
                    {error.line}:{error.column}
                  </span>
                  <span className="shrink-0 uppercase opacity-60">[{error.category}]</span>
                  <span>{error.message}</span>
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}

      {complexityMetrics && (
        <ComplexityMetricsPanel metrics={complexityMetrics} />
      )}
    </div>
  )
}

interface ComplexityMetricsPanelProps {
  metrics: ComplexityMetrics
}

function ComplexityMetricsPanel({ metrics }: ComplexityMetricsPanelProps) {
  const { handlerCalls, operations, estimatedDurationMs, complexityScore } = metrics

  const scoreColor =
    complexityScore <= 3
      ? 'text-green-600 dark:text-green-400'
      : complexityScore <= 6
        ? 'text-yellow-600 dark:text-yellow-400'
        : 'text-red-600 dark:text-red-400'

  return (
    <div
      data-testid="complexity-metrics-panel"
      className="rounded border border-border bg-muted/30 px-3 py-2"
    >
      <p className="mb-1.5 text-xs font-medium text-muted-foreground">
        Complexity Metrics
      </p>
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 text-xs">
        <div className="flex flex-col gap-0.5">
          <span className="text-muted-foreground">Handler Calls</span>
          <span className="font-mono font-semibold">{handlerCalls}</span>
        </div>
        <div className="flex flex-col gap-0.5">
          <span className="text-muted-foreground">Operations</span>
          <span className="font-mono font-semibold">{operations}</span>
        </div>
        <div className="flex flex-col gap-0.5">
          <span className="text-muted-foreground">Est. Duration</span>
          <span className="font-mono font-semibold">{estimatedDurationMs} ms</span>
        </div>
        <div className="flex flex-col gap-0.5">
          <span className="text-muted-foreground">Complexity</span>
          <span className={cn('font-mono font-semibold', scoreColor)}>
            {complexityScore} / 10
          </span>
        </div>
      </div>
    </div>
  )
}
