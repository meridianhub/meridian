import { useCallback, useMemo, useState } from 'react'
import CodeMirror from '@uiw/react-codemirror'
import { yaml } from '@codemirror/lang-yaml'
import { linter, lintGutter, type Diagnostic } from '@codemirror/lint'
import { pathToLine } from './path-to-line'
import type { ValidationError } from '@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb'

interface ManifestEditorProps {
  value: string
  onChange: (value: string) => void
  validationErrors?: ValidationError[]
  className?: string
}

function createDiagnostics(
  source: string,
  errors: ValidationError[],
): Diagnostic[] {
  if (!source || errors.length === 0) return []

  return errors
    .map((error) => {
      const lineNum = pathToLine(source, error.path)
      if (lineNum === null) return null

      const lines = source.split('\n')
      const lineIndex = lineNum - 1
      if (lineIndex < 0 || lineIndex >= lines.length) return null

      // Calculate character offsets
      let from = 0
      for (let i = 0; i < lineIndex; i++) {
        from += lines[i].length + 1 // +1 for newline
      }
      const to = from + lines[lineIndex].length

      return {
        from,
        to,
        severity: error.severity === 'ERROR' ? 'error' : ('warning' as const),
        message: error.message,
      }
    })
    .filter((d): d is Diagnostic => d !== null)
}

export function ManifestEditor({
  value,
  onChange,
  validationErrors = [],
  className,
}: ManifestEditorProps) {
  const [diagnostics, setDiagnostics] = useState<Diagnostic[]>([])

  // Recalculate diagnostics when validation errors change
  const prevErrorsRef = useMemo(() => ({ errors: validationErrors, value }), [validationErrors, value])
  const currentDiagnostics = createDiagnostics(prevErrorsRef.value, prevErrorsRef.errors)
  if (JSON.stringify(currentDiagnostics) !== JSON.stringify(diagnostics)) {
    setDiagnostics(currentDiagnostics)
  }

  const extensions = useMemo(() => {
    const linterExtension = linter(() => diagnostics)
    return [yaml(), lintGutter(), linterExtension]
  }, [diagnostics])

  const handleChange = useCallback(
    (val: string) => {
      onChange(val)
    },
    [onChange],
  )

  return (
    <CodeMirror
      data-testid="codemirror-editor"
      value={value}
      onChange={handleChange}
      extensions={extensions}
      className={className}
      basicSetup={{
        lineNumbers: true,
        foldGutter: true,
        highlightActiveLine: true,
        bracketMatching: true,
      }}
    />
  )
}
