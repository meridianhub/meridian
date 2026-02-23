import { useEffect, useRef } from 'react'
import { EditorView } from '@codemirror/view'
import { Compartment, Transaction } from '@codemirror/state'
import { linter, lintGutter, type Diagnostic } from '@codemirror/lint'
import { basicSetup } from 'codemirror'
import { cn } from '@/lib/utils'

export type CELContext = 'validation' | 'bucketKey' | 'eligibility' | 'value'

export interface CELError {
  message: string
  line?: number
}

export interface CELEditorProps {
  value: string
  onChange: (value: string) => void
  context: CELContext
  errors?: CELError[]
  readOnly?: boolean
  showVariables?: boolean
  className?: string
}

// eslint-disable-next-line react-refresh/only-export-components
export const CONTEXT_VARIABLES: Record<CELContext, string[]> = {
  validation: ['attributes', 'amount', 'valid_from', 'valid_to', 'source'],
  bucketKey: ['attributes'],
  eligibility: [
    'party.type',
    'party.status',
    'party.external_reference_type',
    'attributes',
  ],
  value: ['attributes', 'amount', 'valid_from', 'valid_to', 'source'],
}

function getLineOffset(doc: string, line: number): number {
  const lines = doc.split('\n')
  let offset = 0
  for (let i = 0; i < line - 1 && i < lines.length; i++) {
    offset += lines[i].length + 1
  }
  return Math.min(offset, doc.length)
}

export function CELEditor({
  value,
  onChange,
  context,
  errors = [],
  readOnly = false,
  showVariables = true,
  className,
}: CELEditorProps) {
  const editorRef = useRef<HTMLDivElement>(null)
  const viewRef = useRef<EditorView | null>(null)
  const readOnlyCompartment = useRef(new Compartment())
  const linterCompartment = useRef(new Compartment())

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
      return currentErrors
        .filter((e) => e.line != null)
        .map((e) => {
          const from = getLineOffset(currentDoc, e.line!)
          const to = Math.max(from, Math.min(from + 10, currentDoc.length))
          return {
            from,
            to,
            severity: 'error' as const,
            message: e.message,
          }
        })
    })

    const view = new EditorView({
      doc: value,
      extensions: [
        basicSetup,
        lintGutter(),
        linterCompartment.current.of(errorLinter),
        readOnlyCompartment.current.of(EditorView.editable.of(!readOnly)),
        EditorView.updateListener.of((update) => {
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

    return () => {
      view.destroy()
      viewRef.current = null
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Sync external value changes
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
      effects: readOnlyCompartment.current.reconfigure(
        EditorView.editable.of(!readOnly),
      ),
    })
  }, [readOnly])

  // Force linter to re-run when errors change
  useEffect(() => {
    const view = viewRef.current
    if (!view) return

    const errorLinter = linter((): Diagnostic[] => {
      const currentErrors = errorsRef.current
      const currentDoc = view.state.doc.toString()
      return currentErrors
        .filter((e) => e.line != null)
        .map((e) => {
          const from = getLineOffset(currentDoc, e.line!)
          const to = Math.max(from, Math.min(from + 10, currentDoc.length))
          return {
            from,
            to,
            severity: 'error' as const,
            message: e.message,
          }
        })
    })

    view.dispatch({
      effects: linterCompartment.current.reconfigure(errorLinter),
    })
  }, [errors])

  const variables = CONTEXT_VARIABLES[context]

  return (
    <div
      data-testid="cel-editor"
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
          className="min-h-[60px] rounded border border-input bg-background text-sm"
        />
      </div>

      <div
        data-testid="security-constraints"
        className="flex flex-wrap gap-3 text-xs text-muted-foreground"
      >
        <span>Max 4,096 bytes</span>
        <span>Max nesting depth 10</span>
        <span>Cost limit 10,000</span>
      </div>

      {showVariables && variables.length > 0 && (
        <div
          data-testid="variables-panel"
          className="rounded border border-border bg-muted/30 px-3 py-2"
        >
          <p className="mb-1.5 text-xs font-medium text-muted-foreground">
            Available variables
          </p>
          <div className="flex flex-wrap gap-1.5">
            {variables.map((v) => (
              <code
                key={v}
                className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs text-foreground"
              >
                {v}
              </code>
            ))}
          </div>
        </div>
      )}

      {errors.length > 0 && (
        <div
          data-testid="error-panel"
          className="rounded border border-destructive/30 bg-destructive/5"
        >
          <div className="border-b border-destructive/20 px-3 py-1.5 text-xs font-medium text-destructive">
            {errors.length} {errors.length === 1 ? 'issue' : 'issues'}
          </div>
          <ul className="divide-y divide-destructive/10 text-xs">
            {errors.map((error, index) => (
              <li
                key={index}
                data-testid={`error-item-${index}`}
                className="flex items-start gap-2 px-3 py-1.5 text-destructive"
              >
                {error.line != null && (
                  <span className="shrink-0 font-mono text-muted-foreground">
                    {error.line}
                  </span>
                )}
                <span>{error.message}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}
